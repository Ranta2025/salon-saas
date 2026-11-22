package domain

// S-F2d3-3: la regla de quién puede CUBRIR un turno de una persona ausente.
// Es dominio puro: el caso de uso le pasa los hechos (atiende, hace el servicio,
// está de turno ese día, está ausente, ya tiene un turno) y el dominio devuelve
// el mensaje del contrato que corresponde.
import (
	"errors"
	"strings"
	"testing"
	"time"
)

func coverBooking(t *testing.T, serviceID string) Booking {
	t.Helper()
	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	b, err := NewBooking("s1", "u-ana", "Lía", "555", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	b.ServiceID = serviceID
	return b
}

// professional devuelve un candidato con los hechos pedidos.
func professional(id string, role Role, attends bool) StaffMember {
	return StaffMember{ID: id, Name: id, Role: role, Attends: attends}
}

func assertHide(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("esperaba el error %q", text)
	}
	if !errors.Is(err, kind) {
		t.Fatalf("tipo de error: %v (esperaba %v)", err, kind)
	}
	if err.Error() != text {
		t.Fatalf("mensaje: %q (esperaba %q)", err.Error(), text)
	}
}

// S-F2d3-3: el destino tiene que ser RESERVABLE (F2a). Recepción y cliente no
// atienden, y tampoco un dueño/administración sin la marca.
func TestCoverage_candidate_must_be_bookable(t *testing.T) {
	b := coverBooking(t, "sv-corte")
	for _, c := range []CoverageCandidate{
		{Professional: professional("recep", RoleReception, false), DoesService: true, Scheduled: true},
		{Professional: professional("duena", RoleOwner, false), DoesService: true, Scheduled: true},
		{Professional: professional("clienta", RoleClient, false), DoesService: true, Scheduled: true},
		// Un candidato que no es de este salón llega como cero: no atiende acá.
		{DoesService: true, Scheduled: true},
	} {
		assertHide(t, c.Covers(b), ErrValidation, MsgCoverNotBookable)
	}
	// El dueño-barbero (attends) sí puede.
	c := CoverageCandidate{Professional: professional("duena", RoleOwner, true), DoesService: true, Scheduled: true}
	if err := c.Covers(b); err != nil {
		t.Fatalf("el dueño que atiende puede cubrir: %v", err)
	}
}

// S-F2d3-3 + F2d1/F2d2: quien está ausente o no trabaja ese día (descanso
// semanal, día libre, salón cerrado) no cubre. Los dos casos son el mismo
// mensaje para la persona.
func TestCoverage_candidate_must_be_on_shift(t *testing.T) {
	b := coverBooking(t, "sv-corte")
	base := CoverageCandidate{Professional: professional("u-bea", RoleProfessional, true), DoesService: true}

	away := base
	away.Away = true
	assertHide(t, away.Covers(b), ErrValidation, MsgCoverNotScheduled)

	// Sin ventana ese día: no está de turno (F2d1).
	offDuty := base
	offDuty.Scheduled = false
	assertHide(t, offDuty.Covers(b), ErrValidation, MsgCoverNotScheduled)
}

// S-F2d3-3: si el turno tiene servicio, el destino tiene que hacerlo (F2c).
func TestCoverage_candidate_must_do_the_service(t *testing.T) {
	b := coverBooking(t, "sv-keratina")
	c := CoverageCandidate{Professional: professional("u-bea", RoleProfessional, true), Scheduled: true}
	assertHide(t, c.Covers(b), ErrValidation, MsgCoverWrongService)

	// Un turno SIN servicio (agendado a mano desde el calendario) lo puede
	// tomar cualquier reservable que esté de turno.
	sinServicio := coverBooking(t, "")
	if err := c.Covers(sinServicio); err != nil {
		t.Fatalf("un turno sin servicio no exige oficio: %v", err)
	}
}

// S-F2d3-3: si el destino ya tiene un turno en ese horario, la reasignación es
// un 409 (la EXCLUDE de la DB es la garantía; el mensaje es el del contrato).
// El display (la lista de candidatos) lo marca como no disponible.
func TestCoverage_candidate_busy_is_conflict(t *testing.T) {
	b := coverBooking(t, "")
	c := CoverageCandidate{Professional: professional("u-bea", RoleProfessional, true), DoesService: true, Scheduled: true, Busy: true}
	// Ocupada no es lo mismo que "no hay nadie": la dueña tiene que leer por
	// qué esa persona no sirve, al lado de su nombre.
	assertHide(t, c.Covers(b), ErrAlreadyExists, "Ya tiene un turno a esa hora")

	// El motivo del display es el mismo texto que devuelve el POST.
	if got := c.Unavailable(); got != "Ya tiene un turno a esa hora" {
		t.Fatalf("motivo del candidato ocupado: %q", got)
	}
}

// S-F2d3-2/3: el turno de otra profesional o que no pisa la ausencia no es de
// esa ausencia; uno cancelado ya liberó su hueco.
func TestCoverage_booking_belongs_to_absence(t *testing.T) {
	b := coverBooking(t, "")
	off := TimeOff{
		SalonID: "s1", UserID: "u-ana",
		StartsAt: b.StartsAt.Add(-time.Hour), EndsAt: b.EndsAt.Add(time.Hour),
	}
	if !b.BelongsToTimeOff(off) {
		t.Fatal("el turno de la persona ausente, dentro del período, es de la ausencia")
	}
	otra := off
	otra.UserID = "u-bea"
	if b.BelongsToTimeOff(otra) {
		t.Fatal("el turno de otra persona no es de esa ausencia")
	}
	lejos := off
	lejos.StartsAt = b.EndsAt
	lejos.EndsAt = b.EndsAt.Add(time.Hour)
	if b.BelongsToTimeOff(lejos) {
		t.Fatal("un período que solo toca el borde no se pisa (semiabierto)")
	}
}

// S-F2d3-3: el turno ya no reservado no se reasigna (el mensaje del contrato).
func TestCoverage_booking_must_be_booked(t *testing.T) {
	b := coverBooking(t, "")
	if err := b.Coverable(); err != nil {
		t.Fatalf("un turno reservado se puede cubrir: %v", err)
	}
	for _, status := range []BookingStatus{BookingCancelled, BookingDone, BookingNoShow} {
		closed := b
		closed.Status = status
		assertHide(t, closed.Coverable(), ErrAlreadyExists, MsgCoverNotBooked)
	}
}

// S-F2d3-1: reasignar es cambiar de profesional con el MISMO horario, cliente y
// estado: la clienta conserva su hora (S-F2d3-4) y el turno no se reabre.
func TestBooking_reassign_keeps_time_and_status(t *testing.T) {
	b := coverBooking(t, "sv-corte")
	moved, err := b.Reassign("u-bea")
	if err != nil {
		t.Fatalf("reasignar: %v", err)
	}
	if moved.ProfessionalID != "u-bea" {
		t.Fatalf("profesional: %q", moved.ProfessionalID)
	}
	if !moved.StartsAt.Equal(b.StartsAt) || !moved.EndsAt.Equal(b.EndsAt) {
		t.Fatalf("el horario no se mueve: %v-%v", moved.StartsAt, moved.EndsAt)
	}
	if moved.ClientName != b.ClientName || moved.ServiceID != b.ServiceID || moved.Status != b.Status {
		t.Fatalf("solo cambia la profesional: %+v", moved)
	}
	// El turno original no se toca (es un valor).
	if b.ProfessionalID != "u-ana" {
		t.Fatalf("el original se modificó: %+v", b)
	}
	// Vacío = error de validación, nunca un turno sin profesional.
	if _, err := b.Reassign("  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin destino: %v", err)
	}
}

// S-F2d3-2: el motivo de una cancelación se recorta y no puede romper el aviso
// interno (mismo criterio que el motivo de una ausencia).
func TestCoverage_normalize_cancel_reason(t *testing.T) {
	got, err := NormalizeCoverReason("  se enfermó  ")
	if err != nil || got != "se enfermó" {
		t.Fatalf("motivo: %q %v", got, err)
	}
	if got, err := NormalizeCoverReason(""); err != nil || got != "" {
		t.Fatalf("sin motivo es válido: %q %v", got, err)
	}
	if _, err := NormalizeCoverReason("se\nenfermó"); err == nil || err.Error() != "El motivo no puede tener saltos de línea" {
		t.Fatalf("salto de línea: %v", err)
	}
	long := strings.Repeat("a", 121)
	if _, err := NormalizeCoverReason(long); err == nil || err.Error() != "El motivo no puede superar los 120 caracteres" {
		t.Fatalf("motivo largo: %v", err)
	}
}
