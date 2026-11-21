// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
package ports

import (
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

// MemberAdminRepository: pantalla de equipo (listar con nombre/email, quitar y
// contar por rol). Va aparte de MembershipRepository (ISP): los repos de
// reservas o ventas no cargan con la administración del equipo.
type MemberAdminRepository interface {
	ListMembers(salonID string) ([]domain.Member, error)
	DeleteMember(userID, salonID string) error
	CountRole(salonID string, role domain.Role) (int, error)
}

// SalonSettingsRepository: configuración operativa del salón (zona horaria,
// horario y paso de turnos). La lee la reserva pública y el cierre de caja.
type SalonSettingsRepository interface {
	Settings(salonID string) (domain.SalonSettings, error)
	SaveSettings(s domain.SalonSettings) error
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
