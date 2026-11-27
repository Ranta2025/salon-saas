// SalonHoursService: el horario semanal del salón (GET/PUT /salons/:id/hours).
// Cualquier miembro lo lee (la agenda lo necesita); solo owner/admin lo edita
// (los horarios son de gestión). Sin filas propias el salón responde la ventana
// única de /settings expandida a los 7 días y custom=false.
package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type SalonHoursService struct {
	Hours       ports.WeeklyHoursRepository
	Settings    ports.SalonSettingsRepository
	Memberships ports.MembershipRepository
}

// Get devuelve los días abiertos y si el salón tiene horario propio.
func (s SalonHoursService) Get(actor domain.Membership, salonID string) ([]domain.WeekdayHours, bool, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, false, err
	}
	return s.current(salonID)
}

// Set reemplaza el horario del salón (el adaptador lo hace en una transacción:
// o queda el nuevo entero o queda el anterior). La lista vacía borra las filas y
// vuelve al horario único. Se valida ANTES de escribir: un cuerpo inválido no
// deja media semana cargada.
func (s SalonHoursService) Set(actor domain.Membership, salonID string, days []domain.WeekdayHours) ([]domain.WeekdayHours, bool, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, false, err
	}
	if !m.CanManage() {
		return nil, false, domain.ErrForbidden
	}
	weekly, err := domain.NewWeeklyHours(days)
	if err != nil {
		return nil, false, err
	}
	if err := s.Hours.SaveSalonHours(salonID, weekly.Days()); err != nil {
		return nil, false, err
	}
	return s.current(salonID)
}

// current lee el horario tal como lo devuelve la API: las filas propias, o la
// ventana única expandida cuando el salón nunca configuró días.
func (s SalonHoursService) current(salonID string) ([]domain.WeekdayHours, bool, error) {
	weekly, err := salonWeeklyHours(s.Hours, salonID)
	if err != nil {
		return nil, false, err
	}
	if weekly.Custom() {
		return weekly.Days(), true, nil
	}
	settings, err := kernel.LoadSalonSettings(s.Settings, salonID)
	if err != nil {
		return nil, false, err
	}
	return expandedDays(settings), false, nil
}

// member es la autorización de lectura: el salon_id de la ruta no es la
// credencial, la membresía sí.
func (s SalonHoursService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if s.Memberships == nil || actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
