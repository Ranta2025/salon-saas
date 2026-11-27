package ports

import "salon-saas/internal/domain"

// --- Catálogo de servicios ---

type ServiceRepository interface {
	// Save inserta o actualiza por id. Rechaza nombre activo duplicado por
	// salón (índice único sobre lower(name) WHERE active).
	Save(s domain.Service) error
	FindByID(salonID, id string) (domain.Service, error)
	List(salonID string, includeInactive bool) ([]domain.Service, error)
}
