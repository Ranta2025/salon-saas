package httpapi

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fake de las liquidaciones (F3.3) para el contrato HTTP: misma semántica que
// la migración 025 (EXCLUDE por salón, inmutabilidad de la pagada) y datos
// sembrables desde el test.

type mPayrollRuns struct {
	runs map[string]domain.PayrollRun
	now  func() time.Time
	// settings es la configuración que lee la creación dentro de la transacción
	// (zona horaria del período, F3.3).
	settings *msettings
	// body y sales son la fuente de la vista previa dentro de la transacción.
	body  *mPayModels
	sales *mPayrollSales
	// failAfter simula una falla dentro de la transacción después de escribir.
	failAfter func() error
}

func newMPayrollRuns(now func() time.Time) *mPayrollRuns {
	return &mPayrollRuns{runs: map[string]domain.PayrollRun{}, now: now}
}

func clonePRun(r domain.PayrollRun) domain.PayrollRun {
	out := r
	out.Lines = append([]domain.PayrollLine(nil), r.Lines...)
	return out
}

func (f *mPayrollRuns) seed(r domain.PayrollRun) { f.runs[r.ID] = clonePRun(r) }

func runOverlaps(a, b domain.PayrollRun) bool {
	return a.SalonID == b.SalonID && a.From <= b.To && b.From <= a.To
}

func (f *mPayrollRuns) ListRuns(salonID string) ([]domain.PayrollRunSummary, error) {
	out := []domain.PayrollRunSummary{}
	for _, r := range f.runs {
		if r.SalonID != salonID {
			continue
		}
		out = append(out, domain.PayrollRunSummary{
			ID: r.ID, Kind: r.Kind, From: r.From, To: r.To, Status: r.Status,
			CreatedAt: r.CreatedAt, PaidAt: r.PaidAt,
			LinesCount: len(r.Lines), Totals: r.Totals(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From > out[j].From
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (f *mPayrollRuns) FindRun(salonID, runID string) (domain.PayrollRun, error) {
	r, ok := f.runs[runID]
	if !ok || r.SalonID != salonID {
		return domain.PayrollRun{}, domain.ErrNotFound
	}
	return clonePRun(r), nil
}

// Settings modela la configuración leída bajo el lock de la transacción.
func (f *mPayrollRuns) Settings(salonID string) (domain.SalonSettings, error) {
	if f.settings == nil {
		return domain.SalonSettings{}, domain.ErrNotFound
	}
	return f.settings.Settings(salonID)
}

// PreviewSource modela la vista previa leída en la MISMA transacción.
func (f *mPayrollRuns) PreviewSource() ports.PayrollPreviewSource {
	return mRunPreview{body: f.body, sales: f.sales}
}

type mRunPreview struct {
	body  *mPayModels
	sales *mPayrollSales
}

func (p mRunPreview) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	return p.body.ListPayModels(salonID)
}

func (p mRunPreview) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return p.sales.CommissionTotals(salonID, from, to)
}

func (f *mPayrollRuns) SaveRun(run domain.PayrollRun) error {
	for _, r := range f.runs {
		if runOverlaps(r, run) {
			return domain.ErrAlreadyExists
		}
	}
	f.runs[run.ID] = clonePRun(run)
	return nil
}

func (f *mPayrollRuns) AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error {
	r, err := f.FindRun(salonID, runID)
	if err != nil {
		return err
	}
	if r.Paid() {
		return domain.ErrAlreadyExists
	}
	found := false
	for i := range r.Lines {
		if r.Lines[i].UserID != userID {
			continue
		}
		found = true
		r.Lines[i].AdjustmentCents = adjustmentCents
		r.Lines[i].AdjustmentNote = note
		r.Lines[i].TotalCents = r.Lines[i].CommissionCents + r.Lines[i].FixedCents + adjustmentCents
		if r.Lines[i].TotalCents < 0 {
			return domain.NewMessage(domain.ErrValidation, domain.MsgPayrollNegativeTotal)
		}
	}
	if !found {
		return domain.ErrNotFound
	}
	f.runs[runID] = r
	return nil
}

func (f *mPayrollRuns) PayRun(salonID, runID, paidBy string) error {
	r, err := f.FindRun(salonID, runID)
	if err != nil {
		return err
	}
	if r.Paid() {
		return domain.ErrAlreadyExists
	}
	at := f.now()
	r.Status = domain.PayrollRunPaid
	r.PaidAt = &at
	r.PaidBy = paidBy
	f.runs[runID] = r
	return nil
}

func (f *mPayrollRuns) DeleteRun(salonID, runID string) error {
	r, err := f.FindRun(salonID, runID)
	if err != nil {
		return err
	}
	if r.Paid() {
		return domain.ErrAlreadyExists
	}
	delete(f.runs, runID)
	return nil
}

func (f *mPayrollRuns) InPayrollTx(salonID string, fn func(tx ports.PayrollRunTx) error) error {
	backup := map[string]domain.PayrollRun{}
	for id, r := range f.runs {
		backup[id] = clonePRun(r)
	}
	err := fn(f)
	if err == nil && f.failAfter != nil {
		err = f.failAfter()
	}
	if err != nil {
		f.runs = backup
		return err
	}
	return nil
}

var (
	_ ports.PayrollRunRepository = (*mPayrollRuns)(nil)
	_ ports.PayrollTxRunner      = (*mPayrollRuns)(nil)
)
