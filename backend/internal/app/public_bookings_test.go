package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func publicInput() PublicBookingInput {
	return PublicBookingInput{
		ServiceID:   "",
		ClientName:  "Lía",
		ClientPhone: "555",
		StartsAt:    time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC),
	}
}

// T6: POST /public/salons/:slug/bookings. La duración la pone el servicio y el
// hueco lo elige el cliente; el profesional es opcional.
func TestPublicBooking_con_profesional(t *testing.T) {
	env := newPublicEnv(t)
	in := publicInput()
	in.ServiceID = env.corte.ID
	in.ProfessionalID = "prof1"

	b, err := env.book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if b.ProfessionalID != "prof1" || b.ClientName != "Lía" || b.ClientPhone != "555" || b.Status != domain.BookingBooked {
		t.Fatalf("reserva: %+v", b)
	}
	// 09:00 local + 45 min de servicio.
	if b.StartsAt.Format(time.RFC3339) != "2026-12-01T15:00:00Z" || b.EndsAt.Format(time.RFC3339) != "2026-12-01T15:45:00Z" {
		t.Fatalf("horario: %v - %v", b.StartsAt, b.EndsAt)
	}
	// Publica el evento para que el calendario del salón se actualice solo.
	if ev, n := env.events.typed("booking.created"); n != 1 {
		t.Fatalf("esperaba un booking.created, hubo %d (%+v)", n, env.events.All)
	} else if data, ok := ev.Data.(map[string]any); !ok || data["id"] != b.ID {
		t.Fatalf("payload del evento: %+v", ev.Data)
	}
	if env.notifier.calls != 1 {
		t.Fatalf("la reserva pública debe dejar un aviso, hubo %d", env.notifier.calls)
	}

	// El mismo hueco ya no está: 409 de dominio.
	in.ClientName = "Ana"
	if _, err := env.book.Book("navajas-oro", in); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("hueco tomado debe dar AlreadyExists, dio %v", err)
	}
}

func TestPublicBooking_sin_profesional_elige_uno_libre(t *testing.T) {
	env := newPublicEnv(t)
	start := time.Date(2026, 12, 1, 15, 30, 0, 0, time.UTC) // 09:30 local
	// prof1 ya tiene ese hueco.
	ocupada, _ := domain.NewBooking("s1", "prof1", "Ocupa", "", start, start.Add(45*time.Minute))
	_ = env.bookings.Save(ocupada)

	in := publicInput()
	in.ServiceID = env.corte.ID
	in.StartsAt = start
	b, err := env.book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar sin profesional: %v", err)
	}
	if b.ProfessionalID != "prof2" {
		t.Fatalf("debe elegir al profesional libre (prof2), dio %q", b.ProfessionalID)
	}
	// Con todos ocupados: 409, no un turno sin profesional.
	in.StartsAt = start.Add(30 * time.Minute)
	_ = env.bookings.Save(domain.Booking{ID: "x1", SalonID: "s1", ProfessionalID: "prof2", StartsAt: in.StartsAt, EndsAt: in.StartsAt.Add(45 * time.Minute), Status: domain.BookingBooked})
	if _, err := env.book.Book("navajas-oro", in); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin profesionales libres: %v", err)
	}
}

func TestPublicBooking_validacion(t *testing.T) {
	env := newPublicEnv(t)
	base := func() PublicBookingInput {
		in := publicInput()
		in.ServiceID = env.corte.ID
		return in
	}
	// Datos faltantes / inválidos: 400 de dominio, nunca 500.
	cases := []struct {
		name string
		mut  func(*PublicBookingInput)
	}{
		{"sin servicio", func(in *PublicBookingInput) { in.ServiceID = "" }},
		{"servicio inexistente", func(in *PublicBookingInput) { in.ServiceID = "svc-no" }},
		{"servicio inactivo", func(in *PublicBookingInput) { in.ServiceID = env.viejo.ID }},
		{"sin nombre", func(in *PublicBookingInput) { in.ClientName = "" }},
		{"sin fecha", func(in *PublicBookingInput) { in.StartsAt = time.Time{} }},
		{"en el pasado", func(in *PublicBookingInput) { in.StartsAt = time.Now().UTC().Add(-48 * time.Hour) }},
		{"antes de abrir", func(in *PublicBookingInput) { in.StartsAt = time.Date(2026, 12, 1, 14, 0, 0, 0, time.UTC) }},
		{"termina después de cerrar", func(in *PublicBookingInput) { in.StartsAt = time.Date(2026, 12, 1, 17, 30, 0, 0, time.UTC) }},
		{"profesional ajeno", func(in *PublicBookingInput) { in.ProfessionalID = "prof-ajeno" }},
	}
	for _, c := range cases {
		in := base()
		c.mut(&in)
		_, err := env.book.Book("navajas-oro", in)
		if err == nil {
			t.Fatalf("%s debe fallar", c.name)
		}
		if !errors.Is(err, domain.ErrValidation) && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("%s: error de dominio esperado, dio %v", c.name, err)
		}
	}
	// Un profesional que no atiende en el salón no existe para el público.
	in := base()
	in.ProfessionalID = "prof-ajeno"
	if _, err := env.book.Book("navajas-oro", in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("profesional ajeno: %v", err)
	}
	// Slug inexistente: 404.
	if _, err := env.book.Book("no-existe", base()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente: %v", err)
	}
	if len(env.bookings.All) != 0 {
		t.Fatalf("ningún intento inválido puede quedar agendado: %+v", env.bookings.All)
	}
}

// El reintento del navegador con el mismo client_op_id no agenda dos veces.
func TestPublicBooking_idempotente(t *testing.T) {
	env := newPublicEnv(t)
	in := publicInput()
	in.ServiceID = env.corte.ID
	in.ClientOpID = "pub-op-1"

	first, err := env.book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	second, err := env.book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("el reintento debe devolver la misma reserva: %q vs %q", first.ID, second.ID)
	}
	if len(env.bookings.All) != 1 {
		t.Fatalf("debe haber una sola reserva, hay %d", len(env.bookings.All))
	}
}
