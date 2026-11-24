// Vínculos por id contra Postgres real (F5, D1/D2). Lo que importa acá es la
// garantía dura: el relleno de la migración es el del contrato, el vínculo
// sobrevive a un cambio de teléfono y borrar la ficha deja el historial en NULL.
// Corre con: SALON_IT=1 go test ./internal/adapters/postgres/ -run TestClientLinks -v
package pgstore

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// f5Migration reejecuta el archivo 028 sobre la base ya migrada: es la MISMA
// migración, así que una segunda pasada tiene que ser un no-op.
func f5Migration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "028_client_links.sql"))
	if err != nil {
		t.Fatalf("leer la migración: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("aplicar la migración 028: %v", err)
	}
}

// f5Booking inserta un turno directo (sin pasar por el repo: el vínculo no está
// en el INSERT, D1).
func f5Booking(t *testing.T, pool *pgxpool.Pool, id, salonID, profID, name, phone, status string, startsAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,status)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, salonID, profID, name, phone, startsAt, startsAt.Add(45*time.Minute), status)
	if err != nil {
		t.Fatalf("insertar turno %s: %v", id, err)
	}
}

// f5Sale inserta una venta directa, con client_id opcional.
func f5Sale(t *testing.T, pool *pgxpool.Pool, id, salonID, profID string, total int, method string, createdAt time.Time, clientID string) {
	t.Helper()
	var linked any
	if clientID != "" {
		linked = clientID
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,created_at,client_id)
		 VALUES($1,$2,$3,'Lía',$4,$5,$6,$7)`,
		id, salonID, profID, total, method, createdAt, linked)
	if err != nil {
		t.Fatalf("insertar venta %s: %v", id, err)
	}
}

func f5Scalar(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var v *string
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("scalar: %v", err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// f5Digest es el md5 de las filas SIN las columnas nuevas: es la prueba de que
// la migración no toca datos existentes.
func f5Digest(t *testing.T, pool *pgxpool.Pool, query string) string {
	t.Helper()
	var out []string
	rows, err := pool.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("digest scan: %v", err)
		}
		out = append(out, line)
	}
	sum := md5.Sum([]byte(strings.Join(out, "\n")))
	return hex.EncodeToString(sum[:])
}

func f5Digests(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	bookings := f5Digest(t, pool, `SELECT id||'|'||salon_id||'|'||professional_id||'|'||client_name||'|'||
		client_phone||'|'||starts_at::text||'|'||ends_at::text||'|'||status||'|'||created_at::text
		FROM bookings ORDER BY id`)
	sales := f5Digest(t, pool, `SELECT id||'|'||salon_id||'|'||professional_id||'|'||client_name||'|'||
		total_cents::text||'|'||method||'|'||created_at::text FROM sales ORDER BY id`)
	clients := f5Digest(t, pool, `SELECT id||'|'||salon_id||'|'||name||'|'||phone||'|'||email||'|'||
		notes||'|'||created_at::text FROM clients ORDER BY id`)
	return bookings + sales + clients
}

func f5Seed(t *testing.T, pool *pgxpool.Pool, salon, user string) {
	t.Helper()
	seedTenant(t, salon, user)
	_, _ = pool.Exec(context.Background(), `DELETE FROM sale_items WHERE sale_id IN (SELECT id FROM sales WHERE salon_id=$1)`, salon)
	_, _ = pool.Exec(context.Background(), `DELETE FROM sales WHERE salon_id=$1`, salon)
	_, _ = pool.Exec(context.Background(), `DELETE FROM bookings WHERE salon_id=$1`, salon)
	_, _ = pool.Exec(context.Background(), `DELETE FROM clients WHERE salon_id=$1`, salon)
}

// D2: el relleno solo vincula con coincidencia ÚNICA, nunca cruza de salón, no
// toca teléfonos vacíos y una segunda pasada no cambia nada. Los datos
// existentes siguen idénticos (md5 sin las columnas nuevas).
func TestClientLinks_migracion_y_relleno(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	f5Seed(t, pool, "s-f5a", "u-f5a")
	f5Seed(t, pool, "s-f5b", "u-f5b")
	repo := Clients{Pool: pool}

	// Salón A: una ficha única, una ficha duplicada (ambiguo) y otra sin teléfono.
	unica, _ := domain.NewClient("s-f5a", "Única", "11 5555-1234", "", "")
	dupA, _ := domain.NewClient("s-f5a", "Duplicada A", "11 6666-0000", "", "")
	dupB, _ := domain.NewClient("s-f5a", "Duplicada B", "11 6666-0000", "", "")
	sinTel, _ := domain.NewClient("s-f5a", "Sin teléfono", "", "", "")
	for _, c := range []domain.Client{unica, dupA, dupB, sinTel} {
		if err := repo.Save(c); err != nil {
			t.Fatalf("ficha: %v", err)
		}
	}
	// Salón B: el MISMO teléfono de la ficha única (nunca se vincula al turno A).
	otra, _ := domain.NewClient("s-f5b", "Otra", "11 5555-1234", "", "")
	if err := repo.Save(otra); err != nil {
		t.Fatalf("ficha ajena: %v", err)
	}

	day := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	f5Booking(t, pool, "bg-ok", "s-f5a", "u-f5a", "Única", "11 5555-1234", "done", day)
	f5Booking(t, pool, "bg-dup", "s-f5a", "u-f5a", "Duplicada", "11 6666-0000", "done", day)
	f5Booking(t, pool, "bg-empty", "s-f5a", "u-f5a", "Sin ficha", "", "booked", day)
	f5Booking(t, pool, "bg-none", "s-f5a", "u-f5a", "Nadie", "999", "booked", day.Add(2*time.Hour))
	f5Booking(t, pool, "bg-other", "s-f5b", "u-f5b", "Otra", "11 5555-1234", "done", day)
	// Una venta vieja: el relleno NO la toca (D2).
	f5Sale(t, pool, "sa-old", "s-f5a", "u-f5a", 1000, "cash", day, "")

	antes := f5Digests(t, pool)
	f5Migration(t, pool)
	despues := f5Digests(t, pool)
	if antes != despues {
		t.Fatalf("la migración cambió datos existentes:\n%s\n%s", antes, despues)
	}

	// Única coincidencia (con formato distinto): vincula.
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bg-ok'`); got != unica.ID {
		t.Fatalf("bg-ok: client_id %q, esperaba %q", got, unica.ID)
	}
	// Dos fichas con el mismo teléfono: ambiguo, queda NULL.
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bg-dup'`); got != "" {
		t.Fatalf("bg-dup debe quedar NULL, dio %q", got)
	}
	// Teléfono vacío y sin coincidencia: NULL.
	for _, id := range []string{"bg-empty", "bg-none"} {
		if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id=`+f5Quote(id)); got != "" {
			t.Fatalf("%s debe quedar NULL, dio %q", id, got)
		}
	}
	// Otro salón con el mismo teléfono: se vincula a la ficha de SU salón.
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bg-other'`); got != otra.ID {
		t.Fatalf("bg-other debe vincular la ficha de su salón %q, dio %q", otra.ID, got)
	}
	// La venta vieja sigue sin vínculo.
	if got := f5Scalar(t, pool, `SELECT client_id FROM sales WHERE id='sa-old'`); got != "" {
		t.Fatalf("una venta vieja no se rellena, dio %q", got)
	}

	// Segunda pasada: no toca filas ya vinculadas ni cambia el md5.
	unicaID := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bg-ok'`)
	f5Migration(t, pool)
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bg-ok'`); got != unicaID {
		t.Fatalf("la segunda pasada movió el vínculo: %q -> %q", unicaID, got)
	}
	if got := f5Digests(t, pool); got != despues {
		t.Fatalf("la segunda pasada cambió filas: %s vs %s", despues, got)
	}
}

func f5Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// D1/D3: el vínculo por id sobrevive a un cambio de teléfono; borrar la ficha
// deja turnos y ventas con client_id NULL (ON DELETE SET NULL). El vínculo de la
// venta tampoco cruza de salón.
func TestClientLinks_vinculo_estable_y_borrado(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	f5Seed(t, pool, "s-f5c", "u-f5c")
	f5Seed(t, pool, "s-f5d", "u-f5d")
	repo := Clients{Pool: pool}
	links := ClientLinks{Pool: pool}

	c, _ := domain.NewClient("s-f5c", "Lía", "11 5555-1234", "", "")
	if err := repo.Save(c); err != nil {
		t.Fatalf("ficha: %v", err)
	}
	ajena, _ := domain.NewClient("s-f5d", "Ajena", "11 5555-1234", "", "")
	if err := repo.Save(ajena); err != nil {
		t.Fatalf("ficha ajena: %v", err)
	}
	day := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	f5Booking(t, pool, "bc-1", "s-f5c", "u-f5c", "Lía", "11 5555-1234", "done", day)
	f5Sale(t, pool, "sc-1", "s-f5c", "u-f5c", 5000, "cash", day, "")

	// El vínculo por id funciona; el de otro salón no toca nada.
	if err := links.LinkSaleByID("s-f5d", "sc-1", ajena.ID); err != nil {
		t.Fatalf("vínculo ajeno: %v", err)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM sales WHERE id='sc-1'`); got != "" {
		t.Fatalf("una ficha de otro salón no debe vincular, dio %q", got)
	}
	if err := links.LinkBookingByID("s-f5c", "bc-1", c.ID); err != nil {
		t.Fatalf("vínculo turno: %v", err)
	}
	if err := links.LinkSaleByID("s-f5c", "sc-1", c.ID); err != nil {
		t.Fatalf("vínculo venta: %v", err)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bc-1'`); got != c.ID {
		t.Fatalf("turno sin vincular: %q", got)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM sales WHERE id='sc-1'`); got != c.ID {
		t.Fatalf("venta sin vincular: %q", got)
	}

	// Cambiar el teléfono de la ficha NO desvincula (D1).
	c.Phone = "11 9999-0000"
	if err := repo.Save(c); err != nil {
		t.Fatalf("cambiar teléfono: %v", err)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bc-1'`); got != c.ID {
		t.Fatalf("el turno se desvinculó al cambiar el teléfono: %q", got)
	}
	// Y el vínculo por teléfono de un turno nuevo ya no encuentra la ficha vieja
	// por el número anterior.
	f5Booking(t, pool, "bc-2", "s-f5c", "u-f5c", "Lía", "11 5555-1234", "booked", day.Add(24*time.Hour))
	if err := links.LinkBookingByPhone("s-f5c", "bc-2"); err != nil {
		t.Fatalf("vínculo por teléfono: %v", err)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bc-2'`); got != "" {
		t.Fatalf("el teléfono viejo ya no está en la ficha: %q", got)
	}

	// Borrar la ficha: SET NULL en turnos y ventas, nunca borrar el historial.
	if _, err := pool.Exec(context.Background(), `DELETE FROM clients WHERE id=$1 AND salon_id='s-f5c'`, c.ID); err != nil {
		t.Fatalf("borrar ficha: %v", err)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM bookings WHERE id='bc-1'`); got != "" {
		t.Fatalf("el turno debe quedar con client_id NULL, dio %q", got)
	}
	if got := f5Scalar(t, pool, `SELECT client_id FROM sales WHERE id='sc-1'`); got != "" {
		t.Fatalf("la venta debe quedar con client_id NULL, dio %q", got)
	}
	if got := f5Scalar(t, pool, `SELECT status FROM bookings WHERE id='bc-1'`); got != "done" {
		t.Fatalf("el turno no debe borrarse: %q", got)
	}
	if _, err := repo.FindByID("s-f5c", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la ficha borrada no existe: %v", err)
	}
}
