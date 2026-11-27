// T12 contra Postgres real (SALON_IT=1): email del turno y cola de
// recordatorios. La consulta del claim es la pieza delicada: tiene que ser
// atómica, respetar los reintentos y no cruzar salones.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil/itdb"
)

// bookingEmailFixtures reusa los dos salones de la ronda 2 y los deja sin
// reservas (los emails cuelgan de bookings con ON DELETE CASCADE).
func bookingEmailFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool, ctx := itdb.Round2Fixtures(t)
	if _, err := pool.Exec(ctx, `DELETE FROM bookings WHERE salon_id IN ('s-it-r2','s-it-r2b')`); err != nil {
		t.Fatalf("limpieza de reservas: %v", err)
	}
	return pool, ctx
}

// seedBookingEmail inserta una reserva con created_at explícito: la regla del
// recordatorio compara created_at con starts_at - 24 h.
func seedBookingEmail(t *testing.T, pool *pgxpool.Pool, salonID, bookingID, clientName string,
	startsAt, createdAt time.Time, status domain.BookingStatus) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO bookings(id,salon_id,professional_id,client_name,starts_at,ends_at,status,created_at)
		 VALUES($1,$2,'u-it-r2-prof',$3,$4,$5,$6,$7)`,
		bookingID, salonID, clientName, startsAt, startsAt.Add(45*time.Minute), string(status), createdAt)
	if err != nil {
		t.Fatalf("fixture reserva %s: %v", bookingID, err)
	}
}

func saveEmail(t *testing.T, repo BookingEmails, salonID, bookingID, email string) {
	t.Helper()
	if err := repo.Save(salonID, bookingID, email); err != nil {
		t.Fatalf("guardar email de %s: %v", bookingID, err)
	}
}

// Claim: solo los turnos que vencen en [now, now+24h), creados con al menos
// 24 h de anticipación y todavía activos.
func TestBookingEmails_IT_claim_due(t *testing.T) {
	pool, _ := bookingEmailFixtures(t)
	repo := BookingEmails{Pool: pool}
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	dueStart := now.Add(2 * time.Hour)
	createdEarly := dueStart.Add(-48 * time.Hour)

	seedBookingEmail(t, pool, "s-it-r2", "b-due-1", "Lía", dueStart, createdEarly, domain.BookingBooked)
	// Reserva hecha con menos de 24 h de anticipación: ya recibió su
	// confirmación, no lleva recordatorio una hora después.
	seedBookingEmail(t, pool, "s-it-r2", "b-late", "Ana", now.Add(3*time.Hour), now.Add(-time.Hour), domain.BookingBooked)
	// Fuera de la ventana de 24 h.
	seedBookingEmail(t, pool, "s-it-r2", "b-far", "Eva", now.Add(30*time.Hour), now.Add(6*time.Hour), domain.BookingBooked)
	// Ya pasó / cancelado.
	seedBookingEmail(t, pool, "s-it-r2", "b-past", "Iris", now.Add(-time.Hour), now.Add(-72*time.Hour), domain.BookingBooked)
	seedBookingEmail(t, pool, "s-it-r2", "b-cancel", "Sol", now.Add(4*time.Hour), now.Add(-72*time.Hour), domain.BookingCancelled)
	for _, id := range []string{"b-due-1", "b-late", "b-far", "b-past", "b-cancel"} {
		saveEmail(t, repo, "s-it-r2", id, id+"@mail.com")
	}

	due, err := repo.ClaimDueReminders(now, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("esperaba un solo recordatorio, hubo %d: %+v", len(due), due)
	}
	got := due[0]
	if got.BookingID != "b-due-1" || got.SalonID != "s-it-r2" || got.SalonName != "Salón Ronda 2" ||
		got.ClientName != "Lía" || got.Email != "b-due-1@mail.com" || !got.StartsAt.Equal(dueStart) {
		t.Fatalf("recordatorio reclamado: %+v", got)
	}
	// Ya enviado: no vuelve nunca.
	if err := repo.MarkReminderSent("b-due-1"); err != nil {
		t.Fatalf("marcar enviado: %v", err)
	}
	if again, err := repo.ClaimDueReminders(now, 10); err != nil || len(again) != 0 {
		t.Fatalf("después de enviado no debe volver: %+v %v", again, err)
	}
	// El estado de las filas que NO vencían no se tocó.
	var attempts int
	if err := pool.QueryRow(context.Background(),
		`SELECT reminder_attempts FROM booking_emails WHERE booking_id='b-late'`).Scan(&attempts); err != nil {
		t.Fatalf("leer intentos de b-late: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("una reserva no vencida no se reclama (intentos=%d)", attempts)
	}
}

// Reintentos: el claim toma el recordatorio, lo bloquea 5 minutos y se rinde a
// los 3 intentos. Un fallo de envío no pierde el recordatorio para siempre.
func TestBookingEmails_IT_claim_retries(t *testing.T) {
	pool, ctx := bookingEmailFixtures(t)
	repo := BookingEmails{Pool: pool}
	now := time.Date(2027, 3, 2, 12, 0, 0, 0, time.UTC)
	start := now.Add(2 * time.Hour)
	seedBookingEmail(t, pool, "s-it-r2", "b-retry", "Lía", start, start.Add(-48*time.Hour), domain.BookingBooked)
	saveEmail(t, repo, "s-it-r2", "b-retry", "lia@mail.com")

	claim := func(when time.Time) ([]ports.DueReminder, error) { return repo.ClaimDueReminders(when, 10) }

	first, err := claim(now)
	if err != nil || len(first) != 1 {
		t.Fatalf("primer claim: %+v %v", first, err)
	}
	// Reclamo reciente (sin marcar enviado): el otro proceso no lo toma.
	if again, err := claim(now); err != nil || len(again) != 0 {
		t.Fatalf("reclamo dentro de los 5 minutos: %+v %v", again, err)
	}
	// Pasados los 5 minutos, se reintenta (intento 2).
	if _, err := pool.Exec(ctx,
		`UPDATE booking_emails SET reminder_claimed_at = now() - interval '6 minutes' WHERE booking_id='b-retry'`); err != nil {
		t.Fatalf("liberar reclamo: %v", err)
	}
	second, err := claim(now)
	if err != nil || len(second) != 1 {
		t.Fatalf("reintento: %+v %v", second, err)
	}
	// Tercer intento y después nada: el tope es 3.
	if _, err := pool.Exec(ctx,
		`UPDATE booking_emails SET reminder_claimed_at = now() - interval '6 minutes' WHERE booking_id='b-retry'`); err != nil {
		t.Fatalf("liberar reclamo: %v", err)
	}
	if third, err := claim(now); err != nil || len(third) != 1 {
		t.Fatalf("tercer intento: %+v %v", third, err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE booking_emails SET reminder_claimed_at = now() - interval '6 minutes' WHERE booking_id='b-retry'`); err != nil {
		t.Fatalf("liberar reclamo: %v", err)
	}
	if none, err := claim(now); err != nil || len(none) != 0 {
		t.Fatalf("agotados los 3 intentos no debe volver: %+v %v", none, err)
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT reminder_attempts FROM booking_emails WHERE booking_id='b-retry'`).Scan(&attempts); err != nil {
		t.Fatalf("intentos: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("esperaba 3 intentos, hay %d", attempts)
	}
}

// Aislamiento: el claim cruza booking_emails con la reserva y el salón. Una fila
// de un salón que apunta a una reserva de otro NO se reclama (no se filtra un
// turno ajeno por email).
func TestBookingEmails_IT_claim_tenant_isolation(t *testing.T) {
	pool, ctx := bookingEmailFixtures(t)
	repo := BookingEmails{Pool: pool}
	now := time.Date(2027, 3, 3, 12, 0, 0, 0, time.UTC)
	start := now.Add(2 * time.Hour)
	created := start.Add(-48 * time.Hour)

	seedBookingEmail(t, pool, "s-it-r2", "b-own-r2", "Lía", start, created, domain.BookingBooked)
	seedBookingEmail(t, pool, "s-it-r2b", "b-own-r2b", "Ana", start.Add(30*time.Minute), created, domain.BookingBooked)
	saveEmail(t, repo, "s-it-r2", "b-own-r2", "lia@mail.com")
	saveEmail(t, repo, "s-it-r2b", "b-own-r2b", "ana@mail.com")
	// Fila cruzada: una reserva de B con el email registrado a nombre de A.
	seedBookingEmail(t, pool, "s-it-r2b", "b-cross", "Sol", start.Add(2*time.Hour), created, domain.BookingBooked)
	if _, err := pool.Exec(ctx,
		`INSERT INTO booking_emails(booking_id,salon_id,email) VALUES('b-cross','s-it-r2','cruzado@mail.com')`); err != nil {
		t.Fatalf("fila cruzada: %v", err)
	}

	due, err := repo.ClaimDueReminders(now, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("esperaba un recordatorio por salón (2), hubo %d: %+v", len(due), due)
	}
	for _, d := range due {
		want := map[string]struct{ salon, name, email string }{
			"b-own-r2":  {"s-it-r2", "Salón Ronda 2", "lia@mail.com"},
			"b-own-r2b": {"s-it-r2b", "Salón Ronda 2 B", "ana@mail.com"},
		}[d.BookingID]
		if d.SalonID != want.salon || d.SalonName != want.name || d.Email != want.email {
			t.Fatalf("recordatorio de otro salón: %+v", d)
		}
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT reminder_attempts FROM booking_emails WHERE booking_id='b-cross'`).Scan(&attempts); err != nil {
		t.Fatalf("leer fila cruzada: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("la fila cruzada no se puede reclamar (intentos=%d)", attempts)
	}
}

// Save/MarkReminderSent: el email de un turno no se duplica ni se pisa, y
// marcar un turno inexistente es ErrNotFound.
func TestBookingEmails_IT_save_y_mark(t *testing.T) {
	pool, ctx := bookingEmailFixtures(t)
	repo := BookingEmails{Pool: pool}
	start := time.Date(2027, 3, 4, 15, 0, 0, 0, time.UTC)
	seedBookingEmail(t, pool, "s-it-r2", "b-save", "Lía", start, start.Add(-48*time.Hour), domain.BookingBooked)

	saveEmail(t, repo, "s-it-r2", "b-save", "Lia@Mail.com")
	// Un segundo guardado del mismo turno no cambia la dirección.
	saveEmail(t, repo, "s-it-r2", "b-save", "otra@mail.com")
	var email string
	if err := pool.QueryRow(ctx,
		`SELECT email FROM booking_emails WHERE booking_id='b-save'`).Scan(&email); err != nil {
		t.Fatalf("leer email: %v", err)
	}
	if email != "Lia@Mail.com" {
		t.Fatalf("el email guardado no se puede pisar: %q", email)
	}
	// Reserva inexistente: la FK da ErrValidation.
	if err := repo.Save("s-it-r2", "b-no-existe", "x@mail.com"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("email de una reserva inexistente: %v", err)
	}
	if err := repo.MarkReminderSent("b-no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("marcar un recordatorio inexistente: %v", err)
	}
	// Borrar la reserva se lleva el email (ON DELETE CASCADE), sin tocar la
	// tabla de reservas en el camino normal.
	if _, err := pool.Exec(ctx, `DELETE FROM bookings WHERE id='b-save'`); err != nil {
		t.Fatalf("borrar reserva: %v", err)
	}
	var still bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM booking_emails WHERE booking_id='b-save')`).Scan(&still); err != nil {
		t.Fatalf("exists: %v", err)
	}
	if still {
		t.Fatal("el email del turno debe irse con la reserva")
	}
}

var _ ports.BookingEmailRepository = BookingEmails{}
