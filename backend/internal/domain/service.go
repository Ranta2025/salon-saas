package domain

import (
	"fmt"
	"strings"
)

// Service es un servicio del catálogo del salón: lo que el negocio vende y
// cuánto dura. El precio vive en centavos (int): nunca float para dinero.
// La "duración" importa porque el calendario reserva bloques, no horas sueltas.
type Service struct {
	ID            string
	SalonID       string
	Name          string
	DurationMin   int
	PriceCents    int
	CommissionPct int // 0..100: % que se lleva quien lo atiende
	Active        bool
}

// NewService valida y crea un servicio activo.
func NewService(salonID, name string, durationMin, priceCents, commissionPct int) (Service, error) {
	clean, err := validateServiceFields(salonID, name, durationMin, priceCents, commissionPct)
	if err != nil {
		return Service{}, err
	}
	return Service{
		ID:            NewID(),
		SalonID:       salonID,
		Name:          clean,
		DurationMin:   durationMin,
		PriceCents:    priceCents,
		CommissionPct: commissionPct,
		Active:        true,
	}, nil
}

// WithDetails devuelve el servicio con los datos nuevos, revalidando todo.
// Mantiene ID y SalonID: actualizar no cambia la identidad ni el tenant.
func (s Service) WithDetails(name string, durationMin, priceCents, commissionPct int, active bool) (Service, error) {
	clean, err := validateServiceFields(s.SalonID, name, durationMin, priceCents, commissionPct)
	if err != nil {
		return Service{}, err
	}
	s.Name = clean
	s.DurationMin = durationMin
	s.PriceCents = priceCents
	s.CommissionPct = commissionPct
	s.Active = active
	return s, nil
}

// validateServiceFields concentra las reglas del catálogo: crear y actualizar
// no pueden divergir (una sola fuente de verdad).
func validateServiceFields(salonID, name string, durationMin, priceCents, commissionPct int) (string, error) {
	name = strings.TrimSpace(name)
	if salonID == "" {
		return "", fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if name == "" {
		return "", fmt.Errorf("%w: el nombre del servicio es obligatorio", ErrValidation)
	}
	if durationMin <= 0 {
		return "", fmt.Errorf("%w: la duración debe ser mayor a 0 minutos", ErrValidation)
	}
	if priceCents < 0 {
		return "", fmt.Errorf("%w: el precio no puede ser negativo", ErrValidation)
	}
	if commissionPct < 0 || commissionPct > 100 {
		return "", fmt.Errorf("%w: la comisión debe estar entre 0 y 100", ErrValidation)
	}
	return name, nil
}
