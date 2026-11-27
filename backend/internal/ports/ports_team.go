package ports

import (
	"salon-saas/internal/domain"
	"time"
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
