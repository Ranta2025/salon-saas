// CashCloseService: cierre de caja. Quien cierra compara lo que el sistema
// espera (efectivo de las ventas del turno) con lo que contó a mano; la
// diferencia es el número que se investiga. Solo owner/admin cierran caja.
package app

import (
	"errors"
	"fmt"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type CashCloseService struct {
	CashCloses  ports.CashCloseRepository
	Sales       ports.SaleRepository
	Memberships ports.MembershipRepository
	// Events y Notifier son opcionales (nil = no avisa): el cierre queda
	// igual en la DB; el realtime es una comodidad, no un requisito.
	Events   ports.EventPublisher
	Notifier Notifier
}

// CloseCash cierra el turno actual. La ventana arranca en el cierre anterior
// (o a las 00:00 UTC de hoy si es el primero) y termina ahora: así el mismo
// efectivo no se cuenta dos veces.
func (s CashCloseService) CloseCash(actor domain.Membership, salonID string, countedCents int, note string) (domain.CashClose, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.CashClose{}, err
	}
	if !m.CanManage() {
		return domain.CashClose{}, domain.ErrForbidden
	}
	now := time.Now().UTC()
	openedAt := startOfUTCDay(now)
	last, err := s.CashCloses.Last(salonID)
	switch {
	case err == nil:
		openedAt = last.ClosedAt
	case !errors.Is(err, domain.ErrNotFound):
		return domain.CashClose{}, err
	}
	// Solo efectivo: tarjeta y transferencia no están en el cajón.
	systemCash, err := s.Sales.SumCents(salonID, openedAt, now, domain.MethodCash)
	if err != nil {
		return domain.CashClose{}, err
	}
	c, err := domain.NewCashClose(salonID, openedAt, now, systemCash, countedCents, note)
	if err != nil {
		return domain.CashClose{}, err
	}
	if err := s.CashCloses.Save(c); err != nil {
		return domain.CashClose{}, err
	}
	// El turno cerró: el equipo se entera sin refrescar (y queda el aviso).
	publish(s.Events, ports.Event{
		Type:    "cash_closed",
		SalonID: salonID,
		Data: map[string]any{
			"id":                c.ID,
			"system_cash_cents": c.SystemCashCents,
			"counted_cents":     c.CountedCents,
			"difference_cents":  c.DifferenceCents,
		},
		TS: c.ClosedAt,
	})
	notify(s.Notifier, salonID, "", "Cierre de caja",
		fmt.Sprintf("Esperado %s · contado %s · diferencia %s",
			FormatCents(c.SystemCashCents), FormatCents(c.CountedCents), FormatCents(c.DifferenceCents)),
		domain.KindCash)
	return c, nil
}

// ListCloses trae los cierres del salón, del más reciente al más viejo.
func (s CashCloseService) ListCloses(actor domain.Membership, salonID string) ([]domain.CashClose, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !puedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	return s.CashCloses.List(salonID)
}

func (s CashCloseService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
