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
	// TimeOff son las ausencias (F2d2): a quien está ausente no se le asigna un
	// turno, y elegirla a mano es 409 (la disponibilidad no la ofrece).
	TimeOff ports.TimeOffRepository
	// Resources es el equipamiento con capacidad (F2e): un turno de un servicio
	// con tipo de equipo ocupa UNA unidad y la reserva guarda cuál. nil o
	// servicio sin tipo = comportamiento de antes.
	Resources ports.ResourceRepository
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
	// Links emite el enlace de un solo uso que viaja en la confirmación (F1).
	// nil = este entorno no emite enlaces y el correo sale con la línea de
	// contacto. Solo se emite para un turno CON email (D1).
	Links ports.BookingLinkRepository
	// AppURL es la base del enlace (APP_URL).
	AppURL string
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

	// Ausencias (F2d2): a quien está ausente no se le asigna el turno, y elegirla
	// a mano es el mismo 409 que "no trabaja en ese horario" (la disponibilidad
	// no la ofrece, así que la reserva no puede entrar por atrás).
	absences, err := timeOffRange(s.TimeOff, salon.ID, startsAt, endsAt)
	if err != nil {
		return domain.Booking{}, err
	}
	away := awaySet(absences, startsAt, endsAt)

	// Equipo (F2e, S-F2e-4): si el servicio exige un tipo, el turno ocupa UNA
	// unidad y sin ninguna activa no hay hueco (409, nunca 500).
	units, err := s.equipment(salon.ID, service.ResourceTypeID)
	if err != nil {
		return domain.Booking{}, err
	}

	professionalID := strings.TrimSpace(in.ProfessionalID)
	if professionalID != "" {
		if !staffHas(staff, professionalID) {
			return domain.Booking{}, domain.ErrNotFound
		}
		if !eligible[professionalID] {
			return domain.Booking{}, fmt.Errorf("%w: el profesional elegido no hace ese servicio", domain.ErrValidation)
		}
		if !scheduled[professionalID] || away[professionalID] {
			// No trabaja en ese horario (o está ausente): la disponibilidad no
			// ofrece ese hueco.
			return domain.Booking{}, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
		}
	}

	// Candidatos en orden: el elegido a mano o quien atiende, hace el servicio,
	// trabaja ese horario y está presente (F2a/F2c/F2d1/F2d2).
	candidates := []string{professionalID}
	msg := "el horario ya no está disponible"
	if professionalID == "" {
		candidates = s.freeProfessionals(staff, eligible, scheduled, away)
		msg = "no hay profesional libre en ese horario"
	}

	// Un turno ocupa UNA profesional Y UNA unidad. La EXCLUDE de Postgres decide
	// el doble turno; si rechaza el par (otra reserva entró entre el chequeo y el
	// INSERT), se reintenta con OTRO par hasta maxBookingAttempts veces. Nunca
	// 500 y nunca dos turnos en la misma unidad/hora.
	// Se prueba CADA par posible antes de rendirse: con las escrituras
	// serializadas por salón, un intento fallido significa que ese par ya está
	// tomado, no un interbloqueo, así que si queda un par libre se lo encuentra
	// (un "sin lugar" falso es peor que un 500: la clienta se va).
	attempts := maxBookingAttempts
	if pairs := len(candidates) * maxInt(1, len(units)); pairs > attempts {
		attempts = pairs
	}
	if attempts > maxPairAttempts {
		attempts = maxPairAttempts
	}
	busy := make(map[string]bool, attempts)
	var booking domain.Booking
	for attempt := 0; attempt < attempts; attempt++ {
		prof, unit, err := s.pickPair(salon.ID, candidates, units, startsAt, endsAt, busy, msg)
		if err != nil {
			return domain.Booking{}, err
		}
		candidate, err := domain.NewBooking(salon.ID, prof, in.ClientName, in.ClientPhone, startsAt, endsAt)
		if err != nil {
			return domain.Booking{}, err
		}
		candidate.ClientOpID = opID
		// El turno guarda qué se va a hacer: es lo que después permite contar
		// servicios, comisiones y "N profesionales lo hacen".
		candidate.ServiceID = service.ID
		candidate.ResourceUnitID = unit
		err = s.Bookings.Save(candidate)
		if err == nil {
			booking = candidate
			break
		}
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Booking{}, err
		}
		// Carrera real: la EXCLUDE dejó pasar a otro. El reintento con op id
		// devuelve la reserva que sí entró.
		if opID != "" {
			if existing, rerr := s.Bookings.FindByClientOpID(salon.ID, opID); rerr == nil {
				return existing, nil
			}
		}
		busy[pairKey(prof, unit)] = true
	}
	if booking.ID == "" {
		return domain.Booking{}, fmt.Errorf("%w: %s", domain.ErrAlreadyExists, msg)
	}
	b := booking

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
	// El enlace de un solo uso para cambiar o cancelar (F1/D1): un token NUEVO por
	// cada email enviado y solo para un turno con email. Si no se puede emitir, la
	// confirmación sale igual con la línea de contacto: el turno ya está agendado
	// y un enlace que falta no puede dejar a la persona sin su correo.
	link := ""
	if clientEmail != "" && s.Links != nil {
		if _, token, err := mintBookingLink(s.Links, salon.ID, b.ID, time.Now().UTC()); err != nil {
			// Sin el token en el log: el id del turno y el salón alcanzan.
			log.Printf("reserva pública: emitir el enlace del turno %s en el salón %s: %v", b.ID, salon.ID, err)
		} else {
			link = bookingLinkURL(s.AppURL, token)
		}
	}
	// La confirmación sale en segundo plano: la respuesta 201 no espera al SMTP.
	s.sendBookingConfirmation(salon.Name, b, clientEmail, settings, link)
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

// maxBookingAttempts es cuántas veces se reintenta un par (profesional, unidad)
// cuando la EXCLUDE rechaza el guardado por la carrera (S-F2e-4).
const maxBookingAttempts = 3

// maxPairAttempts acota el barrido de pares (profesional, unidad) de una
// reserva: 5 profesionales x 6 unidades ya son 30 intentos.
const maxPairAttempts = 40

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// equipment resuelve las unidades ACTIVAS del tipo de equipo del servicio. Sin
// tipo devuelve nil (el turno no ocupa unidad). Sin unidades activas es 409:
// el servicio está configurado pero hoy no se puede atender.
func (s PublicBookingService) equipment(salonID, typeID string) ([]domain.ResourceUnit, error) {
	if strings.TrimSpace(typeID) == "" {
		return nil, nil
	}
	if s.Resources == nil {
		return nil, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
	}
	units, err := s.Resources.ActiveUnits(salonID, typeID)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("%w: el horario ya no está disponible", domain.ErrAlreadyExists)
	}
	return units, nil
}

// freeProfessionals son los candidatos que HACEN el servicio, TRABAJAN ese
// horario (F2d1) y están presentes (F2d2), en el orden del equipo. Que estén
// libres de turno se mira al elegir el par: el estado cambia entre reintentos.
func (s PublicBookingService) freeProfessionals(staff []domain.StaffMember, eligible, scheduled, away map[string]bool) []string {
	out := make([]string, 0, len(staff))
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
		// Y solo quien está: las vacaciones y los días libres puntuales tampoco
		// se saltean (F2d2).
		if away[m.ID] {
			continue
		}
		out = append(out, m.ID)
	}
	return out
}

// pickPair elige la combinación (profesional, unidad) a intentar: un
// profesional sin turno en el hueco y una unidad ACTIVA libre en todo el
// intervalo, salteando los pares que ya rechazó la DB. Sin combinación posible
// es 409 con el mensaje del camino (asignación automática o profesional
// elegido), nunca un 500.
func (s PublicBookingService) pickPair(salonID string, candidates []string, units []domain.ResourceUnit, start, end time.Time, attempted map[string]bool, msg string) (string, string, error) {
	var busy []domain.UnitOccupancy
	if units != nil {
		var err error
		busy, err = s.Resources.OccupiedUnits(salonID, start.UTC(), end.UTC(), "")
		if err != nil {
			return "", "", err
		}
	}
	noRoom := fmt.Errorf("%w: %s", domain.ErrAlreadyExists, msg)
	for _, prof := range candidates {
		taken, err := s.Bookings.FindOverlapping(salonID, prof, start, end)
		if err != nil {
			return "", "", err
		}
		if len(taken) > 0 {
			continue
		}
		if units == nil {
			if !attempted[pairKey(prof, "")] {
				return prof, "", nil
			}
			continue
		}
		free := domain.FreeUnits(units, busy, start, end)
		for _, u := range free {
			if !attempted[pairKey(prof, u.ID)] {
				return prof, u.ID, nil
			}
		}
	}
	return "", "", noRoom
}

// pairKey identifica un par intentado para no repetirlo en los reintentos.
func pairKey(professionalID, unitID string) string { return professionalID + "|" + unitID }

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
