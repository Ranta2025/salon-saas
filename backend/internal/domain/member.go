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
}

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
