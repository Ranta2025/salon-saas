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
