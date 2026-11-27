package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
)

// T12: la reserva pública acepta client_email. Viaja normalizado, se guarda para
// el recordatorio y NO aparece en el JSON del turno.
func TestHTTP_public_booking_email(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "reserva-email")
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z",` +
		`"client_email":" Lia@Mail.com "}`
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/salons/reserva-email/bookings", body, "")
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
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID == "" {
		t.Fatalf("reserva sin id: %s", w.Body.String())
	}

	msgs := env.Mailer.WaitCount(t, 1)
	if msgs[0].To != "lia@mail.com" {
		t.Fatalf("destinatario normalizado: %q", msgs[0].To)
	}
	if !strings.HasPrefix(msgs[0].Subject, "Turno confirmado · ") {
		t.Fatalf("asunto: %q", msgs[0].Subject)
	}
	saved := env.BookingEmails.SavedEmails()
	if len(saved) != 1 || saved[0] != salonID+"|"+out.Booking.ID+"|lia@mail.com" {
		t.Fatalf("fila del email: %v", saved)
	}
}

// Email inválido: 400 con el mensaje limpio y la reserva NO entra.
func TestHTTP_public_booking_email_invalido(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "reserva-mala")
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","starts_at":"2026-12-01T15:00:00Z","client_email":"sin-arroba"}`
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/salons/reserva-mala/bookings", body, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("email inválido: %d %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"Email inválido"}` {
		t.Fatalf("mensaje: %s", got)
	}
	if len(env.Bookings.All) != 0 {
		t.Fatalf("un email inválido no agenda: %+v", env.Bookings.All)
	}
	time.Sleep(30 * time.Millisecond)
	if env.Mailer.Count() != 0 || len(env.BookingEmails.SavedEmails()) != 0 {
		t.Fatal("un email inválido no guarda ni manda nada")
	}
}

// PRESERVE (comportamiento previo): la reserva pública sin client_email sigue
// respondiendo 201 con la misma forma y no manda ningún correo.
func TestHTTP_public_booking_sin_email_igual_que_antes(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "reserva-clasica")
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	salonSettings(env, salonID)
	_, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})

	body := `{"service_id":"` + svcID + `","professional_id":"` + profID +
		`","client_name":"Lía","client_phone":"555","starts_at":"2026-12-01T15:00:00Z"}`
	w := httpkit.Do(env.Router, "POST", "/api/v1/public/salons/reserva-clasica/bookings", body, "")
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
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out.Booking.ID == "" || out.Booking.ProfessionalID != profID ||
		out.Booking.StartsAt != "2026-12-01T15:00:00Z" || out.Booking.EndsAt != "2026-12-01T15:45:00Z" ||
		out.Booking.Status != "booked" {
		t.Fatalf("forma de la reserva: %s", w.Body.String())
	}
	time.Sleep(30 * time.Millisecond)
	if env.Mailer.Count() != 0 || len(env.BookingEmails.SavedEmails()) != 0 {
		t.Fatal("sin client_email no se manda ni se guarda nada")
	}
}
