package domain

// Member es una persona del equipo del salón con lo que necesita la pantalla
// de equipo: id, nombre, email, rol y si atiende clientes (F2a). Es la
// proyección de memberships + users (no tiene tabla propia).
type Member struct {
	ID      string
	Name    string
	Email   string
	Role    Role
	Attends bool
	// Trade es el oficio (solo en quien atiende) y ServiceIDs es la lista de
	// servicios que hace. La pantalla de equipo es la única que ve los ids: la
	// agenda (staff) comparte solo el oficio (S-F2c-3).
	Trade      string
	ServiceIDs []string
}

// Bookable dice si esta persona atiende clientes. La regla es la misma que
// Membership.Bookable: el oficio y los servicios solo existen en quien atiende.
func (m Member) Bookable() bool { return bookable(m.Role, m.Attends) }

// AddableBy dice si `actor` puede sumar a alguien con el rol `target`.
//
// El owner agrega admins (y profesionales o recepción). Un admin agrega
// profesionales y recepción: dar de alta otro admin es escalar privilegios.
// El rol owner no se reparte por la API: el owner nace con el salón.
func (r Role) AddableBy(actor Role) bool {
	switch actor {
	case RoleOwner:
		return r == RoleAdmin || r == RoleProfessional || r == RoleReception
	case RoleAdmin:
		return r == RoleProfessional || r == RoleReception
	default:
		return false
	}
}
