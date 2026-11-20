// PublicSalonService: la vidriera pública del salón (landing sin login).
// Solo datos que el negocio quiere mostrar: nombre, slug y la carta de
// servicios ACTIVOS. Nada de plata facturada, clientes ni personal.
package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PublicSalon es la respuesta de GET /public/salons/:slug.
type PublicSalon struct {
	Salon    domain.Salon
	Services []domain.Service
}

type PublicSalonService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
}

// Public resuelve el salón por slug y devuelve sus servicios activos.
// Es la única query sin scope de membership a propósito: el slug ES la
// credencial pública, y solo expone lo que ya está en la vidriera.
func (s PublicSalonService) Public(slug string) (PublicSalon, error) {
	salon, err := s.Salons.FindBySlug(slug)
	if err != nil {
		return PublicSalon{}, err
	}
	services, err := s.Services.List(salon.ID, false)
	if err != nil {
		return PublicSalon{}, err
	}
	if services == nil {
		services = []domain.Service{}
	}
	return PublicSalon{Salon: salon, Services: services}, nil
}
