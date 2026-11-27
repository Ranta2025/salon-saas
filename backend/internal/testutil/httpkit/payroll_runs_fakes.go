package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"time"
)

// Fake de las liquidaciones (F3.3) para el contrato HTTP: misma semántica que
// la migración 025 (EXCLUDE por salón, inmutabilidad de la pagada) y datos
// sembrables desde el test.

type PayrollRuns struct {
	Runs map[string]domain.PayrollRun
	now  func() time.Time
	// settings es la configuración que lee la creación dentro de la transacción
	// (zona horaria del período, F3.3).
	SettingsRepo *Settings
	// body y sales son la fuente de la vista previa dentro de la transacción.
	Body  *PayModels
	Sales *PayrollSales
	// failAfter simula una falla dentro de la transacción después de escribir.
	FailAfter func() error
}

func NewPayrollRuns(now func() time.Time) *PayrollRuns {
	return &PayrollRuns{Runs: map[string]domain.PayrollRun{}, now: now}
}

func ClonePRun(r domain.PayrollRun) domain.PayrollRun {
	out := r
	out.Lines = append([]domain.PayrollLine(nil), r.Lines...)
	return out
}

func (f *PayrollRuns) Seed(r domain.PayrollRun) { f.Runs[r.ID] = ClonePRun(r) }

func RunOverlaps(a, b domain.PayrollRun) bool {
	return a.SalonID == b.SalonID && a.From <= b.To && b.From <= a.To
}

func (f *PayrollRuns) ListRuns(salonID string) ([]domain.PayrollRunSummary, error) {
	out := []domain.PayrollRunSummary{}
	for _, r := range f.Runs {
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

func (f *PayrollRuns) FindRun(salonID, runID string) (domain.PayrollRun, error) {
	r, ok := f.Runs[runID]
	if !ok || r.SalonID != salonID {
		return domain.PayrollRun{}, domain.ErrNotFound
	}
	return ClonePRun(r), nil
}

// Settings modela la configuración leída bajo el lock de la transacción.
func (f *PayrollRuns) Settings(salonID string) (domain.SalonSettings, error) {
	if f.SettingsRepo == nil {
		return domain.SalonSettings{}, domain.ErrNotFound
	}
	return f.SettingsRepo.Settings(salonID)
}

// PreviewSource modela la vista previa leída en la MISMA transacción.
func (f *PayrollRuns) PreviewSource() ports.PayrollPreviewSource {
	return RunPreview{Body: f.Body, Sales: f.Sales}
}

type RunPreview struct {
	Body  *PayModels
	Sales *PayrollSales
}

func (p RunPreview) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	return p.Body.ListPayModels(salonID)
}

func (p RunPreview) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return p.Sales.CommissionTotals(salonID, from, to)
}

func (f *PayrollRuns) SaveRun(run domain.PayrollRun) error {
	for _, r := range f.Runs {
		if RunOverlaps(r, run) {
			return domain.ErrAlreadyExists
		}
	}
	f.Runs[run.ID] = ClonePRun(run)
	return nil
}

func (f *PayrollRuns) AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error {
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
	f.Runs[runID] = r
	return nil
}

func (f *PayrollRuns) PayRun(salonID, runID, paidBy string) error {
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
	f.Runs[runID] = r
	return nil
}

func (f *PayrollRuns) DeleteRun(salonID, runID string) error {
	r, err := f.FindRun(salonID, runID)
	if err != nil {
		return err
	}
	if r.Paid() {
		return domain.ErrAlreadyExists
	}
	delete(f.Runs, runID)
	return nil
}

func (f *PayrollRuns) InPayrollTx(salonID string, fn func(tx ports.PayrollRunTx) error) error {
	backup := map[string]domain.PayrollRun{}
	for id, r := range f.Runs {
		backup[id] = ClonePRun(r)
	}
	err := fn(f)
	if err == nil && f.FailAfter != nil {
		err = f.FailAfter()
	}
	if err != nil {
		f.Runs = backup
		return err
	}
	return nil
}

var (
	_ ports.PayrollRunRepository = (*PayrollRuns)(nil)
	_ ports.PayrollTxRunner      = (*PayrollRuns)(nil)
)
