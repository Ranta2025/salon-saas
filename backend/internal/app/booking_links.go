// Enlace firmado de un solo uso para cancelar o reprogramar el turno desde el
// email (F1 del contrato de la fase 6).
//
// Reglas que viven acá (D1..D9):
//   - un token NUEVO por cada email enviado, guardado solo como SHA-256. Un turno
//     sin email no tiene enlace;
//   - un solo uso exitoso: el reclamo (used_at) y el cambio del turno commitean
//     juntos, así un horario ocupado NO consume el token y un cancelado sí;
//   - la vigencia la decide el TURNO (está `booked` y faltan 2 horas o más): no
//     hay `expires_at`, un turno cancelado mata el enlace y uno movido por el
//     personal vale contra el horario nuevo;
//   - todo pasa en UNA transacción con UN advisory lock (el mismo
//     `booking-write:<salón>` de Book y Update): los pedidos simultáneos con el
//     mismo token se serializan y solo uno gana; el EXCLUDE de solape sigue
//     siendo la última barrera del horario nuevo;
//   - respuestas sin datos personales: ni teléfono, ni email, ni ids internos, y
//     el token siempre en el cuerpo (nunca en la ruta ni en el query);
//   - los eventos de tiempo real y los avisos internos son los MISMOS que publica
//     BookingService.Update, y los correos salen en segundo plano: un SMTP caído
//     nunca cambia la respuesta.
package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// bookingLinkTimeout acota el envío del correo de cambio de turno. Va en segundo
// plano (la respuesta no espera al SMTP) y con techo propio, igual que la
// confirmación de la reserva.
const bookingLinkTimeout = 15 * time.Second

// BookingLinkView es lo que se muestra de un turno abierto por su enlace. No
// lleva teléfono, email ni ids internos (D6): nombre del salón, qué se hace, con
// quién y cuándo, en la zona del salón.
type BookingLinkView struct {
	SalonName        string
	ServiceName      string
	ProfessionalName string
	StartsAt         time.Time
	EndsAt           time.Time
	Timezone         string
	// CanChange es false cuando faltan menos de LinkMinNoticeHours: el turno se
	// muestra igual, pero no se puede cancelar ni reprogramar.
	CanChange bool
}

// BookingLinkService: las cuatro rutas públicas del enlace.
type BookingLinkService struct {
	// Links y Tx: el alta del token y la transacción de un solo uso.
	Links ports.BookingLinkRepository
	Tx    ports.BookingLinkTxRunner
	// Bookings, Salons, Services y Staff son las lecturas del turno y de lo que
	// la pantalla muestra (nunca datos personales del cliente).
	Bookings ports.BookingRepository
	Salons   ports.SalonRepository
	Services ports.ServiceRepository
	Staff    ports.StaffLister
	// Settings da la zona horaria del salón (el día local de los horarios y el
	// texto del correo).
	Settings ports.SalonSettingsRepository
	// Professionals, Hours, TimeOff y Resources son las mismas reglas de una
	// reserva nueva (D5): el horario nuevo tiene que entrar en la ventana del
	// salón y de la persona, no pisar una ausencia y dejar libre una unidad de
	// equipo.
	Professionals ports.ProfessionalServiceRepository
	Hours         ports.WeeklyHoursRepository
	TimeOff       ports.TimeOffRepository
	Resources     ports.ResourceRepository
	// Availability ofrece los horarios del día excluyendo el propio turno.
	Availability AvailabilityService
	// Events y Notifier: los MISMOS del calendario del salón (opcionales).
	Events   ports.EventPublisher
	Notifier Notifier
	// Mailer y Emails: el aviso al cliente (opcionales). Emails dice si el turno
	// tiene dirección: sin dirección no hay correo ni token nuevo (D1).
	Mailer ports.MailSender
	Emails ports.BookingEmailFinder
	AppURL string
	// Now es el reloj del caso de uso; nil = time.Now.
	Now func() time.Time
}

// Lookup describe el turno de un token vigente. Cualquier motivo para no servir
// (no existe, ya se usó, está mal formado, o su turno ya no está vigente)
// devuelve el MISMO 404 con el MISMO texto: distinguirlos sería un oráculo (D6).
func (s BookingLinkService) Lookup(token string) (BookingLinkView, error) {
	got, err := s.resolve(token)
	if err != nil {
		return BookingLinkView{}, err
	}
	return s.describe(got)
}

// Slots ofrece los horarios libres de un día LOCAL del salón para el MISMO
// servicio y el MISMO profesional del turno, con el propio turno excluido de los
// conflictos (D5). Con menos de 2 horas de anticipación es 409: el turno sigue
// vigente pero ya no se puede cambiar.
func (s BookingLinkService) Slots(token, date string) (DayAvailability, error) {
	got, err := s.resolve(token)
	if err != nil {
		return DayAvailability{}, err
	}
	if !got.booking.LinkCanChange(s.now()) {
		return DayAvailability{}, bookingLinkMinNotice()
	}
	loc, err := got.settings.Location()
	if err != nil {
		return DayAvailability{}, err
	}
	if _, err := time.ParseInLocation(dateLayout, strings.TrimSpace(date), loc); err != nil {
		return DayAvailability{}, domain.NewMessage(domain.ErrValidation, domain.MsgBookingLinkDate)
	}
	return s.Availability.DayExcluding(got.salon.Slug, date, got.booking.ServiceID, got.booking.ProfessionalID, got.booking.ID)
}

// Cancel cancela el turno del enlace y devuelve el error del contrato.
//
// El reclamo del token y la cancelación commitean juntos: si el turno ya no se
// puede cambiar (menos de 2 horas), la transacción se deshace y el token sigue
// vivo (D2).
func (s BookingLinkService) Cancel(token string) error {
	var done linkedBooking
	err := s.Tx.InBookingLinkTx(func(tx ports.BookingLinkTx) error {
		got, err := s.claim(tx, token)
		if err != nil {
			return err
		}
		cancelled, err := got.booking.SetStatus(domain.BookingCancelled)
		if err != nil {
			return err
		}
		if err := tx.UpdateBooking(cancelled); err != nil {
			return err
		}
		got.booking = cancelled
		done = got
		return nil
	})
	if err != nil {
		return err
	}
	// Después del commit: el calendario del salón se actualiza solo y el mostrador
	// ve el mismo aviso que un cancelado desde la agenda (D8).
	s.announce(done.booking, false)
	s.sendChangeMail(done.booking, mailCancelNotice, "")
	return nil
}

// mailCancelNotice/mailRescheduleNotice dicen qué plantilla usar.
const (
	mailCancelNotice = iota
	mailRescheduleNotice
)

// Reschedule mueve el turno a `startsAt` (RFC3339 ya parseado) con las MISMAS
// reglas que una reserva pública nueva (D5) y emite un token nuevo por email.
// Devuelve si el correo con el enlace nuevo quedó encolado (new_link_sent); el
// token NUNCA viaja en la respuesta.
func (s BookingLinkService) Reschedule(token string, startsAt time.Time) (BookingLinkView, bool, error) {
	none := BookingLinkView{}
	if startsAt.IsZero() {
		return none, false, domain.NewMessage(domain.ErrValidation, domain.MsgBookingLinkInvalid)
	}
	// Lectura previa (fuera de la transacción, como hace la reserva pública): con
	// qué turno se trabaja y qué hay que validar. La transacción vuelve a leer el
	// turno y es la que decide.
	got, err := s.resolve(token)
	if err != nil {
		return none, false, err
	}
	if !got.booking.LinkCanChange(s.now()) {
		return none, false, bookingLinkMinNotice()
	}
	if startsAt.Equal(got.booking.StartsAt) {
		return none, false, domain.NewMessage(domain.ErrValidation, domain.MsgBookingLinkSameSlot)
	}
	now := s.now()
	// El nuevo horario tiene que ser futuro: mover un turno vigente a un instante
	// que ya pasó dejaría un turno "ya empezado" que nadie atiende (R3-001). Es el
	// mismo 409 que cualquier horario que no entra; el token no se consume.
	if !startsAt.After(now) {
		return none, false, bookingLinkSlotTaken()
	}
	unitID, endsAt, err := s.checkNewTime(got, startsAt)
	if err != nil {
		return none, false, err
	}

	// La dirección se lee ANTES de la transacción (es una lectura de la cola de
	// recordatorios, no del cambio) y el token nuevo se genera afuera: adentro
	// solo se escribe, para no sostener la conexión mientras se pide entropía.
	var email string
	if s.Emails != nil {
		found, err := s.Emails.FindEmail(got.salon.ID, got.booking.ID)
		switch {
		case err == nil:
			email = found
		case !errors.Is(err, domain.ErrNotFound):
			return none, false, err
		}
	}
	newToken := ""
	if email != "" && s.Links != nil {
		token, err := domain.NewBookingLinkToken()
		if err != nil {
			return none, false, err
		}
		newToken = token
	}

	var done linkedBooking
	err = s.Tx.InBookingLinkTx(func(tx ports.BookingLinkTx) error {
		claimed, err := s.claim(tx, token)
		if err != nil {
			return err
		}
		// La verdad es la fila de adentro: si el personal movió el turno entre la
		// lectura y el lock, el horario elegido ya se validó contra otro turno y
		// hay que volver a elegir (409, el token no se consume).
		if !sameBooking(claimed.booking, got.booking) {
			return bookingLinkSlotTaken()
		}
		moved, err := claimed.booking.Reschedule("", startsAt, endsAt)
		if err != nil {
			return bookingLinkSlotTaken()
		}
		if unitID != "" {
			moved.ResourceUnitID = unitID
		}
		// La EXCLUDE de Postgres decide el solape de verdad: si rechaza, el
		// token no se consume (la transacción se deshace con él ya reclamado).
		if err := tx.UpdateBooking(moved); err != nil {
			if errors.Is(err, domain.ErrAlreadyExists) {
				return bookingLinkSlotTaken()
			}
			return err
		}
		if newToken != "" {
			link, err := domain.NewBookingLink(claimed.booking.SalonID, moved.ID, domain.HashBookingLinkToken(newToken), now)
			if err != nil {
				return err
			}
			if err := tx.SaveLink(link); err != nil {
				return err
			}
		}
		done = linkedBooking{booking: moved, salon: got.salon, settings: got.settings}
		return nil
	})
	if err != nil {
		return none, false, err
	}
	s.announce(done.booking, true)
	// El aviso al cliente sale solo si hay enlace nuevo que mandar: `new_link_sent`
	// dice exactamente eso (si no se pudo emitir el token, el turno se movió y la
	// persona se entera por el próximo correo, no con un aviso sin enlace).
	sent := false
	if newToken != "" {
		sent = s.sendChangeMail(done.booking, mailRescheduleNotice, bookingLinkURL(s.AppURL, newToken))
	}
	view, err := s.describe(done)
	if err != nil {
		return none, sent, err
	}
	return view, sent, nil
}

// linkedBooking es un enlace ya cruzado con su turno, su salón y su
// configuración: lo que necesitan la pantalla, el evento y el correo.
type linkedBooking struct {
	booking  domain.Booking
	salon    domain.Salon
	settings domain.SalonSettings
}

// resolve es el camino de las lecturas (lookup y horarios): enlace vivo + turno
// vigente. Cualquier motivo de "no sirve" es el 404 genérico.
func (s BookingLinkService) resolve(token string) (linkedBooking, error) {
	link, err := s.linkByToken(token)
	if err != nil {
		return linkedBooking{}, err
	}
	b, err := s.Bookings.FindByID(link.SalonID, link.BookingID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return linkedBooking{}, bookingLinkInvalid()
		}
		return linkedBooking{}, err
	}
	// Defensa en profundidad: el turno tiene que ser del salón del enlace y estar
	// vigente (booked y en el futuro).
	if b.SalonID != link.SalonID || !b.LinkLive(s.now()) {
		return linkedBooking{}, bookingLinkInvalid()
	}
	salon, err := s.Salons.FindByID(b.SalonID)
	if err != nil {
		return linkedBooking{}, err
	}
	settings, err := loadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return linkedBooking{}, err
	}
	return linkedBooking{booking: b, salon: salon, settings: settings}, nil
}

// linkByToken resuelve el token hasheado y exige que el enlace siga sin usar.
// Cualquier otra cosa es el 404 genérico: no se distingue "no existe" de "ya se
// usó" ni de "mal formado".
func (s BookingLinkService) linkByToken(token string) (domain.BookingLink, error) {
	if !domain.ValidBookingLinkToken(token) {
		return domain.BookingLink{}, bookingLinkInvalid()
	}
	link, err := s.Links.FindByTokenHash(domain.HashBookingLinkToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.BookingLink{}, bookingLinkInvalid()
		}
		return domain.BookingLink{}, err
	}
	// Comparación de tiempo constante contra el hash guardado y un solo uso.
	if !domain.BookingLinkTokenMatches(link.TokenHash, token) || link.Used() {
		return domain.BookingLink{}, bookingLinkInvalid()
	}
	return link, nil
}

// claim es el corazón de la escritura (D2/D4), siempre DENTRO de la transacción:
//
//  1. resuelve el enlace por su hash (es lo único que dice de qué salón es);
//  2. toma el ÚNICO lock de la operación: `booking-write:<salón>`, el mismo de
//     Book y Update;
//  3. reclama el token con un UPDATE condicional (used_at IS NULL). Los pedidos
//     simultáneos del mismo token ya vieron el usado: el que pierde recibe el 404
//     genérico;
//  4. lee el turno scopeado por el salón y valida la vigencia en vivo.
//
// Si cualquiera de los pasos posteriores falla, el error deshace la transacción y
// el token NO queda consumido.
func (s BookingLinkService) claim(tx ports.BookingLinkTx, token string) (linkedBooking, error) {
	if !domain.ValidBookingLinkToken(token) {
		return linkedBooking{}, bookingLinkInvalid()
	}
	hash := domain.HashBookingLinkToken(token)
	link, err := tx.FindLink(hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return linkedBooking{}, bookingLinkInvalid()
		}
		return linkedBooking{}, err
	}
	if !domain.BookingLinkTokenMatches(link.TokenHash, token) {
		return linkedBooking{}, bookingLinkInvalid()
	}
	if err := tx.LockWrites(link.SalonID); err != nil {
		return linkedBooking{}, err
	}
	claimed, err := tx.ClaimLink(link.ID, s.now())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return linkedBooking{}, bookingLinkInvalid()
		}
		return linkedBooking{}, err
	}
	b, err := tx.FindBooking(claimed.SalonID, claimed.BookingID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return linkedBooking{}, bookingLinkInvalid()
		}
		return linkedBooking{}, err
	}
	if b.SalonID != claimed.SalonID || !b.LinkLive(s.now()) {
		return linkedBooking{}, bookingLinkInvalid()
	}
	if !b.LinkCanChange(s.now()) {
		// El turno sigue vigente pero ya no se puede tocar: 409 y la transacción
		// (con el token reclamado) se deshace.
		return linkedBooking{}, bookingLinkMinNotice()
	}
	return linkedBooking{booking: b}, nil
}

// describe arma la vista pública del turno. Los datos del cliente NO entran: ni
// nombre, ni teléfono, ni email, ni ids internos (D6).
func (s BookingLinkService) describe(got linkedBooking) (BookingLinkView, error) {
	view := BookingLinkView{
		SalonName: got.salon.Name,
		StartsAt:  got.booking.StartsAt,
		EndsAt:    got.booking.EndsAt,
		Timezone:  got.settings.Timezone,
		CanChange: got.booking.LinkCanChange(s.now()),
	}
	if strings.TrimSpace(got.booking.ServiceID) != "" {
		service, err := s.Services.FindByID(got.salon.ID, got.booking.ServiceID)
		switch {
		case err == nil:
			view.ServiceName = service.Name
		case errors.Is(err, domain.ErrNotFound):
			// El servicio se borró del catálogo: el turno sigue en pie y la
			// pantalla muestra el salón y el horario, sin inventar un nombre.
		default:
			return BookingLinkView{}, err
		}
	}
	list, err := s.Staff.ListStaff(got.salon.ID)
	if err != nil {
		return BookingLinkView{}, err
	}
	for _, m := range list {
		if m.ID == got.booking.ProfessionalID {
			view.ProfessionalName = m.Name
		}
	}
	return view, nil
}

// checkNewTime valida el horario nuevo con las reglas de la reserva pública (D5)
// para el MISMO servicio y el MISMO profesional, con este turno excluido de los
// conflictos. Devuelve la unidad de equipo que ocupará. Cualquier horario que no
// entre es el 409 del contrato (nunca un 500 y nunca un "no se puede" con otro
// texto).
func (s BookingLinkService) checkNewTime(got linkedBooking, startsAt time.Time) (string, time.Time, error) {
	b := got.booking
	settings := got.settings
	loc, err := settings.Location()
	if err != nil {
		return "", time.Time{}, err
	}
	service, err := s.Services.FindByID(got.salon.ID, b.ServiceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", time.Time{}, bookingLinkSlotTaken()
		}
		return "", time.Time{}, err
	}
	if !service.Active {
		return "", time.Time{}, bookingLinkSlotTaken()
	}
	// La duración es la del servicio HOY, igual que una reserva nueva y que los
	// horarios de `slots`: si el salón la cambió después de reservar, el turno
	// movido la sigue.
	endsAt := startsAt.Add(time.Duration(service.DurationMin) * time.Minute)
	// El profesional tiene que seguir haciendo ese servicio: si el salón se lo
	// quitó, el turno no puede volver a entrar por el enlace.
	if s.Professionals != nil {
		eligible, err := s.Professionals.ListProfessionalsFor(got.salon.ID, service.ID)
		if err != nil {
			return "", time.Time{}, err
		}
		does := false
		for _, m := range eligible {
			if m.ID == b.ProfessionalID {
				does = true
			}
		}
		if !does {
			return "", time.Time{}, bookingLinkSlotTaken()
		}
	}

	// Ventana del salón y de la persona (F2d1), en el día LOCAL del salón.
	single, err := singleWindow(settings)
	if err != nil {
		return "", time.Time{}, err
	}
	weekly, err := salonWeeklyHours(s.Hours, got.salon.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	segments, err := memberHours(s.Hours, got.salon.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	weekday := domain.IsoWeekday(startsAt, loc)
	startMin := localMinutes(startsAt, loc)
	endMin := startMin + int(endsAt.Sub(startsAt)/time.Minute)
	salonWindows, err := salonWindow(weekly, single, weekday)
	if err != nil {
		return "", time.Time{}, err
	}
	if len(salonWindows) == 0 || !salonWindows[0].Contains(startMin, endMin) {
		return "", time.Time{}, bookingLinkSlotTaken()
	}
	plan := domain.DayPlan{Single: single, Weekly: weekly, Segments: segments[b.ProfessionalID]}
	fits, err := plan.Fits(weekday, startMin, endMin)
	if err != nil {
		return "", time.Time{}, err
	}
	if !fits {
		return "", time.Time{}, bookingLinkSlotTaken()
	}

	// Ausencias (F2d2): a quien está ausente no se le mueve un turno encima.
	absences, err := timeOffRange(s.TimeOff, got.salon.ID, startsAt, endsAt)
	if err != nil {
		return "", time.Time{}, err
	}
	if awayAny(absences, b.ProfessionalID, startsAt, endsAt) {
		return "", time.Time{}, bookingLinkSlotTaken()
	}

	// Solape con otro turno del mismo profesional (el EXCLUDE de la transacción
	// sigue siendo la barrera real; esto es el filtro amable de la reserva).
	busy, err := s.Bookings.FindOverlapping(got.salon.ID, b.ProfessionalID, startsAt, endsAt)
	if err != nil {
		return "", time.Time{}, err
	}
	for _, other := range busy {
		if other.ID != b.ID {
			return "", time.Time{}, bookingLinkSlotTaken()
		}
	}

	// Equipo (F2e): se conserva la unidad si sigue libre y, si no, se toma otra
	// ACTIVA del mismo tipo. Cambiar de unidad es válido: el turno no cambia de
	// servicio ni de profesional.
	if strings.TrimSpace(b.ResourceUnitID) == "" {
		return "", endsAt, nil
	}
	if s.Resources == nil {
		return "", time.Time{}, bookingLinkSlotTaken()
	}
	occupied, err := s.Resources.OccupiedUnits(got.salon.ID, startsAt, endsAt, b.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	units, err := s.Resources.ActiveUnits(got.salon.ID, service.ResourceTypeID)
	if err != nil {
		return "", time.Time{}, err
	}
	// Una unidad desactivada no se conserva (una reserva nueva nunca cae en ella).
	stillActive := false
	for _, u := range units {
		if u.ID == b.ResourceUnitID {
			stillActive = true
		}
	}
	if stillActive && domain.UnitFree(occupied, b.ResourceUnitID, startsAt, endsAt) {
		return b.ResourceUnitID, endsAt, nil
	}
	free := domain.FreeUnits(units, occupied, startsAt, endsAt)
	if len(free) == 0 {
		return "", time.Time{}, bookingLinkSlotTaken()
	}
	return free[0].ID, endsAt, nil
}

// announce publica lo MISMO que BookingService.Update (D8): el evento
// `booking.updated` para que el calendario del salón se refresque solo y el aviso
// interno del mostrador con la hora local.
func (s BookingLinkService) announce(b domain.Booking, rescheduled bool) {
	publish(s.Events, ports.Event{
		Type:    "booking.updated",
		SalonID: b.SalonID,
		Data: map[string]any{
			"id":              b.ID,
			"professional_id": b.ProfessionalID,
			"client_name":     b.ClientName,
			"starts_at":       b.StartsAt.Format(time.RFC3339),
			"ends_at":         b.EndsAt.Format(time.RFC3339),
			"status":          string(b.Status),
		},
		TS: time.Now().UTC(),
	})
	title := "Turno reprogramado"
	if !rescheduled {
		title = bookingChangeTitle(b.Status)
	}
	notify(s.Notifier, b.SalonID, "", title,
		b.ClientName+" · "+salonTimeLabel(s.Settings, b.SalonID, b.StartsAt), domain.KindBooking)
}

// sendChangeMail avisa al cliente del cambio en SEGUNDO PLANO (best effort): un
// SMTP caído se loguea y nunca cambia la respuesta. Devuelve si el correo quedó
// encolado, que es lo que el contrato reporta como `new_link_sent`.
//
// El log nunca lleva la dirección ni el enlace: el id del turno y el salón
// alcanzan para diagnosticar.
func (s BookingLinkService) sendChangeMail(b domain.Booking, kind int, link string) bool {
	if s.Mailer == nil || s.Emails == nil {
		return false
	}
	email, err := s.Emails.FindEmail(b.SalonID, b.ID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			log.Printf("enlace del turno %s del salón %s: leer la dirección: %v", b.ID, b.SalonID, err)
		}
		return false
	}
	salon, err := s.Salons.FindByID(b.SalonID)
	if err != nil {
		log.Printf("enlace del turno %s del salón %s: leer el salón: %v", b.ID, b.SalonID, err)
		return false
	}
	settings, err := loadSalonSettings(s.Settings, b.SalonID)
	if err != nil {
		log.Printf("enlace del turno %s del salón %s: leer la configuración: %v", b.ID, b.SalonID, err)
		settings = domain.SalonSettings{SalonID: b.SalonID, Timezone: "UTC"}
	}
	// La reprogramación reusa la plantilla de confirmación (el turno nuevo queda
	// confirmado con SU enlace) y la cancelación tiene su propio asunto.
	mailKind := bookingMailCancelled
	if kind == mailRescheduleNotice {
		mailKind = bookingMailConfirmation
	}
	msg := bookingMailWithLink(mailKind, salon.Name, b.ClientName, b.StartsAt, settings, link)
	msg.To = email
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("enlace del turno %s: panic enviando el aviso: %v", b.ID, r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), bookingLinkTimeout)
		defer cancel()
		if err := s.Mailer.Send(ctx, msg); err != nil {
			log.Printf("enlace del turno %s del salón %s: enviar el aviso: %v", b.ID, b.SalonID, err)
		}
	}()
	return true
}

// sameBooking dice si la fila leída dentro de la transacción es la misma con la
// que se validó el horario nuevo: mismo turno, mismo horario, mismo servicio y
// mismo profesional. Si el personal lo movió en el medio, hay que volver a elegir.
func sameBooking(now, before domain.Booking) bool {
	return now.ID == before.ID &&
		now.ProfessionalID == before.ProfessionalID &&
		now.ServiceID == before.ServiceID &&
		now.StartsAt.Equal(before.StartsAt) &&
		now.EndsAt.Equal(before.EndsAt)
}

func (s BookingLinkService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// mintBookingLink emite un token NUEVO y guarda SOLO su SHA-256. Devuelve el
// enlace y el token en claro (que solo vive en la llamada y en el correo).
func mintBookingLink(repo ports.BookingLinkRepository, salonID, bookingID string, now time.Time) (domain.BookingLink, string, error) {
	token, err := domain.NewBookingLinkToken()
	if err != nil {
		return domain.BookingLink{}, "", err
	}
	link, err := domain.NewBookingLink(salonID, bookingID, domain.HashBookingLinkToken(token), now)
	if err != nil {
		return domain.BookingLink{}, "", err
	}
	if err := repo.Save(link); err != nil {
		return domain.BookingLink{}, "", err
	}
	return link, token, nil
}

// --- errores del contrato (mismo texto y mismo status en todas las rutas) ---

// bookingLinkInvalid es el 404 único de "este enlace no sirve" (D6).
func bookingLinkInvalid() error {
	return domain.NewMessage(domain.ErrNotFound, domain.MsgBookingLinkInvalid)
}

// bookingLinkMinNotice es el 409 de un turno dentro de las 2 horas (D3).
func bookingLinkMinNotice() error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgBookingLinkMinNotice)
}

// bookingLinkSlotTaken es el 409 de un horario nuevo que ya no entra (D5).
func bookingLinkSlotTaken() error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgBookingLinkSlotTaken)
}
