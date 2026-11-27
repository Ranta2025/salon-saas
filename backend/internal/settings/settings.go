// SettingsService: la configuración operativa del salón (GET/PUT
// /salons/:id/settings). Cualquier miembro la lee (la necesita la agenda);
// solo owner/admin la edita (precios, horarios y personal son de gestión).
package settings

import (
	"errors"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type SettingsService struct {
	Settings    ports.SalonSettingsRepository
	Memberships ports.MembershipRepository
}

// Get devuelve la configuración. Si el salón todavía no tiene fila (base vieja
// sin la migración de settings) responde la default de fábrica: un salón nunca
// se queda sin horario.
func (s SettingsService) Get(actor domain.Membership, salonID string) (domain.SalonSettings, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return domain.SalonSettings{}, err
	}
	return kernel.LoadSalonSettings(s.Settings, salonID)
}

// Update valida y persiste. El cuerpo es completo (los cuatro campos): un PUT
// parcial dejaría el salón con una configuración a medio cambiar.
func (s SettingsService) Update(actor domain.Membership, salonID string, in domain.SalonSettings) (domain.SalonSettings, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.SalonSettings{}, err
	}
	if !m.CanManage() {
		return domain.SalonSettings{}, domain.ErrForbidden
	}
	settings, err := domain.NewSalonSettings(salonID, in.Timezone, in.OpenTime, in.CloseTime, in.SlotStepMin)
	if err != nil {
		return domain.SalonSettings{}, err
	}
	// La escritura pasa por el camino GUARDADO: toma el advisory lock de la
	// nómina y, si la zona horaria cambia con liquidaciones hechas, no escribe
	// nada. Sin eso, una corrida guardada en fechas locales quedaría calculada
	// con una zona que ya no es la del salón (ventas pagadas dos veces o en
	// ninguna).
	if err := s.Settings.SaveSettingsGuarded(settings); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.SalonSettings{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPayrollTimezoneFrozen)
		}
		return domain.SalonSettings{}, err
	}
	return settings, nil
}

func (s SettingsService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// loadSalonSettings es el único camino de lectura de la configuración: fila
// ausente = defaults de fábrica; cualquier otro error se propaga (nunca se
// inventa una zona horaria con la DB caída).
