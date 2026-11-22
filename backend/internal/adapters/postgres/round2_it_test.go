// Ronda 2 contra Postgres real (SALON_IT=1): settings del salón, búsqueda y
// reprogramación de turnos con la EXCLUDE como árbitro, administración de
// miembros e índices B-19.
package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// round2Fixtures crea dos salones limpios (s-it-r2 y s-it-r2b) con usuarios.
func round2Fixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id IN ('s-it-r2','s-it-r2b')`,
		`DELETE FROM users WHERE id LIKE 'u-it-r2-%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
			('u-it-r2-owner','Ana Owner','u-it-r2-owner@test.com','x'),
			('u-it-r2-prof','Bruno Prof','u-it-r2-prof@test.com','x'),
			('u-it-r2-owner2','Eva Owner','u-it-r2-owner2@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES
			('s-it-r2','s-it-r2','Salón Ronda 2'),
			('s-it-r2b','s-it-r2b','Salón Ronda 2 B')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return pool, ctx
}

// Una base migrada tiene la configuración default; guardar y releer respeta la
// zona y el scope por salón.
func TestSalons_IT_settings(t *testing.T) {
	pool, ctx := round2Fixtures(t)
	salons := Salons{Pool: pool}

	got, err := salons.Settings("s-it-r2")
	if err != nil {
		t.Fatalf("settings default: %v", err)
	}
	if got.SalonID != "s-it-r2" || got.Timezone != domain.DefaultTimezone ||
		got.OpenTime != domain.DefaultOpenTime || got.CloseTime != domain.DefaultCloseTime ||
		got.SlotStepMin != domain.DefaultSlotStepMin {
		t.Fatalf("defaults de la migración: %+v", got)
	}

	settings, err := domain.NewSalonSettings("s-it-r2", "America/Bogota", "08:00", "17:30", 20)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := salons.SaveSettings(settings); err != nil {
		t.Fatalf("guardar settings: %v", err)
	}
	got, err = salons.Settings("s-it-r2")
	if err != nil {
		t.Fatalf("releer: %v", err)
	}
	if got.Timezone != "America/Bogota" || got.OpenTime != "08:00" || got.CloseTime != "17:30" || got.SlotStepMin != 20 {
		t.Fatalf("no persistió: %+v", got)
	}
	// El otro salón conserva sus defaults (scope por salón).
	other, err := salons.Settings("s-it-r2b")
	if err != nil || other.Timezone != domain.DefaultTimezone {
		t.Fatalf("settings del otro salón: %+v %v", other, err)
	}
	// Salón inexistente: ErrNotFound (no fila fantasma).
	if _, err := salons.Settings("s-no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("salón inexistente: %v", err)
	}
	sinSalon := settings
	sinSalon.SalonID = "s-no-existe"
	if err := salons.SaveSettings(sinSalon); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("guardar settings de salón inexistente: %v", err)
	}
	_ = ctx
}

// FindByID/Update con scope por salón y la EXCLUDE como árbitro final.
func TestBookings_IT_find_update_y_solape(t *testing.T) {
	pool, _ := round2Fixtures(t)
	repo := Bookings{Pool: pool}
	day := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)

	first, err := domain.NewBooking("s-it-r2", "u-it-r2-prof", "Lía", "555",
		day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Save(first); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	got, err := repo.FindByID("s-it-r2", first.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ClientName != "Lía" || !got.StartsAt.Equal(first.StartsAt) {
		t.Fatalf("releer: %+v", got)
	}
	// Scope: el id no existe en otro salón.
	if _, err := repo.FindByID("s-it-r2b", first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}

	// Reprogramar a las 12-13 y pasarlo a done.
	moved, err := first.Reschedule("", day.Add(12*time.Hour), day.Add(13*time.Hour))
	if err != nil {
		t.Fatalf("dominio reschedule: %v", err)
	}
	if err := repo.Update(moved); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = repo.FindByID("s-it-r2", first.ID)
	if !got.StartsAt.Equal(day.Add(12 * time.Hour)) {
		t.Fatalf("no persisitió el horario: %+v", got)
	}

	// Un segundo turno en 14-15 y el primero reprogramado encima: 409 (EXCLUDE).
	second, _ := domain.NewBooking("s-it-r2", "u-it-r2-prof", "Ana", "", day.Add(14*time.Hour), day.Add(15*time.Hour))
	if err := repo.Save(second); err != nil {
		t.Fatalf("segundo: %v", err)
	}
	clash, err := got.Reschedule("", second.StartsAt, second.EndsAt)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Update(clash); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("solape debe dar ErrAlreadyExists, dio %v", err)
	}
	// La fila quedó como estaba (el UPDATE rechazado no escribe a medias).
	got, _ = repo.FindByID("s-it-r2", first.ID)
	if !got.StartsAt.Equal(day.Add(12 * time.Hour)) {
		t.Fatalf("el solape no puede tocar la fila: %+v", got)
	}
	// Update de un turno inexistente: ErrNotFound.
	ghost, _ := domain.NewBooking("s-it-r2", "u-it-r2-prof", "Nadie", "", day.Add(16*time.Hour), day.Add(17*time.Hour))
	if err := repo.Update(ghost); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update inexistente: %v", err)
	}
	// La cancelación libera el hueco: el EXCLUDE solo mira status='booked'.
	cancelled, _ := got.SetStatus(domain.BookingCancelled)
	if err := repo.Update(cancelled); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if free, err := repo.FindOverlapping("s-it-r2", "u-it-r2-prof", day.Add(12*time.Hour), day.Add(13*time.Hour)); err != nil || len(free) != 0 {
		t.Fatalf("cancelada debe liberar: %+v %v", free, err)
	}
}

// Índices B-19: agenda por salón+fecha y memberships por salón.
func TestIndexes_IT_B19(t *testing.T) {
	pool, ctx := round2Fixtures(t)
	for _, idx := range []string{"bookings_salon_starts_idx", "memberships_salon_idx"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname=$1)`, idx).Scan(&exists); err != nil {
			t.Fatalf("pg_indexes: %v", err)
		}
		if !exists {
			t.Fatalf("falta el índice %s (finding B-19)", idx)
		}
	}
}

// Pantalla de equipo: listar con nombre/email, contar por rol y quitar.
func TestMemberships_IT_admin(t *testing.T) {
	pool, _ := round2Fixtures(t)
	mems := Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-r2-owner", SalonID: "s-it-r2", Role: domain.RoleOwner},
		{UserID: "u-it-r2-prof", SalonID: "s-it-r2", Role: domain.RoleProfessional},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}

	list, err := mems.ListMembers("s-it-r2")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba 2, dio %+v", list)
	}
	emails := map[string]string{}
	for _, m := range list {
		if m.ID == "" || m.Name == "" || m.Email == "" || m.Role == "" {
			t.Fatalf("miembro incompleto: %+v", m)
		}
		emails[m.ID] = m.Email
	}
	if emails["u-it-r2-prof"] != "u-it-r2-prof@test.com" {
		t.Fatalf("email del profesional: %+v", emails)
	}
	// El otro salón no ve esta gente.
	if other, err := mems.ListMembers("s-it-r2b"); err != nil || len(other) != 0 {
		t.Fatalf("otro salón: %+v %v", other, err)
	}
	// CountRole scopeado.
	if n, err := mems.CountRole("s-it-r2", domain.RoleOwner); err != nil || n != 1 {
		t.Fatalf("owners: %d %v", n, err)
	}
	if n, err := mems.CountRole("s-it-r2b", domain.RoleOwner); err != nil || n != 0 {
		t.Fatalf("owners del otro salón: %d %v", n, err)
	}
	// DeleteMember con scope: el mismo user en otro salón no se borra.
	_ = mems.Save(domain.Membership{UserID: "u-it-r2-prof", SalonID: "s-it-r2b", Role: domain.RoleProfessional})
	if err := mems.DeleteMember("u-it-r2-prof", "s-it-r2"); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if err := mems.DeleteMember("u-it-r2-prof", "s-it-r2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar dos veces: %v", err)
	}
	if _, err := mems.Find("u-it-r2-prof", "s-it-r2b"); err != nil {
		t.Fatalf("el membership del otro salón debe seguir: %v", err)
	}
}

// La EXCLUDE de Postgres es el árbitro final de la reserva pública: dos
// personas pidiendo el mismo hueco a la vez dejan pasar una y la otra recibe
// ErrAlreadyExists (409). Corre contra la DB real con SALON_IT=1.
func TestPublicBooking_IT_carrera_un_solo_gana(t *testing.T) {
	pool, _ := round2Fixtures(t)
	salons := Salons{Pool: pool}
	services := Services{Pool: pool}
	mems := Memberships{Pool: pool}
	bookings := Bookings{Pool: pool}

	if err := mems.Save(domain.Membership{UserID: "u-it-r2-prof", SalonID: "s-it-r2", Role: domain.RoleProfessional}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	settings, _ := domain.NewSalonSettings("s-it-r2", "America/Mexico_City", "09:00", "19:00", 30)
	if err := salons.SaveSettings(settings); err != nil {
		t.Fatalf("settings: %v", err)
	}
	svc, err := domain.NewService("s-it-r2", "Corte carrera", 45, 150000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	// F2c: la reserva pública solo asigna a quien HACE el servicio.
	professionals := ProfessionalServices{Pool: pool}
	if err := professionals.SetServices("s-it-r2", "u-it-r2-prof", []string{svc.ID}); err != nil {
		t.Fatalf("servicios de la profesional: %v", err)
	}
	svcBooking := app.PublicBookingService{
		Salons: salons, Services: services, Settings: salons, Staff: mems,
		Bookings: bookings, Professionals: professionals,
	}
	start := time.Date(2027, 3, 1, 16, 0, 0, 0, time.UTC) // 10:00 CDMX

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svcBooking.Book("s-it-r2", app.PublicBookingInput{
				ServiceID:  svc.ID,
				ClientName: "Cliente",
				StartsAt:   start,
			})
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrAlreadyExists):
			conflicts++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("esperaba 1 ganador + 1 conflicto, dio wins=%d conflicts=%d (%v)", wins, conflicts, errs)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings WHERE salon_id='s-it-r2' AND starts_at=$1`, start).Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if n != 1 {
		t.Fatalf("debe quedar UN turno en ese hueco, hay %d", n)
	}
}
