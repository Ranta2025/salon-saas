// ProfessionalHoursService: los tramos semanales de cada profesional
// (GET/PUT /salons/:id/members/:uid/hours).
//
// Reglas (S-F2d1-2): sin tramos propios la persona sigue el horario del salón;
// con tramos, un día sin tramo es día libre. Los lee owner/admin o la propia
// persona (cada una ve su horario); solo owner/admin los edita.
package app

import (
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type ProfessionalHoursService struct {
	Hours       ports.WeeklyHoursRepository
	Memberships ports.MembershipRepository
}

// Get devuelve los tramos del miembro y si tiene horario propio. Un miembro de
// otro salón no existe acá (404); otra profesional del mismo salón no puede
// espiar la agenda de su compañera (403).
func (s ProfessionalHoursService) Get(actor domain.Membership, salonID, userID string) ([]domain.HoursSegment, bool, error) {
	actorM, err := s.member(actor, salonID)
	if err != nil {
		return nil, false, err
	}
	userID = strings.TrimSpace(userID)
	if actorM.UserID != userID && !actorM.CanManage() {
		return nil, false, domain.ErrForbidden
	}
	if _, err := s.Memberships.Find(userID, salonID); err != nil {
		return nil, false, domain.ErrNotFound
	}
	segments, err := s.Hours.MemberHours(salonID, userID)
	if err != nil {
		return nil, false, err
	}
	return segments, len(segments) > 0, nil
}

// Set reemplaza los tramos del miembro (atómico en el adaptador). Solo
// owner/admin; solo quien atiende tiene horario; un miembro de otro salón es
// 404. La lista vacía vuelve a "igual que el salón".
func (s ProfessionalHoursService) Set(actor domain.Membership, salonID, userID string, segments []domain.HoursSegment) ([]domain.HoursSegment, bool, error) {
	actorM, err := s.member(actor, salonID)
	if err != nil {
		return nil, false, err
	}
	if !actorM.CanManage() {
		return nil, false, domain.ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	target, err := s.Memberships.Find(userID, salonID)
	if err != nil {
		return nil, false, domain.ErrNotFound
	}
	if !target.Bookable() {
		return nil, false, domain.NewMessage(domain.ErrValidation, "Ese rol no tiene horario")
	}
	hours, err := domain.NewProfessionalHours(segments)
	if err != nil {
		return nil, false, err
	}
	if err := s.Hours.SaveMemberHours(salonID, userID, hours.Segments()); err != nil {
		return nil, false, err
	}
	return hours.Segments(), hours.Custom(), nil
}

// member es la autorización: el actor tiene que pertenecer al salón de la ruta.
func (s ProfessionalHoursService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if s.Memberships == nil || actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
