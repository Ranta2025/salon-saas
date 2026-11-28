package app

// E-03 del hallazgo de la auditoría: la reserva pública (sin login) no dejaba
// ficha del cliente, así que la pantalla de Clientes del equipo seguía vacía
// después de las reservas de la web. Además el aviso de la reserva mostraba la
// hora en UTC sin decirlo (E-04): 09:00 en CDMX se leía "15:00".

import (
	"salon-saas/internal/testutil/agendakit"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// fichas ordena por nombre para comparar sin depender del orden de inserción.
func fichas(f *testutil.FakeClients, salonID string) []domain.Client {
	var out []domain.Client
	for _, c := range f.Clients {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out
}

func TestPublicBooking_crea_ficha_del_cliente(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID

	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reservar: %v", err)
	}
	list := fichas(env.Clients, "s1")
	if len(list) != 1 {
		t.Fatalf("la reserva pública tiene que dejar una ficha: %+v", env.Clients.Clients)
	}
	if list[0].Name != "Lía" || list[0].Phone != "555" {
		t.Fatalf("la ficha debe llevar nombre y teléfono de la reserva: %+v", list[0])
	}

	// La misma persona reservando de nuevo (teléfono escrito distinto) no
	// duplica la ficha: el teléfono se compara normalizado.
	in.StartsAt = in.StartsAt.Add(45 * time.Minute)
	in.ClientPhone = " 5-5-5 "
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("segunda reserva: %v", err)
	}
	if got := len(fichas(env.Clients, "s1")); got != 1 {
		t.Fatalf("el mismo teléfono no puede crear otra ficha, hay %d", got)
	}

	// Otra persona (otro teléfono): ficha nueva.
	in.StartsAt = in.StartsAt.Add(45 * time.Minute)
	in.ClientPhone = "556677"
	in.ClientName = "Ana"
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("tercera reserva: %v", err)
	}
	if got := len(fichas(env.Clients, "s1")); got != 2 {
		t.Fatalf("esperaba 2 fichas, hay %d: %+v", got, env.Clients.Clients)
	}
}

// Sin teléfono la ficha se busca por nombre exacto: dos reservas del mismo
// nombre no crean dos fichas (lo que sí pasaría si se creara siempre).
func TestPublicBooking_sin_telefono_no_duplica_por_nombre(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	in.ClientPhone = ""

	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reservar: %v", err)
	}
	in.StartsAt = in.StartsAt.Add(45 * time.Minute)
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reintento de la misma persona: %v", err)
	}
	if got := len(fichas(env.Clients, "s1")); got != 1 {
		t.Fatalf("el mismo nombre sin teléfono no duplica la ficha, hay %d", got)
	}
	in.StartsAt = in.StartsAt.Add(45 * time.Minute)
	in.ClientName = "Ana"
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("otra persona: %v", err)
	}
	if got := len(fichas(env.Clients, "s1")); got != 2 {
		t.Fatalf("otra persona necesita su ficha, hay %d", got)
	}
}

// Aislamiento entre salones: la ficha que existe en otro salón con el mismo
// teléfono no se reusa ni se toca. Cada salón tiene la suya.
func TestPublicBooking_ficha_aislada_por_salon(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	ajena, err := domain.NewClient("s2", "Lía de otro salón", "555", "", "no tocar")
	if err != nil {
		t.Fatalf("ficha ajena: %v", err)
	}
	if err := env.Clients.Save(ajena); err != nil {
		t.Fatalf("guardar ficha ajena: %v", err)
	}

	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if got := len(fichas(env.Clients, "s1")); got != 1 {
		t.Fatalf("el salón de la reserva necesita su propia ficha, hay %d", got)
	}
	if got := fichas(env.Clients, "s2"); len(got) != 1 || got[0].ID != ajena.ID || got[0].Notes != "no tocar" {
		t.Fatalf("la ficha de otro salón no se puede reusar ni modificar: %+v", got)
	}
}

// La reserva GANA: si la ficha no se puede escribir, el visitante no ve un
// error y su turno queda agendado (la ficha se puede crear después a mano).
func TestPublicBooking_si_falla_la_ficha_la_reserva_entra(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Clients.FailSave = true
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID

	b, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("la reserva no puede fallar por la ficha: %v", err)
	}
	if b.ID == "" || len(env.Bookings.All) != 1 {
		t.Fatalf("la reserva tiene que quedar agendada: %+v", env.Bookings.All)
	}
	if got := len(env.Clients.Clients); got != 0 {
		t.Fatalf("la ficha falló, no debería haber quedado nada: %d", got)
	}
}

// E-04: el aviso de la reserva pública muestra la hora LOCAL del salón y dice
// la zona; el evento websocket sigue mandando UTC RFC3339.
func TestPublicBooking_aviso_con_hora_del_salon(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID

	b, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	body := env.Notifier.LastBody()
	if !strings.Contains(body, "09:00 (America/Mexico_City)") {
		t.Fatalf("el aviso debe decir la hora local con la zona: %q", body)
	}
	if strings.Contains(body, "15:00") {
		t.Fatalf("el aviso no puede mostrar la hora UTC: %q", body)
	}
	// El payload del websocket NO cambia: starts_at sigue en UTC RFC3339.
	ev, _ := env.Events.Typed("booking.created")
	data, _ := ev.Data.(map[string]any)
	if data["starts_at"] != b.StartsAt.Format(time.RFC3339) || data["starts_at"] != "2026-12-01T15:00:00Z" {
		t.Fatalf("starts_at del evento: %+v", data["starts_at"])
	}
}
