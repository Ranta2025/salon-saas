package app

import (
	"errors"
	"fmt"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ensureProfessional valida que el profesional elegido sea alguien que atiende
// EN ESTE salón (B-12). Antes bastaba con mandar cualquier id: la venta o el
// turno quedaban colgados de un usuario inexistente o de otro tenant, y el
// calendario mostraba agendas fantasma.
//
// Regla en un solo lugar: la usan ventas y reservas. Un id que no existe en el
// salón es un error de validación (400), no un 404: no se confirma ni se niega
// la existencia de usuarios de otros salones.
func ensureProfessional(memberships ports.MembershipRepository, salonID, professionalID string) error {
	professionalID = strings.TrimSpace(professionalID)
	if professionalID == "" {
		return fmt.Errorf("%w: el profesional es obligatorio", domain.ErrValidation)
	}
	m, err := memberships.Find(professionalID, salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: el profesional no pertenece al salón", domain.ErrValidation)
		}
		return err
	}
	// Quién atiende lo decide el dominio (profesional siempre; dueño/administración
	// solo con la marca attends): el rol solo ya no alcanza (F2a).
	if !m.Bookable() {
		return fmt.Errorf("%w: el profesional elegido no atiende en el salón", domain.ErrValidation)
	}
	return nil
}
