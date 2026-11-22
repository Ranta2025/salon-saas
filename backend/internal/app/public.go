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
	// Hours son los días ABIERTOS del salón, ya expandidos (F2d1): la landing
	// muestra "Cerrado" en los días que no están acá. Sin horario propio son los
	// 7 días de la ventana única de /settings.
	Hours []domain.WeekdayHours
}

type PublicSalonService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	// Settings y Hours dan la ventana única y el horario semanal (F2d1).
	Settings ports.SalonSettingsRepository
	Hours    ports.WeeklyHoursRepository
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
	// Horario visible: las filas propias o, si el salón nunca configuró días, la
	// ventana única expandida a la semana (lo que hoy se mostraba como texto
	// indicativo).
	weekly, err := salonWeeklyHours(s.Hours, salon.ID)
	if err != nil {
		return PublicSalon{}, err
	}
	hours := weekly.Days()
	if !weekly.Custom() {
		settings, err := loadSalonSettings(s.Settings, salon.ID)
		if err != nil {
			return PublicSalon{}, err
		}
		hours = expandedDays(settings)
	}
	return PublicSalon{Salon: salon, Services: services, Hours: hours}, nil
}
