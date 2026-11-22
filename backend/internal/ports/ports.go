// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
package ports

import (
	"context"
	"time"

	"salon-saas/internal/domain"
)

// --- Repositorios (puertos de salida hacia persistencia) ---

type UserRepository interface {
	Save(u domain.User) error
	FindByEmail(email string) (domain.User, error)
	// FindByID resuelve el usuario del token (GET /me no tiene otro dato).
	FindByID(id string) (domain.User, error)
}

type SalonRepository interface {
	Save(s domain.Salon) error
	FindBySlug(slug string) (domain.Salon, error)
	// FindByID resuelve el salón de una membership (GET /me devuelve nombre y
	// slug de cada salón del usuario).
	FindByID(id string) (domain.Salon, error)
}

type MembershipRepository interface {
	Save(m domain.Membership) error
	Find(userID, salonID string) (domain.Membership, error)
}

// MembershipLister: los salones a los que pertenece un usuario. Es el puerto
// de las features que solo reciben el JWT y no un :salon_id en la ruta: el hub
// de websockets (rooms por salón), "marcar notificación leída" y GET /me.
//
// Va aparte de MembershipRepository (ISP): los repos de reservas o ventas no
// tienen por qué implementar listados que nadie les pide.
type MembershipLister interface {
	ListByUser(userID string) ([]domain.Membership, error)
}

// StaffLister: el personal que atiende en el salón, con nombre (memberships
// cruzado con users). Es la fuente del select de profesional del frontend.
type StaffLister interface {
	ListStaff(salonID string) ([]domain.StaffMember, error)
}

// MemberAdminRepository: pantalla de equipo (listar con nombre/email, quitar,
// contar por rol y la marca de quién atiende). Va aparte de
// MembershipRepository (ISP): los repos de reservas o ventas no cargan con la
// administración del equipo.
type MemberAdminRepository interface {
	ListMembers(salonID string) ([]domain.Member, error)
	DeleteMember(userID, salonID string) error
	CountRole(salonID string, role domain.Role) (int, error)
	// SetAttends cambia la marca de "atiende clientes" de un miembro. El UPDATE
	// es la autorización y el scope: si la persona no pertenece a ESE salón no
	// se afectan filas (ErrNotFound, nunca un upsert).
	SetAttends(userID, salonID string, attends bool) error
	// SetTrade guarda el oficio visible ("Barbero"). Mismo scope que SetAttends:
	// un miembro de otro salón no se toca (ErrNotFound).
	SetTrade(userID, salonID, trade string) error
}

// ProfessionalServiceRepository: qué servicios hace cada profesional (F2c).
// El oficio es un texto (no un permiso); esta tabla es la que decide quién
// puede recibir un turno de cada servicio.
type ProfessionalServiceRepository interface {
	// ListServiceIDs trae los servicios que hace un miembro de ESE salón,
	// ordenados por id (una lista vacía es válida: no hace ninguno).
	ListServiceIDs(salonID, userID string) ([]string, error)
	// SetServices REEMPLAZA la lista del miembro en una transacción: o queda la
	// lista nueva entera o queda la anterior, nunca media lista.
	SetServices(salonID, userID string, serviceIDs []string) error
	// ListProfessionalsFor trae quien ATIENDE (Membership.Bookable) y hace ese
	// servicio: los elegibles de la disponibilidad y de la reserva pública.
	ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error)
}

// SalonSettingsRepository: configuración operativa del salón (zona horaria,
// horario y paso de turnos). La lee la reserva pública y el cierre de caja.
type SalonSettingsRepository interface {
	Settings(salonID string) (domain.SalonSettings, error)
	SaveSettings(s domain.SalonSettings) error
}

// WeeklyHoursRepository: horario semanal del salón y de cada profesional (F2d1).
//
// La ausencia de filas es comportamiento, no dato faltante: un salón sin filas
// usa la ventana única de /settings todos los días y una profesional sin filas
// sigue el horario del salón. Por eso no hay backfill en la migración 017.
// Todas las queries van scopeadas por salon_id: el horario de otro tenant no
// existe acá.
type WeeklyHoursRepository interface {
	// SalonHours trae las ventanas por día del salón (vacío = sin horario
	// propio). El día sin fila, cuando hay filas, está cerrado.
	SalonHours(salonID string) ([]domain.WeekdayHours, error)
	// SaveSalonHours REEMPLAZA el horario en una transacción: o queda el nuevo
	// entero o queda el anterior. Lista vacía = volver al horario único.
	SaveSalonHours(salonID string, days []domain.WeekdayHours) error
	// MemberHours trae los tramos de un miembro de ESE salón (vacío = sigue al
	// salón).
	MemberHours(salonID, userID string) ([]domain.HoursSegment, error)
	// SaveMemberHours REEMPLAZA los tramos del miembro en una transacción.
	// Lista vacía = vuelve a seguir el horario del salón.
	SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error
	// AllMemberHours trae los tramos de todo el salón indexados por persona: la
	// disponibilidad pregunta una vez por día, no una query por profesional.
	AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error)
}

// TimeOffRepository: ausencias (vacaciones y días libres puntuales) de las
// profesionales de un salón (F2d2).
//
// Una ausencia es un período [starts_at, ends_at) en UTC de UNA persona
// reservable. Todas las queries van scopeadas por salon_id: la ausencia de otro
// tenant no se lista, no se encuentra y no se borra (ErrNotFound -> 404).
//
// No hay unicidad de período en la DB a propósito: "dos ausencias de la misma
// persona no se pisan" es una regla de negocio que el caso de uso verifica
// contra las filas existentes (el CHECKing de rangos en SQL necesitaría una
// EXCLUDE y una migración de extensión; el mensaje tiene que ser en español).
type TimeOffRepository interface {
	// Save inserta la ausencia. La FK (user_id, salon_id) impide dejar una
	// ausencia colgada de alguien que no es del salón.
	Save(t domain.TimeOff) error
	// ListRange trae las ausencias del salón que PISAN [from, to) ordenadas por
	// inicio: es la query de la pantalla de equipo y de la disponibilidad.
	ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error)
	// FindByID resuelve una ausencia scopeada por salón (ErrNotFound si es de
	// otro tenant).
	FindByID(salonID, id string) (domain.TimeOff, error)
	// Delete borra la ausencia del salón; otra id o de otro salón es ErrNotFound
	// (nunca un borrado silencioso).
	Delete(salonID, id string) error
}

// ResourceRepository: puestos y equipamiento con capacidad (F2e).
//
// Un TIPO agrupa unidades ("Puesto de peluquería" x 3) y un SERVICIO puede
// exigir un tipo; cada TURNO ocupa una UNIDAD concreta. La capacidad no es un
// contador: es una fila por unidad con su propia EXCLUDE en la DB (migración
// 020), así dos reservas simultáneas nunca se llevan la última.
//
// Todas las queries van scopeadas por salon_id: el equipo de otro tenant no se
// lista, no se encuentra y no se borra (ErrNotFound -> 404).
//
// Sin backfill: un servicio sin tipo de equipo (y un turno sin unidad) se
// comporta exactamente como antes.
type ResourceRepository interface {
	// CreateType crea el tipo y sus unidades en UNA transacción (el alta con
	// `count` no puede quedar a medias).
	CreateType(t domain.ResourceType, units []domain.ResourceUnit) error
	// SaveType actualiza el nombre (sin tocar identidad ni salón).
	SaveType(t domain.ResourceType) error
	// DeleteType borra el tipo y sus unidades en UNA transacción, scopeado por
	// salón; ErrNotFound si es de otro tenant. La FK de servicios (RESTRICT) y
	// una unidad con turnos lo frenan con ErrAlreadyExists: el caso de uso ya lo
	// verificó, el adaptador es la garantía dura.
	DeleteType(salonID, id string) error
	FindType(salonID, id string) (domain.ResourceType, error)
	ListTypes(salonID string) ([]domain.ResourceType, error)

	// SaveUnit inserta o actualiza una unidad (nombre y active).
	SaveUnit(u domain.ResourceUnit) error
	FindUnit(salonID, id string) (domain.ResourceUnit, error)
	// ListUnits trae las unidades de un tipo, incluidas las desactivadas.
	ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error)
	// ActiveUnits son las que la disponibilidad puede ofrecer.
	ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error)
	// UnitsWithBookings cuenta unidades del tipo con algún turno `booked`: es lo
	// que impide borrar el tipo.
	UnitsWithBookings(salonID, typeID string) (int, error)
	// UnitFutureBookings cuenta los turnos `booked` que empiezan después de now
	// y usan la unidad (informativo al desactivarla).
	UnitFutureBookings(salonID, unitID string, now time.Time) (int, error)
	// OccupiedUnits trae los intervalos tomados por turnos `booked` con unidad
	// en [from,to). excludeBookingID permite reprogramar un turno sin que su
	// propio intervalo cuente como ocupado.
	OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error)
}

// BookingTx es la vista transaccional de la agenda: cubrir una ausencia mueve
// TODOS sus turnos o ninguno (S-F2d3-3). Es el mismo repositorio de reservas que
// fuera de la transacción, para que el caso de uso no tenga dos formas de leer
// un turno.
type BookingTx interface {
	BookingRepository
}

// BookingTxRunner abre una transacción con un lock por salón (el mismo patrón que
// el cierre de caja): dos "cubrir la ausencia" simultáneos sobre el mismo salón
// no se pisan y la EXCLUDE de bookings sigue siendo la que decide el solape.
type BookingTxRunner interface {
	InSalonTx(salonID string, fn func(tx BookingTx) error) error
}

// BookingEmailFinder resuelve la dirección con la que se avisa un cambio de
// turno (cubrir una ausencia o cancelar, F2d3). Va aparte de
// BookingEmailRepository (ISP): quien solo avisa no carga con la cola de
// recordatorios.
type BookingEmailFinder interface {
	// FindEmail devuelve la dirección guardada del turno del salón. Sin fila es
	// ErrNotFound (el turno se agendó sin email: no hay a quién escribir).
	FindEmail(salonID, bookingID string) (string, error)
}

// --- Seguridad (puertos de salida hacia crypto/tokens) ---

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) bool
	// DummyCompare gasta el tiempo de un Compare real y siempre devuelve false.
	// Lo usa el login cuando el usuario no existe: sin eso, el tiempo de
	// respuesta delata qué emails están registrados.
	DummyCompare(plain string) bool
}

type TokenIssuer interface {
	Issue(userID string) (string, error)
	Parse(token string) (string, error) // devuelve userID
}

// --- Reservas ---

type BookingRepository interface {
	Save(b domain.Booking) error
	// FindByClientOpID busca una reserva ya registrada por su operation id.
	FindByClientOpID(salonID, clientOpID string) (domain.Booking, error)
	// FindByID resuelve un turno scopeado por salón: el id de otro tenant no
	// existe (404), que es la autorización del PATCH.
	FindByID(salonID, id string) (domain.Booking, error)
	// Update persiste los cambios de un turno existente (estado y horario).
	// La EXCLUDE de la DB sigue siendo el árbitro final del solape.
	Update(b domain.Booking) error
	// FindOverlapping trae activas del profesional que pisan [start,end).
	// El caso de uso la usa como filtro amable; la EXCLUDE de la DB decide.
	FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error)
	// ListRange trae todo lo del salón en [from,to) para el calendario.
	ListRange(salonID string, from, to time.Time) ([]domain.Booking, error)
}

// --- Catálogo de servicios ---

type ServiceRepository interface {
	// Save inserta o actualiza por id. Rechaza nombre activo duplicado por
	// salón (índice único sobre lower(name) WHERE active).
	Save(s domain.Service) error
	FindByID(salonID, id string) (domain.Service, error)
	List(salonID string, includeInactive bool) ([]domain.Service, error)
}

// --- POS: ventas y caja ---

type SaleRepository interface {
	Save(s domain.Sale) error
	// FindByClientOpID busca una venta ya registrada por su operation id.
	// Es la base de la idempotencia de la cola offline del cliente.
	FindByClientOpID(salonID, clientOpID string) (domain.Sale, error)
	// ListRange trae las ventas del salón en [from,to) con sus ítems.
	ListRange(salonID string, from, to time.Time) ([]domain.Sale, error)
	// SumCents suma totales en [from,to). method vacío suma todos los métodos;
	// MethodCash es lo que el cierre de caja espera encontrar en el cajón.
	SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error)
	// TopServices ranking de servicios vendidos en [from,to), de mayor a menor.
	TopServices(salonID string, from, to time.Time, limit int) ([]domain.TopService, error)
}

type CashCloseRepository interface {
	Save(c domain.CashClose) error
	// Last es el cierre más reciente del salón (ErrNotFound si nunca cerró):
	// marca la apertura del turno actual.
	Last(salonID string) (domain.CashClose, error)
	List(salonID string) ([]domain.CashClose, error)
}

// CashCloseTx es la vista de la caja DENTRO de una transacción con lock por
// salón: el cierre tiene que leer el último cierre, sumar el efectivo de la
// ventana y guardar el cierre nuevo viendo siempre lo mismo.
type CashCloseTx interface {
	CashCloseRepository
	// SumCashCents es la suma de efectivo de las ventas en [from,to).
	SumCashCents(salonID string, from, to time.Time) (int, error)
}

// SalonTxRunner ejecuta una función dentro de una transacción con un lock por
// salón (pg_advisory_xact_lock). Sin esto, dos cierres concurrentes leen el
// mismo "último cierre", suman la MISMA ventana de efectivo y el salón cierra
// dos veces lo mismo: la caja queda contada al doble.
type SalonTxRunner interface {
	InSalonTx(salonID string, fn func(tx CashCloseTx) error) error
}

// --- Agregados de lectura para el dashboard ---
// Puertos chicos y específicos (ISP): el dashboard no obliga a los repos de
// reservas o de membresías a cargar con métodos que nadie más usa.

// AgendaStats: cuántos turnos y cuántos minutos reservados hay en una ventana.
type AgendaStats interface {
	StatsRange(salonID string, from, to time.Time) (appointments int, bookedMinutes int, err error)
}

// StaffCounter: capacidad instalada del salón (profesionales que atienden).
type StaffCounter interface {
	CountProfessionals(salonID string) (int, error)
}

// --- Realtime ---

// Event es lo que se publica a los clientes conectados de UN salón.
// Type viaja tal cual al frontend ("booking.created", "notify", ...) y Data es
// el payload que ya viene listo para serializar.
type Event struct {
	Type    string
	SalonID string
	Data    any
	TS      time.Time
}

// EventPublisher es el puerto de salida del realtime. Los casos de uso avisan
// "pasó algo en este salón" sin saber que atrás hay websockets (o mañana Redis
// pub/sub, o nada): el hub es un adaptador más.
type EventPublisher interface {
	Publish(ev Event) error
}

// --- Notificaciones ---

// NotificationRepository: avisos persistidos del salón. Las queries del salón
// (List/MarkRead/UnreadCount) reciben userID porque un aviso puede ser para una
// persona o para todo el salón (user_id NULL).
type NotificationRepository interface {
	Save(n domain.Notification) error
	List(salonID, userID string, limit int) ([]domain.Notification, error)
	MarkRead(salonID, userID, id string) error
	UnreadCount(salonID, userID string) (int, error)
}

// --- Email a clientes ---

// Message es un correo ya armado (texto plano + HTML alternativos). El caso de
// uso decide el contenido y el adaptador solo habla SMTP.
//
// To y Subject son headers: el adaptador rechaza CR/LF en ellos (header
// injection). Text y HTML son cuerpos completos, no fragmentos.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// MailSender es el puerto de salida hacia un servidor de correo. El adaptador
// de cmd/api puede ser un no-op cuando SMTP_HOST está vacío (entorno sin
// email): el caso de uso no tiene que saberlo.
type MailSender interface {
	Send(ctx context.Context, msg Message) error
}

// DueReminder es un recordatorio ya reclamado: trae todo lo que la plantilla
// necesita (salón, reserva y email), así el envío no vuelve a consultar la DB.
type DueReminder struct {
	BookingID  string
	SalonID    string
	SalonName  string
	ClientName string
	Email      string
	StartsAt   time.Time
}

// BookingEmailRepository: el email de un turno reservado por la web y la cola
// de recordatorios. Vive en su propia tabla para no tocar las SELECT de
// reservas (y para que un turno sin email no ocupe una fila de bookings).
type BookingEmailRepository interface {
	// Save guarda el email del turno. Un turno ya guardado no se pisa.
	Save(salonID, bookingID, email string) error
	// ClaimDueReminders reclama ATÓMICAMENTE hasta limit recordatorios que
	// vencen en [now, now+24h) y todavía no se enviaron ni agotaron intentos.
	// Lo reclamado no lo vuelve a tomar otro proceso (5 minutos de gracia).
	ClaimDueReminders(now time.Time, limit int) ([]DueReminder, error)
	// MarkReminderSent cierra el recordatorio del turno: no se reintenta más.
	MarkReminderSent(bookingID string) error
}

// --- Clientes y adjuntos ---

// ClientRepository: fichas de clientes del salón y sus fotos.
// Toda query va scopeada por salon_id: el id de otro tenant no existe acá.
type ClientRepository interface {
	Save(c domain.Client) error
	FindByID(salonID, id string) (domain.Client, error)
	List(salonID string) ([]domain.Client, error)
	// FindByPhone busca la ficha por teléfono NORMALIZADO (domain.NormalizePhone)
	// dentro del salón. La usa la reserva pública para no crear una ficha nueva
	// cada vez que la misma persona reserva.
	FindByPhone(salonID, phone string) (domain.Client, error)
	// FindByName busca la ficha por nombre exacto dentro del salón: es el
	// respaldo de la reserva pública cuando la persona no dejó teléfono.
	FindByName(salonID, name string) (domain.Client, error)
	SavePhoto(p domain.ClientPhoto) error
	ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error)
	DeletePhoto(salonID, clientID, id string) error
	// FindPhoto resuelve una foto con el scope completo (salón + cliente).
	FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error)
	// PhotoByID resuelve por id solo: lo usa el streaming /photos/:pid, donde
	// el id aleatorio es la capability que el navegador usa en <img src>.
	PhotoByID(id string) (domain.ClientPhoto, error)
}

// PhotoStorage guarda los binarios fuera de la DB. El adaptador puede ser el
// filesystem local o S3/MinIO (se elige por S3_ENDPOINT en el composition
// root); el puerto es el mismo.
type PhotoStorage interface {
	// Put guarda data bajo key y devuelve la ruta estable a persistir en la DB.
	Put(key string, data []byte, contentType string) (storedPath string, err error)
	Get(path string) ([]byte, error)
	Delete(path string) error
}

// PhotoURLSigner firma la URL de una foto con vencimiento (HMAC). El navegador
// usa la url tal cual en <img src> y no puede mandar Authorization; la firma es
// la capability temporal (24 h).
type PhotoURLSigner interface {
	// Sign devuelve la firma (texto seguro para URL) de photoID con ese vencimiento.
	Sign(photoID string, expiresUnix int64) string
	// Verify dice si la firma es válida y no venció.
	Verify(photoID string, expiresUnix int64, sig string) bool
}

// ImageProcessor normaliza la subida: devuelve la variante que se persiste
// (WebP, lado mayor acotado) y sus dimensiones reales. Es un puerto porque el
// día que haya que usar otra herramienta, cambia el adaptador y nada más.
type ImageProcessor interface {
	ToWebP(data []byte) (out []byte, width, height int, err error)
}
