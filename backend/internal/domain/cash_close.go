package domain

import (
	"fmt"
	"strings"
	"time"
)

// CashClose es el cierre de caja de un turno: lo que el sistema dice que debería
// haber (efectivo de las ventas en la ventana) contra lo que la persona contó.
// La diferencia es el dato contable que se investiga: negativa = faltante,
// positiva = sobrante, cero = cuadró.
type CashClose struct {
	ID              string
	SalonID         string
	OpenedAt        time.Time
	ClosedAt        time.Time
	SystemCashCents int
	CountedCents    int
	DifferenceCents int
	Note            string
}

// NewCashClose valida y crea el cierre, calculando la diferencia.
func NewCashClose(salonID string, openedAt, closedAt time.Time, systemCashCents, countedCents int, note string) (CashClose, error) {
	if salonID == "" {
		return CashClose{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if closedAt.Before(openedAt) {
		return CashClose{}, fmt.Errorf("%w: el cierre no puede ser anterior a la apertura", ErrValidation)
	}
	if systemCashCents < 0 || countedCents < 0 {
		return CashClose{}, fmt.Errorf("%w: los montos de caja no pueden ser negativos", ErrValidation)
	}
	// Mismo techo que una venta: las columnas de la DB son INTEGER y una suma
	// fuera de rango rompe el cierre en vez de avisar.
	if systemCashCents > MaxCashCents || countedCents > MaxCashCents {
		return CashClose{}, fmt.Errorf("%w: los montos de caja no pueden superar %d centavos", ErrValidation, MaxCashCents)
	}
	return CashClose{
		ID:              NewID(),
		SalonID:         salonID,
		OpenedAt:        openedAt.UTC(),
		ClosedAt:        closedAt.UTC(),
		SystemCashCents: systemCashCents,
		CountedCents:    countedCents,
		DifferenceCents: countedCents - systemCashCents,
		Note:            strings.TrimSpace(note),
	}, nil
}
