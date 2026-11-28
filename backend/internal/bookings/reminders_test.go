package bookings_test

import (
	"context"
	"errors"
	"salon-saas/internal/testutil"
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// newReminderEnv: salón s1 en CDMX y una cola vacía. La configuración del salón
// es la misma del resto de los tests (round2_fakes_test.go).
func newReminderEnv(t *testing.T) (bkg.ReminderService, *testutil.FakeBookingEmails, *testutil.FakeMailer, *testutil.FakeSettings) {
	t.Helper()
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	emails := &testutil.FakeBookingEmails{}
	mailer := &testutil.FakeMailer{}
	return bkg.ReminderService{Emails: emails, Mailer: mailer, Settings: settings}, emails, mailer, settings
}

func dueFor(bookingID, email string, startsAt time.Time) ports.DueReminder {
	return ports.DueReminder{
		BookingID: bookingID, SalonID: "s1", SalonName: "Navajas de Oro",
		ClientName: "Lía", Email: email, StartsAt: startsAt,
	}
}

// T12: la tarea periódica manda el recordatorio de los turnos que vencen en las
// próximas 24 h y, después de enviado, no lo repite.
func TestReminderService_runOnce(t *testing.T) {
	svc, emails, mailer, _ := newReminderEnv(t)
	emails.Due = []ports.DueReminder{dueFor("b1", "lia@mail.com", time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))}

	now := time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC)
	sent, failed := svc.RunOnce(context.Background(), now)
	if sent != 1 || failed != 0 {
		t.Fatalf("RunOnce: enviados=%d fallidos=%d", sent, failed)
	}
	msgs := mailer.WaitCount(t, 1)
	msg := msgs[0]
	if msg.To != "lia@mail.com" {
		t.Fatalf("destinatario: %q", msg.To)
	}
	if msg.Subject != "Recordatorio de tu turno · Navajas de Oro" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	for _, want := range []string{"Lía", "Navajas de Oro", "01/12 09:00 (America/Mexico_City)", "escribile al salón"} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("falta %q en el texto:\n%s", want, msg.Text)
		}
	}
	// Sin ids ni tokens: el correo lo lee una persona, no un programador.
	for _, forbidden := range []string{"b1", "s1"} {
		if strings.Contains(msg.Text, forbidden) {
			t.Fatalf("el texto no puede llevar ids (%q):\n%s", forbidden, msg.Text)
		}
	}
	if !emails.IsSent("b1") {
		t.Fatal("después de enviar, el recordatorio se marca enviado")
	}
	// Segundo tick inmediato: nada.
	if sent, failed := svc.RunOnce(context.Background(), now.Add(time.Minute)); sent != 0 || failed != 0 {
		t.Fatalf("segundo RunOnce: enviados=%d fallidos=%d", sent, failed)
	}
	if mailer.Count() != 1 {
		t.Fatalf("un recordatorio ya enviado no se repite: %d", mailer.Count())
	}
}

// Un fallo de envío no pierde el recordatorio ni frena al resto del lote.
func TestReminderService_fallo_no_frena_el_lote(t *testing.T) {
	svc, emails, mailer, _ := newReminderEnv(t)
	emails.Due = []ports.DueReminder{
		dueFor("b1", "falla@mail.com", time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)),
		dueFor("b2", "ana@mail.com", time.Date(2026, 12, 1, 16, 0, 0, 0, time.UTC)),
	}
	mailer.ErrFor = map[string]error{"falla@mail.com": errors.New("smtp caído")}

	now := time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC)
	sent, failed := svc.RunOnce(context.Background(), now)
	if sent != 1 || failed != 1 {
		t.Fatalf("enviados=%d fallidos=%d", sent, failed)
	}
	if emails.IsSent("b1") {
		t.Fatal("un envío fallido NO se marca enviado (tiene que reintentarse)")
	}
	if !emails.IsSent("b2") {
		t.Fatal("el segundo recordatorio del lote sí se envía")
	}
	// El reintento del siguiente tick vuelve a intentar SOLO el fallido.
	delete(mailer.ErrFor, "falla@mail.com")
	if sent, failed := svc.RunOnce(context.Background(), now.Add(time.Minute)); sent != 1 || failed != 0 {
		t.Fatalf("reintento: enviados=%d fallidos=%d", sent, failed)
	}
	if mailer.Count() != 2 {
		t.Fatalf("esperaba 2 envíos exitosos, hubo %d", mailer.Count())
	}
}

// Configuración del salón ilegible: el recordatorio sale igual, con la hora UTC
// a la vista. Un correo con hora es mejor que un recordatorio perdido.
func TestReminderService_settings_rota(t *testing.T) {
	svc, emails, mailer, settings := newReminderEnv(t)
	settings.BySalon = nil
	settings.Err = errors.New("db caída")
	emails.Due = []ports.DueReminder{dueFor("b1", "lia@mail.com", time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))}

	sent, failed := svc.RunOnce(context.Background(), time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC))
	if sent != 1 || failed != 0 {
		t.Fatalf("enviados=%d fallidos=%d", sent, failed)
	}
	msgs := mailer.WaitCount(t, 1)
	if !strings.Contains(msgs[0].Text, "01/12 15:00 (UTC)") {
		t.Fatalf("sin configuración, la hora sale en UTC:\n%s", msgs[0].Text)
	}
}

// Sin repositorio o sin sender (email deshabilitado) la tarea es un no-op: no
// reclama nada y no rompe el proceso.
func TestReminderService_deshabilitado(t *testing.T) {
	emails := &testutil.FakeBookingEmails{Due: []ports.DueReminder{dueFor("b1", "l@mail.com", time.Now())}}
	if sent, failed := (bkg.ReminderService{Emails: emails}).RunOnce(context.Background(), time.Now()); sent != 0 || failed != 0 {
		t.Fatalf("sin sender: enviados=%d fallidos=%d", sent, failed)
	}
	if emails.Claims != 0 {
		t.Fatal("sin sender no se reclama nada")
	}
	if sent, failed := (bkg.ReminderService{Mailer: &testutil.FakeMailer{}}).RunOnce(context.Background(), time.Now()); sent != 0 || failed != 0 {
		t.Fatalf("sin repositorio: enviados=%d fallidos=%d", sent, failed)
	}
}
