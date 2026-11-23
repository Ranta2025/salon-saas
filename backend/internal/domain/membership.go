package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Role define qué puede hacer un usuario dentro de un salón (RBAC mínimo).
type Role string

const (
	RoleOwner Role = "owner"
	RoleAdmin Role = "admin"
	// RoleProfessional es quien atiende: profesional, estilista, peluquera,
	// colorista, manicurista. El oficio va en los servicios, no en el rol:
	// el rol dice QUÉ puede hacer en el sistema, el servicio dice QUÉ sabe hacer.
	RoleProfessional Role = "professional"
	RoleReception    Role = "reception"
	RoleClient       Role = "client"
)

// Membership une un User con un Salon + su Role.
// Es la pieza que hace cumplir el multi-tenancy: sin membership no hay acceso.
//
// Attends es la marca de "esta persona atiende clientes": la necesitan el dueño
// y la administración que también trabajan en la silla (el dueño-barbero). El
// rol professional atiende siempre y recepción/cliente nunca, así que la marca
// solo cambia el resultado para owner/admin (ver Bookable).
type Membership struct {
	UserID  string
	SalonID string
	Role    Role
	Attends bool
	// Trade es el oficio visible de quien atiende ("Barbero", "Colorista"): un
	// dato de la persona, no un permiso (S-F2c-1). Vacío es válido.
	Trade string
	// EndedAt es la baja del equipo (S-F2g2-1): nil = activa. La fila no se
	// borra, así el historial y lo que F3 necesita para liquidar comisiones
	// quedan intactos y la persona se puede reactivar.
	EndedAt *time.Time
}

// Active dice si la membresía sigue vigente. Es la única pregunta que hacen
// las reglas: una baja no cuenta como profesional, dueño ni admin.
func (m Membership) Active() bool { return m.EndedAt == nil }

// RemovableBy dice si `actor` puede dar de baja (o reactivar) a alguien con el
// rol del receptor. NO es la regla del alta: dar de baja cubre más que sumar.
//
// El dueño da de baja a CUALQUIER rol, incluido otro dueño y a sí mismo (el
// guard del último owner ACTIVO va aparte y corre ANTES de esta regla, en
// app.MemberService.Remove); la administración solo a profesional, recepción o
// cliente —el mismo criterio de su invitación de F2g1: un admin no reparte
// privilegios, así que no toca a otro admin ni al dueño—. Recepción, profesional
// y cliente no dan de baja a nadie: manageActor ya los rechaza antes.
func (r Role) RemovableBy(actor Role) bool {
	switch actor {
	case RoleOwner:
		return r.Valid()
	case RoleAdmin:
		return r == RoleProfessional || r == RoleReception || r == RoleClient
	default:
		return false
	}
}

// NewMembership valida la unión usuario-salón-rol.
//
// La marca nace con el rol: un profesional atiende (es su oficio) y el resto no.
// El dueño de un salón recién creado la enciende el caso de uso (salón de una
// sola persona, S-F2a-4) y la pantalla de equipo la cambia después.
func NewMembership(userID, salonID string, role Role) (Membership, error) {
	if userID == "" || salonID == "" {
		return Membership{}, fmt.Errorf("%w: user y salón son obligatorios", ErrValidation)
	}
	switch role {
	case RoleOwner, RoleAdmin, RoleProfessional, RoleReception, RoleClient:
		return Membership{UserID: userID, SalonID: salonID, Role: role, Attends: role == RoleProfessional}, nil
	default:
		return Membership{}, fmt.Errorf("%w: rol desconocido %q", ErrValidation, role)
	}
}

// Bookable es LA regla de quién atiende clientes: rol professional, o owner /
// admin con la marca encendida. Recepción y cliente no atienden nunca. Es la
// única fuente de verdad: la agenda, la disponibilidad y la validación del
// professional_id de un turno la consultan acá.
func (m Membership) Bookable() bool { return m.Active() && bookable(m.Role, m.Attends) }

// SetAttends devuelve la membresía con la marca cambiada, o el error de
// validación (400) con el texto que ve la persona. Un profesional no puede
// apagarse (su rol es su oficio) y recepción/cliente no pueden encenderse.
func (m Membership) SetAttends(attends bool) (Membership, error) {
	switch m.Role {
	case RoleProfessional:
		// Encendida es un no-op válido: ya atiende.
		if !attends {
			return Membership{}, NewMessage(ErrValidation, "Las profesionales siempre atienden")
		}
	case RoleOwner, RoleAdmin:
		// Los únicos que la pueden cambiar.
	default:
		return Membership{}, NewMessage(ErrValidation, "Ese rol no atiende clientes")
	}
	m.Attends = attends
	return m, nil
}

// SetTrade devuelve la membresía con el oficio cambiado. Solo quien atiende
// tiene oficio: recepción y cliente nunca, y el dueño/administración solo con
// la marca encendida. El texto va recortado, sin saltos de línea y con tope de
// MaxTextTrade caracteres.
func (m Membership) SetTrade(trade string) (Membership, error) {
	if !m.Bookable() {
		return Membership{}, NewMessage(ErrValidation, "Ese rol no tiene oficio")
	}
	clean := strings.TrimSpace(trade)
	if strings.ContainsAny(clean, "\r\n") {
		return Membership{}, NewMessage(ErrValidation, "El oficio no puede tener saltos de línea")
	}
	if utf8.RuneCountInString(clean) > MaxTextTrade {
		return Membership{}, NewMessage(ErrValidation,
			fmt.Sprintf("El oficio no puede superar los %d caracteres", MaxTextTrade))
	}
	m.Trade = clean
	return m, nil
}

// bookable vive una sola vez para que Membership (quien entra) y StaffMember
// (lo que muestra la agenda) no puedan divergir.
func bookable(role Role, attends bool) bool {
	switch role {
	case RoleProfessional:
		return true
	case RoleOwner, RoleAdmin:
		return attends
	default:
		return false
	}
}

// Valid dice si el rol es uno de los conocidos. Sirve para distinguir "rol que
// no existe" (400) de "rol que no podés asignar" (403).
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleProfessional, RoleReception, RoleClient:
		return true
	default:
		return false
	}
}

// Label es el nombre del rol para MOSTRAR a una persona: "owner" o
// "professional" no dicen nada en un mensaje de la pantalla de equipo.
func (r Role) Label() string {
	switch r {
	case RoleOwner:
		return "dueño"
	case RoleAdmin:
		return "administración"
	case RoleProfessional:
		return "profesional"
	case RoleReception:
		return "recepción"
	case RoleClient:
		return "cliente"
	default:
		return "rol desconocido"
	}
}

// CanManage cataloga quién puede configurar el salón (precios, horarios, personal).
// Una membresía dada de baja no administra: el acceso por token deja de servir
// en su próximo pedido (S-F2g2-3).
func (m Membership) CanManage() bool {
	return m.Active() && (m.Role == RoleOwner || m.Role == RoleAdmin)
}
