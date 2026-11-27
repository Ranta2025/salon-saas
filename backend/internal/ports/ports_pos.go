package ports

import (
	"salon-saas/internal/domain"
	"time"
)

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
