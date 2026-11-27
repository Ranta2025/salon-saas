// CashCloseService: cierre de caja. Quien cierra compara lo que el sistema
// espera (efectivo de las ventas del turno) con lo que contó a mano; la
// diferencia es el número que se investiga. Solo owner/admin cierran caja.
package pos

import (
	"errors"
	"fmt"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type CashCloseService struct {
	CashCloses  ports.CashCloseRepository
	Sales       ports.SaleRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón: la ventana del turno arranca a
	// medianoche LOCAL, no UTC (un salón de CDMX cierra a las 03:00 UTC y su
	// caja abrió el día anterior local). nil = día UTC (compatibilidad).
	Settings ports.SalonSettingsRepository
	// Tx ejecuta el cierre en una transacción con lock por salón (B-09). Es
	// opcional solo para los fakes de los tests: el composition root siempre lo
	// configura, porque sin él dos cierres concurrentes cuentan la misma
	// ventana de efectivo dos veces.
	Tx ports.SalonTxRunner
	// Events y Notifier son opcionales (nil = no avisa): el cierre queda
	// igual en la DB; el realtime es una comodidad, no un requisito.
	Events   ports.EventPublisher
	Notifier kernel.Notifier
}

// cashWindow es lo único que el cierre necesita de la caja: el último cierre,
// la suma de efectivo de la ventana y guardar el nuevo. La implementan tanto la
// transacción (ports.CashCloseTx) como los repositorios sueltos.
type cashWindow interface {
	Last(salonID string) (domain.CashClose, error)
	SumCashCents(salonID string, from, to time.Time) (int, error)
	Save(c domain.CashClose) error
}

// CloseCash cierra el turno actual. La ventana arranca en el cierre anterior
// (o a las 00:00 UTC de hoy si es el primero) y termina ahora: así el mismo
// efectivo no se cuenta dos veces.
//
// Concurrency (B-09): todo el cálculo corre dentro de InSalonTx, que serializa
// los cierres del salón con pg_advisory_xact_lock. Dos cierres simultáneos se
// ordenan: el segundo ya ve al primero como "cierre anterior" y su ventana
// arranca donde terminó el otro, así el efectivo se contabiliza UNA vez.
func (s CashCloseService) CloseCash(actor domain.Membership, salonID string, countedCents int, note string) (domain.CashClose, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.CashClose{}, err
	}
	if !m.CanManage() {
		return domain.CashClose{}, domain.ErrForbidden
	}
	var closed domain.CashClose
	err = s.inWindow(salonID, func(w cashWindow) error {
		c, err := s.closeWith(w, salonID, countedCents, note)
		if err != nil {
			return err
		}
		closed = c
		return nil
	})
	if err != nil {
		return domain.CashClose{}, err
	}
	// El turno cerró: el equipo se entera sin refrescar (y queda el aviso).
	kernel.Publish(s.Events, ports.Event{
		Type:    "cash_closed",
		SalonID: salonID,
		Data: map[string]any{
			"id":                closed.ID,
			"system_cash_cents": closed.SystemCashCents,
			"counted_cents":     closed.CountedCents,
			"difference_cents":  closed.DifferenceCents,
		},
		TS: closed.ClosedAt,
	})
	kernel.Notify(s.Notifier, salonID, "", "Cierre de caja",
		fmt.Sprintf("Esperado %s · contado %s · diferencia %s",
			kernel.FormatCents(closed.SystemCashCents), kernel.FormatCents(closed.CountedCents), kernel.FormatCents(closed.DifferenceCents)),
		domain.KindCash)
	return closed, nil
}

// closeWith hace el cálculo del turno usando la ventana recibida.
func (s CashCloseService) closeWith(w cashWindow, salonID string, countedCents int, note string) (domain.CashClose, error) {
	now := time.Now().UTC()
	openedAt, err := s.openOfShift(salonID, now)
	if err != nil {
		return domain.CashClose{}, err
	}
	last, err := w.Last(salonID)
	switch {
	case err == nil:
		openedAt = last.ClosedAt
	case !errors.Is(err, domain.ErrNotFound):
		return domain.CashClose{}, err
	}
	// Solo efectivo: tarjeta y transferencia no están en el cajón.
	systemCash, err := w.SumCashCents(salonID, openedAt, now)
	if err != nil {
		return domain.CashClose{}, err
	}
	c, err := domain.NewCashClose(salonID, openedAt, now, systemCash, countedCents, note)
	if err != nil {
		return domain.CashClose{}, err
	}
	if err := w.Save(c); err != nil {
		return domain.CashClose{}, err
	}
	return c, nil
}

// openOfShift es la apertura del turno sin cierre previo: medianoche local del
// salón. Si la configuración no se puede leer, el cierre falla (nunca se cuenta
// plata con una ventana adivinada).
func (s CashCloseService) openOfShift(salonID string, now time.Time) (time.Time, error) {
	settings, err := kernel.LoadSalonSettings(s.Settings, salonID)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := settings.Location()
	if err != nil {
		return time.Time{}, err
	}
	return kernel.StartOfSalonDay(now, loc), nil
}

// inWindow corre fn en la transacción con lock si está configurada; si no, con
// los repositorios sueltos (tests sin Postgres).
func (s CashCloseService) inWindow(salonID string, fn func(cashWindow) error) error {
	if s.Tx != nil {
		return s.Tx.InSalonTx(salonID, func(tx ports.CashCloseTx) error { return fn(tx) })
	}
	return fn(looseWindow{cashCloses: s.CashCloses, sales: s.Sales})
}

// looseWindow adapta los repositorios sueltos a cashWindow.
type looseWindow struct {
	cashCloses ports.CashCloseRepository
	sales      ports.SaleRepository
}

func (w looseWindow) Last(salonID string) (domain.CashClose, error) {
	return w.cashCloses.Last(salonID)
}
func (w looseWindow) SumCashCents(salonID string, from, to time.Time) (int, error) {
	return w.sales.SumCents(salonID, from, to, domain.MethodCash)
}
func (w looseWindow) Save(c domain.CashClose) error { return w.cashCloses.Save(c) }

// ListCloses trae los cierres del salón, del más reciente al más viejo.
func (s CashCloseService) ListCloses(actor domain.Membership, salonID string) ([]domain.CashClose, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !kernel.PuedeVerPlata(m) {
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
