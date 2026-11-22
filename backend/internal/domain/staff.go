package domain

// StaffMember es una persona que atiende en el salón, con la forma mínima que
// necesita el frontend para armar el select de profesional: id, nombre, rol y
// la marca de quién atiende clientes. No es una entidad persistida (no tiene
// tabla propia): es una proyección de memberships + users.
type StaffMember struct {
	ID      string
	Name    string
	Role    Role
	Attends bool
	// Trade es el oficio visible (barbero, estilista...). La agenda lo muestra
	// junto al nombre y no expone qué servicios hace cada quien (S-F2c-3).
	Trade string
}

// Bookable dice si esta persona atiende clientes. La regla NO vive acá: es
// Membership.Bookable (profesional siempre; owner/admin solo con attends; el
// resto nunca). Antes el rol solo alcanzaba (Role.IsStaff), y por eso el dueño
// y la administración inflaban la disponibilidad aunque no atendieran (F2a).
func (m StaffMember) Bookable() bool { return bookable(m.Role, m.Attends) }
