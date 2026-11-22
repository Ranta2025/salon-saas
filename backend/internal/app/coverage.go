// CoverageService: cubrir una ausencia (F2d3). Cuando alguien falta, el salón
// reasigna sus turnos a otra profesional que pueda tomarlos y avisa a la clienta.
//
//	GET  /salons/:id/absences?from&to
//	POST /salons/:id/absences/:tid/reassign
//	POST /salons/:id/bookings/:bid/reassign      (+ cancelar con motivo)
//
// Decisiones:
//   - Es MANUAL: elegir quién cubre es del salón. Acá no se elige sola.
//   - No hay tabla nueva: reasignar es cambiar professional_id del turno y la
//     EXCLUDE de bookings sigue siendo la que impide el doble turno (S-F2d3-1).
//   - "Reasignar todo" es TODO O NADA: una transacción con lock por salón mueve
//     los turnos o no mueve ninguno (S-F2d3-3).
//   - La regla de quién puede cubrir es del dominio (domain.CoverageCandidate):
//     atiende (F2a), está de turno ese día (F2d1), no está ausente (F2d2) y hace
//     el servicio (F2c).
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// coverSendTimeout es el techo de UN correo de cambio de turno: un SMTP colgado
// no puede frenar la respuesta ni el proceso.
const coverSendTimeout = 15 * time.Second

type CoverageService struct {
	Bookings    ports.BookingRepository
	Memberships ports.MembershipRepository
	// Staff da la plantilla con nombre (la lista de candidatas y los avisos).
	Staff ports.StaffLister
	// Professionals dice quién hace cada servicio (F2c).
	Professionals ports.ProfessionalServiceRepository
	// Hours es el horario semanal (F2d1): quien no trabaja ese día no cubre.
	Hours ports.WeeklyHoursRepository
	// TimeOff son las ausencias (F2d2): quien está ausente tampoco.
	TimeOff ports.TimeOffRepository
	// Services solo da el NOMBRE del servicio de cada turno en el listado.
	Services ports.ServiceRepository
	// Settings da la zona horaria del salón para los textos y el horario.
	Settings ports.SalonSettingsRepository
	Salons   ports.SalonRepository
	// Tx es la transacción del "todo o nada". Sin ella no se reasigna: media
	// reasignación es peor que ninguna (S-F2d3-3).
	Tx       ports.BookingTxRunner
	Events   ports.EventPublisher
	Notifier Notifier
	// Email a la clienta (T12). Opcionales: nil = este entorno no manda correos.
	Mailer ports.MailSender
	Emails ports.BookingEmailFinder
}

// CoverageConflict es la negativa de una reasignación CON el detalle de los
// turnos que no se pudieron mover (S-F2d3-3). El status HTTP lo decide Kind:
// ErrValidation = 400 (la regla no se cumple) y ErrAlreadyExists = 409 (el turno
// ya no está reservado o el destino ya tiene ese horario).
type CoverageConflict struct {
	Kind     error
	Text     string
	Bookings []domain.Booking
}

func (e *CoverageConflict) Error() string { return e.Text }
func (e *CoverageConflict) Unwrap() error { return e.Kind }

// AbsenceCoverage es una ausencia con los turnos que hay que cubrir.
type AbsenceCoverage struct {
	TimeOff  domain.TimeOff
	UserName string
	Bookings []CoverageBooking
}

// CoverageBooking es un turno de la ausencia con sus candidatas.
type CoverageBooking struct {
	Booking     domain.Booking
	ServiceName string
	Candidates  []CoverageCandidate
}

// CoverageCandidate es una persona de la plantilla vista por el selector: si
// Reason está vacío puede tomar el turno; si no, ese es el motivo a mostrar (el
// mismo texto que devuelve el POST como error).
type CoverageCandidate struct {
	ProfessionalID string
	Name           string
	Reason         string
}

// ReassignResult es la respuesta de una reasignación: cuántos turnos se movieron
// y cuáles (con su horario intacto).
type ReassignResult struct {
	Reassigned int
	Bookings   []domain.Booking
}

// Absences lista las ausencias del período con sus turnos reservados y las
// candidatas de cada turno. Solo dueño/administración: el motivo es privado
// (S-F2d3-2).
func (s CoverageService) Absences(actor domain.Membership, salonID string, from, to time.Time) ([]AbsenceCoverage, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !m.CanManage() {
		return nil, domain.ErrForbidden
	}
	if err := domain.ValidateTimeOffRange(from, to); err != nil {
		return nil, err
	}
	if s.TimeOff == nil {
		return []AbsenceCoverage{}, nil
	}
	list, err := s.TimeOff.ListRange(salonID, from, to)
	if err != nil {
		return nil, err
	}
	set, err := s.loadCandidates(salonID)
	if err != nil {
		return nil, err
	}
	out := make([]AbsenceCoverage, 0, len(list))
	for _, off := range list {
		item := AbsenceCoverage{TimeOff: off, UserName: set.name(off.UserID)}
		bookings, err := s.absenceBookings(off)
		if err != nil {
			return nil, err
		}
		for _, b := range bookings {
			candidates, err := s.candidatesFor(set, b, true)
			if err != nil {
				return nil, err
			}
			item.Bookings = append(item.Bookings, CoverageBooking{
				Booking:     b,
				ServiceName: s.serviceName(salonID, b.ServiceID),
				Candidates:  displayCandidates(candidates, b),
			})
		}
		out = append(out, item)
	}
	return out, nil
}

// ReassignAbsence mueve los turnos de una ausencia a otra profesional. Con
// bookingIDs vacío mueve TODOS los de la ausencia; con una lista, solo esos.
func (s CoverageService) ReassignAbsence(actor domain.Membership, salonID, absenceID, professionalID string, bookingIDs []string) (ReassignResult, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return ReassignResult{}, err
	}
	if !m.CanManage() {
		return ReassignResult{}, domain.ErrForbidden
	}
	if s.TimeOff == nil {
		return ReassignResult{}, domain.ErrNotFound
	}
	off, err := s.TimeOff.FindByID(salonID, strings.TrimSpace(absenceID))
	if err != nil {
		return ReassignResult{}, err
	}
	all, err := s.absenceBookings(off)
	if err != nil {
		return ReassignResult{}, err
	}
	targets, err := s.selectCoverageBookings(off, all, bookingIDs)
	if err != nil {
		return ReassignResult{}, err
	}
	return s.reassign(salonID, targets, professionalID)
}

// ReassignBooking reasigna UN turno suelto (el calendario, o un turno que la
// ausencia no explica).
func (s CoverageService) ReassignBooking(actor domain.Membership, salonID, bookingID, professionalID string) (ReassignResult, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return ReassignResult{}, err
	}
	if !m.CanManage() {
		return ReassignResult{}, domain.ErrForbidden
	}
	b, err := s.Bookings.FindByID(salonID, strings.TrimSpace(bookingID))
	if err != nil {
		return ReassignResult{}, err
	}
	if err := b.Coverable(); err != nil {
		return ReassignResult{}, conflictFrom(err, []domain.Booking{b})
	}
	return s.reassign(salonID, []domain.Booking{b}, professionalID)
}

// CancelBooking cancela un turno desde la pantalla de cobertura y REGISTRA el
// motivo en el aviso interno (S-F2d3-2). La clienta recibe que el salón la
// contacta.
func (s CoverageService) CancelBooking(actor domain.Membership, salonID, bookingID, reason string) (domain.Booking, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Booking{}, err
	}
	if !m.CanManage() {
		return domain.Booking{}, domain.ErrForbidden
	}
	b, err := s.Bookings.FindByID(salonID, strings.TrimSpace(bookingID))
	if err != nil {
		return domain.Booking{}, err
	}
	if err := b.Coverable(); err != nil {
		return domain.Booking{}, conflictFrom(err, []domain.Booking{b})
	}
	motivo, err := domain.NormalizeCoverReason(reason)
	if err != nil {
		return domain.Booking{}, err
	}
	cancelled, err := b.SetStatus(domain.BookingCancelled)
	if err != nil {
		return domain.Booking{}, err
	}
	if err := s.Bookings.Update(cancelled); err != nil {
		return domain.Booking{}, err
	}
	publish(s.Events, ports.Event{
		Type:    "booking.updated",
		SalonID: salonID,
		Data:    bookingEventData(cancelled),
		TS:      time.Now().UTC(),
	})
	notify(s.Notifier, salonID, "", "Turno cancelado",
		cancelNoticeBody(cancelled, motivo, salonTimeLabel(s.Settings, salonID, cancelled.StartsAt)),
		domain.KindBooking)
	s.sendCoverMail(salonID, cancelled, "", coverMailCancelled)
	return cancelled, nil
}

// reassign valida el destino contra cada turno y después mueve todo dentro de
// UNA transacción: si un turno no entra, no se movió ninguno (S-F2d3-3).
func (s CoverageService) reassign(salonID string, targets []domain.Booking, professionalID string) (ReassignResult, error) {
	if len(targets) == 0 {
		return ReassignResult{Bookings: []domain.Booking{}}, nil
	}
	// Sin destino no hay a quién reasignar: el pedido tiene que nombrar a alguien
	// (elegir quién cubre es una decisión del salón, no del servidor).
	if strings.TrimSpace(professionalID) == "" {
		return ReassignResult{}, conflictFrom(domain.NewMessage(domain.ErrValidation, domain.MsgCoverNoCandidate), targets)
	}
	set, err := s.loadCandidates(salonID)
	if err != nil {
		return ReassignResult{}, err
	}
	// Plan: la validación del dominio es por turno (el servicio cambia de turno
	// a turno) y se hace ANTES de abrir la transacción, así un pedido inválido no
	// llega a escribir nada.
	plan := make([]plannedMove, 0, len(targets))
	for _, b := range targets {
		candidates, err := s.candidatesFor(set, b, false)
		if err != nil {
			return ReassignResult{}, err
		}
		target, ok := findCandidate(candidates, professionalID)
		if !ok {
			// Un id que no está en la plantilla no atiende en este salón.
			target = domain.CoverageCandidate{}
		}
		if err := target.Covers(b); err != nil {
			return ReassignResult{}, conflictFrom(err, []domain.Booking{b})
		}
		moved, err := b.Reassign(target.Professional.ID)
		if err != nil {
			return ReassignResult{}, conflictFrom(err, []domain.Booking{b})
		}
		plan = append(plan, plannedMove{from: b, to: moved, professionalName: target.Professional.Name})
	}
	if s.Tx == nil {
		return ReassignResult{}, fmt.Errorf("cobertura: falta la transacción")
	}
	err = s.Tx.InSalonTx(salonID, func(tx ports.BookingTx) error {
		for _, move := range plan {
			// El turno se relee DENTRO de la transacción: entre la validación y el
			// lock alguien pudo cancelarlo o moverlo, y este pase no puede pisar eso.
			current, err := tx.FindByID(salonID, move.from.ID)
			if err != nil {
				return err
			}
			if err := current.Coverable(); err != nil {
				return conflictFrom(err, []domain.Booking{current})
			}
			if !sameSlot(current, move.from) {
				return conflictFrom(domain.NewMessage(domain.ErrAlreadyExists, domain.MsgCoverNotBooked),
					[]domain.Booking{current})
			}
			// La EXCLUDE de la tabla es la que decide el doble turno: acá no hay
			// pre-chequeo de solape, el error de la DB se traduce a 409.
			if err := tx.Update(move.to); err != nil {
				if errors.Is(err, domain.ErrAlreadyExists) {
					return conflictFrom(domain.NewMessage(domain.ErrAlreadyExists, domain.MsgCoverBusy),
						[]domain.Booking{current})
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ReassignResult{}, err
	}
	// Después del commit: los avisos no pueden deshacer un turno ya movido.
	movedOut := make([]domain.Booking, 0, len(plan))
	for _, move := range plan {
		s.announceReassign(salonID, move.to, move.professionalName)
		movedOut = append(movedOut, move.to)
	}
	return ReassignResult{Reassigned: len(movedOut), Bookings: movedOut}, nil
}

// plannedMove es un turno que se va a mover y quién lo toma.
type plannedMove struct {
	from             domain.Booking
	to               domain.Booking
	professionalName string
}

// selectCoverageBookings elige los turnos de la ausencia que se mueven: todos
// (sin lista) o exactamente los pedidos. Un id que no es de esa ausencia o que
// ya no está reservado se rechaza con su mensaje.
func (s CoverageService) selectCoverageBookings(off domain.TimeOff, all []domain.Booking, bookingIDs []string) ([]domain.Booking, error) {
	if len(bookingIDs) == 0 {
		return all, nil
	}
	want := make(map[string]bool, len(bookingIDs))
	for _, id := range bookingIDs {
		if clean := strings.TrimSpace(id); clean != "" {
			want[clean] = true
		}
	}
	out := make([]domain.Booking, 0, len(want))
	for _, b := range all {
		if want[b.ID] {
			out = append(out, b)
			delete(want, b.ID)
		}
	}
	for id := range want {
		b, err := s.Bookings.FindByID(off.SalonID, id)
		if err != nil {
			return nil, err // no existe en este salón (404)
		}
		if !b.BelongsToTimeOff(off) {
			return nil, conflictFrom(domain.NewMessage(domain.ErrValidation, domain.MsgCoverNotThisAbsence),
				[]domain.Booking{b})
		}
		return nil, conflictFrom(domain.NewMessage(domain.ErrAlreadyExists, domain.MsgCoverNotBooked),
			[]domain.Booking{b})
	}
	return out, nil
}

// absenceBookings son los turnos `booked` de la persona de la ausencia que se
// pisan con ella, ordenados por inicio.
func (s CoverageService) absenceBookings(off domain.TimeOff) ([]domain.Booking, error) {
	window, err := s.Bookings.ListRange(off.SalonID, off.StartsAt.Add(-domain.MaxBookingDuration), off.EndsAt)
	if err != nil {
		return nil, err
	}
	return bookedOverlaps(window, off.UserID, off.StartsAt, off.EndsAt), nil
}

// announceReassign publica el evento para el calendario, deja el aviso interno y
// manda el correo a la clienta: el turno se mantiene y quién la atiende ahora
// (S-F2d3-4).
func (s CoverageService) announceReassign(salonID string, b domain.Booking, professionalName string) {
	publish(s.Events, ports.Event{
		Type:    "booking.updated",
		SalonID: salonID,
		Data:    bookingEventData(b),
		TS:      time.Now().UTC(),
	})
	label := salonTimeLabel(s.Settings, salonID, b.StartsAt)
	body := b.ClientName + " · " + label
	if name := strings.TrimSpace(professionalName); name != "" {
		body += " · ahora la atiende " + name
	}
	notify(s.Notifier, salonID, "", "Turno reasignado", body, domain.KindBooking)
	s.sendCoverMail(salonID, b, professionalName, coverMailReassigned)
}

// sendCoverMail manda el correo del cambio de turno en segundo plano, igual que
// la confirmación de la reserva: la respuesta no espera al SMTP.
func (s CoverageService) sendCoverMail(salonID string, b domain.Booking, professionalName string, kind coverMailKind) {
	if s.Mailer == nil || s.Emails == nil {
		return
	}
	email, err := s.Emails.FindEmail(salonID, b.ID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			// Sin la dirección en el log: turno y salón alcanzan.
			log.Printf("cobertura: buscar el email del turno %s del salón %s: %v", b.ID, salonID, err)
		}
		return
	}
	settings, err := loadSalonSettings(s.Settings, salonID)
	if err != nil {
		log.Printf("cobertura: settings del salón %s: %v", salonID, err)
		settings = domain.SalonSettings{SalonID: salonID, Timezone: "UTC"}
	}
	msg := coverMail(kind, s.salonName(salonID), b.ClientName, professionalName, b.StartsAt, settings)
	msg.To = email
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("cobertura: panic enviando el aviso del turno %s: %v", b.ID, r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), coverSendTimeout)
		defer cancel()
		if err := s.Mailer.Send(ctx, msg); err != nil {
			log.Printf("cobertura: enviar el aviso del turno %s del salón %s: %v", b.ID, salonID, err)
		}
	}()
}

// member es la autorización: el actor tiene que pertenecer al salón de la ruta.
func (s CoverageService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if s.Memberships == nil || actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

func (s CoverageService) salonName(salonID string) string {
	if s.Salons == nil {
		return ""
	}
	salon, err := s.Salons.FindByID(salonID)
	if err != nil {
		return ""
	}
	return salon.Name
}

func (s CoverageService) serviceName(salonID, serviceID string) string {
	if serviceID == "" || s.Services == nil {
		return ""
	}
	svc, err := s.Services.FindByID(salonID, serviceID)
	if err != nil {
		return ""
	}
	return svc.Name
}

// conflictFrom arma la negativa con el detalle de los turnos afectados.
func conflictFrom(err error, bookings []domain.Booking) error {
	kind := domain.ErrValidation
	if errors.Is(err, domain.ErrAlreadyExists) {
		kind = domain.ErrAlreadyExists
	}
	return &CoverageConflict{Kind: kind, Text: err.Error(), Bookings: bookings}
}

// bookingEventData es el payload del evento booking.updated (el mismo que usa la
// agenda desde siempre: los calendarios abiertos lo entienden tal cual).
func bookingEventData(b domain.Booking) map[string]any {
	return map[string]any{
		"id":              b.ID,
		"professional_id": b.ProfessionalID,
		"client_name":     b.ClientName,
		"starts_at":       b.StartsAt.Format(time.RFC3339),
		"ends_at":         b.EndsAt.Format(time.RFC3339),
		"status":          string(b.Status),
	}
}

// cancelNoticeBody es el aviso interno de una cancelación: dejó el MOTIVO escrito
// (S-F2d3-2), que es lo que después se consulta.
func cancelNoticeBody(b domain.Booking, reason, label string) string {
	body := b.ClientName + " · " + label
	if reason != "" {
		body += " · Motivo: " + reason
	}
	return body
}

// sameSlot dice si el turno recién leído es el mismo que se validó (misma
// persona, mismo horario): si cambió, el plan quedó viejo.
func sameSlot(current, planned domain.Booking) bool {
	return current.ProfessionalID == planned.ProfessionalID &&
		current.StartsAt.Equal(planned.StartsAt) &&
		current.EndsAt.Equal(planned.EndsAt)
}

// candidateSet es el estado de la plantilla para un turno: una lectura de
// horarios, una de servicios por servicio distinto y una de ausencias por
// ventana, no una por persona.
type candidateSet struct {
	salonID  string
	staff    []domain.StaffMember
	single   domain.Interval
	weekly   domain.WeeklyHours
	segments map[string][]domain.HoursSegment
	loc      *time.Location
	services map[string]map[string]bool // serviceID -> ids que lo hacen
}

// loadCandidates arma el estado de la plantilla del salón.
func (s CoverageService) loadCandidates(salonID string) (*candidateSet, error) {
	staff, err := s.Staff.ListStaff(salonID)
	if err != nil {
		return nil, err
	}
	settings, err := loadSalonSettings(s.Settings, salonID)
	if err != nil {
		return nil, err
	}
	loc, err := settings.Location()
	if err != nil {
		return nil, err
	}
	single, err := singleWindow(settings)
	if err != nil {
		return nil, err
	}
	weekly, err := salonWeeklyHours(s.Hours, salonID)
	if err != nil {
		return nil, err
	}
	segments, err := memberHours(s.Hours, salonID)
	if err != nil {
		return nil, err
	}
	return &candidateSet{
		salonID: salonID, staff: staff, single: single, weekly: weekly,
		segments: segments, loc: loc, services: map[string]map[string]bool{},
	}, nil
}

// name es el nombre de una persona de la plantilla (el id si ya no está).
func (c *candidateSet) name(userID string) string {
	for _, m := range c.staff {
		if m.ID == userID {
			return m.Name
		}
	}
	return userID
}

// candidatesFor arma los hechos de cada persona de la plantilla para un turno.
// checkBusy agrega la consulta de solape: la usa el LISTADO para marcar a las
// ocupadas. En la escritura no se pre-chequea el solape a propósito: el árbitro
// es la EXCLUDE de la DB (S-F2d3-1).
func (s CoverageService) candidatesFor(set *candidateSet, b domain.Booking, checkBusy bool) ([]domain.CoverageCandidate, error) {
	weekday := domain.IsoWeekday(b.StartsAt, set.loc)
	startMin := localMinutes(b.StartsAt, set.loc)
	endMin := startMin + int(b.EndsAt.Sub(b.StartsAt).Minutes())
	does, err := s.doesService(set, b)
	if err != nil {
		return nil, err
	}
	absences, err := timeOffRange(s.TimeOff, set.salonID, b.StartsAt, b.EndsAt)
	if err != nil {
		return nil, err
	}
	away := awaySet(absences, b.StartsAt, b.EndsAt)
	out := make([]domain.CoverageCandidate, 0, len(set.staff))
	for _, m := range set.staff {
		fits, err := domain.DayPlan{Single: set.single, Weekly: set.weekly, Segments: set.segments[m.ID]}.
			Fits(weekday, startMin, endMin)
		if err != nil {
			return nil, err
		}
		candidate := domain.CoverageCandidate{
			Professional: m, DoesService: does[m.ID], Scheduled: fits, Away: away[m.ID],
		}
		if checkBusy {
			busy, err := s.Bookings.FindOverlapping(set.salonID, m.ID, b.StartsAt, b.EndsAt)
			if err != nil {
				return nil, err
			}
			candidate.Busy = overlapsOther(busy, b.ID)
		}
		out = append(out, candidate)
	}
	return out, nil
}

// doesService resuelve (y memoriza) quién hace el servicio del turno. Un turno
// sin servicio no exige oficio: devuelve nil y la regla lo ignora.
func (s CoverageService) doesService(set *candidateSet, b domain.Booking) (map[string]bool, error) {
	if b.ServiceID == "" {
		return nil, nil
	}
	if cached, ok := set.services[b.ServiceID]; ok {
		return cached, nil
	}
	out := map[string]bool{}
	if s.Professionals != nil {
		list, err := s.Professionals.ListProfessionalsFor(set.salonID, b.ServiceID)
		if err != nil {
			return nil, err
		}
		for _, m := range list {
			out[m.ID] = true
		}
	}
	set.services[b.ServiceID] = out
	return out, nil
}

// displayCandidates es la lista que viaja a la pantalla: id, nombre y motivo.
func displayCandidates(candidates []domain.CoverageCandidate, b domain.Booking) []CoverageCandidate {
	out := make([]CoverageCandidate, 0, len(candidates))
	for _, c := range candidates {
		reason := ""
		if err := c.Covers(b); err != nil {
			reason = err.Error()
		}
		out = append(out, CoverageCandidate{
			ProfessionalID: c.Professional.ID, Name: c.Professional.Name, Reason: reason,
		})
	}
	return out
}

// findCandidate busca a una persona en la lista de hechos del turno.
func findCandidate(candidates []domain.CoverageCandidate, professionalID string) (domain.CoverageCandidate, bool) {
	want := strings.TrimSpace(professionalID)
	if want == "" {
		return domain.CoverageCandidate{}, false
	}
	for _, c := range candidates {
		if c.Professional.ID == want {
			return c, true
		}
	}
	return domain.CoverageCandidate{}, false
}

// overlapsOther dice si alguno de los turnos traídos es otro turno (el mismo no
// cuenta: un turno no se pisa consigo mismo).
func overlapsOther(bookings []domain.Booking, bookingID string) bool {
	for _, b := range bookings {
		if b.ID != bookingID {
			return true
		}
	}
	return false
}
