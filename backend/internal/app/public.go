// PublicSalonService: la vidriera pública del salón (landing sin login).
// Solo datos que el negocio quiere mostrar: nombre, slug y la carta de
// servicios ACTIVOS. Nada de plata facturada, clientes ni personal.
package app

import (
	"errors"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
	"salon-saas/internal/schedules"
)

// PublicSalon es la respuesta de GET /public/salons/:slug.
type PublicSalon struct {
	Salon    domain.Salon
	Services []domain.Service
	// Hours son los días ABIERTOS del salón, ya expandidos (F2d1): la landing
	// muestra "Cerrado" en los días que no están acá. Sin horario propio son los
	// 7 días de la ventana única de /settings.
	Hours []domain.WeekdayHours
	// F16 (D4): lo que la web pública muestra del perfil del salón, sin que nada
	// de lo anterior cambie. Timezone sale de /settings.
	Profile  domain.SalonProfile
	Timezone string
	// Team: solo quienes atienden clientes y siguen activas; sin email, pagos ni ids.
	Team []domain.StaffMember
	// Gallery con la URL pública de cada foto (D3).
	Gallery []GalleryPhoto
	// Packages: el catálogo F8 ACTIVO cuyo servicio sigue activo.
	Packages []domain.ServicePackage
	// Loyalty (F9, D8): el programa de fidelidad SOLO si está activado y su
	// servicio de regalo sigue activo; nil en cualquier otro caso.
	Loyalty *PublicLoyalty
	// Deposit (F11, D11): la política de señas SOLO si está activada; nil si el
	// salón no cobra seña. La landing muestra el aviso «Pago simulado» y, por
	// servicio, el monto que el servidor calcula con la fórmula de D4.
	Deposit *PublicDeposit
}

// PublicDeposit es lo único de la política de señas que ve el público (D11).
// `simulated` no viaja acá: la web lo agrega porque el adaptador de hoy es el
// banco simulado.
type PublicDeposit struct {
	Percent     int
	HoldMinutes int
	RefundHours int
}

// PublicLoyalty es lo único del programa de fidelidad que ve el público: cada
// cuántas visitas y qué servicio se regala. Ni progreso ni clientas (D8).
type PublicLoyalty struct {
	VisitsRequired    int
	RewardServiceName string
}

type PublicSalonService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	// Settings y Hours dan la ventana única y el horario semanal (F2d1).
	Settings ports.SalonSettingsRepository
	Hours    ports.WeeklyHoursRepository
	// F16 (D4). Todos opcionales: nil = esa sección sale vacía (y, sin
	// Professionals, la carta no se filtra por profesional: solo tests; el
	// composition root los cablea a todos).
	Profiles ports.SalonProfileRepository
	// Staff da el equipo que atiende (ListStaff: activas y reservables).
	Staff ports.StaffLister
	// Professionals es el filtro F2c de la carta: la MISMA regla que usa la
	// disponibilidad (ListProfessionalsFor).
	Professionals ports.ProfessionalServiceRepository
	Gallery       ports.SalonGalleryRepository
	Packages      ports.PackageRepository
	// Loyalty (F9, D8) es opcional: nil = el payload lleva `loyalty: null`.
	Loyalty ports.LoyaltyProgramRepository
	// Deposits (F11, D11) es opcional: nil = el payload lleva `deposit: null`.
	Deposits ports.DepositPolicyRepository
}

// Public resuelve el salón por slug y devuelve su vidriera. Es la única query
// sin scope de membership a propósito: el slug ES la credencial pública, y solo
// expone lo que el negocio quiere mostrar (F16, D4).
func (s PublicSalonService) Public(slug string) (PublicSalon, error) {
	salon, err := s.Salons.FindBySlug(slug)
	if err != nil {
		return PublicSalon{}, err
	}
	active, err := s.Services.List(salon.ID, false)
	if err != nil {
		return PublicSalon{}, err
	}
	services, err := s.offeredServices(salon.ID, active)
	if err != nil {
		return PublicSalon{}, err
	}
	// Horario visible: las filas propias o, si el salón nunca configuró días, la
	// ventana única expandida a la semana (lo que hoy se mostraba como texto
	// indicativo).
	weekly, err := schedules.SalonWeeklyHours(s.Hours, salon.ID)
	if err != nil {
		return PublicSalon{}, err
	}
	settings, err := kernel.LoadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return PublicSalon{}, err
	}
	hours := weekly.Days()
	if !weekly.Custom() {
		hours = schedules.ExpandedDays(settings)
	}
	out := PublicSalon{Salon: salon, Services: services, Hours: hours, Timezone: settings.Timezone,
		Team: []domain.StaffMember{}, Gallery: []GalleryPhoto{}, Packages: []domain.ServicePackage{},
		Profile: domain.SalonProfile{SalonID: salon.ID, Slug: salon.Slug, Name: salon.Name}}
	if s.Profiles != nil {
		profile, err := s.Profiles.Profile(salon.ID)
		if err != nil {
			return PublicSalon{}, err
		}
		out.Profile = profile
	}
	if s.Staff != nil {
		staff, err := s.Staff.ListStaff(salon.ID)
		if err != nil {
			return PublicSalon{}, err
		}
		for _, m := range staff {
			// Solo quien atiende clientes: recepción o un dueño sin la marca no
			// son «el equipo» de cara a la clienta.
			if m.Bookable() {
				out.Team = append(out.Team, m)
			}
		}
	}
	if s.Gallery != nil {
		photos, err := s.Gallery.List(salon.ID)
		if err != nil {
			return PublicSalon{}, err
		}
		for _, p := range photos {
			out.Gallery = append(out.Gallery, withURL(salon.Slug, p))
		}
	}
	if s.Packages != nil {
		packages, err := s.Packages.List(salon.ID)
		if err != nil {
			return PublicSalon{}, err
		}
		// Servicio activo: el mismo conjunto de servicios activos del salón (la
		// compra sigue siendo del personal; acá solo se muestra el catálogo).
		activeIDs := make(map[string]bool, len(active))
		for _, svc := range active {
			activeIDs[svc.ID] = true
		}
		for _, p := range packages {
			if p.Active && activeIDs[p.ServiceID] {
				out.Packages = append(out.Packages, p)
			}
		}
	}
	if s.Loyalty != nil {
		prog, err := s.Loyalty.Find(salon.ID)
		switch {
		case err == nil:
			// D8: solo con el programa ACTIVADO y su servicio de regalo ACTIVO
			// (el mismo conjunto de servicios activos de la carta).
			if prog.Enabled {
				for _, svc := range active {
					if svc.ID == prog.RewardServiceID {
						out.Loyalty = &PublicLoyalty{VisitsRequired: prog.VisitsRequired, RewardServiceName: svc.Name}
						break
					}
				}
			}
		case errors.Is(err, domain.ErrNotFound):
			// Nunca guardado: sin sección.
		default:
			// Additive section: a failing loyalty query (e.g. migration 033 not yet
			// applied during a rolling deploy) degrades to `loyalty: null` and never
			// takes the whole public page down.
		}
	}
	// F11 (D11): la seña SOLO si la política está activada. Sin fila (nunca
	// guardada) o con la seña apagada el payload lleva `deposit: null`, igual que
	// hoy. Es una sección aditiva: un fallo de la consulta no tumba la landing.
	if s.Deposits != nil {
		policy, err := s.Deposits.Find(salon.ID)
		switch {
		case err == nil:
			if policy.Enabled {
				out.Deposit = &PublicDeposit{
					Percent: policy.Percent, HoldMinutes: policy.HoldMinutes, RefundHours: policy.RefundHours,
				}
			}
		case errors.Is(err, domain.ErrNotFound):
			// Nunca guardada: sin seña.
		default:
			// Sección aditiva: `deposit: null`.
		}
	}
	return out, nil
}

// offeredServices aplica el filtro de la carta pública (F16, D4): solo los
// servicios activos que al menos UNA profesional activa y reservable hace. Usa la
// MISMA consulta que la disponibilidad (ListProfessionalsFor, F2c): así la carta
// y los horarios no pueden divergir. Sin Professionals no se filtra.
func (s PublicSalonService) offeredServices(salonID string, active []domain.Service) ([]domain.Service, error) {
	out := make([]domain.Service, 0, len(active))
	for _, svc := range active {
		if s.Professionals != nil {
			who, err := s.Professionals.ListProfessionalsFor(salonID, svc.ID)
			if err != nil {
				return nil, err
			}
			offered := false
			for _, m := range who {
				if m.Bookable() {
					offered = true
					break
				}
			}
			if !offered {
				continue
			}
		}
		out = append(out, svc)
	}
	return out, nil
}
