// Package ports declara lo que el dominio NECESITA, sin decir cómo se hace.
// Los adaptadores (postgres, bcrypt, jwt, http) implementan estas interfaces.
// Esto es DIP: app depende de abstracciones, no de Gin ni SQL.
package ports

import (
	"context"
	"net/http"
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

// MemberLookup resuelve una membresía INCLUYENDO a quien fue dado de baja. Es
// la lectura que necesitan el alta por email y la invitación (F2g2): con
// MembershipRepository.Find una baja parece alguien nuevo y el upsert del alta
// la reactivaba con el rol que eligiera el actor, sin mirar su rol guardado.
type MemberLookup interface {
	FindMember(userID, salonID string) (domain.Membership, error)
}

// MemberDirectory es el directorio del equipo: la lectura activa compartida
// (autorización de cada pedido) más la que ve la baja (autorización por rol
// guardado). Va junto porque una operación de alta/invitación necesita las dos.
type MemberDirectory interface {
	MembershipRepository
	MemberLookup
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
	// FindMember resuelve la membresía de la pantalla de equipo INCLUYENDO a
	// quien fue dado de baja: es la única lectura que necesita ver el estado
	// anterior (reactivar y la autorización por rol). `MembershipRepository.Find`
	// sigue devolviendo solo activas (S-F2g2-3).
	FindMember(userID, salonID string) (domain.Membership, error)
	// DeactivateMember da de baja una membresía ACTIVA (UPDATE condicional por
	// ended_at). Si ya estaba dada de baja (o es de otro salón) no afecta filas:
	// ErrNotFound. Dos bajas simultáneas dejan una sola fila consistente.
	DeactivateMember(userID, salonID string, endedAt time.Time) error
	// ReactivateMember vuelve a activar una membresía dada de baja (UPDATE
	// condicional ended_at IS NOT NULL). Sin filas: ErrNotFound (ya activa o de
	// otro salón). No cambia rol, attends ni trade.
	ReactivateMember(userID, salonID string) error
	// CountFutureBookings cuenta los turnos FUTUROS no cancelados de la persona:
	// con al menos uno no se la puede dar de baja (409 del contrato).
	CountFutureBookings(salonID, userID string, now time.Time) (int, error)
	// SetAttends cambia la marca de "atiende clientes" de un miembro. El UPDATE
	// es la autorización y el scope: si la persona no pertenece a ESE salón no
	// se afectan filas (ErrNotFound, nunca un upsert).
	SetAttends(userID, salonID string, attends bool) error
	// SetTrade guarda el oficio visible ("Barbero"). Mismo scope que SetAttends:
	// un miembro de otro salón no se toca (ErrNotFound).
	SetTrade(userID, salonID, trade string) error
}

// MemberTx es la vista TRANSACCIONAL de la baja: desactivar la membresía y
// revocar las invitaciones pendientes que esa persona emitió commitean juntos o
// no pasan (S-F2g2-4). El conteo de turnos futuros corre DENTRO de la misma
// transacción, después del UPDATE condicional, así un turno no puede colarse
// entre el chequeo y la baja.
//
// CountRole corre también dentro de la transacción (después del UPDATE) porque
// el chequeo del último owner fuera de ella no alcanza: dos owners que se dan de
// baja MUTUAMENTE bloquean filas distintas, así que el row lock no los
// serializa y los dos podían ver «queda otro owner» a la vez. El adaptador toma
// además un lock por salón al desactivar, así el conteo de adentro ve el
// resultado de la baja que ya commiteó.
type MemberTx interface {
	DeactivateMember(userID, salonID string, endedAt time.Time) error
	CountFutureBookings(salonID, userID string, now time.Time) (int, error)
	// CountRole cuenta los miembros ACTIVOS con ese rol en el salón: la misma
	// semántica que MemberAdminRepository.CountRole (una baja no cuenta).
	CountRole(salonID string, role domain.Role) (int, error)
	RevokeInvitationsBy(salonID, createdBy string, now time.Time) error
}

// MemberTxRunner abre la transacción de la baja. El adaptador serializa las
// bajas del mismo salón con un lock corto por salón: el row lock del UPDATE
// condicional solo alcanza cuando las dos bajas apuntan a la MISMA persona.
type MemberTxRunner interface {
	InMemberTx(fn func(tx MemberTx) error) error
}

// InvitationRepository: LECTURA de las invitaciones de equipo (F2g1).
//
// Todas las queries van scopeadas por salon_id EXCEPTO FindByTokenHash: el
// token es la capability que resuelve la invitación antes de saber de qué salón
// es (la persona invitada todavía no tiene sesión ni conoce el salón).
//
// El token en claro no está en esta tabla ni en estas firmas: la DB guarda su
// SHA-256 y el caso de uso hashea lo que llega.
type InvitationRepository interface {
	// ListPending trae las pendientes del salón (ni aceptadas, ni revocadas, ni
	// vencidas) de la más vieja a la más nueva.
	ListPending(salonID string, now time.Time) ([]domain.Invitation, error)
	// FindByID resuelve una invitación del salón (el id de otro tenant es
	// ErrNotFound).
	FindByID(salonID, id string) (domain.Invitation, error)
	// FindByTokenHash es la ÚNICA query sin salon_id del sistema: el hash del
	// token es la capability. Sin fila es ErrNotFound (el caso de uso responde
	// el 404 genérico).
	FindByTokenHash(tokenHash string) (domain.Invitation, error)
}

// InvitationTx es la vista TRANSACCIONAL de las invitaciones: todas las
// escrituras (emitir, revocar, canjear) pasan por acá, así nunca queda media
// operación.
//
// El canje es lo más delicado: Claim es un UPDATE condicional
// (accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()) que se
// lleva la fila con un row lock; el segundo canje simultáneo del mismo token ve
// 0 filas y recibe ErrNotFound. La cuenta, la membresía y el estado de la
// invitación commitean juntos o no existen.
type InvitationTx interface {
	// CountPending cuenta las pendientes del salón (tope de 50).
	CountPending(salonID string, now time.Time) (int, error)
	// RevokePendingByEmail da de baja las pendientes de ese email: reinvitar
	// revoca la anterior (y el índice único parcial deja emitir la nueva).
	RevokePendingByEmail(salonID, email string, now time.Time) error
	// Save inserta la invitación nueva.
	Save(i domain.Invitation) error
	// Claim marca la invitación como aceptada por userID y devuelve la fila
	// reclamada. Sin filas (vencida, usada o revocada) es ErrNotFound.
	Claim(id, userID string) (domain.Invitation, error)
	// Revoke revoca una pendiente del salón (ErrNotFound si ya no lo está).
	Revoke(salonID, id string, now time.Time) error
	// SaveUser crea la cuenta de quien acepta (solo el canje público).
	SaveUser(u domain.User) error
	// MembershipExists dice si la persona ya es parte del salón: el canje no
	// cambia el rol de alguien que ya está adentro.
	MembershipExists(salonID, userID string) (bool, error)
	// SaveMembership crea la membresía con el rol y la marca de la invitación.
	SaveMembership(m domain.Membership) error
}

// InvitationTxRunner abre la transacción de las invitaciones. No toma lock por
// salón: la atomicidad la da el UPDATE condicional de Claim (row lock) y el
// índice único parcial de las pendientes.
type InvitationTxRunner interface {
	InInvitationTx(fn func(tx InvitationTx) error) error
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
	// SaveSettingsGuarded es el ÚNICO camino de escritura de la API: corre en
	// UNA transacción que toma el MISMO advisory lock por salón que la creación
	// de liquidaciones ("payroll-write:<salon_id>") y, si la zona horaria CAMBIA
	// y el salón ya tiene liquidaciones (abiertas o pagadas), no escribe NADA
	// (ni los otros campos) y devuelve ErrAlreadyExists -> 409. Una corrida
	// guarda fechas LOCALES y la EXCLUDE compara fechas: cambiar la zona después
	// haría que las mismas ventas cuenten en dos liquidaciones (se pagan dos
	// veces) o en ninguna. Es EXISTS, no una marca: borrar la última corrida
	// abierta descongela la zona.
	SaveSettingsGuarded(s domain.SalonSettings) error
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

// --- Enlaces firmados de un turno (F1) ---
//
// El enlace de cancelar/reprogramar es una capability: la fila guarda SOLO el
// SHA-256 del token (nunca el token en claro) y `FindByTokenHash` es la única
// lectura del sistema que no va scopeada por salón, porque el token resuelve el
// turno antes de saber de qué salón es (quien abre el enlace no tiene sesión).
type BookingLinkRepository interface {
	// Save inserta un token nuevo. El UNIQUE de token_hash es la garantía de
	// que un token no se repite (no hay upsert: cada email lleva el suyo).
	Save(l domain.BookingLink) error
	// FindByTokenHash resuelve el enlace por su hash. Sin fila es ErrNotFound
	// (el caso de uso responde el 404 genérico, sin oráculo).
	FindByTokenHash(tokenHash string) (domain.BookingLink, error)
}

// BookingLinkTx es la vista TRANSACCIONAL del enlace (D2/D4): reclamar el
// token, validar y cambiar el turno commitean juntos o no pasan. El lock es el
// MISMO `booking-write:<salón>` de la agenda y se toma dentro del caso de uso,
// después de resolver el enlace (que es lo único que dice de qué salón es).
type BookingLinkTx interface {
	// FindLink resuelve el enlace por su hash SIN reclamarlo: es lo que dice de
	// qué salón es para poder tomar el lock.
	FindLink(tokenHash string) (domain.BookingLink, error)
	// LockWrites toma el advisory lock de escrituras del salón
	// (`booking-write:<salón>`), el mismo de Book y Update. Es el ÚNICO lock de
	// esta transacción.
	LockWrites(salonID string) error
	// ClaimLink marca el enlace como usado (UPDATE condicional used_at IS NULL)
	// y devuelve la fila reclamada. Sin filas (ya usado, o reclamado por otro
	// pedido simultáneo) es ErrNotFound.
	ClaimLink(id string, now time.Time) (domain.BookingLink, error)
	// SaveLink emite el token nuevo de una reprogramación dentro de la MISMA
	// transacción del cambio: si el cambio no commitea, el token no existe.
	SaveLink(l domain.BookingLink) error
	// FindBooking resuelve el turno scopeado por el salón del enlace (el turno
	// de otro tenant no existe).
	FindBooking(salonID, id string) (domain.Booking, error)
	// UpdateBooking persiste el cambio del turno. La EXCLUDE de Postgres sigue
	// siendo el árbitro del solape (23P01 -> ErrAlreadyExists -> 409).
	UpdateBooking(b domain.Booking) error
}

// BookingLinkTxRunner abre la transacción del enlace: una sola conexión del
// pool y, adentro, un solo advisory lock (el de la agenda del salón).
type BookingLinkTxRunner interface {
	InBookingLinkTx(fn func(tx BookingLinkTx) error) error
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

// --- Exportación CSV (F4) ---

// ExportRepository son las lecturas MASIVAS de la exportación (F4). Cada método
// es UNA sola sentencia SQL en el adaptador (así el archivo es una foto
// consistente) y recibe el salón, la ventana y el LIMIT: el caso de uso pide el
// tope + 1 para saber si hay más filas y devolver 400 sin archivo parcial (D3).
//
// Va aparte de SaleRepository/CashCloseRepository/ClientRepository (ISP): el
// export no reusa los listados existentes (D8) y sus repos no cargan con
// métodos que solo usa esta ruta.
type ExportRepository interface {
	// ListExportSales son las ventas con el nombre de la cuenta profesional
	// (vacío si no hay fila en users) y la suma de cantidades de sus ítems, más
	// reciente primero (created_at DESC, id).
	ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error)
	// ListExportCashCloses son los cierres con closed_at en [from,to), más
	// reciente primero.
	ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error)
	// ListExportClients son las fichas del salón ordenadas por nombre.
	ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error)
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

// --- Nómina y comisiones (F3.2 del contrato F3) ---

// MemberPayRepository: el modelo de pago de cada miembro del salón (D3). Sin
// fila rige domain.DefaultMemberPay (solo comisión), así que la ausencia de
// datos es comportamiento, no un error.
//
// Todas las queries van scopeadas por salon_id: el modelo de pago de otro
// tenant no se lista, no se encuentra y no se escribe (ErrNotFound -> 404).
type MemberPayRepository interface {
	// ListPayModels trae el modelo de CADA miembro del salón —activos y dados de
	// baja— con nombre, rol y estado. Es la lectura de la pantalla de Equipo y la
	// que arma las líneas de la vista previa.
	ListPayModels(salonID string) ([]domain.MemberPayView, error)
	// FindPayModel resuelve una persona del salón. Una persona que no es miembro
	// es ErrNotFound; un miembro sin fila devuelve el default de D3.
	FindPayModel(salonID, userID string) (domain.MemberPayView, error)
	// SavePayModel inserta o actualiza el modelo. La FK compuesta a
	// memberships(user_id, salon_id) es la autorización: una persona de otro
	// salón no tiene dónde guardarse.
	SavePayModel(p domain.MemberPay) error
}

// PayrollSalesRepository: los agregados de ventas por profesional de una
// ventana, con el NUMERADOR de la comisión (Σ qty × precio × pct, D2). El
// adaptador suma en enteros (bigint) y domain hace la única división.
//
// La foto del porcentaje (sale_items.commission_pct) manda; una línea anterior a
// F3.1 (NULL) se estima con la tarifa ACTUAL del servicio y marca la fila.
type PayrollSalesRepository interface {
	// CommissionTotals agrupa por profesional las ventas del salón en [from,to).
	// Un profesional sin ventas no aparece; uno con ventas siempre aparece, aunque
	// ya no esté en el equipo.
	CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error)
	// CommissionTotalFor es el mismo agregado para UNA persona: es la única
	// lectura de «Mis comisiones» (nunca trae los números de otra).
	// Sin ventas devuelve el total en cero, no ErrNotFound.
	CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error)
}

// --- Liquidaciones de nómina (F3.3, D6 del contrato F3) ---

// PayrollRunRepository: las liquidaciones del salón. La cabecera congela el
// período y las líneas congelan los montos de cada persona; toda consulta va
// scopeada por salon_id (una liquidación de otro tenant no existe acá).
type PayrollRunRepository interface {
	// ListRuns trae las liquidaciones del salón, la más nueva primero (por
	// from_date desc), con la cantidad de líneas y los totales.
	ListRuns(salonID string) ([]domain.PayrollRunSummary, error)
	// FindRun trae una liquidación con sus líneas. Inexistente o de otro salón
	// es ErrNotFound (404), sin distinguir cuál de las dos cosas pasó.
	FindRun(salonID, runID string) (domain.PayrollRun, error)
}

// PayrollRunTx es la vista TRANSACCIONAL de las liquidaciones. Cada escritura
// lleva la condición de que la corrida siga ABIERTA: la inmutabilidad de una
// pagada la sostiene la DB y no solo la API.
type PayrollRunTx interface {
	// SaveRun inserta la cabecera y TODAS sus líneas. La EXCLUDE por salón es la
	// que rechaza días ya cubiertos: ErrAlreadyExists (409), nunca un 500.
	SaveRun(run domain.PayrollRun) error
	// FindRun lee la corrida recién escrita (o la que se está por pagar) dentro
	// de la misma transacción.
	FindRun(salonID, runID string) (domain.PayrollRun, error)
	// AdjustLine cambia el ajuste y la nota de una línea SOLO si la corrida está
	// abierta y la línea existe. Pagada -> ErrAlreadyExists; sin línea o sin
	// corrida -> ErrNotFound.
	AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error
	// PayRun marca la corrida como pagada SOLO si está abierta. Pagada (o un
	// segundo pago simultáneo) -> ErrAlreadyExists; inexistente -> ErrNotFound.
	PayRun(salonID, runID, paidBy string) error
	// DeleteRun borra la corrida (y sus líneas en cascada) SOLO si está abierta,
	// liberando los días. Pagada -> ErrAlreadyExists.
	DeleteRun(salonID, runID string) error
	// Settings lee la configuración del salón DENTRO de la transacción, ya con
	// el advisory lock por salón tomado: la creación deriva el período con la
	// zona que vale en ese instante, nunca con una que el PUT de settings está
	// cambiando en paralelo. Fila ausente -> ErrNotFound (el caso de uso aplica
	// los defaults, igual que la lectura normal).
	Settings(salonID string) (domain.SalonSettings, error)
	// PreviewSource da las lecturas de la vista previa sobre la MISMA conexión
	// de la transacción. La creación ya tiene el advisory lock tomado: si estas
	// lecturas pidieran otra conexión del pool, muchas creaciones simultáneas
	// del mismo salón podrían agotarlo y esperarse entre sí.
	PreviewSource() PayrollPreviewSource
}

// PayrollPreviewSource: lo que el motor de la vista previa necesita leer de la
// nómina (modelos de pago y agregados de ventas) DENTRO de la transacción de una
// liquidación. Es un subconjunto de MemberPayRepository/PayrollSalesRepository.
type PayrollPreviewSource interface {
	// ListPayModels trae el modelo de pago de cada miembro del salón.
	ListPayModels(salonID string) ([]domain.MemberPayView, error)
	// CommissionTotals agrupa por profesional las ventas del salón en [from,to).
	CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error)
}

// PayrollTxRunner ejecuta una escritura de liquidaciones en UNA transacción que
// primero toma un advisory lock por salón (prefijo "payroll-write:"). El lock
// serializa las creaciones del mismo salón; la EXCLUDE es la red de seguridad.
type PayrollTxRunner interface {
	InPayrollTx(salonID string, fn func(tx PayrollRunTx) error) error
}

// --- Gastos y ganancia neta (F3b del contrato Fase 6) ---

// ExpenseRepository: los gastos operativos del salón (migración 026). El gasto
// guarda un DÍA LOCAL (`spent_on DATE`) y un monto entero en centavos.
//
// Todas las queries van scopeadas por salon_id: el gasto de otro tenant no se
// lista, no se encuentra, no se edita y no se borra (ErrNotFound -> 404).
type ExpenseRepository interface {
	// Save inserta el gasto. Los CHECK y las FK de la migración son la red de
	// seguridad de los límites que ya validó el dominio.
	Save(e domain.Expense) error
	// ListRange trae hasta `limit` gastos del salón con spent_on en
	// [from, to] (inclusivo), del más nuevo al más viejo (spent_on y created_at
	// descendentes). El segundo valor dice si había MÁS filas que el tope.
	ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error)
	// FindByID resuelve un gasto del salón (el id de otro tenant es ErrNotFound).
	FindByID(salonID, id string) (domain.Expense, error)
	// Update reemplaza los cuatro campos del gasto SOLO si es de ese salón. Sin
	// filas afectadas es ErrNotFound (nunca un upsert).
	Update(e domain.Expense) error
	// Delete borra el gasto del salón (DELETE físico: es dinero operativo).
	// Sin filas es ErrNotFound.
	Delete(salonID, id string) error
}

// ProfitRepository: la foto de la ganancia neta del salón (D1, D2 y D6). El
// adaptador lee ingresos, gastos y nómina PAGADA en UNA sola transacción
// REPEATABLE READ de solo lectura, así los tres componentes son la misma foto.
//
// El día es el LOCAL del salón (zona `timezone`): las ventas y los pagos se
// agrupan por `AT TIME ZONE` y la ventana es [from, to), las medianoches locales
// que derivó el dominio. La nómina suma `payroll_lines.total_cents` de las
// corridas con status='paid' en el día local de `paid_at`; las abiertas no
// cuentan (D1).
type ProfitRepository interface {
	// DailyProfit devuelve los montos por día local (clave YYYY-MM-DD) y el
	// total por categoría de los gastos del período. Un día sin movimientos no
	// aparece en los mapas: el reporte lo rellena con ceros.
	DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error)
}

// --- Historial de la clienta y vínculos por id (F5 del contrato Fase 6) ---

// ClientHistoryRepository: la LECTURA del historial (D6). Todo sale de UNA
// transacción REPEATABLE READ de solo lectura con tope de 25 s en el adaptador,
// así la foto es una sola: un turno que se marca hecho a mitad de camino no
// puede entrar en los conteos y no en la lista.
//
// `now` lo decide el caso de uso (los próximos son los `booked` con starts_at >
// now) y `limit` acota CADA lista (más reciente primero). Las queries van
// scopeadas por salon_id Y client_id: el historial de otra ficha no existe.
type ClientHistoryRepository interface {
	History(salonID, clientID string, now time.Time, limit int) (domain.ClientHistoryData, error)
}

// ClientLinkRepository: el UPDATE aparte que vincula un turno o una venta con la
// ficha (D1/D3). NO es parte del INSERT: es best-effort, un fallo se loguea y
// nunca tumba el turno ni la venta.
//
// Todas las escrituras van scopeadas por salon_id: la ficha de otro salón no se
// vincula (el EXISTS de la query es la garantía dura, no solo el caso de uso).
type ClientLinkRepository interface {
	// LinkBookingByID vincula el turno con ESA ficha (misma ficha que ya creó o
	// reusó la reserva pública).
	LinkBookingByID(salonID, bookingID, clientID string) error
	// LinkBookingByPhone vincula el turno del personal con la ÚNICA ficha del
	// salón cuyo teléfono normalizado coincide: sin coincidencia o con dos
	// candidatas no toca nada (nunca crea una ficha, D3-b).
	LinkBookingByPhone(salonID, bookingID string) error
	// LinkSaleByID vincula la venta del POS con la ficha elegida a mano (D3-c).
	LinkSaleByID(salonID, saleID, clientID string) error
	// SaleClientID devuelve el vínculo REAL de la venta ("" si no tiene): la
	// respuesta de POST /sales dice lo que quedó guardado y no lo que se pidió
	// (R4-001: un UPDATE caído o un replay no deben afirmar un vínculo falso).
	SaleClientID(salonID, saleID string) (string, error)
}

// --- Productos e inventario del POS (F6 del contrato Fase 6) ---

// ProductRepository: el catálogo de productos y su libro de stock (D1/D2).
// Toda consulta va scopeada por salon_id: el producto de otro tenant no existe
// acá (ErrNotFound).
type ProductRepository interface {
	// Create inserta el producto y, si initial != nil, su movimiento inicial en
	// la MISMA transacción (alta con stock > 0).
	Create(p domain.Product, initial *domain.StockMovement) error
	// Save actualiza nombre, precio, umbral y estado de un producto existente.
	// NO toca el stock: el PUT del contrato no lo cambia.
	Save(p domain.Product) error
	FindByID(salonID, id string) (domain.Product, error)
	// List trae todos los productos del salón (activos e inactivos) por nombre.
	List(salonID string) ([]domain.Product, error)
	// Adjust aplica el delta con el UPDATE condicional de stock (nunca negativo)
	// y escribe el movimiento en la MISMA transacción. Devuelve el producto ya
	// actualizado (con el stock real que quedó).
	Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error)
	// ListMovements trae el libro de un producto, más reciente primero.
	ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error)
}

// SaleProductsRepository: la venta CON productos (D3), en UNA sola transacción:
// la venta, sus líneas de servicio, las líneas de producto, el descuento de
// stock de cada producto (ordenado por id para no formar interbloqueos) y un
// movimiento `sale` por descuento. Si algo falla se deshace TODO.
//
// Devuelve los productos que CRUZARON el umbral de stock bajo en esta venta
// (D8): el caso de uso avisa DESPUÉS del commit y un fallo al avisar nunca
// cambia la respuesta de la venta.
type SaleProductsRepository interface {
	SaveWithProducts(s domain.SaleWithProducts) (crossed []domain.Product, err error)
}

// --- Lista de espera (F7) ---

// WaitlistRepository: la lista de espera del salón (migración 030). Una entrada
// es un servicio + un día LOCAL + (profesional opcional) + contacto, y el token
// de la baja se guarda SOLO como SHA-256.
//
// Toda consulta va scopeada por salon_id: la entrada de otro tenant no se lista,
// no se encuentra y no se da de baja (ErrNotFound). Las fechas NULL son la
// verdad del estado (D3): no hay una columna `status` que pueda contradecirlas.
type WaitlistRepository interface {
	// FindActive resuelve la entrada ACTIVA idéntica (mismo email, servicio,
	// profesional y día): es la idempotencia del alta (D4). Sin fila es
	// ErrNotFound.
	FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error)
	// FindByTokenHash es la ÚNICA query sin salon_id: el hash del token es la
	// capability que resuelve la entrada antes de saber de qué salón es (quien
	// abre el enlace no tiene sesión). Sin fila es ErrNotFound.
	FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error)
	// Leave setea left_at con un UPDATE condicional por token_hash. Repetirlo no
	// hace nada (la baja es idempotente, D5): no es un error.
	Leave(tokenHash string, now time.Time) error
	// ListUpcoming son las entradas vigentes del salón con día >= fromDay (día
	// LOCAL del salón), ordenadas por día y created_at (D8).
	ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error)
	// RemoveByID da de baja una entrada del salón (UPDATE condicional left_at
	// IS NULL). De otro salón o ya dada de baja: ErrNotFound.
	RemoveByID(salonID, id string, now time.Time) error
	// ListGroups son los grupos (salón, servicio, profesional, día) con al menos
	// una entrada RECLAMABLE ahora: sin baja, sin aviso, con intentos
	// disponibles, con el reclamo vencido y con el día >= hoy en la zona del
	// salón (D3/D6). Es lo que la tarea periódica recorre.
	ListGroups(now time.Time) ([]domain.WaitlistGroup, error)
	// MarkNotified cierra la entrada después de un envío exitoso: nunca se
	// vuelve a avisar (D6).
	MarkNotified(id string, now time.Time) error
}

// WaitlistTx es la vista TRANSACCIONAL del barrido (D6): el reclamo de los
// avisos de UN grupo corre dentro de una transacción con
// `pg_advisory_xact_lock(hashtext('waitlist-write:<salón>'))` y RECUENTA los
// avisos retenidos ahí adentro. Dos instancias del job (o dos ticks) no pueden
// avisar dos veces a la misma entrada: el lock serializa y el UPDATE ... FOR
// UPDATE SKIP LOCKED se lleva cada fila una sola vez.
type WaitlistTx interface {
	// LockWrites toma el advisory lock de escrituras de la lista del salón. Es
	// el único lock de la transacción y se libera solo al terminar.
	LockWrites(salonID string) error
	// FindActive, CountActiveByEmail, CountActiveBySalon, Create y MarkLeft son las
	// escrituras del ALTA (D4) atadas a la MISMA transacción que LockWrites: el
	// conteo de los topes y el insert no pueden intercalarse con otra alta del
	// salón. FindActive resuelve la entrada ACTIVA idéntica (mismo email,
	// servicio, profesional y día). fromDay es HOY en la zona del salón: un aviso
	// de un día pasado no ocupa cupo. Create devuelve ErrAlreadyExists si el
	// índice único parcial rechaza. MarkLeft cierra una entrada por id (la ya
	// avisada que se reemplaza al volver a anotarse).
	FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error)
	CountActiveByEmail(salonID, email, fromDay string) (int, error)
	CountActiveBySalon(salonID, fromDay string) (int, error)
	Create(e domain.WaitlistEntry) error
	MarkLeft(id string, now time.Time) error
	// CountRetained cuenta los avisos RETENIDOS del grupo (D6): los enviados con
	// reclamo de las últimas 2 h y los que están EN VUELO (reclamo de los
	// últimos 5 minutos sin aviso). Un envío que falló no retiene nada: se
	// reintenta pasada la gracia.
	CountRetained(g domain.WaitlistGroup, now time.Time) (int, error)
	// Claim reclama hasta limit entradas del grupo, FIFO por (created_at, id),
	// con UN solo UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED):
	// setea notify_claimed_at = now y notify_attempts = notify_attempts + 1.
	Claim(g domain.WaitlistGroup, now time.Time, limit int) ([]domain.WaitlistEntry, error)
	// SetToken guarda el SHA-256 del token de la baja de UNA entrada reclamada.
	// La capability se emite en el momento del aviso (el token en claro solo
	// viaja en el email): una entrada que nunca se avisa no tiene enlace vivo.
	SetToken(id, tokenHash string) error
}

// WaitlistTxRunner abre la transacción del reclamo. Una sola conexión del pool
// para toda la operación.
type WaitlistTxRunner interface {
	InWaitlistTx(fn func(tx WaitlistTx) error) error
}

// ===== F8: paquetes y bonos prepagos con saldo por clienta (inicio) =====

// PackageRepository: el catálogo de paquetes del salón (migración 031). Toda
// consulta va scopeada por salon_id: el paquete de otro tenant no existe acá
// (ErrNotFound). Quitar un paquete es desactivarlo: no hay borrado.
type PackageRepository interface {
	// Create inserta el paquete. Un nombre ACTIVO repetido en el salón (sin
	// importar mayúsculas) es ErrAlreadyExists.
	Create(p domain.ServicePackage) error
	// Save reemplaza nombre, precio, vigencia y estado de un paquete existente
	// (servicio y sesiones no se editan). ErrNotFound si no es del salón;
	// ErrAlreadyExists si el nombre activo ya existe.
	Save(p domain.ServicePackage) error
	FindByID(salonID, id string) (domain.ServicePackage, error)
	// List trae los paquetes del salón (activos e inactivos) por nombre, con el
	// nombre del servicio.
	List(salonID string) ([]domain.ServicePackage, error)
}

// ClientPackageRepository: los paquetes comprados por una clienta y su libro de
// saldo (D3/D4). Toda consulta va scopeada por salon_id y client_id. Cada cambio
// de saldo escribe su movimiento en la MISMA transacción: en todo momento
// sessions_left = Σ delta.
type ClientPackageRepository interface {
	// Purchase escribe la compra en UNA transacción: la venta, el paquete de la
	// clienta y el movimiento `purchase`. Si algo falla no queda nada. Un
	// client_op_id ya usado (índice único de sales) es ErrAlreadyExists y NO
	// deja nada escrito.
	Purchase(o domain.PackagePurchaseOrder) error
	// FindPurchaseByOp resuelve la compra original de un client_op_id. Sin venta
	// con ese id es ErrNotFound; con una venta que NO es una compra de paquete es
	// ErrAlreadyExists (el id ya se usó para otra cosa).
	FindPurchaseByOp(salonID, clientOpID string) (domain.ClientPackage, error)
	// Redeem descuenta UNA sesión con un UPDATE condicional (saldo >= 1 y no
	// vencido en r.Now) más el movimiento `redeem`, en una transacción. Sin filas
	// afectadas distingue ErrNotFound («Paquete no encontrado»), sin saldo y
	// vencido (409 con su texto). Un client_op_id repetido devuelve el estado
	// actual SIN descontar otra vez.
	Redeem(r domain.PackageRedeemRequest) (domain.ClientPackage, error)
	// Adjust mueve el saldo con un UPDATE condicional (0..sessions_total) y escribe
	// el movimiento `adjust`. ErrNotFound o 409 «El ajuste deja el saldo fuera de
	// rango».
	Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error)
	// ListByClient trae los paquetes de la clienta (los vigentes con saldo
	// primero, tope MaxPackageListed) con el nombre del servicio.
	ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error)
	// ListMovements trae el libro de UN paquete de esa clienta, más nuevo
	// primero, con tope MaxPackageListed. ErrNotFound si el paquete no es de esa
	// clienta en ese salón.
	ListMovements(salonID, clientID, clientPackageID string) ([]domain.PackageMovement, error)
}

// ===== F8: paquetes y bonos prepagos con saldo por clienta (fin) =====

// ===== F16: web pública fiel al panel (inicio) =====

// SalonProfileRepository: el perfil público del salón (migración 032, D1). Toda
// consulta va scopeada por el id del salón; el slug no se escribe nunca.
type SalonProfileRepository interface {
	// Profile lee nombre, slug y los cinco datos opcionales. ErrNotFound si el
	// salón no existe.
	Profile(salonID string) (domain.SalonProfile, error)
	// SaveProfile reemplaza nombre y los cinco datos en UN UPDATE (un texto vacío
	// es NULL). No toca el slug. ErrNotFound si el salón no existe.
	SaveProfile(p domain.SalonProfile) error
}

// SalonGalleryRepository: la galería pública del salón (tabla salon_photos). Es
// una tabla APARTE de client_photos: ninguna de estas consultas puede devolver
// una foto de clienta. Toda consulta va scopeada por salón.
type SalonGalleryRepository interface {
	// Add inserta la foto bajo un lock por salón y cuenta DENTRO del lock: con
	// `max` fotos ya guardadas no escribe nada y devuelve ErrAlreadyExists con el
	// texto de domain.MsgGalleryFull. Dos subidas simultáneas con 11 fotos dejan 12.
	Add(p domain.SalonPhoto, max int) error
	Count(salonID string) (int, error)
	// List trae la galería en orden de subida (la más vieja primero).
	List(salonID string) ([]domain.SalonPhoto, error)
	// Find resuelve una foto del salón (ErrNotFound si es de otro salón).
	Find(salonID, id string) (domain.SalonPhoto, error)
	// FindPublic resuelve una foto por el SLUG del salón y el id: la ruta pública
	// sin sesión. Un id de otro salón o inexistente es ErrNotFound.
	FindPublic(slug, id string) (domain.SalonPhoto, error)
	// SetCaption reemplaza la leyenda ("" = sin leyenda). ErrNotFound si la foto
	// no es del salón.
	SetCaption(salonID, id, caption string) error
	// Delete borra la fila (el caso de uso borra después el archivo). ErrNotFound
	// si la foto no es del salón.
	Delete(salonID, id string) error
}

// ===== F16: web pública fiel al panel (fin) =====

// ===== F9: fidelidad, cada N visitas un servicio de regalo (inicio) =====

// LoyaltyProgramRepository: el programa de fidelidad del salón (migración 033,
// D3): a lo sumo UNA fila por salón. Toda consulta va scopeada por salon_id.
type LoyaltyProgramRepository interface {
	// Find lee el programa con el nombre del servicio de regalo. ErrNotFound si
	// el salón nunca lo guardó.
	Find(salonID string) (domain.LoyaltyProgram, error)
	// Save guarda el programa en UN upsert: el primer guardado fija
	// counting_from = p.CountingFrom y los siguientes LO CONSERVAN (D2/D4).
	// Devuelve el programa tal como quedó guardado, con el nombre del servicio.
	Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error)
}

// LoyaltyRewardRepository: los vales entregados y las visitas de una clienta
// (D2, D5..D7). Toda consulta va scopeada por salon_id y client_id.
type LoyaltyRewardRepository interface {
	// Visits cuenta, con una consulta de SOLO LECTURA, las ventas de la clienta
	// con created_at >= since y al menos una línea de servicio en sale_items (una
	// venta solo de productos y la compra de un paquete no cuentan, D2).
	Visits(salonID, clientID string, since time.Time) (int, error)
	// CountGranted cuenta los vales ya entregados a la clienta (usados o no).
	CountGranted(salonID, clientID string) (int, error)
	// ListByClient trae los vales de la clienta, los más nuevos primero, con
	// tope MaxLoyaltyListed.
	ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error)
	// Grant entrega un vale en UNA transacción bajo un advisory lock por clienta:
	// recalcula visitas y entregados y aplica domain.NewLoyaltyGrant. UNIQUE
	// (client_id, reward_number) es la guarda final. Un client_op_id repetido
	// devuelve el MISMO vale sin entregar otro. 409 «La fidelidad no está
	// activada» / «No hay premios disponibles».
	Grant(r domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error)
	// Redeem usa UN vale con un UPDATE condicional (no usado y no vencido en
	// r.Now): dos usos simultáneos => UNO gana. Sin filas afectadas distingue
	// ErrNotFound («Premio no encontrado»), usado y vencido (409 con su texto) en
	// la misma transacción. Un client_op_id repetido devuelve el estado actual
	// SIN usar otra vez.
	Redeem(r domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error)
}

// ===== F9: fidelidad, cada N visitas un servicio de regalo (fin) =====

// ===== F10: registro de auditoría (inicio) =====

// AuditRepository: el registro de auditoría del salón (migración 034, D1). Es
// APPEND-ONLY: la base rechaza UPDATE y DELETE sobre audit_events (solo se borra
// en cascada con el salón), así que el puerto no tiene nada para modificar ni
// borrar. Toda consulta va scopeada por salon_id.
type AuditRepository interface {
	// Append escribe UN evento. El middleware lo llama después de una respuesta
	// 2xx: un error acá se loguea y no rompe la petición (D2).
	Append(e domain.AuditEvent) error
	// List trae, del más nuevo al más viejo (created_at DESC, id DESC), a lo
	// sumo q.Limit eventos del salón, ESTRICTAMENTE anteriores a q.Before (si
	// viene) y que cumplan domain.AuditActionMatches(q.Action, action).
	List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error)
}

// ===== F10: registro de auditoría (fin) =====

// ===== F10b: 2FA TOTP (inicio) =====

// TwoFactorRepository: el 2FA de cada usuario (migración 035, D6): la fila de
// user_totp y los códigos de recuperación. Toda operación va scopeada por
// user_id. El secreto viaja SIEMPRE cifrado: este puerto no ve texto claro.
type TwoFactorRepository interface {
	// Find lee el estado. ErrNotFound si el usuario nunca hizo setup.
	Find(userID string) (domain.TwoFactorState, error)
	// SavePending guarda (o REEMPLAZA) el enrolamiento pendiente con un UPSERT
	// que no pisa un 2FA ya activo: ErrAlreadyExists si ya está habilitado.
	SavePending(userID string, secretEnc []byte) error
	// Enable, en UNA transacción, marca enabled=true (solo si sigue pendiente y
	// con ESE secreto), deja last_step=step e inserta los hashes de los códigos
	// de recuperación. ErrNotFound si no hay enrolamiento con ese secreto;
	// ErrAlreadyExists si ya estaba habilitado.
	Enable(userID string, secretEnc []byte, step int64, codeHashes []string) error
	// BeginAttempt reserva un intento de verificar un código con UN solo
	// `UPDATE ... RETURNING` que, si el usuario no está bloqueado, cuenta el
	// intento como fallido (failed_count+1) y al llegar a
	// domain.TwoFactorMaxFailures fija locked_until = now() +
	// domain.TwoFactorLockDuration. Devuelve el secreto cifrado y last_step en
	// ese mismo paso. domain.ErrTooManyAttempts si está bloqueado (ANTES de
	// comparar nada); ErrNotFound si no hay 2FA habilitado.
	BeginAttempt(userID string) (domain.TwoFactorAttempt, error)
	// CompleteStep marca el intento como acierto con un código TOTP del paso
	// `step`: UN UPDATE condicionado a `last_step < step` que además reinicia el
	// contador y el bloqueo. false = el paso ya estaba usado (replay): cuenta
	// como fallo (ya quedó contado en BeginAttempt).
	CompleteStep(userID string, step int64) (bool, error)
	// UseRecoveryCode gasta UN código con un UPDATE condicionado a
	// `used_at IS NULL` y, en la misma sentencia, reinicia el contador. false =
	// no existe o ya se usó: de dos usos simultáneos gana UNO.
	UseRecoveryCode(userID, codeHash string) (bool, error)
	// Disable borra, en UNA transacción, el secreto y los códigos de
	// recuperación. ErrNotFound si no había nada.
	Disable(userID string) error
}

// ChallengeIssuer: el desafío del segundo paso del login (D8). Es un JWT con
// claim typ:"2fa" y 5 minutos de vida que NUNCA sirve de sesión: TokenIssuer.Parse
// lo rechaza, y ParseChallenge rechaza cualquier token sin ese claim.
type ChallengeIssuer interface {
	IssueChallenge(userID string) (string, error)
	// ParseChallenge devuelve el userID de un desafío vigente.
	ParseChallenge(token string) (string, error)
}

// ChallengeClaimsParser es la interfaz OPCIONAL de un ChallengeIssuer que además
// sabe CUÁNDO se emitió el desafío (claim `ia`, milisegundos Unix, el mismo de
// las sesiones). El login lo usa para rechazar un desafío emitido antes del
// último cambio de contraseña (F15, D4). Es opcional a propósito: los fakes de
// ChallengeIssuer de las pruebas existentes no cambian de firma y un emisor sin
// ella se trata como «desafío sin `ia`» (vale solo si la contraseña nunca
// cambió).
type ChallengeClaimsParser interface {
	// ParseChallengeClaims valida igual que ParseChallenge y devuelve además el
	// instante de emisión.
	ParseChallengeClaims(token string) (domain.SessionClaims, error)
}

// TwoFactorLoginGate es lo único que el login necesita saber del 2FA: si la
// cuenta lo exige y, en ese caso, el desafío. required=false => login de
// siempre (sin fila en user_totp o enabled=false).
type TwoFactorLoginGate interface {
	ChallengeFor(userID string) (challenge string, required bool, err error)
}

// ===== F10b: 2FA TOTP (fin) =====

// ===== F15: recuperar contraseña, revocar sesiones y verificar email =====

// PasswordResetRepository guarda los pedidos de restablecer contraseña
// (migración 036). El token en claro nunca entra: solo su SHA-256.
type PasswordResetRepository interface {
	// IssueReset emite el pedido en UNA transacción con el usuario bloqueado: si
	// domain.ResetThrottled lo frena (r.CreatedAt es el «ahora» del caso de uso)
	// devuelve issued=false y no escribe nada; si no, invalida los pedidos
	// anteriores sin usar del usuario y guarda este. Un solo vigente por usuario.
	IssueReset(r domain.PasswordReset) (issued bool, err error)
	// FindReset resuelve un pedido por el hash de su token (ErrNotFound si no
	// existe). Es solo lectura: no consume nada.
	FindReset(tokenHash string) (domain.PasswordReset, error)
	// ConsumeReset es el canje, TODO en UNA transacción: UPDATE condicional de
	// used_at (used_at IS NULL AND expires_at > now()), nueva pass_hash,
	// password_changed_at igual al used_at (UN solo valor del reloj de la base),
	// email_verified_at si era NULL, e invalidación de los demás pedidos del
	// usuario. No toca el 2FA ni emite sesión. ErrNotFound si el pedido no se
	// pudo canjear (desconocido, usado o vencido): de 30 canjes paralelos del
	// mismo token gana exactamente UNO.
	ConsumeReset(tokenHash, newPassHash string) error
}

// EmailVerificationRepository guarda los pedidos de verificar email (036).
type EmailVerificationRepository interface {
	// IssueVerification guarda el pedido bajo el lock del usuario. Con
	// minInterval > 0, si ya hay uno creado hace menos de minInterval devuelve
	// issued=false y no escribe nada (el reenvío: 429). minInterval = 0 no
	// limita (el primer correo del registro).
	IssueVerification(v domain.EmailVerification, minInterval time.Duration) (issued bool, err error)
	// ConsumeVerification marca el email como verificado con UN UPDATE
	// condicional (used_at IS NULL AND expires_at > now()) en la misma
	// transacción que users.email_verified_at. Idempotente: un token ya usado
	// cuyo usuario ya está verificado es nil. ErrNotFound en cualquier otro caso
	// (desconocido, vencido, usado sin verificar).
	ConsumeVerification(tokenHash string) error
}

// VerificationSender lo usa el registro para disparar el correo de verificación
// (asíncrono: nunca demora ni falla el registro).
type VerificationSender interface {
	SendVerification(u domain.User)
}

// SessionTokenParser es la interfaz OPCIONAL de un TokenIssuer que sabe leer el
// claim `ia` (D4). Es opcional a propósito: los fakes de TokenIssuer de las
// pruebas existentes no cambian de firma; sin ella un token se trata como «sin
// ia».
type SessionTokenParser interface {
	// ParseSession valida igual que TokenIssuer.Parse (método, firma,
	// vencimiento, tipo: un desafío de 2FA nunca es sesión) y devuelve además el
	// instante de emisión.
	ParseSession(token string) (domain.SessionClaims, error)
}

// SessionGate es la compuerta de revocación (D4): cuándo cambió por última vez
// la contraseña del usuario. nil = nunca. Un error es un fallo de la consulta:
// quien la usa FALLA CERRADO (401).
type SessionGate interface {
	PasswordChangedAt(userID string) (*time.Time, error)
}

// ===== F15 (fin) =====

// ===== F11: sena online con banco simulado (inicio) =====
//
// Este bloque es el contrato que el adaptador Postgres (migración 037), el banco
// simulado (adapters/simbank) y las rutas de la SLICE 2 implementan. Todo el
// dinero de F11 vive en tablas PROPIAS: ninguna consulta de acá toca ventas,
// caja, nómina, ganancia ni exportes (D1).

// DepositPolicyRepository: la política de señas del salón (migración 037, a lo
// sumo UNA fila por salón). Toda consulta va scopeada por salon_id.
type DepositPolicyRepository interface {
	// Find lee la política. ErrNotFound si el salón nunca la guardó (el caso de
	// uso lo traduce a Saved=false: enabled false y el resto null, D3).
	Find(salonID string) (domain.DepositPolicy, error)
	// Save es UN upsert que reemplaza enabled, percent, hold_minutes,
	// refund_hours y updated_at. No toca ninguna otra tabla y devuelve la fila
	// tal como quedó guardada (Saved=true).
	Save(p domain.DepositPolicy) (domain.DepositPolicy, error)
}

// DepositRepository: las señas de las reservas y su libro mayor (booking_deposits
// y deposit_events, migración 037).
//
// Invariantes del adaptador:
//   - TODA lectura y escritura va en UNA transacción con
//     `pg_advisory_xact_lock('deposit:' || <id de la sena>)` cuando toca una sena
//     concreta (Apply*, Settle*): así dos callbacks del mismo cargo no se
//     intercalan ni con el barrido.
//   - todo cambio de estado es UN solo `UPDATE ... WHERE status = <el de origen>`
//     (o `IN (...)` en el pago tardío) y, en la MISMA transacción, inserta su
//     evento en deposit_events. Si el UPDATE no afecta filas, NO se inserta el
//     evento y el resultado dice que no se aplicó: el reintento no tiene efecto.
//   - `deposit_events` es APPEND-ONLY (trigger de la migración 037, igual que
//     audit_events: solo se borra en cascada con la sena y el salón) y su índice
//     único parcial `bank_event_id` es la guarda de "el evento del banco se aplica
//     UNA sola vez".
//   - las lecturas que devuelven BookingDeposit completan SIEMPRE los campos de
//     solo lectura del turno (BookingStatus, StartsAt, ClientName) con un join:
//     el caso de uso decide con ellos sin volver a consultar.
//   - un id con NUL o UTF-8 inválido nunca llega a una query: el caso de uso lo
//     resuelve como 404 antes de llamar.
type DepositRepository interface {
	// Create inserta la sena y su evento `created` en UNA transacción. Es
	// idempotente por las dos claves del contrato: con el MISMO client_op_id (o
	// para el MISMO booking_id, que es UNIQUE) devuelve la sena original SIN
	// escribir nada y SIN error. Dos altas simultáneas: una gana y la otra
	// recibe ErrAlreadyExists (el caso de uso vuelve a leer y devuelve la
	// original; el cargo huérfano que alcanzó a crear el banco vence solo).
	Create(d domain.BookingDeposit) (domain.BookingDeposit, error)
	// FindByBooking resuelve la sena de un turno del salón. ErrNotFound si ese
	// turno no tiene sena (o es de otro salón). Es la lectura de la idempotencia
	// de D8: el reintento del navegador devuelve el MISMO payment.
	FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error)
	// FindByCharge resuelve una sena por el id del cargo. NO va scopeada por
	// salón a propósito: el id del cargo es la credencial pública (D8) y el
	// callback del banco no sabe de qué salón es. ErrNotFound si no existe.
	FindByCharge(chargeID string) (domain.BookingDeposit, error)
	// ApplyApproved aplica un evento `approved` (D7) bajo el lock de la sena:
	//   - si el index único de bank_event_id ya tiene ese evento: Applied=false,
	//     sin efecto (el reenvío del banco es 200);
	//   - si la sena es pagable (domain.BookingDeposit.Payable): UN UPDATE
	//     condicional `status='pending'` -> `paid` con paid_at y el evento
	//     `paid` -> Applied=true;
	//   - si ya está resuelta (paid/refunded/forfeited): no escribe nada,
	//     Applied=false;
	//   - si llegó tarde (vencida o reserva cancelada): NO ESCRIBE NADA y
	//     devuelve LatePayment=true. Quien llama pide el reembolso al banco y
	//     recién después llama a SettleLateRefund: escribir antes dejaría una
	//     sena «devuelta» cuyo reembolso falló y no se reintenta nunca.
	ApplyApproved(r domain.DepositCallback) (domain.DepositApply, error)
	// ApplyDeclined registra un intento rechazado (`declined` o `insufficient`,
	// D7): evento `attempt_declined` con amount_cents y la MISMA guarda de
	// idempotencia por bank_event_id. La sena SIGUE `pending` (el cliente puede
	// reintentar hasta que venza). false = ese evento ya se había registrado.
	ApplyDeclined(r domain.DepositCallback) (bool, error)
	// ExpirePending vence hasta `limit` señas `pending` con expires_at <= now, en
	// UNA transacción: UN `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP
	// LOCKED) ... RETURNING` a `expired` con su evento `expired`, y devuelve las
	// filas que acaba de vencer. Debe devolver ADEMÁS las señas que ya están
	// `expired` y cuyo turno sigue `booked` (un tick anterior no alcanzó a
	// cancelarlo): el caso de uso reintenta la cancelación y el turno no queda
	// retenido para siempre. Nunca devuelve señas con el turno ya cerrado.
	ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error)
	// ListPaidCancelled trae hasta `limit` señas `paid` cuyo turno quedó
	// `cancelled`, las más viejas primero (D9(b)). `done` y `no_show` NO entran
	// nunca: esas no se reembolsan.
	ListPaidCancelled(limit int) ([]domain.BookingDeposit, error)
	// SettleLateRefund cierra una sena que estaba `pending` o `expired` (pago
	// tardío de D7): UN UPDATE condicional a `refunded` con settled_at y el
	// evento `late_payment_refunded` (bank_event_id con su guarda única).
	// false = ya estaba cerrada (otro callback o el barrido ganaron). El
	// reembolso al banco lo pidió quien llama ANTES, y es idempotente por cargo.
	SettleLateRefund(s domain.DepositSettlement) (bool, error)
	// SettlePaidCancelled cierra una sena `paid` cuyo turno se canceló (D9(b)):
	// UN UPDATE condicional `status='paid'` -> s.Status (`refunded` o
	// `forfeited`) con settled_at y el evento del mismo nombre. false = ya
	// estaba cerrada. El reembolso (cuando toca) lo pidió quien llama antes.
	SettlePaidCancelled(s domain.DepositSettlement) (bool, error)
	// List trae la pantalla «Señas» (D10) del salón: de la más nueva a la más
	// vieja por (created_at DESC, id DESC), a lo sumo q.Limit, opcionalmente
	// filtrada por estado y ESTRICTAMENTE anterior al cursor q.Before. Aislamiento
	// por salon_id obligatorio.
	List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error)
}

// ChargeRequest es lo que el comercio le pide al banco (D5).
type ChargeRequest struct {
	// Reference es el id del turno: el banco lo devuelve en el callback.
	Reference   string
	AmountCents int
	Description string
	ExpiresAt   time.Time
}

// Charge es lo que el banco devuelve al crear el cargo.
type Charge struct {
	// ID es la credencial pública (43 caracteres base64url, D8): el caso de uso
	// la guarda en booking_deposits.charge_id.
	ID string
	// PayPath es la ruta de la página de pago del banco (para armar pay_url).
	PayPath string
}

// CallbackEvent es un evento del banco YA verificado y parseado (D5).
type CallbackEvent struct {
	EventID     string
	ChargeID    string
	Reference   string
	Kind        string
	AmountCents int
}

// PaymentGateway es el banco (D5). La verificación de la firma vive COMPLETA en
// el adaptador: el caso de uso nunca ve bytes sin verificar.
type PaymentGateway interface {
	// Enabled dice si hay banco configurado (PAYMENT_SIM_SECRET). Con false las
	// rutas del banco y el callback no existen (404), no se puede activar la
	// política (409) y el trabajo periódico no corre (D12).
	Enabled() bool
	// CreateCharge crea el cargo. El id que devuelve tiene que ser el de D8
	// (domain.NewDepositChargeID). Un error se propaga: la reserva se cancela por
	// el camino existente y el cliente recibe el error de servidor de hoy (D8).
	CreateCharge(ctx context.Context, r ChargeRequest) (Charge, error)
	// Refund devuelve el dinero del cargo y es IDEMPOTENTE por cargo:
	// reembolsar dos veces devuelve nil sin duplicar (lo garantiza el banco
	// simulado con su tabla simbank_refunds). Lo usan el pago tardío (D7) y el
	// barrido (D9(b)); por eso el orden «reembolsar y después cerrar» es seguro.
	Refund(ctx context.Context, chargeID string, amountCents int, reason string) error
	// ParseCallback verifica la firma (`X-Simbank-Signature`, +-300 s, tiempo
	// constante) y devuelve el evento. Cualquier motivo de rechazo es el MISMO
	// error uniforme (domain.MsgPaymentSignatureInvalid, 400). Un cuerpo de más de
	// 8 KB se rechaza antes de parsear.
	ParseCallback(header http.Header, body []byte, now time.Time) (CallbackEvent, error)
}

// NoopGateway es el banco que NO está configurado (D5/D12). No crea cargos, no
// reembolsa nada y no valida callbacks: sin PAYMENT_SIM_SECRET no hay cobro.
type NoopGateway struct{}

// Enabled: sin banco configurado siempre false.
func (NoopGateway) Enabled() bool { return false }

// CreateCharge es imposible sin banco: el 409 del contrato (D3).
func (NoopGateway) CreateCharge(context.Context, ChargeRequest) (Charge, error) {
	return Charge{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
}

// Refund no tiene nada que devolver: nil (un barrido no puede quedar trabado
// para siempre por un banco que no existe).
func (NoopGateway) Refund(context.Context, string, int, string) error { return nil }

// ParseCallback rechaza todo: sin banco no hay firma que valga.
func (NoopGateway) ParseCallback(http.Header, []byte, time.Time) (CallbackEvent, error) {
	return CallbackEvent{}, domain.NewMessage(domain.ErrValidation, domain.MsgPaymentSignatureInvalid)
}

var _ PaymentGateway = NoopGateway{}

// BookingCanceller cancela un turno por el camino EXISTENTE de la agenda (D9):
// `domain.Booking.SetStatus(cancelled)` + el UPDATE del repositorio de reservas
// bajo el lock de escrituras del salón, con sus eventos y avisos. El trabajo
// periódico no tiene sesión ni actor, así que este puerto es la única puerta.
//
// Contrato: cancelar un turno que YA está cerrado (cancelled/done/no_show) es
// nil, no error: el barrido no reintenta ni ensucia el log. Un turno que no
// existe también es nil (su sena se fue en cascada). Cualquier otro error se
// devuelve al trabajo periódico, que lo loguea y reintenta en el próximo tick.
type BookingCanceller interface {
	CancelBooking(salonID, bookingID string, now time.Time) error
}

// BookingNoticeRepository resuelve lo que necesita el correo de confirmación de
// una reserva con sena pagada (D7) sin tocar BookingEmailRepository (que no
// tiene lectura): el nombre del salón, el turno y el email de la clienta.
type BookingNoticeRepository interface {
	// Notice trae el aviso del turno. ErrNotFound si el turno no existe o es de
	// otro salón. ClientEmail vacío = reserva anónima (no hay a quién avisar).
	Notice(salonID, bookingID string) (domain.BookingNotice, error)
}

// ===== F11: sena online con banco simulado (fin) =====
