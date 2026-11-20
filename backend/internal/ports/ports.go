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
}

type SalonRepository interface {
	Save(s domain.Salon) error
	FindBySlug(slug string) (domain.Salon, error)
}

type MembershipRepository interface {
	Save(m domain.Membership) error
	Find(userID, salonID string) (domain.Membership, error)
}

// --- Seguridad (puertos de salida hacia crypto/tokens) ---

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) bool
}

type TokenIssuer interface {
	Issue(userID string) (string, error)
	Parse(token string) (string, error) // devuelve userID
}

// --- Reservas ---

type BookingRepository interface {
	Save(b domain.Booking) error
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
