// WaitlistService: la lista de espera del salón (F7 del contrato de la fase 6).
//
// Reglas que viven acá:
//   - D4 (alta pública): el día tiene que estar entre hoy y hoy+30 en la zona
//     del salón y NO puede tener horarios libres (la lista no sirve para
//     reservar ni para sondear). Un repetido idéntico ACTIVO es idempotente y un
//     pedido inválido no escribe nada. Topes: 3 avisos activos por email y 500
//     por salón.
//   - D5 (baja por enlace): un token de 32 bytes en base64url, guardado solo
//     como SHA-256 y emitido en el momento del aviso; repetir la baja es 200 y
//     un token que no sirve es el MISMO 404.
//   - D6 (barrido): por grupo con hueco libre se reclaman `libres - retenidos`
//     entradas FIFO, dentro de una transacción con el advisory lock de la lista
//     del salón; el email sale SIN lock y recién después se marca notified_at.
//     Un envío fallido se reintenta pasada la gracia de 5 minutos, hasta 3
//     intentos.
//   - D7: sin MailSender configurado la tarea NO reclama nada.
//   - D8 (staff): owner/admin/recepción listan y dan de baja; el resto 403 y
//     toda consulta va scopeada por salón.
//
// La detección es por tarea periódica y no por ganchos: así se cubren TODOS los
// caminos que liberan un hueco sin tocar bookings, coverage, booking_links ni
// availability (D1). La disponibilidad se consulta con AvailabilityService.Day,
// tal cual está.
package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

const (
	// waitlistSendTimeout es el techo de UN envío del aviso: un SMTP colgado no
	// puede frenar el resto de los avisos ni el siguiente tick.
	waitlistSendTimeout = 15 * time.Second
	// waitlistGroupsPerPass acota cuántos grupos recorre una pasada (el barrido
	// ya viene acotado por el índice parcial y por el día).
	waitlistGroupsPerPass = 200
)

// WaitlistJoinInput es el cuerpo de POST /public/salons/:slug/waitlist ya
// parseado.
type WaitlistJoinInput struct {
	ServiceID      string
	ProfessionalID string // vacío = cualquiera
	Date           string // "YYYY-MM-DD" del día local del salón
	Name           string
	Email          string
	Phone          string
}

// WaitlistJoinResult es la respuesta del alta: 201 la primera vez y 200 cuando
// el pedido idéntico ya estaba en la lista (misma forma, D4). Nunca lleva el
// token ni el id.
type WaitlistJoinResult struct {
	Status    string
	Date      string
	ServiceID string
	Created   bool
}

// WaitlistService es el caso de uso de la lista. Las dependencias son puertos:
// el reloj y el mailer son inyectables para los tests.
type WaitlistService struct {
	Waitlist ports.WaitlistRepository
	// Tx es la transacción del reclamo (advisory lock + FOR UPDATE SKIP LOCKED).
	Tx            ports.WaitlistTxRunner
	Salons        ports.SalonRepository
	Services      ports.ServiceRepository
	Professionals ports.ProfessionalServiceRepository
	Settings      ports.SalonSettingsRepository
	// Availability es la MISMA disponibilidad de la reserva pública (D1): la
	// lista no la modifica ni la reimplementa.
	Availability AvailabilityService
	// Memberships resuelve los permisos de la pantalla del personal (D8).
	Memberships ports.MembershipRepository
	// Mailer es el canal del aviso (D7): nil = email deshabilitado y la tarea
	// no reclama nada.
	Mailer ports.MailSender
	// AppURL es la base de los enlaces del email (APP_URL).
	AppURL string
	// Now es el reloj del caso de uso; nil = time.Now.
	Now func() time.Time
}

// Join anota a una persona en la lista de espera de un día sin horarios.
//
// Orden de las reglas (y por qué): (1) el salón existe; (2) el servicio es un
// servicio ACTIVO del salón; (3) la profesional (si viene) es del salón y hace
// ese servicio; (4) el día y el contacto se validan; (5) un pedido idéntico ya
// anotado devuelve 200 sin escribir nada (idempotencia TOTAL: no depende de que
// el día siga sin horarios); (6) un día con horarios libres es 409; (7) los
// topes; (8) se crea con el token hasheado.
func (s WaitlistService) Join(slug string, in WaitlistJoinInput) (WaitlistJoinResult, error) {
	none := WaitlistJoinResult{}
	salon, err := s.Salons.FindBySlug(strings.TrimSpace(slug))
	if err != nil {
		return none, err
	}
	settings, err := kernel.LoadSalonSettings(s.Settings, salon.ID)
	if err != nil {
		return none, err
	}
	loc, err := settings.Location()
	if err != nil {
		return none, err
	}
	service, err := s.service(salon.ID, in.ServiceID)
	if err != nil {
		return none, err
	}
	professionalID := strings.TrimSpace(in.ProfessionalID)
	if professionalID != "" {
		if err := s.ensureDoes(salon.ID, service.ID, professionalID); err != nil {
			return none, err
		}
	}
	day, err := domain.WaitlistDay(in.Date, s.now(), loc)
	if err != nil {
		return none, err
	}
	contact, err := domain.NewWaitlistContact(in.Name, in.Email, in.Phone)
	if err != nil {
		return none, err
	}

	// Idempotencia (D4): el mismo pedido, ya anotado y vigente, devuelve la misma
	// forma sin crear otra entrada ni gastar cupo.
	// Solo corta acá la entrada que ESPERA: una ya avisada (notified_at) no es un
	// repetido sino una persona que vuelve a anotarse (D6) y sigue al alta.
	existing, err := s.Waitlist.FindActive(salon.ID, service.ID, professionalID, day, contact.Email)
	switch {
	case err == nil && existing.NotifiedAt == nil:
		return WaitlistJoinResult{
			Status: existing.Status(), Date: existing.Day, ServiceID: existing.ServiceID, Created: false,
		}, nil
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return none, err
	}

	// Con horarios libres ese día, la lista no aplica: sirve para sondear o
	// saturar y la persona puede reservar ahora mismo.
	avail, err := s.Availability.Day(salon.Slug, day, service.ID, professionalID)
	if err != nil {
		return none, err
	}
	if len(avail.Slots) > 0 {
		return none, waitlistSlotsAvailable()
	}

	// El aviso todavía no tiene token: la capability de la baja se emite cuando
	// hay un email que mandar (D5). La entrada nace sin enlace vivo.
	entry, err := domain.NewWaitlistEntry(salon.ID, service.ID, professionalID, day, contact, s.now())
	if err != nil {
		return none, err
	}
	if s.Tx == nil {
		return none, errors.New("lista de espera: falta el ejecutor de transacciones")
	}
	// Los topes cuentan solo los avisos de HOY en adelante en la zona del salón.
	today := s.now().In(loc).Format("2006-01-02")
	result := WaitlistJoinResult{
		Status: domain.WaitlistStatusWaiting, Date: entry.Day, ServiceID: entry.ServiceID, Created: true,
	}
	// La escritura del alta es UNA transacción bajo el lock de la lista del salón
	// (el mismo del barrido): el conteo de los topes y el insert no se intercalan
	// con otra alta (seis altas simultáneas ya no pasaban el tope de 3). La
	// consulta de disponibilidad de arriba quedó FUERA del lock.
	err = s.Tx.InWaitlistTx(func(tx ports.WaitlistTx) error {
		if err := tx.LockWrites(salon.ID); err != nil {
			return err
		}
		current, err := tx.FindActive(salon.ID, service.ID, professionalID, day, contact.Email)
		switch {
		case err == nil && current.NotifiedAt == nil:
			// Otra alta idéntica ganó la carrera: también es idempotente (200).
			result = WaitlistJoinResult{
				Status: current.Status(), Date: current.Day, ServiceID: current.ServiceID, Created: false,
			}
			return nil
		case err == nil:
			// Ya fue avisada y vuelve a anotarse: se cierra la vieja (baja) para
			// que el índice único parcial deje entrar a la nueva (D6).
			if err := tx.MarkLeft(current.ID, s.now()); err != nil {
				return err
			}
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
		if err := checkWaitlistCaps(tx, salon.ID, contact.Email, today); err != nil {
			return err
		}
		return tx.Create(entry)
	})
	if err != nil {
		return none, err
	}
	return result, nil
}

// Leave da de baja la entrada del token (D5). Es idempotente: repetir el mismo
// token responde 200 igual. Un token inexistente, mal formado o con espacios
// alrededor es el MISMO 404 genérico (no se recorta ni se distingue el motivo).
func (s WaitlistService) Leave(token string) error {
	if s.Waitlist == nil || !domain.ValidWaitlistToken(token) {
		return waitlistNotFound()
	}
	hash := domain.HashWaitlistToken(token)
	entry, err := s.Waitlist.FindByTokenHash(hash)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return waitlistNotFound()
		}
		return err
	}
	if !domain.WaitlistTokenMatches(entry.TokenHash, token) {
		return waitlistNotFound()
	}
	if entry.LeftAt != nil {
		// Ya está dada de baja: 200 igual, sin escribir.
		return nil
	}
	return s.Waitlist.Leave(hash, s.now())
}

// List es la pantalla del personal (D8): las entradas vigentes del salón con día
// local >= hoy, ordenadas por día y alta. Solo owner, administración y
// recepción.
func (s WaitlistService) List(actor domain.Membership, salonID string) ([]domain.WaitlistStaffEntry, error) {
	if _, err := s.staffActor(actor, salonID); err != nil {
		return nil, err
	}
	loc, err := kernel.SalonLocation(s.Settings, salonID)
	if err != nil {
		return nil, err
	}
	list, err := s.Waitlist.ListUpcoming(salonID, s.now().In(loc).Format(dateLayout))
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.WaitlistStaffEntry{} // el frontend recibe [] y no null
	}
	return list, nil
}

// Remove da de baja una entrada desde el panel (D8). Una entrada de otro salón o
// ya dada de baja es el 404 del contrato; sin permiso, 403.
func (s WaitlistService) Remove(actor domain.Membership, salonID, entryID string) error {
	if _, err := s.staffActor(actor, salonID); err != nil {
		return err
	}
	if err := s.Waitlist.RemoveByID(salonID, entryID, s.now()); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return waitlistNotFound()
		}
		return err
	}
	return nil
}

// RunOnce es UNA pasada de la tarea periódica (D1/D6): recorre los grupos con
// avisos en espera, consulta la disponibilidad del día y reclama avisos. Envía
// FUERA del lock y recién después marca `notified_at`. Separa los errores de la
// DB (que cortan la pasada y se loguean) de los del envío (que se reintentan).
func (s WaitlistService) RunOnce(ctx context.Context, now time.Time) (notified, failed int) {
	if s.Waitlist == nil || s.Tx == nil || s.Mailer == nil || ctx.Err() != nil {
		// Sin canal de email no se reclama NADA: lo reclamado y no enviado
		// quedaría frenado por la gracia (igual que los recordatorios).
		return 0, 0
	}
	groups, err := s.Waitlist.ListGroups(now.UTC())
	if err != nil {
		log.Printf("lista de espera: listar grupos: %v", err)
		return 0, 0
	}
	if len(groups) > waitlistGroupsPerPass {
		groups = groups[:waitlistGroupsPerPass]
	}
	for _, g := range groups {
		if ctx.Err() != nil {
			return notified, failed
		}
		// Cuántos huecos ofrece HOY ese día para ese servicio y esa profesional
		// (tope de 5, D6). Sin huecos no hay nada que avisar.
		avail, err := s.Availability.Day(g.SalonSlug, g.Day, g.ServiceID, g.ProfessionalID)
		if err != nil {
			// La disponibilidad no se pudo calcular: no se reclama nada de este
			// grupo y se sigue con el resto.
			log.Printf("lista de espera: disponibilidad del salón %s (%s): %v", g.SalonID, g.Day, err)
			continue
		}
		libres := len(avail.Slots)
		if libres == 0 {
			continue
		}
		notices, err := s.claimGroup(g, libres, now.UTC())
		if err != nil {
			log.Printf("lista de espera: reclamar avisos del salón %s (%s): %v", g.SalonID, g.Day, err)
			continue
		}
		// La zona del salón se lee UNA vez por grupo (el email la usa para
		// mostrar el día local con su nombre).
		loc := s.salonLocation(g.SalonID)
		for _, n := range notices {
			if ctx.Err() != nil {
				// Apagado en medio del lote: lo reclamado y no enviado vuelve
				// solo pasada la gracia.
				return notified, failed
			}
			msg := waitlistMail(waitlistMailData{
				SalonName:   g.SalonName,
				ServiceName: g.ServiceName,
				Day:         n.entry.Day,
				Location:    loc,
				BookURL:     publicSalonURL(s.AppURL, g.SalonSlug),
				LeaveURL:    waitlistLeaveURL(s.AppURL, n.token),
			}, n.entry.Name)
			msg.To = n.entry.Email
			sendCtx, cancel := context.WithTimeout(ctx, waitlistSendTimeout)
			err := s.Mailer.Send(sendCtx, msg)
			cancel()
			if err != nil {
				failed++
				// Sin dirección ni cuerpo en el log: el id de la entrada y el
				// salón alcanzan para diagnosticar.
				log.Printf("lista de espera: enviar el aviso %s del salón %s: %v", n.entry.ID, g.SalonID, err)
				continue
			}
			if err := s.Waitlist.MarkNotified(n.entry.ID, s.now()); err != nil {
				// El email ya salió: se cuenta como enviado. Si el marcado falló,
				// pasada la retención podría repetirse: se prefiere un aviso
				// repetido a uno perdido.
				log.Printf("lista de espera: marcar avisado %s: %v", n.entry.ID, err)
			}
			notified++
		}
	}
	return notified, failed
}

// waitlistNotice es un aviso ya reclamado con su token EN CLARO (vive solo en
// esta llamada y en el email: la DB guarda el hash).
type waitlistNotice struct {
	entry domain.WaitlistEntry
	token string
}

// claimGroup es el corazón de D6: en UNA transacción con el advisory lock de la
// lista del salón, recuenta los retenidos y reclama `libres - retenidos`
// entradas FIFO. Los tokens se generan acá adentro y se guardan hasheados: si la
// transacción no commitea, ni el reclamo ni el token existen.
func (s WaitlistService) claimGroup(g domain.WaitlistGroup, libres int, now time.Time) ([]waitlistNotice, error) {
	var notices []waitlistNotice
	err := s.Tx.InWaitlistTx(func(tx ports.WaitlistTx) error {
		if err := tx.LockWrites(g.SalonID); err != nil {
			return err
		}
		// El recuento va DENTRO del lock: es lo que hace que dos instancias del
		// job no avisen de más al mismo grupo.
		retained, err := tx.CountRetained(g, now)
		if err != nil {
			return err
		}
		want := domain.WaitlistNoticesToClaim(libres, retained)
		if want <= 0 {
			return nil
		}
		claimed, err := tx.Claim(g, now, want)
		if err != nil {
			return err
		}
		for _, entry := range claimed {
			token, err := domain.NewWaitlistToken()
			if err != nil {
				return err
			}
			hash := domain.HashWaitlistToken(token)
			if err := tx.SetToken(entry.ID, hash); err != nil {
				return err
			}
			entry.TokenHash = hash
			notices = append(notices, waitlistNotice{entry: entry, token: token})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return notices, nil
}

// service resuelve un servicio ACTIVO del salón. Un 400 con el texto del
// contrato: la lista solo se anota para algo que el salón ofrece hoy.
func (s WaitlistService) service(salonID, serviceID string) (domain.Service, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgWaitlistServiceInvalid)
	}
	service, err := s.Services.FindByID(salonID, serviceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgWaitlistServiceInvalid)
		}
		return domain.Service{}, err
	}
	if !service.Active {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgWaitlistServiceInvalid)
	}
	return service, nil
}

// ensureDoes exige que la profesional pedida sea del salón y haga ese servicio
// (F2c), la misma regla de la reserva pública.
func (s WaitlistService) ensureDoes(salonID, serviceID, userID string) error {
	eligible, err := s.Professionals.ListProfessionalsFor(salonID, serviceID)
	if err != nil {
		return err
	}
	for _, m := range eligible {
		if m.ID == userID {
			return nil
		}
	}
	return domain.NewMessage(domain.ErrValidation, domain.MsgWaitlistProfessionalInvalid)
}

// waitlistCounter es lo que checkWaitlistCaps necesita contar: lo cumple la
// transacción del alta (los conteos corren bajo el lock del salón).
type waitlistCounter interface {
	CountActiveByEmail(salonID, email, fromDay string) (int, error)
	CountActiveBySalon(salonID, fromDay string) (int, error)
}

// checkWaitlistCaps aplica los topes de D4: 3 avisos activos por email y 500 por salón.
// fromDay es hoy en la zona del salón: los avisos de días pasados no cuentan
// (revisión nativa R4-001).
func checkWaitlistCaps(c waitlistCounter, salonID, email, fromDay string) error {
	mine, err := c.CountActiveByEmail(salonID, email, fromDay)
	if err != nil {
		return err
	}
	if mine >= domain.MaxWaitlistActivePerEmail {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgWaitlistEmailLimit)
	}
	total, err := c.CountActiveBySalon(salonID, fromDay)
	if err != nil {
		return err
	}
	if total >= domain.MaxWaitlistActivePerSalon {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgWaitlistSalonFull)
	}
	return nil
}

// staffActor exige una membresía ACTIVA del salón con permiso de mostrador:
// owner, administración o recepción (D8). Profesional, cliente y una baja son
// 403; pedir otro salón también.
func (s WaitlistService) staffActor(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	if s.Memberships == nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	if !m.CanManage() && m.Role != domain.RoleReception {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// salonLocation es la zona del salón (nil = UTC y un salón sin configuración
// legible no rompe la pantalla ni el barrido).
func (s WaitlistService) salonLocation(salonID string) *time.Location {
	loc, err := kernel.SalonLocation(s.Settings, salonID)
	if err != nil {
		return time.UTC
	}
	return loc
}

// now es el reloj del caso de uso.
func (s WaitlistService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// --- errores del contrato (mismo texto y mismo status en todas las rutas) ---

// waitlistSlotsAvailable es el 409 de un día que todavía tiene horarios (D4).
func waitlistSlotsAvailable() error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgWaitlistSlotsAvailable)
}

// waitlistNotFound es el 404 ÚNICO de la lista (D5/D8): token que no sirve,
// entrada de otro salón o entrada ya dada de baja.
func waitlistNotFound() error {
	return domain.NewMessage(domain.ErrNotFound, domain.MsgWaitlistNotFound)
}
