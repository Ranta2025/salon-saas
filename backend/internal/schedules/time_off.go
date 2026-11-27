// TimeOffService: vacaciones y días libres puntuales de cada profesional
// (F2d2).
//
//	GET    /salons/:id/time-off?from&to
//	POST   /salons/:id/members/:uid/time-off
//	DELETE /salons/:id/time-off/:tid
//
// Reglas: cualquier miembro LEE las ausencias (la agenda las necesita) pero el
// motivo es privado (dueño/administración y la propia persona); solo
// dueño/administración carga y borra; solo quien atiende tiene ausencias (F2a).
// Crear una ausencia NO cancela turnos: devuelve los que se pisan para que el
// salón los reasigne (F2d3) o los cancele a mano.
package schedules

import (
	"errors"
	"sort"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type TimeOffService struct {
	TimeOff ports.TimeOffRepository
	// Bookings es solo para los turnos en conflicto del alta: la ausencia no
	// toca la agenda, avisa qué hay que mover.
	Bookings    ports.BookingRepository
	Memberships ports.MembershipRepository
}

// TimeOffResult es la respuesta del alta: la ausencia creada y los turnos
// `booked` de ESA persona que se pisan con ella (no se cancelan solos).
type TimeOffResult struct {
	TimeOff   domain.TimeOff
	Conflicts []domain.Booking
}

// List devuelve las ausencias del salón que pisan [from, to), con el motivo
// recortado según quién mira.
func (s TimeOffService) List(actor domain.Membership, salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateTimeOffRange(from, to); err != nil {
		return nil, err
	}
	if s.TimeOff == nil {
		return []domain.TimeOff{}, nil
	}
	list, err := s.TimeOff.ListRange(salonID, from, to)
	if err != nil {
		return nil, err
	}
	for i := range list {
		// El motivo es privado: una compañera ve que está ausente (la agenda lo
		// necesita) pero no por qué; recepción tampoco. Dueño/administración ve
		// todo y cada persona el suyo.
		if list[i].UserID != m.UserID && !m.CanManage() {
			list[i].Reason = ""
		}
	}
	return list, nil
}

// Create carga una ausencia. Solo dueño/administración; el miembro tiene que ser
// de ESTE salón (404 si es de otro) y tiene que atender (400 si no). Se valida
// antes de escribir: un pedido inválido no deja filas.
func (s TimeOffService) Create(actor domain.Membership, salonID, userID string, startsAt, endsAt time.Time, reason string) (TimeOffResult, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return TimeOffResult{}, err
	}
	if !m.CanManage() {
		return TimeOffResult{}, domain.ErrForbidden
	}
	target, err := s.Memberships.Find(strings.TrimSpace(userID), salonID)
	if err != nil {
		// No distingue "no existe" de "es de otro salón": las dos son 404.
		return TimeOffResult{}, domain.ErrNotFound
	}
	if !target.HasTimeOff() {
		return TimeOffResult{}, domain.NewMessage(domain.ErrValidation, domain.MsgTimeOffRole)
	}
	off, err := domain.NewTimeOff(domain.TimeOffInput{
		SalonID: salonID, UserID: target.UserID,
		StartsAt: startsAt, EndsAt: endsAt, Reason: reason, CreatedBy: m.UserID,
	}, time.Now())
	if err != nil {
		return TimeOffResult{}, err
	}
	// Dos ausencias de la misma persona no se pisan. Se mira contra lo guardado
	// (el rango de la nueva ya trae solo lo que la pisa) y antes de escribir.
	existing, err := s.TimeOff.ListRange(salonID, off.StartsAt, off.EndsAt)
	if err != nil {
		return TimeOffResult{}, err
	}
	for _, other := range existing {
		if other.UserID == off.UserID && other.Overlaps(off.StartsAt, off.EndsAt) {
			return TimeOffResult{}, domain.NewMessage(domain.ErrValidation, domain.MsgTimeOffOverlap)
		}
	}
	conflicts, err := s.conflicts(salonID, off.UserID, off.StartsAt, off.EndsAt)
	if err != nil {
		return TimeOffResult{}, err
	}
	if err := s.TimeOff.Save(off); err != nil {
		// Carrera real: dos altas a la vez pasan las dos el chequeo previo y la
		// EXCLUDE deja pasar una. Para la persona es el mismo caso que el
		// solape, así que ve el mismo mensaje (400), no un "ya existe" (409).
		if errors.Is(err, domain.ErrAlreadyExists) {
			return TimeOffResult{}, domain.NewMessage(domain.ErrValidation, domain.MsgTimeOffOverlap)
		}
		return TimeOffResult{}, err
	}
	return TimeOffResult{TimeOff: off, Conflicts: conflicts}, nil
}

// Delete borra una ausencia del salón. Solo dueño/administración; el id de otro
// salón es 404 (no un borrado silencioso).
func (s TimeOffService) Delete(actor domain.Membership, salonID, id string) error {
	m, err := s.member(actor, salonID)
	if err != nil {
		return err
	}
	if !m.CanManage() {
		return domain.ErrForbidden
	}
	return s.TimeOff.Delete(salonID, strings.TrimSpace(id))
}

// conflicts son los turnos `booked` de esa persona que se pisan con la ausencia,
// ordenados por inicio. No se toca ninguno: la respuesta es un aviso.
//
// Bookings.ListRange filtra por starts_at, así que la ventana se abre hacia
// atrás el máximo de duración de un turno (12 h): un turno que empezó antes de
// la ausencia y la pisa tiene que aparecer igual.
func (s TimeOffService) conflicts(salonID, userID string, startsAt, endsAt time.Time) ([]domain.Booking, error) {
	if s.Bookings == nil {
		return []domain.Booking{}, nil
	}
	window, err := s.Bookings.ListRange(salonID, startsAt.Add(-domain.MaxBookingDuration), endsAt)
	if err != nil {
		return nil, err
	}
	return BookedOverlaps(window, userID, startsAt, endsAt), nil
}

// BookedOverlaps filtra de una ventana de agenda los turnos `booked` de esa
// persona que se pisan con [startsAt, endsAt), ordenados por inicio. Lo comparten
// el alta de una ausencia (F2d2, los turnos en conflicto) y el "Reasignar todo"
// de F2d3: la misma definición de "turno que hay que mover".
func BookedOverlaps(window []domain.Booking, userID string, startsAt, endsAt time.Time) []domain.Booking {
	out := make([]domain.Booking, 0, len(window))
	for _, b := range window {
		if b.ProfessionalID != userID || b.Status != domain.BookingBooked {
			continue
		}
		if b.StartsAt.Before(endsAt) && startsAt.Before(b.EndsAt) {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	return out
}

// member es la autorización: el actor tiene que pertenecer al salón de la ruta.
func (s TimeOffService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if s.Memberships == nil || actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// TimeOffRange carga las ausencias de un salón que pisan [from, to). Repo nil =
// no hay ausencias en este entorno (como el horario semanal: nil es "sin
// configuración", no un dato faltante).
func TimeOffRange(repo ports.TimeOffRepository, salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	if repo == nil {
		return nil, nil
	}
	return repo.ListRange(salonID, from, to)
}

// AwayAny dice si esa persona está ausente en algún momento de [start, end).
func AwayAny(absences []domain.TimeOff, professionalID string, start, end time.Time) bool {
	for _, off := range absences {
		if off.UserID == professionalID && off.Overlaps(start, end) {
			return true
		}
	}
	return false
}

// AwaySet es AwayAny para todo el equipo de una vez: la reserva pública lo
// pregunta una sola vez por turno.
func AwaySet(absences []domain.TimeOff, start, end time.Time) map[string]bool {
	if len(absences) == 0 {
		return nil
	}
	out := make(map[string]bool, len(absences))
	for _, off := range absences {
		if off.Overlaps(start, end) {
			out[off.UserID] = true
		}
	}
	return out
}
