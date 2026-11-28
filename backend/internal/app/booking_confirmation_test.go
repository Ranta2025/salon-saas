package app

import (
	"errors"
	"salon-saas/internal/testutil"
	"salon-saas/internal/testutil/agendakit"
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// fullPublicInput arma una reserva válida del fixture (09:00 local del 1/12).
func fullPublicInput(env *agendakit.PublicEnv) bkg.PublicBookingInput {
	in := agendakit.PublicInput()
	in.ServiceID = env.Corte.ID
	in.ClientEmail = "Lia@Mail.com"
	return in
}

// T12: la reserva pública con email manda UNA confirmación, con la copia en
// español y la hora del salón, y guarda la dirección normalizada para el
// recordatorio.
func TestPublicBooking_confirmacion(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	b, err := env.Book.Book("navajas-oro", fullPublicInput(env))
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	msgs := env.Mailer.WaitCount(t, 1)
	if msg := msgs[0]; msg.To != "lia@mail.com" {
		t.Fatalf("el email se normaliza (minúsculas): %q", msg.To)
	}
	msg := msgs[0]
	if msg.Subject != "Turno confirmado · Navajas de Oro" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	for _, want := range []string{"Lía", "Navajas de Oro", "01/12 09:00 (America/Mexico_City)", "escribile al salón"} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("falta %q en el correo:\n%s", want, msg.Text)
		}
	}
	// La fila para el recordatorio (misma dirección normalizada).
	saved, ok := env.Emails.SavedFor(b.ID)
	if !ok || saved.SalonID != "s1" || saved.Email != "lia@mail.com" {
		t.Fatalf("fila del email: %+v (guardada=%v)", saved, ok)
	}
}

// El reintento idempotente devuelve la reserva original y NO manda una segunda
// confirmación ni guarda dos veces el email.
func TestPublicBooking_replay_no_repite_confirmacion(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	in := fullPublicInput(env)
	in.ClientOpID = "op-conf-1"
	first, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	env.Mailer.WaitCount(t, 1)
	second, err := env.Book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("el replay devuelve la misma reserva: %q vs %q", first.ID, second.ID)
	}
	// Dar tiempo a una segunda goroutine (si existiera) antes de contar.
	time.Sleep(50 * time.Millisecond)
	if n := env.Mailer.Count(); n != 1 {
		t.Fatalf("el replay no puede reenviar la confirmación: %d envíos", n)
	}
	if len(env.Emails.Saved) != 1 {
		t.Fatalf("el email se guarda una sola vez: %+v", env.Emails.Saved)
	}
}

// Sin email no hay correo ni fila: la reserva sigue siendo anónima.
func TestPublicBooking_sin_email_no_manda_nada(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	in := fullPublicInput(env)
	in.ClientEmail = "   "
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reservar sin email: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := env.Mailer.Count(); n != 0 {
		t.Fatalf("sin email no se manda nada: %d", n)
	}
	if len(env.Emails.Saved) != 0 {
		t.Fatalf("sin email no se guarda fila: %+v", env.Emails.Saved)
	}
}

// Email con forma inválida: 400 con el mensaje limpio (sin el prefijo interno)
// y la reserva NO entra.
func TestPublicBooking_email_invalido(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	for _, bad := range []string{
		"sin-arroba",
		"a@b",
		strings.Repeat("a", 250) + "@mail.com", // > 254
	} {
		in := fullPublicInput(env)
		in.ClientEmail = bad
		_, err := env.Book.Book("navajas-oro", in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q debe dar validación, dio %v", bad, err)
		}
		if err.Error() != "Email inválido" {
			t.Fatalf("%q: el mensaje debe ser limpio, dio %q", bad, err.Error())
		}
	}
	if len(env.Bookings.All) != 0 {
		t.Fatalf("un email inválido no puede agendar: %+v", env.Bookings.All)
	}
	if len(env.Emails.Saved) != 0 || env.Mailer.Count() != 0 {
		t.Fatal("un email inválido no guarda fila ni manda correo")
	}
}

// Un sender lento o caído no demora ni tumba la reserva (goroutine con su
// propio timeout).
func TestPublicBooking_confirmacion_lenta_no_demora(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	release := make(chan struct{})
	env.Mailer = &testutil.FakeMailer{Started: make(chan struct{}, 1), Release: release}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	start := time.Now()
	if _, err := env.Book.Book("navajas-oro", fullPublicInput(env)); err != nil {
		t.Fatalf("reservar con sender lento: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("la reserva esperó al sender: %v", elapsed)
	}
	select {
	case <-env.Mailer.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("la confirmación nunca intentó enviarse")
	}
	close(release)
	env.Mailer.WaitCount(t, 1)
}

// Un sender que falla no cambia la respuesta de la reserva.
func TestPublicBooking_confirmacion_fallida_no_tumba_la_reserva(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{Err: errors.New("smtp caído")}
	env.Emails = &testutil.FakeBookingEmails{}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	b, err := env.Book.Book("navajas-oro", fullPublicInput(env))
	if err != nil || b.ID == "" {
		t.Fatalf("la reserva gana aunque el correo falle: %+v %v", b, err)
	}
	// La fila del recordatorio se guarda aunque el envío falle.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := env.Emails.SavedFor(b.ID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("la fila del email se guarda aunque el envío falle")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Si guardar la fila falla (DB caída), la reserva igual entra y la confirmación
// se manda igual: el email es best-effort, el turno es lo que pidió la persona.
func TestPublicBooking_fila_de_email_fallida_no_tumba_la_reserva(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	env.Mailer = &testutil.FakeMailer{}
	env.Emails = &testutil.FakeBookingEmails{SaveErr: errors.New("db caída")}
	env.Book.Mailer = env.Mailer
	env.Book.Emails = env.Emails

	b, err := env.Book.Book("navajas-oro", fullPublicInput(env))
	if err != nil || b.ID == "" {
		t.Fatalf("la reserva gana aunque falle la fila: %+v %v", b, err)
	}
	env.Mailer.WaitCount(t, 1)
}

// Sin mailer/emails configurados (email deshabilitado) la reserva funciona
// exactamente como antes.
func TestPublicBooking_sin_email_configurado(t *testing.T) {
	env := agendakit.NewPublicEnv(t)
	in := fullPublicInput(env)
	if _, err := env.Book.Book("navajas-oro", in); err != nil {
		t.Fatalf("reserva sin email configurado: %v", err)
	}
	if len(env.Bookings.All) != 1 {
		t.Fatalf("la reserva entra igual: %+v", env.Bookings.All)
	}
}
