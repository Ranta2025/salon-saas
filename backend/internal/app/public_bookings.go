// PublicBookingService: la reserva pública (sin login) de la landing.
// La duración la pone el servicio, el profesional es opcional (se elige uno
// libre) y la garantía anti-solape sigue siendo la EXCLUDE de Postgres: dos
// personas reservando el mismo hueco a la vez terminan en 409 para una.
package app

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PublicBookingInput es el body de POST /public/salons/:slug/bookings ya
// parseado (el handler traduce starts_at a time.Time).
type PublicBookingInput struct {
	ServiceID      string
	ProfessionalID string // vacío = el sistema elige uno libre
	ClientName     string
	ClientPhone    string
	// ClientEmail es opcional: con email, la persona recibe la confirmación y,
	// más tarde, el recordatorio. Vacío = reserva anónima, sin correos.
	ClientEmail string
	StartsAt    time.Time
	ClientOpID  string // reintento del navegador: idempotencia
}

type PublicBookingService struct {
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	Settings ports.SalonSettingsRepository
	Staff    ports.StaffLister
	Bookings ports.BookingRepository
	// Professionals dice quién hace el servicio: solo esa gente puede recibir
	// el turno (F2c). Si nadie lo hace, la reserva es 409.
	Professionals ports.ProfessionalServiceRepository
	// Hours es el horario semanal del salón y de cada profesional (F2d1): un
	// turno se ofrece solo si entra entero en la ventana efectiva de la persona.
	// nil o sin filas = la ventana única de /settings (comportamiento de hoy).
	Hours ports.WeeklyHoursRepository
	// Clients deja la ficha del visitante cuando la reserva entra (E-03). Es
	// opcional como los avisos: nil = este entorno no lleva fichas.
	Clients ports.ClientRepository
	// Opcionales: nil = no avisa (tests sin infraestructura).
	Events   ports.EventPublisher
	Notifier Notifier
	// Email a la persona que reserva (T12). Los dos son opcionales: nil = este
	// entorno no manda correos. Mailer sin Emails manda la confirmación pero
	// no deja recordatorio; Emails sin Mailer solo guarda la dirección.
	Mailer ports.MailSender
	Emails ports.BookingEmailRepository
}

// Book agenda un turno público. Errores: 404 (slug/profesional inexistente),
// 400 (datos faltantes, servicio inactivo, fuera de horario, en el pasado) y
// 409 (hueco tomado, sin profesional libre).
func (s PublicBookingService) Book(slug string, in PublicBookingInput) (domain.Booking, error) {
	salon, err := s.Salons.FindBySlug(strings.TrimSpace(slug))
	if err != nil {
		return domain.Booking{}, err
	}
	if in.StartsAt.IsZero() {
		return domain.Booking{}, fmt.Errorf("%w: falta el horario del turno", domain.ErrValidation)
	}
	opID := strings.TrimSpace(in.ClientOpID)
	// Idempotencia antes de mirar la agenda: el reintento devuelve la reserva
	// original aunque su hueco ya esté ocupado (por ella misma).
	if opID != "" {
		existing, err := s.Bookings.FindByClientOpID(salon.ID, opID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return domain.Booking{}, err
		}
	}

	// El email es opcional, pero si viene tiene que ser un email (misma regla
	// que la ficha del mostrador). Se valida ANTES de agendar: un turno creado
	// con un email que la persona no puede recibir no le sirve a nadie.
	clientEmail, err := domain.NormalizeAndValidateEmail(in.ClientEmail)
	if err != nil {
		return domain.Booking{}, err
	}

	settings, err := loadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}
	loc, err := settings.Location()
	if err != nil {
		return domain.Booking{}, err
	}
	service, err := resolvePublicService(s.Services, salon.ID, in.ServiceID)
	if err != nil {
		return domain.Booking{}, err
	}
	staff, err := s.Staff.ListStaff(salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}
	// Quién puede recibir ESTE turno: quien atiende y hace el servicio. El
	// oficio no alcanza, la marca attends tampoco: el servicio manda (F2c).
	eligible, err := s.professionalIDs(salon.ID, service.ID)
	if err != nil {
		return domain.Booking{}, err
	}

	// Horario semanal (F2d1): sin filas todo esto es la ventana única de antes.
	single, err := singleWindow(settings)
	if err != nil {
		return domain.Booking{}, err
	}
	weekly, err := salonWeeklyHours(s.Hours, salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}
	segments, err := memberHours(s.Hours, salon.ID)
	if err != nil {
		return domain.Booking{}, err
	}

	startsAt := in.StartsAt.UTC()
	endsAt := startsAt.Add(time.Duration(service.DurationMin) * time.Minute)
	// El día y el minuto son los del salón, no los de UTC: un domingo 23:30 en
	// CDMX es lunes en UTC y sigue siendo domingo para la agenda.
	weekday := domain.IsoWeekday(startsAt, loc)
	startMin := localMinutes(startsAt, loc)
	endMin := startMin + service.DurationMin

	// La ventana del salón de ESE día. Si el salón abre, el turno tiene que
	// entrar completo (400, como siempre). Si el día está cerrado no hay ventana
	// y tampoco hay nadie con horario: la reserva cae en el 409 de abajo, que es
	// la misma respuesta que la disponibilidad ({"slots":[]}).
	salonWindows, err := salonWindow(weekly, single, weekday)
	if err != nil {
		return domain.Booking{}, err
	}
	if len(salonWindows) > 0 && !salonWindows[0].Contains(startMin, endMin) {
		return domain.Booking{}, fmt.Errorf("%w: el horario elegido está fuera del horario del salón", domain.ErrValidation)
	}

	// Quién TRABAJA en ese horario: el turno tiene que caber entero en su ventana
	// efectiva (salón ∩ sus tramos). Sin nadie con ventana ese día no hay hueco.
	scheduled := make(map[string]bool, len(staff))
	for _, m := range staff {
		fits, err := domain.DayPlan{Single: single, Weekly: weekly, Segments: segments[m.ID]}.Fits(weekday, startMin, endMin)
		if err != nil {
			return domain.Booking{}, err
		}
		if fits {
			scheduled[m.ID] = true
		}
	}

	professionalID := strings.TrimSpace(in.ProfessionalID)
	if professionalID == "" {
		professionalID, err = s.freeProfessional(staff, eligible, scheduled, salon.ID, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
	} else {
		if !staffHas(staff, professionalID) {
			return domain.Booking{}, domain.ErrNotFound
		}
		if !eligible[professionalID] {
			return domain.Booking{}, fmt.Errorf("%w: el profesional elegido no hace ese servicio", domain.ErrValidation)
		}
		if !scheduled[professionalID] {
			// No trabaja en ese horario: la disponibilidad no ofrece ese hueco.
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
		busy, err := s.Bookings.FindOverlapping(salon.ID, professionalID, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
		if len(busy) > 0 {
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
	}

	b, err := domain.NewBooking(salon.ID, professionalID, in.ClientName, in.ClientPhone, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	b.ClientOpID = opID
	// El turno guarda qué se va a hacer: es lo que después permite contar
	// servicios, comisiones y "N profesionales lo hacen".
	b.ServiceID = service.ID
	if err := s.Bookings.Save(b); err != nil {
		// Carrera real: la EXCLUDE dejó pasar a otro. El reintento con op id
		// devuelve la reserva que sí entró.
		if opID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Bookings.FindByClientOpID(salon.ID, opID); rerr == nil {
				return existing, nil
			}
		}
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
		return domain.Booking{}, err
	}

	// La ficha del visitante se escribe DESPUÉS de la reserva y no puede
	// tumbarla: el turno es lo que la persona pidió (E-03). Si falla, queda el
	// detalle en el log y la ficha se puede crear a mano desde el mostrador.
	s.upsertClient(salon.ID, b.ClientName, b.ClientPhone)

	// La dirección para la confirmación y el recordatorio. Best-effort igual
	// que la ficha: si la DB falla, el turno ya entró y la confirmación se
	// manda igual (lo único que se pierde es el recordatorio).
	if clientEmail != "" && s.Emails != nil {
		if err := s.Emails.Save(salon.ID, b.ID, clientEmail); err != nil {
			// Sin la dirección en el log (dato personal): turno y salón alcanzan.
			log.Printf("reserva pública: guardar el email del turno %s en el salón %s: %v", b.ID, salon.ID, err)
		}
	}

	publish(s.Events, ports.Event{
		Type:    "booking.created",
		SalonID: salon.ID,
		Data: map[string]any{
			"id":              b.ID,
			"professional_id": b.ProfessionalID,
			"client_name":     b.ClientName,
			"starts_at":       b.StartsAt.Format(time.RFC3339),
			"ends_at":         b.EndsAt.Format(time.RFC3339),
			"status":          string(b.Status),
		},
		TS: b.CreatedAt,
	})
	notify(s.Notifier, salon.ID, "", "Nuevo turno",
		b.ClientName+" · "+FormatSalonTime(settings, b.StartsAt)+" (reserva web)", domain.KindBooking)
	// La confirmación sale en segundo plano: la respuesta 201 no espera al SMTP.
	s.sendBookingConfirmation(salon.Name, b, clientEmail, settings)
	return b, nil
}

// upsertClient deja la ficha del visitante en el salón de la reserva. Busca
// primero para NO duplicar: por teléfono normalizado (la misma persona suele
// escribirlo distinto) y, si no dejó teléfono, por nombre exacto.
//
// Si la ficha ya existe no se toca: la del mostrador puede tener notas, fotos y
// un nombre corregido a mano que una reserva desde la web no tiene por qué
// pisar. Best-effort a propósito: la reserva ya entró.
func (s PublicBookingService) upsertClient(salonID, name, phone string) {
	if s.Clients == nil {
		return
	}
	phoneNorm := domain.NormalizePhone(phone)
	var err error
	if phoneNorm != "" {
		_, err = s.Clients.FindByPhone(salonID, phoneNorm)
	} else {
		_, err = s.Clients.FindByName(salonID, strings.TrimSpace(name))
	}
	switch {
	case err == nil:
		return // ya tiene ficha
	case !errors.Is(err, domain.ErrNotFound):
		log.Printf("reserva pública: buscar ficha en el salón %s: %v", salonID, err)
		return
	}
	c, err := domain.NewClient(salonID, name, phone, "", "")
	if err != nil {
		log.Printf("reserva pública: ficha inválida en el salón %s: %v", salonID, err)
		return
	}
	if err := s.Clients.Save(c); err != nil {
		log.Printf("reserva pública: guardar ficha en el salón %s: %v", salonID, err)
	}
}

// freeProfessional elige el primer profesional del equipo que HACE el servicio,
// TRABAJA en ese horario (su ventana efectiva del día, F2d1) y está sin turno en
// el hueco. Si no queda ninguno es un 409, no una reserva sin profesional (ni un
// 500 cuando nadie hace el servicio o el salón está cerrado).
func (s PublicBookingService) freeProfessional(staff []domain.StaffMember, eligible, scheduled map[string]bool, salonID string, start, end time.Time) (string, error) {
	for _, m := range staff {
		// Solo quien atiende: un dueño/administración sin la marca no recibe
		// turnos por asignación automática (F2a).
		if !m.Bookable() {
			continue
		}
		// Y solo quien hace el servicio (F2c).
		if !eligible[m.ID] {
			continue
		}
		// Y solo quien tiene ventana en ese horario (F2d1): el descanso y el día
		// libre no se saltean con la asignación automática.
		if !scheduled[m.ID] {
			continue
		}
		busy, err := s.Bookings.FindOverlapping(salonID, m.ID, start, end)
		if err != nil {
			return "", err
		}
		if len(busy) == 0 {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("%w: no hay profesional libre en ese horario", domain.ErrAlreadyExists)
}

// professionalIDs devuelve el conjunto de miembros que hacen el servicio, para
// preguntarlo una sola vez por reserva.
func (s PublicBookingService) professionalIDs(salonID, serviceID string) (map[string]bool, error) {
	list, err := s.Professionals.ListProfessionalsFor(salonID, serviceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list))
	for _, m := range list {
		out[m.ID] = true
	}
	return out, nil
}

// staffHas dice si userID atiende en el salón.
func staffHas(staff []domain.StaffMember, userID string) bool {
	for _, m := range staff {
		if m.ID == userID && m.Bookable() {
			return true
		}
	}
	return false
}

// resolvePublicService resuelve un servicio ACTIVO del salón. Para el público,
// un servicio inactivo o de otro tenant no existe en la carta (400).
func resolvePublicService(services ports.ServiceRepository, salonID, serviceID string) (domain.Service, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return domain.Service{}, fmt.Errorf("%w: falta el servicio", domain.ErrValidation)
	}
	service, err := services.FindByID(salonID, serviceID)
	if err != nil {
		return domain.Service{}, fmt.Errorf("%w: el servicio no está disponible en este salón", domain.ErrValidation)
	}
	if !service.Active {
		return domain.Service{}, fmt.Errorf("%w: el servicio no está disponible en este salón", domain.ErrValidation)
	}
	return service, nil
}
