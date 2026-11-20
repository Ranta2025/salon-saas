package domain

// StaffMember es una persona que atiende en el salón, con la forma mínima que
// necesita el frontend para armar el select de profesional: id, nombre y rol.
// No es una entidad persistida (no tiene tabla propia): es una proyección de
// memberships + users.
type StaffMember struct {
	ID   string
	Name string
	Role Role
}

// IsStaff dice si el rol cuenta como "personal que atiende". Recepción y
// cliente NO atienden: no aparecen en el selector de profesional de la agenda.
// La lista vive acá (un solo lugar) porque la usan el endpoint /staff y la
// validación del professional_id de una reserva o venta.
func (r Role) IsStaff() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleProfessional:
		return true
	default:
		return false
	}
}
