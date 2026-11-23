// PayrollRunService: el ciclo de vida de una liquidación (F3.3, D6/D8 del
// contrato F3). Crear CONGELA lo que la vista previa calculó (el mismo motor de
// F3.2, no una fórmula nueva); ajustar, pagar y borrar solo se pueden mientras
// la corrida está abierta. La transacción con advisory lock y la EXCLUDE de la
// DB son las garantías duras; este caso de uso pone los permisos y las frases.
package app

import (
	"errors"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PayrollRunService orquesta las liquidaciones. No deriva períodos ni calcula
// comisiones: eso lo hace PayrollService.Preview, que es el ÚNICO motor de la
// nómina (una sola fórmula, un solo redondeo).
type PayrollRunService struct {
	Runs ports.PayrollRunRepository
	// Tx ejecuta las escrituras en UNA transacción con advisory lock por salón
	// ("payroll-write:<salon>"). Sin él no hay creación atómica: es obligatorio.
	Tx          ports.PayrollTxRunner
	PayModels   ports.MemberPayRepository
	Sales       ports.PayrollSalesRepository
	Memberships ports.MembershipRepository
	Settings    ports.SalonSettingsRepository
	// Now es el reloj de «hoy» (período cerrado y paid_at). nil = time.Now.
	Now func() time.Time
}

// Create congela el período ya terminado con las líneas de la vista previa.
//
// Reglas del contrato: solo owner/admin, el período tiene que haber terminado
// (to < hoy local del salón), tiene que haber algo para liquidar y los días no
// pueden estar cubiertos por otra liquidación (EXCLUDE -> 409, nunca 500).
func (s PayrollRunService) Create(actor domain.Membership, salonID, kind, date string) (domain.PayrollRun, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.PayrollRun{}, err
	}
	// MISMO motor que la vista previa: el período y los montos no se re-calculan
	// acá (D2/D5 viven en domain y en PayrollService).
	preview, err := s.previewService().Preview(actor, salonID, kind, date)
	if err != nil {
		return domain.PayrollRun{}, err
	}
	if !preview.Closed {
		return domain.PayrollRun{}, domain.NewMessage(domain.ErrValidation, domain.MsgPayrollPeriodNotClosed)
	}
	if len(preview.Lines) == 0 {
		return domain.PayrollRun{}, domain.NewMessage(domain.ErrValidation, domain.MsgPayrollNothingToPay)
	}
	if s.Tx == nil {
		return domain.PayrollRun{}, errors.New("payroll: falta la transacción de liquidaciones")
	}

	lines := make([]domain.PayrollLine, 0, len(preview.Lines))
	for _, l := range preview.Lines {
		lines = append(lines, domain.PayrollLine{
			UserID: l.UserID, Name: l.Name, Active: l.Active,
			SalesCount: l.SalesCount, SalesTotalCents: l.SalesTotalCents,
			CommissionCents: l.CommissionCents, FixedCents: l.FixedCents,
			TotalCents: l.TotalCents, Estimated: l.Estimated,
		})
	}
	run, err := domain.NewPayrollRun(domain.NewID(), salonID, preview.Period, actor.UserID, s.now(), lines)
	if err != nil {
		return domain.PayrollRun{}, err
	}

	var out domain.PayrollRun
	err = s.Tx.InPayrollTx(salonID, func(tx ports.PayrollRunTx) error {
		// Cabecera + TODAS las líneas en la misma transacción: si una línea no
		// entra, no queda una corrida a medias.
		if err := tx.SaveRun(run); err != nil {
			return err
		}
		got, err := tx.FindRun(salonID, run.ID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	if err != nil {
		return domain.PayrollRun{}, payrollOverlapError(err)
	}
	return out, nil
}

// List devuelve las liquidaciones del salón, la más nueva primero.
func (s PayrollRunService) List(actor domain.Membership, salonID string) ([]domain.PayrollRunSummary, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return nil, err
	}
	list, err := s.Runs.ListRuns(salonID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.PayrollRunSummary{} // el frontend recibe [] y no null
	}
	return list, nil
}

// Get devuelve una liquidación con sus líneas. Inexistente o de otro salón: 404.
func (s PayrollRunService) Get(actor domain.Membership, salonID, runID string) (domain.PayrollRun, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.PayrollRun{}, err
	}
	return s.Runs.FindRun(salonID, runID)
}

// AdjustLine cambia el ajuste con signo de una línea de una corrida ABIERTA.
//
// Orden de las reglas (lección de F3.2): (1) permisos, (2) la corrida existe en
// ESTE salón (404), (3) está abierta (409, la pagada es inmutable), (4) la
// persona está en la corrida (404) y (5) el ajuste y la nota son válidos (400).
// La escritura repite la condición status='open' en la DB: aunque una pagada se
// cuele entre la lectura y el UPDATE, el query no la toca.
func (s PayrollRunService) AdjustLine(actor domain.Membership, salonID, runID, userID string, adjustmentCents int, note string) (domain.PayrollRun, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.PayrollRun{}, err
	}
	run, err := s.Runs.FindRun(salonID, runID)
	if err != nil {
		return domain.PayrollRun{}, err
	}
	if run.Paid() {
		return domain.PayrollRun{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPayrollRunPaid)
	}
	line, ok := run.FindLine(userID)
	if !ok {
		return domain.PayrollRun{}, domain.ErrNotFound
	}
	if _, err := line.WithAdjustment(adjustmentCents, note); err != nil {
		return domain.PayrollRun{}, err
	}
	if s.Tx == nil {
		return domain.PayrollRun{}, errors.New("payroll: falta la transacción de liquidaciones")
	}

	var out domain.PayrollRun
	err = s.Tx.InPayrollTx(salonID, func(tx ports.PayrollRunTx) error {
		if err := tx.AdjustLine(salonID, runID, userID, adjustmentCents, note); err != nil {
			return err
		}
		got, err := tx.FindRun(salonID, runID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	if err != nil {
		return domain.PayrollRun{}, payrollPaidError(err)
	}
	return out, nil
}

// Pay marca la corrida como pagada. Desde ahí es INMUTABLE: ni líneas, ni
// borrado, ni un segundo pago (409 con la misma frase).
func (s PayrollRunService) Pay(actor domain.Membership, salonID, runID string) (domain.PayrollRun, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.PayrollRun{}, err
	}
	if s.Tx == nil {
		return domain.PayrollRun{}, errors.New("payroll: falta la transacción de liquidaciones")
	}

	var out domain.PayrollRun
	err := s.Tx.InPayrollTx(salonID, func(tx ports.PayrollRunTx) error {
		// UPDATE condicional status='open': dos pagos simultáneos -> uno gana y
		// el otro vuelve ErrAlreadyExists. La corrida y sus líneas se leen en la
		// misma transacción (el pago y la respuesta son un solo estado).
		if err := tx.PayRun(salonID, runID, actor.UserID); err != nil {
			return err
		}
		got, err := tx.FindRun(salonID, runID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	if err != nil {
		return domain.PayrollRun{}, payrollPaidError(err)
	}
	return out, nil
}

// Delete borra una corrida ABIERTA (y sus líneas en cascada), liberando sus
// días para poder liquidarlos de nuevo. Una pagada es 409.
func (s PayrollRunService) Delete(actor domain.Membership, salonID, runID string) error {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return err
	}
	if s.Tx == nil {
		return errors.New("payroll: falta la transacción de liquidaciones")
	}
	err := s.Tx.InPayrollTx(salonID, func(tx ports.PayrollRunTx) error {
		return tx.DeleteRun(salonID, runID)
	})
	return payrollPaidError(err)
}

// ExportCSV arma el archivo de una liquidación (D8). Se puede exportar una
// abierta o una pagada: los montos son la foto congelada, nunca un recálculo.
func (s PayrollRunService) ExportCSV(actor domain.Membership, salonID, runID string) (string, []byte, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return "", nil, err
	}
	run, err := s.Runs.FindRun(salonID, runID)
	if err != nil {
		return "", nil, err
	}
	return domain.PayrollCSVFilename(run.Kind, run.From, run.To), domain.PayrollCSV(run), nil
}

// previewService es la MISMA configuración del motor de F3.2: el cálculo y la
// derivación del período no se duplican en F3.3.
func (s PayrollRunService) previewService() PayrollService {
	return PayrollService{
		Pay: s.PayModels, Sales: s.Sales, Memberships: s.Memberships,
		Settings: s.Settings, Now: s.Now,
	}
}

// payrollOverlapError traduce el choque de días (EXCLUDE 23P01 -> ErrAlreadyExists)
// a la frase del contrato. Cualquier otro error se propaga sin disfrazarse.
func payrollOverlapError(err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPayrollRunOverlaps)
	}
	return err
}

// payrollPaidError traduce la corrida pagada (UPDATE/DELETE condicional sin
// filas) a la frase del contrato. El ErrNotFound de una corrida que no existe
// sigue siendo 404.
func payrollPaidError(err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPayrollRunPaid)
	}
	return err
}

// now es el reloj del caso de uso (los tests lo fijan).
func (s PayrollRunService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
