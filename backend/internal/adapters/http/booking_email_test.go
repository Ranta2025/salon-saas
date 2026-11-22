package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// mmailer / memails: lo mínimo del puerto de email para verificar el contrato
// HTTP de la reserva pública (el adaptador SMTP tiene sus propios tests). El
// mutex es obligatorio: la confirmación viaja en goroutine y `go test -race` la
// ve.
type mmailer struct {
	mu   sync.Mutex
	sent []ports.Message
}

func (f *mmailer) Send(_ context.Context, msg ports.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

func (f *mmailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *mmailer) waitCount(t *testing.T, n int) []ports.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		sent := append([]ports.Message(nil), f.sent...)
		f.mu.Unlock()
		if len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperaba %d correos, hubo %d", n, len(sent))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type mbookingEmails struct {
	mu    sync.Mutex
	saved []string // "salon|booking|email"
}

func (f *mbookingEmails) Save(salonID, bookingID, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, salonID+"|"+bookingID+"|"+email)
	return nil
}

func (f *mbookingEmails) ClaimDueReminders(time.Time, int) ([]ports.DueReminder, error) {
	return nil, nil
}

func (f *mbookingEmails) MarkReminderSent(string) error { return nil }

// FindEmail: la lectura que usa F2d3 para avisar un cambio de turno.
func (f *mbookingEmails) FindEmail(salonID, bookingID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := salonID + "|" + bookingID + "|"
	for _, saved := range f.saved {
		if strings.HasPrefix(saved, prefix) {
			return strings.TrimPrefix(saved, prefix), nil
		}
	}
	return "", domain.ErrNotFound
}

func (f *mbookingEmails) savedEmails() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.saved...)
}

// T12: la reserva pública acepta client_email. Viaja normalizado, se guarda para
// el recordatorio y NO aparece en el JSON del turno.
func TestHTTP_public_booking_email(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "reserva-email")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z",` +
		`"client_email":" Lia@Mail.com "}`
	w := doReq(env.r, "POST", "/api/v1/public/salons/reserva-email/bookings", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva con email: %d %s", w.Code, w.Body.String())
	}
	// El email es dato de contacto, no parte del turno.
	if strings.Contains(w.Body.String(), "mail.com") || strings.Contains(w.Body.String(), "client_email") {
		t.Fatalf("el JSON del turno no lleva el email: %s", w.Body.String())
	}
	var out struct {
		Booking struct {
			ID string `json:"id"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID == "" {
		t.Fatalf("reserva sin id: %s", w.Body.String())
	}

	msgs := env.mailer.waitCount(t, 1)
	if msgs[0].To != "lia@mail.com" {
		t.Fatalf("destinatario normalizado: %q", msgs[0].To)
	}
	if !strings.HasPrefix(msgs[0].Subject, "Turno confirmado · ") {
		t.Fatalf("asunto: %q", msgs[0].Subject)
	}
	saved := env.emails.savedEmails()
	if len(saved) != 1 || saved[0] != salonID+"|"+out.Booking.ID+"|lia@mail.com" {
		t.Fatalf("fila del email: %v", saved)
	}
}

// Email inválido: 400 con el mensaje limpio y la reserva NO entra.
func TestHTTP_public_booking_email_invalido(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "reserva-mala")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","starts_at":"2026-12-01T15:00:00Z","client_email":"sin-arroba"}`
	w := doReq(env.r, "POST", "/api/v1/public/salons/reserva-mala/bookings", body, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("email inválido: %d %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"Email inválido"}` {
		t.Fatalf("mensaje: %s", got)
	}
	if len(env.bookings.all) != 0 {
		t.Fatalf("un email inválido no agenda: %+v", env.bookings.all)
	}
	time.Sleep(30 * time.Millisecond)
	if env.mailer.count() != 0 || len(env.emails.savedEmails()) != 0 {
		t.Fatal("un email inválido no guarda ni manda nada")
	}
}

// PRESERVE (comportamiento previo): la reserva pública sin client_email sigue
// respondiendo 201 con la misma forma y no manda ningún correo.
func TestHTTP_public_booking_sin_email_igual_que_antes(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "reserva-clasica")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z"}`
	w := doReq(env.r, "POST", "/api/v1/public/salons/reserva-clasica/bookings", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("reserva sin email: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Booking struct {
			ID             string `json:"id"`
			ProfessionalID string `json:"professional_id"`
			StartsAt       string `json:"starts_at"`
			EndsAt         string `json:"ends_at"`
			Status         string `json:"status"`
		} `json:"booking"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID == "" || out.Booking.ProfessionalID != profID ||
		out.Booking.StartsAt != "2026-12-01T15:00:00Z" || out.Booking.EndsAt != "2026-12-01T15:45:00Z" ||
		out.Booking.Status != "booked" {
		t.Fatalf("forma de la reserva: %s", w.Body.String())
	}
	time.Sleep(30 * time.Millisecond)
	if env.mailer.count() != 0 || len(env.emails.savedEmails()) != 0 {
		t.Fatal("sin client_email no se manda ni se guarda nada")
	}
}
