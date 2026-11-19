package domain

import "fmt"

// Role define qué puede hacer un usuario dentro de un salón (RBAC mínimo).
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleBarber    Role = "barber"
	RoleReception Role = "reception"
	RoleClient    Role = "client"
)

// Membership une un User con un Salon + su Role.
// Es la pieza que hace cumplir el multi-tenancy: sin membership no hay acceso.
type Membership struct {
	UserID  string
	SalonID string
	Role    Role
}

// NewMembership valida la unión usuario-salón-rol.
func NewMembership(userID, salonID string, role Role) (Membership, error) {
	if userID == "" || salonID == "" {
		return Membership{}, fmt.Errorf("%w: user y salón son obligatorios", ErrValidation)
	}
	switch role {
	case RoleOwner, RoleAdmin, RoleBarber, RoleReception, RoleClient:
		return Membership{UserID: userID, SalonID: salonID, Role: role}, nil
	default:
		return Membership{}, fmt.Errorf("%w: rol desconocido %q", ErrValidation, role)
	}
}

// CanManage cataloga quién puede configurar el salón (precios, horarios, personal).
func (m Membership) CanManage() bool {
	return m.Role == RoleOwner || m.Role == RoleAdmin
}
