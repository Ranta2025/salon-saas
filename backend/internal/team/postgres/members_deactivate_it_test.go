// F2g2 contra Postgres real (SALON_IT=1): dar de baja conserva el historial y
// los horarios/ausencias/servicios de la persona, el acceso y los selectores
// ven SOLO activos, y la baja con la revocación de las invitaciones que emitió
// son atómicas (S-F2g2-6).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil/itdb"
)

const (
	f2g2SalonA = "s-it-f2g2"
	f2g2SalonB = "s-it-f2g2-b"
	f2g2Owner  = "u-it-f2g2-owner"
	f2g2Pro    = "u-it-f2g2-pro"
	f2g2Recep  = "u-it-f2g2-recep"
	f2g2SvcA   = "svc-it-f2g2"
	f2g2SvcB   = "svc-it-f2g2-b"
	f2g2PastBk = "bk-it-f2g2-past"
	f2g2Sale   = "sale-it-f2g2"
	f2g2Off    = "off-it-f2g2"
)

// f2g2Now es el reloj fijo de la baja (mismo día para todos los asserts).
var f2g2Now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func f2g2Exec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("fixture: %v\n%s", err, q)
	}
}

// f2g2Fixture deja dos salones; la persona (profesional) es miembro de los DOS,
// así se puede probar que la baja en uno no le quita el acceso al otro. El salón
// A arranca con el historial mínimo de la persona: un servicio que hace, un
// tramo de horario, una ausencia futura, un turno pasado y una venta.
func f2g2Fixture(t *testing.T) (*pgxpool.Pool, pgstore.Memberships) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	itdb.ApplyMigrations(t, pool)

	f2g2Exec(t, pool, `DELETE FROM salons WHERE id IN ($1,$2)`, f2g2SalonA, f2g2SalonB)
	f2g2Exec(t, pool, `DELETE FROM users WHERE id LIKE 'u-it-f2g2-%'`)
	f2g2Exec(t, pool, `INSERT INTO users(id,name,email,pass_hash) VALUES
		($1,'Olga F2g2',$1||'@test.com','x'),
		($2,'Pia F2g2',$2||'@test.com','x'),
		($3,'Rita F2g2',$3||'@test.com','x')`, f2g2Owner, f2g2Pro, f2g2Recep)
	f2g2Exec(t, pool, `INSERT INTO salons(id,slug,name) VALUES ($1,$1,'Salón F2g2'),($2,$2,'Salón F2g2 B')`,
		f2g2SalonA, f2g2SalonB)
	f2g2Exec(t, pool, `INSERT INTO memberships(user_id,salon_id,role,attends,trade) VALUES
		($1,$4,'owner',true,''),
		($2,$4,'professional',true,'Barbera'),
		($2,$5,'professional',true,'Barbera'),
		($3,$4,'reception',false,'')`, f2g2Owner, f2g2Pro, f2g2Recep, f2g2SalonA, f2g2SalonB)
	f2g2Exec(t, pool, `INSERT INTO services(id,salon_id,name,duration_min,price_cents) VALUES
		($1,$3,'Corte',30,100000),
		($2,$4,'Corte B',30,100000)`, f2g2SvcA, f2g2SvcB, f2g2SalonA, f2g2SalonB)
	f2g2Exec(t, pool, `INSERT INTO professional_hours(salon_id,user_id,weekday,start_time,end_time) VALUES
		($1,$3,1,'09:00','18:00'),
		($2,$3,2,'09:00','18:00')`, f2g2SalonA, f2g2SalonB, f2g2Pro)
	f2g2Exec(t, pool, `INSERT INTO time_off(id,salon_id,user_id,starts_at,ends_at,reason) VALUES
		($1,$2,$3,$4,$5,'Vacaciones')`, f2g2Off, f2g2SalonA, f2g2Pro,
		f2g2Now.Add(48*time.Hour), f2g2Now.Add(72*time.Hour))
	f2g2Exec(t, pool, `INSERT INTO professional_services(salon_id,user_id,service_id) VALUES
		($1,$3,$4),($2,$3,$5)`, f2g2SalonA, f2g2SalonB, f2g2Pro, f2g2SvcA, f2g2SvcB)
	// Turno PASADO finalizado y venta de la persona: la baja no los toca (es el
	// historial que F3 necesita para liquidar comisiones).
	f2g2Exec(t, pool, `INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,service_id) VALUES
		($1,$2,$3,'Clienta','',$4,$5,'done',$6)`, f2g2PastBk, f2g2SalonA, f2g2Pro,
		f2g2Now.Add(-48*time.Hour), f2g2Now.Add(-47*time.Hour).Add(30*time.Minute), f2g2SvcA)
	f2g2Exec(t, pool, `INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method) VALUES
		($1,$2,$3,'Clienta',100000,'cash')`, f2g2Sale, f2g2SalonA, f2g2Pro)
	// Invitaciones: una pendiente emitida por la persona (se tiene que revocar),
	// una ya aceptada (no se toca), una pendiente de OTRA persona y una pendiente
	// de la misma persona en el OTRO salón (no se toca: scope por salón).
	f2g2Exec(t, pool, `INSERT INTO invitations(id,salon_id,email,role,attends,token_hash,expires_at,created_by) VALUES
		('inv-it-f2g2-pending',$1,'pendiente-f2g2@test.com','professional',true,repeat('a',64),$3,$4),
		('inv-it-f2g2-accepted',$1,'aceptada-f2g2@test.com','professional',true,repeat('b',64),$3,$4),
		('inv-it-f2g2-owner',$1,'otra-f2g2@test.com','professional',true,repeat('c',64),$3,$5),
		('inv-it-f2g2-b',$2,'salonb-f2g2@test.com','professional',true,repeat('d',64),$3,$4)`,
		f2g2SalonA, f2g2SalonB, time.Now().UTC().Add(7*24*time.Hour), f2g2Pro, f2g2Owner)
	f2g2Exec(t, pool, `UPDATE invitations SET accepted_at=now(), accepted_by=$1 WHERE id='inv-it-f2g2-accepted'`, f2g2Owner)

	return pool, pgstore.Memberships{Pool: pool}
}

// f2g2Scalar devuelve un entero de una query (o falla el test).
func f2g2Scalar(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

// f2g2Text devuelve una fila como texto comparable antes/después de la baja.
func f2g2Text(t *testing.T, pool *pgxpool.Pool, q string, args ...any) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}

// f2g2MembershipExists pregunta por la vista transaccional de las invitaciones
// (la que decide si el canje crea o rechaza la membresía).
func f2g2MembershipExists(t *testing.T, pool *pgxpool.Pool, salonID, userID string) bool {
	t.Helper()
	var exists bool
	err := Invitations{Pool: pool}.InInvitationTx(func(tx ports.InvitationTx) error {
		var err error
		exists, err = tx.MembershipExists(salonID, userID)
		return err
	})
	if err != nil {
		t.Fatalf("MembershipExists(%s,%s): %v", salonID, userID, err)
	}
	return exists
}

// S-F2g2-6: la baja NO borra horarios, ausencias ni servicios (las tres tablas
// hijas cuelgan de la membresía en CASCADE) y no toca turnos ni ventas pasadas;
// al reactivar, todo sigue ahí.
func TestMembers_IT_deactivate_keeps_history(t *testing.T) {
	pool, repo := f2g2Fixture(t)
	ctx := context.Background()

	filasHijas := func() map[string]int {
		return map[string]int{
			"professional_hours":    f2g2Scalar(t, pool, `SELECT count(*)::int FROM professional_hours WHERE user_id=$1`, f2g2Pro),
			"time_off":              f2g2Scalar(t, pool, `SELECT count(*)::int FROM time_off WHERE user_id=$1`, f2g2Pro),
			"professional_services": f2g2Scalar(t, pool, `SELECT count(*)::int FROM professional_services WHERE user_id=$1`, f2g2Pro),
			"bookings":              f2g2Scalar(t, pool, `SELECT count(*)::int FROM bookings WHERE professional_id=$1`, f2g2Pro),
			"sales":                 f2g2Scalar(t, pool, `SELECT count(*)::int FROM sales WHERE professional_id=$1`, f2g2Pro),
		}
	}
	turnoAntes := f2g2Text(t, pool, `SELECT status||'|'||professional_id||'|'||starts_at||'|'||ends_at FROM bookings WHERE id=$1`, f2g2PastBk)
	ventaAntes := f2g2Text(t, pool, `SELECT professional_id||'|'||client_name||'|'||total_cents||'|'||method FROM sales WHERE id=$1`, f2g2Sale)
	antes := filasHijas()

	if err := repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
		t.Fatalf("dar de baja: %v", err)
	}
	// La baja es real (no un no-op).
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || m.Active() {
		t.Fatalf("después de la baja FindMember tiene que verla inactiva: %+v, %v", m, err)
	}

	despues := filasHijas()
	for tabla, n := range antes {
		if despues[tabla] != n {
			t.Fatalf("la baja no puede tocar %s: %d -> %d", tabla, n, despues[tabla])
		}
	}
	if got, want := f2g2Text(t, pool, `SELECT status||'|'||professional_id||'|'||starts_at||'|'||ends_at FROM bookings WHERE id=$1`, f2g2PastBk), turnoAntes; got != want {
		t.Fatalf("turno pasado alterado por la baja: %q != %q", got, want)
	}
	if got, want := f2g2Text(t, pool, `SELECT professional_id||'|'||client_name||'|'||total_cents||'|'||method FROM sales WHERE id=$1`, f2g2Sale), ventaAntes; got != want {
		t.Fatalf("venta pasada alterada por la baja: %q != %q", got, want)
	}
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM time_off WHERE id=$1 AND salon_id=$2`, f2g2Off, f2g2SalonA); n != 1 {
		t.Fatalf("las ausencias futuras se conservan: %d", n)
	}

	// Reactivar las devuelve enteras.
	if err := repo.ReactivateMember(f2g2Pro, f2g2SalonA); err != nil {
		t.Fatalf("reactivar: %v", err)
	}
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || !m.Active() {
		t.Fatalf("reactivar tiene que dejarla activa: %+v, %v", m, err)
	}
	if ahora := filasHijas(); fmt.Sprint(ahora) != fmt.Sprint(antes) {
		t.Fatalf("reactivar no recupera el historial: antes %v, ahora %v", antes, ahora)
	}
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM professional_hours WHERE salon_id=$1 AND user_id=$2`, f2g2SalonA, f2g2Pro); n != 1 {
		t.Fatalf("el horario vuelve con la persona: %d", n)
	}
	_ = ctx
}

// S-F2g2-3: Find, ListByUser, CountRole, ListStaff, ListProfessionalsFor,
// CountProfessionals y MembershipExists ven SOLO activos; ListMembers devuelve
// activos Y dados de baja; FindMember es la única lectura que ve la baja. La
// persona conserva el acceso al OTRO salón.
func TestMembers_IT_deactivate_active_only_reads(t *testing.T) {
	pool, repo := f2g2Fixture(t)
	pros := pgstore.ProfessionalServices{Pool: pool}

	// Antes de la baja: todo lo ve.
	if _, err := repo.Find(f2g2Pro, f2g2SalonA); err != nil {
		t.Fatalf("Find antes de la baja: %v", err)
	}
	if !f2g2MembershipExists(t, pool, f2g2SalonA, f2g2Pro) {
		t.Fatal("MembershipExists antes de la baja: la persona es parte del salón")
	}
	if err := repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
		t.Fatalf("dar de baja: %v", err)
	}

	// Find: el salón de la baja ya no existe para ella; el otro sí.
	if _, err := repo.Find(f2g2Pro, f2g2SalonA); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Find tiene que devolver solo activas: %v", err)
	}
	otro, err := repo.Find(f2g2Pro, f2g2SalonB)
	if err != nil {
		t.Fatalf("Find en el otro salón tiene que seguir funcionando: %v", err)
	}
	if otro.Role != domain.RoleProfessional || !otro.Attends || otro.Trade != "Barbera" || !otro.Active() {
		t.Fatalf("la membresía del otro salón queda intacta: %+v", otro)
	}

	// FindMember sí ve la baja (con su fecha).
	baja, err := repo.FindMember(f2g2Pro, f2g2SalonA)
	if err != nil {
		t.Fatalf("FindMember ve la baja: %v", err)
	}
	if baja.Active() || baja.EndedAt == nil || !baja.EndedAt.Equal(f2g2Now) {
		t.Fatalf("FindMember tiene que traer la baja con la fecha: %+v", baja)
	}
	if baja.Role != domain.RoleProfessional || !baja.Attends || baja.Trade != "Barbera" {
		t.Fatalf("la baja conserva rol, attends y oficio: %+v", baja)
	}

	// ListByUser (/me): solo el otro salón.
	salones, err := repo.ListByUser(f2g2Pro)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(salones) != 1 || salones[0].SalonID != f2g2SalonB {
		t.Fatalf("/me deja de listar el salón de la baja: %+v", salones)
	}

	// CountRole / CountProfessionals (último owner y capacidad instalada).
	if n, err := repo.CountRole(f2g2SalonA, domain.RoleProfessional); err != nil || n != 0 {
		t.Fatalf("CountRole tiene que contar solo activos: %d, %v", n, err)
	}
	if n, err := repo.CountProfessionals(f2g2SalonA); err != nil || n != 0 {
		t.Fatalf("CountProfessionals tiene que contar solo activos: %d, %v", n, err)
	}
	if n, err := repo.CountProfessionals(f2g2SalonB); err != nil || n != 1 {
		t.Fatalf("CountProfessionals del otro salón: %d, %v", n, err)
	}

	// ListStaff (agenda): el salón de la baja ya no la lista; el otro sí. El
	// dueño que atiende sigue apareciendo.
	staffA, err := repo.ListStaff(f2g2SalonA)
	if err != nil {
		t.Fatalf("ListStaff: %v", err)
	}
	for _, s := range staffA {
		if s.ID == f2g2Pro {
			t.Fatalf("ListStaff no puede listar a quien está dado de baja: %+v", staffA)
		}
	}
	if len(staffA) != 1 || staffA[0].ID != f2g2Owner {
		t.Fatalf("ListStaff conserva al dueño que atiende: %+v", staffA)
	}
	if staffB, err := repo.ListStaff(f2g2SalonB); err != nil || len(staffB) != 1 || staffB[0].ID != f2g2Pro {
		t.Fatalf("ListStaff del otro salón: %+v, %v", staffB, err)
	}

	// ListProfessionalsFor (disponibilidad y reserva pública).
	if got, err := pros.ListProfessionalsFor(f2g2SalonA, f2g2SvcA); err != nil || len(got) != 0 {
		t.Fatalf("ListProfessionalsFor no puede ofrecer a quien está de baja: %+v, %v", got, err)
	}
	if got, err := pros.ListProfessionalsFor(f2g2SalonB, f2g2SvcB); err != nil || len(got) != 1 || got[0].ID != f2g2Pro {
		t.Fatalf("ListProfessionalsFor del otro salón: %+v, %v", got, err)
	}

	// ListMembers: activos Y dados de baja, con active/ended_at por item.
	lista, err := repo.ListMembers(f2g2SalonA)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	estados := map[string]domain.Member{}
	for _, m := range lista {
		estados[m.ID] = m
	}
	if len(lista) != 3 {
		t.Fatalf("el listado devuelve activos Y dados de baja: %+v", lista)
	}
	if m := estados[f2g2Pro]; m.Active || m.EndedAt == nil || !m.EndedAt.Equal(f2g2Now) {
		t.Fatalf("la baja tiene que venir marcada en el listado: %+v", m)
	}
	for _, id := range []string{f2g2Owner, f2g2Recep} {
		if m := estados[id]; !m.Active || m.EndedAt != nil {
			t.Fatalf("%s sigue activa en el listado: %+v", id, m)
		}
	}

	// MembershipExists: canjear una invitación la reactiva, así que para el
	// salón de la baja "ya no es parte del equipo" (y sigue siéndolo del otro).
	if f2g2MembershipExists(t, pool, f2g2SalonA, f2g2Pro) {
		t.Fatal("MembershipExists no puede decir que una baja sigue en el equipo")
	}
	if !f2g2MembershipExists(t, pool, f2g2SalonB, f2g2Pro) {
		t.Fatal("MembershipExists tiene que seguir viendo el otro salón")
	}
}

// S-F2g2-4: la baja y la revocación de las invitaciones pendientes que emitió la
// persona son ATÓMICAS: si algo falla después de revocar, no queda ni la baja ni
// la revocación. En el camino feliz se revocan solo las pendientes de esa
// persona en ESE salón.
func TestMembers_IT_deactivate_revokes_invitations_atomically(t *testing.T) {
	pool, repo := f2g2Fixture(t)

	revocadas := func() int {
		return f2g2Scalar(t, pool, `SELECT count(*)::int FROM invitations WHERE salon_id=$1 AND created_by=$2 AND revoked_at IS NOT NULL`, f2g2SalonA, f2g2Pro)
	}

	boom := errors.New("falla después de revocar")
	err := repo.InMemberTx(func(tx ports.MemberTx) error {
		if err := tx.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
			return err
		}
		// El conteo va dentro de la misma transacción (después del UPDATE).
		if n, err := tx.CountFutureBookings(f2g2SalonA, f2g2Pro, f2g2Now); err != nil || n != 0 {
			return fmt.Errorf("turnos futuros dentro de la tx: %d, %v", n, err)
		}
		if err := tx.RevokeInvitationsBy(f2g2SalonA, f2g2Pro, f2g2Now); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InMemberTx tiene que devolver el error de fn: %v", err)
	}
	// NADA quedó: la membresía sigue activa y la invitación sigue pendiente.
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || !m.Active() {
		t.Fatalf("la baja se revierte con la transacción: %+v, %v", m, err)
	}
	if n := revocadas(); n != 0 {
		t.Fatalf("la revocación se revierte con la transacción, quedaron %d", n)
	}
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM invitations WHERE id='inv-it-f2g2-pending' AND revoked_at IS NULL`); n != 1 {
		t.Fatal("la invitación pendiente tiene que seguir pendiente")
	}

	// Camino feliz: baja + revocación en el mismo commit.
	err = repo.InMemberTx(func(tx ports.MemberTx) error {
		if err := tx.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
			return err
		}
		if n, err := tx.CountFutureBookings(f2g2SalonA, f2g2Pro, f2g2Now); err != nil || n != 0 {
			return fmt.Errorf("turnos futuros dentro de la tx: %d, %v", n, err)
		}
		return tx.RevokeInvitationsBy(f2g2SalonA, f2g2Pro, f2g2Now)
	})
	if err != nil {
		t.Fatalf("baja + revocación: %v", err)
	}
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || m.Active() {
		t.Fatalf("la baja se aplicó: %+v, %v", m, err)
	}
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM invitations WHERE id='inv-it-f2g2-pending' AND revoked_at=$1`, f2g2Now); n != 1 {
		t.Fatalf("la invitación pendiente se revoca en el mismo commit: %d", n)
	}
	// Las que no corresponden quedan intactas: aceptada, de otra persona, y la
	// pendiente del mismo emisor en el OTRO salón.
	for _, id := range []string{"inv-it-f2g2-accepted", "inv-it-f2g2-owner", "inv-it-f2g2-b"} {
		if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM invitations WHERE id=$1 AND revoked_at IS NULL`, id); n != 1 {
			t.Fatalf("la revocación no puede tocar %s", id)
		}
	}
}

// S-F2g2-4: solo los turnos FUTUROS no cancelados bloquean la baja.
func TestMembers_IT_count_future_bookings(t *testing.T) {
	pool, repo := f2g2Fixture(t)
	cuenta := func() int {
		t.Helper()
		n, err := repo.CountFutureBookings(f2g2SalonA, f2g2Pro, f2g2Now)
		if err != nil {
			t.Fatalf("CountFutureBookings: %v", err)
		}
		return n
	}
	turno := func(id string, desde time.Duration, status string) {
		t.Helper()
		f2g2Exec(t, pool, `INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,service_id)
			VALUES($1,$2,$3,'Clienta','',$4,$5,$6,$7)`, id, f2g2SalonA, f2g2Pro,
			f2g2Now.Add(desde), f2g2Now.Add(desde+30*time.Minute), status, f2g2SvcA)
	}

	// El turno pasado del fixture no cuenta.
	if n := cuenta(); n != 0 {
		t.Fatalf("un turno pasado no cuenta: %d", n)
	}
	turno("bk-it-f2g2-fut1", 24*time.Hour, "booked")
	if n := cuenta(); n != 1 {
		t.Fatalf("un turno futuro reservado cuenta: %d", n)
	}
	// Una cancelada del futuro no cuenta.
	turno("bk-it-f2g2-cancel", 4*time.Hour, "cancelled")
	if n := cuenta(); n != 1 {
		t.Fatalf("una cancelada futura no cuenta: %d", n)
	}
	// Otro turno futuro de la misma persona sí.
	turno("bk-it-f2g2-fut2", 30*time.Hour, "booked")
	if n := cuenta(); n != 2 {
		t.Fatalf("dos turnos futuros: %d", n)
	}
	// Otra persona en el mismo salón no cuenta.
	f2g2Exec(t, pool, `INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,service_id)
		VALUES('bk-it-f2g2-otra',$1,$2,'Clienta','',$3,$4,'booked',$5)`,
		f2g2SalonA, f2g2Recep, f2g2Now.Add(48*time.Hour), f2g2Now.Add(48*time.Hour+30*time.Minute), f2g2SvcA)
	// Ni el mismo turno futuro en el OTRO salón.
	f2g2Exec(t, pool, `INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status,service_id)
		VALUES('bk-it-f2g2-otro-salón',$1,$2,'Clienta','',$3,$4,'booked',$5)`,
		f2g2SalonB, f2g2Pro, f2g2Now.Add(72*time.Hour), f2g2Now.Add(72*time.Hour+30*time.Minute), f2g2SvcB)
	if n := cuenta(); n != 2 {
		t.Fatalf("el conteo va scopeado por salón y persona: %d", n)
	}

	// La misma cuenta dentro de la transacción de la baja.
	var dentro int
	err := repo.InMemberTx(func(tx ports.MemberTx) error {
		var err error
		dentro, err = tx.CountFutureBookings(f2g2SalonA, f2g2Pro, f2g2Now)
		return err
	})
	if err != nil || dentro != 2 {
		t.Fatalf("CountFutureBookings en la tx = %d, %v", dentro, err)
	}
	// La baja se rechaza por contrato (409 del caso de uso) sin tocar nada.
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || !m.Active() {
		t.Fatalf("con turnos futuros la membresía sigue activa: %+v, %v", m, err)
	}
}

// S-F2g2-4: los UPDATE son condicionales por ended_at. Baja dos veces -> la
// segunda ErrNotFound; reactivar a alguien activo -> ErrNotFound; reactivar
// conserva rol, attends y oficio.
func TestMembers_IT_deactivate_reactivate_conditional(t *testing.T) {
	pool, repo := f2g2Fixture(t)

	if err := repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
		t.Fatalf("primera baja: %v", err)
	}
	if err := repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now.Add(time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la segunda baja tiene que ser ErrNotFound: %v", err)
	}
	// La fecha de la primera baja no se pisó.
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE user_id=$1 AND salon_id=$2 AND ended_at=$3`, f2g2Pro, f2g2SalonA, f2g2Now); n != 1 {
		t.Fatalf("dos bajas simultáneas no mueven la fecha: %d", n)
	}
	if err := repo.ReactivateMember(f2g2Pro, f2g2SalonA); err != nil {
		t.Fatalf("reactivar: %v", err)
	}
	if err := repo.ReactivateMember(f2g2Pro, f2g2SalonA); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reactivar a alguien activo tiene que ser ErrNotFound: %v", err)
	}
	m, err := repo.FindMember(f2g2Pro, f2g2SalonA)
	if err != nil || !m.Active() || m.EndedAt != nil {
		t.Fatalf("reactivar deja la membresía activa: %+v, %v", m, err)
	}
	if m.Role != domain.RoleProfessional || !m.Attends || m.Trade != "Barbera" {
		t.Fatalf("reactivar conserva rol, attends y oficio: %+v", m)
	}
	// Un miembro de otro salón (o inexistente) no se da de baja por este salón.
	if err := repo.DeactivateMember(f2g2Recep, f2g2SalonB, f2g2Now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la baja de otro salón es ErrNotFound: %v", err)
	}
	if err := repo.DeactivateMember("u-it-f2g2-nadie", f2g2SalonA, f2g2Now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la baja de alguien inexistente es ErrNotFound: %v", err)
	}
}

// S-F2g2-4: el upsert de la membresía (alta por email y aceptación de
// invitación) limpia ended_at, así las dos vías REACTIVAN a quien estaba de
// baja, con el rol pedido y una sola fila.
func TestMembers_IT_save_reactivates(t *testing.T) {
	pool, repo := f2g2Fixture(t)

	// El estado de partida se fuerza por SQL: lo que se prueba acá es el upsert
	// (la baja en sí tiene sus propios tests).
	f2g2Exec(t, pool, `UPDATE memberships SET ended_at=$3 WHERE user_id=$1 AND salon_id=$2`, f2g2Pro, f2g2SalonA, f2g2Now)
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || m.Active() {
		t.Fatalf("punto de partida: tiene que estar dada de baja: %+v, %v", m, err)
	}

	m, err := domain.NewMembership(f2g2Pro, f2g2SalonA, domain.RoleAdmin)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	m.Attends = true
	m.Trade = "Jefa"
	if err := repo.Save(m); err != nil {
		t.Fatalf("alta por email sobre una baja: %v", err)
	}
	got, err := repo.FindMember(f2g2Pro, f2g2SalonA)
	if err != nil {
		t.Fatalf("FindMember: %v", err)
	}
	if !got.Active() || got.EndedAt != nil {
		t.Fatalf("el upsert tiene que limpiar ended_at: %+v", got)
	}
	if got.Role != domain.RoleAdmin || !got.Attends || got.Trade != "Jefa" {
		t.Fatalf("el alta manda el rol y la marca pedidos: %+v", got)
	}
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE user_id=$1 AND salon_id=$2`, f2g2Pro, f2g2SalonA); n != 1 {
		t.Fatalf("reactivar no puede duplicar la fila: %d", n)
	}
	// Y vuelve a tener acceso por la lectura de siempre.
	if _, err := repo.Find(f2g2Pro, f2g2SalonA); err != nil {
		t.Fatalf("Find después de reactivar: %v", err)
	}
}

// S-F2g2-4: ocho bajas simultáneas de la misma persona -> exactamente una gana,
// las otras siete reciben ErrNotFound y queda UNA fila consistente.
func TestMembers_IT_deactivate_concurrent(t *testing.T) {
	pool, repo := f2g2Fixture(t)

	const n = 8
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now.Add(time.Duration(i)*time.Second))
		}(i)
	}
	close(start)
	wg.Wait()

	gana, pierden := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			gana++
		case errors.Is(err, domain.ErrNotFound):
			pierden++
		default:
			t.Fatalf("goroutine %d: error crudo en la carrera: %v", i, err)
		}
	}
	if gana != 1 || pierden != n-1 {
		t.Fatalf("una sola baja puede ganar: %d ganaron, %d ErrNotFound (%v)", gana, pierden, errs)
	}
	if c := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE user_id=$1 AND salon_id=$2`, f2g2Pro, f2g2SalonA); c != 1 {
		t.Fatalf("la carrera no puede dejar dos filas: %d", c)
	}
	m, err := repo.FindMember(f2g2Pro, f2g2SalonA)
	if err != nil || m.Active() || m.EndedAt == nil {
		t.Fatalf("la fila queda dada de baja: %+v, %v", m, err)
	}
}

// S-F2g2-4: una baja y una reactivación simultáneas dejan un estado consistente
// (una fila, activa o dada de baja) y nunca un error crudo del driver.
func TestMembers_IT_deactivate_reactivate_race(t *testing.T) {
	pool, repo := f2g2Fixture(t)

	// Sin esto la carrera sería vacía: las dos operaciones tienen que hacer algo.
	if err := repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now); err != nil {
		t.Fatalf("baja secuencial: %v", err)
	}
	if m, err := repo.FindMember(f2g2Pro, f2g2SalonA); err != nil || m.Active() {
		t.Fatalf("la baja secuencial se aplica: %+v, %v", m, err)
	}
	if err := repo.ReactivateMember(f2g2Pro, f2g2SalonA); err != nil {
		t.Fatalf("reactivación secuencial: %v", err)
	}

	for vuelta := 0; vuelta < 20; vuelta++ {
		// Punto de partida activo y conocido.
		f2g2Exec(t, pool, `UPDATE memberships SET ended_at=NULL WHERE user_id=$1 AND salon_id=$2`, f2g2Pro, f2g2SalonA)

		errs := make([]error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			errs[0] = repo.DeactivateMember(f2g2Pro, f2g2SalonA, f2g2Now)
		}()
		go func() {
			defer wg.Done()
			<-start
			errs[1] = repo.ReactivateMember(f2g2Pro, f2g2SalonA)
		}()
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("vuelta %d, op %d: error crudo en la carrera: %v", vuelta, i, err)
			}
		}
		if c := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE user_id=$1 AND salon_id=$2`, f2g2Pro, f2g2SalonA); c != 1 {
			t.Fatalf("vuelta %d: la carrera dejó %d filas", vuelta, c)
		}
		m, err := repo.FindMember(f2g2Pro, f2g2SalonA)
		if err != nil {
			t.Fatalf("vuelta %d: la fila existe: %v", vuelta, err)
		}
		if m.Active() != (m.EndedAt == nil) {
			t.Fatalf("vuelta %d: estado inconsistente: %+v", vuelta, m)
		}
		// Consistente = activa o dada de baja; y el estado se puede leer por las
		// dos vistas sin error crudo.
		_, ferr := repo.Find(f2g2Pro, f2g2SalonA)
		if ferr != nil && !errors.Is(ferr, domain.ErrNotFound) {
			t.Fatalf("vuelta %d: Find con error inesperado: %v", vuelta, ferr)
		}
	}
}

// PRESERVE: para cada query compartida que ahora filtra por ended_at, el
// comportamiento con miembros ACTIVOS es exactamente el de antes.
func TestMembers_IT_active_reads_preserved(t *testing.T) {
	pool, repo := f2g2Fixture(t)
	pros := pgstore.ProfessionalServices{Pool: pool}

	t.Run("Find", func(t *testing.T) {
		m, err := repo.Find(f2g2Pro, f2g2SalonA)
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if m.UserID != f2g2Pro || m.SalonID != f2g2SalonA || m.Role != domain.RoleProfessional || !m.Attends || m.Trade != "Barbera" {
			t.Fatalf("Find devuelve la membresía activa entera: %+v", m)
		}
	})
	t.Run("ListByUser", func(t *testing.T) {
		list, err := repo.ListByUser(f2g2Pro)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(list) != 2 || list[0].SalonID != f2g2SalonA || list[1].SalonID != f2g2SalonB {
			t.Fatalf("ListByUser trae los dos salones ordenados: %+v", list)
		}
	})
	t.Run("CountRole", func(t *testing.T) {
		if n, err := repo.CountRole(f2g2SalonA, domain.RoleOwner); err != nil || n != 1 {
			t.Fatalf("CountRole(owner) = %d, %v", n, err)
		}
		if n, err := repo.CountRole(f2g2SalonA, domain.RoleProfessional); err != nil || n != 1 {
			t.Fatalf("CountRole(professional) = %d, %v", n, err)
		}
	})
	t.Run("ListStaff", func(t *testing.T) {
		staff, err := repo.ListStaff(f2g2SalonA)
		if err != nil {
			t.Fatalf("ListStaff: %v", err)
		}
		if len(staff) != 2 || staff[0].ID != f2g2Owner || staff[1].ID != f2g2Pro {
			t.Fatalf("ListStaff lista a quien atiende, por nombre: %+v", staff)
		}
		if staff[1].Trade != "Barbera" || !staff[1].Attends {
			t.Fatalf("ListStaff trae el oficio: %+v", staff[1])
		}
	})
	t.Run("ListProfessionalsFor", func(t *testing.T) {
		got, err := pros.ListProfessionalsFor(f2g2SalonA, f2g2SvcA)
		if err != nil || len(got) != 1 || got[0].ID != f2g2Pro {
			t.Fatalf("ListProfessionalsFor: %+v, %v", got, err)
		}
	})
	t.Run("ListMembers", func(t *testing.T) {
		list, err := repo.ListMembers(f2g2SalonA)
		if err != nil || len(list) != 3 {
			t.Fatalf("ListMembers: %+v, %v", list, err)
		}
		for _, m := range list {
			if !m.Active || m.EndedAt != nil {
				t.Fatalf("sin bajas, todo el equipo está activo: %+v", m)
			}
		}
	})
	t.Run("CountProfessionals", func(t *testing.T) {
		if n, err := repo.CountProfessionals(f2g2SalonA); err != nil || n != 1 {
			t.Fatalf("CountProfessionals = %d, %v", n, err)
		}
	})
	t.Run("MembershipExists", func(t *testing.T) {
		if !f2g2MembershipExists(t, pool, f2g2SalonA, f2g2Pro) {
			t.Fatal("MembershipExists ve a la persona activa")
		}
		if f2g2MembershipExists(t, pool, f2g2SalonA, f2g2Recep+"-nadie") {
			t.Fatal("MembershipExists no inventa miembros")
		}
	})
	t.Run("Save_upsert", func(t *testing.T) {
		// El alta de alguien que ya está ACTIVE sigue cambiando el rol en la
		// misma fila (comportamiento de antes).
		m, err := domain.NewMembership(f2g2Recep, f2g2SalonA, domain.RoleProfessional)
		if err != nil {
			t.Fatalf("dominio: %v", err)
		}
		if err := repo.Save(m); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := repo.FindMember(f2g2Recep, f2g2SalonA)
		if err != nil {
			t.Fatalf("FindMember: %v", err)
		}
		if got.Role != domain.RoleProfessional || !got.Active() || got.EndedAt != nil {
			t.Fatalf("el upsert sobre una activa cambia el rol: %+v", got)
		}
		if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE user_id=$1 AND salon_id=$2`, f2g2Recep, f2g2SalonA); n != 1 {
			t.Fatalf("una sola fila por persona y salón: %d", n)
		}
	})
}

// S-F2g2-1: la migración 022 es idempotente, no hace backfill (todo lo que
// existe queda activo) y crea el índice parcial del equipo activo. Se corre en
// un esquema aislado con una memberships mínima y se revierte al final.
func TestMigration022_membership_ended_at(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "022_membership_ended_at.sql"))
	if err != nil {
		t.Fatalf("leer la migración 022: %v", err)
	}
	sql := string(raw)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	schema := fmt.Sprintf("f2g2_022_%d", time.Now().UnixNano())
	for _, q := range []string{
		"CREATE SCHEMA " + schema,
		"SET LOCAL search_path TO " + schema,
		`CREATE TABLE memberships (user_id TEXT NOT NULL, salon_id TEXT NOT NULL, role TEXT NOT NULL, PRIMARY KEY (user_id, salon_id))`,
		`INSERT INTO memberships(user_id,salon_id,role) VALUES ('u-viejo','s-viejo','owner')`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}

	// Dos pasadas: el runner reaplica todo en cada boot.
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("primera pasada de 022: %v", err)
	}
	if _, err := tx.Exec(ctx, sql); err != nil {
		t.Fatalf("segunda pasada de 022: %v", err)
	}

	var nullable string
	if err := tx.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_schema=$1 AND table_name='memberships' AND column_name='ended_at'`, schema).Scan(&nullable); err != nil {
		t.Fatalf("columna ended_at: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("ended_at tiene que ser NULLable: %q", nullable)
	}
	// Sin backfill: la fila vieja queda ACTIVA.
	var activas int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM memberships WHERE ended_at IS NULL`).Scan(&activas); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if activas != 1 {
		t.Fatalf("la migración no puede dar de baja a nadie: %d activas", activas)
	}
	// Índice parcial del equipo activo.
	var def string
	if err := tx.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND indexname='memberships_active_idx'`, schema).Scan(&def); err != nil {
		t.Fatalf("índice memberships_active_idx: %v", err)
	}
	if !strings.Contains(def, "(salon_id)") || !strings.Contains(def, "WHERE (ended_at IS NULL)") {
		t.Fatalf("el índice tiene que ser parcial por ended_at: %s", def)
	}
}

// migrationsDir es backend/migrations visto desde internal/team/postgres.
func migrationsDir(t *testing.T) string {
	t.Helper()
	return "../../../migrations"
}
