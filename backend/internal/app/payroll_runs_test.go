package app

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Liquidaciones de nómina (F3.3): ciclo de vida sobre el motor de la vista
// previa, con fakes en memoria que respetan las reglas duras de la DB (EXCLUDE
// por salón, inmutabilidad de la pagada) para probar el caso de uso.

// fakePayrollRuns es a la vez ports.PayrollRunRepository y ports.PayrollRunTx.
type fakePayrollRuns struct {
	runs  map[string]domain.PayrollRun
	now   func() time.Time
	order []string
	// settings es la configuración del salón que lee la creación DENTRO de la
	// transacción (zona horaria del período, F3.3).
	settings *fakeSettings
	// pay y sales son la fuente de la vista previa dentro de la transacción.
	pay   *fakePayModels
	sales *fakePayrollSales
	// failAfter simula una falla DESPUÉS de las escrituras de fn, adentro de la
	// transacción: el fake restaura el estado, como el ROLLBACK de Postgres.
	failAfter func() error
}

func newFakePayrollRuns(now func() time.Time) *fakePayrollRuns {
	return &fakePayrollRuns{runs: map[string]domain.PayrollRun{}, now: now}
}

func cloneRun(r domain.PayrollRun) domain.PayrollRun {
	out := r
	out.Lines = append([]domain.PayrollLine(nil), r.Lines...)
	return out
}

// overlaps aplica la MISMA regla que la EXCLUDE de la migración 025: rangos
// inclusivos de días dentro del mismo salón.
func overlaps(a, b domain.PayrollRun) bool {
	return a.SalonID == b.SalonID && a.From <= b.To && b.From <= a.To
}

func (f *fakePayrollRuns) ListRuns(salonID string) ([]domain.PayrollRunSummary, error) {
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

func (f *fakePayrollRuns) FindRun(salonID, runID string) (domain.PayrollRun, error) {
	r, ok := f.runs[runID]
	if !ok || r.SalonID != salonID {
		return domain.PayrollRun{}, domain.ErrNotFound
	}
	return cloneRun(r), nil
}

// Settings modela la lectura de la configuración bajo el lock de la transacción.
func (f *fakePayrollRuns) Settings(salonID string) (domain.SalonSettings, error) {
	if f.settings == nil {
		return domain.SalonSettings{}, domain.ErrNotFound
	}
	return f.settings.Settings(salonID)
}

// PreviewSource modela las lecturas de la vista previa en la MISMA transacción.
func (f *fakePayrollRuns) PreviewSource() ports.PayrollPreviewSource {
	return fakeRunPreview{pay: f.pay, sales: f.sales}
}

type fakeRunPreview struct {
	pay   *fakePayModels
	sales *fakePayrollSales
}

func (s fakeRunPreview) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	return s.pay.ListPayModels(salonID)
}

func (s fakeRunPreview) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return s.sales.CommissionTotals(salonID, from, to)
}

func (f *fakePayrollRuns) SaveRun(run domain.PayrollRun) error {
	for _, r := range f.runs {
		if overlaps(r, run) {
			return domain.ErrAlreadyExists
		}
	}
	f.runs[run.ID] = cloneRun(run)
	f.order = append(f.order, run.ID)
	return nil
}

func (f *fakePayrollRuns) AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error {
	r, err := f.FindRun(salonID, runID)
	if err != nil {
		return err
	}
	if r.Paid() {
		return domain.ErrAlreadyExists
	}
	line, ok := r.FindLine(userID)
	if !ok {
		return domain.ErrNotFound
	}
	// A diferencia de la API, el repositorio NO valida la nota: la DB acepta
	// cualquier texto de hasta 200 caracteres. El total lo calcula como la query.
	adj := adjustmentCents
	total := line.CommissionCents + line.FixedCents + adj
	if total < 0 {
		return domain.NewMessage(domain.ErrValidation, domain.MsgPayrollNegativeTotal)
	}
	line.AdjustmentCents = adj
	line.AdjustmentNote = note
	line.TotalCents = total
	for i := range r.Lines {
		if r.Lines[i].UserID == userID {
			r.Lines[i] = line
		}
	}
	f.runs[runID] = r
	return nil
}

func (f *fakePayrollRuns) PayRun(salonID, runID, paidBy string) error {
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

func (f *fakePayrollRuns) DeleteRun(salonID, runID string) error {
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

// InPayrollTx modela la transacción con rollback: si fn o failAfter fallan, el
// estado vuelve a como estaba.
func (f *fakePayrollRuns) InPayrollTx(salonID string, fn func(tx ports.PayrollRunTx) error) error {
	backup := map[string]domain.PayrollRun{}
	for id, r := range f.runs {
		backup[id] = cloneRun(r)
	}
	order := append([]string(nil), f.order...)
	err := fn(f)
	if err == nil && f.failAfter != nil {
		err = f.failAfter()
	}
	if err != nil {
		f.runs = backup
		f.order = order
		return err
	}
	return nil
}

var (
	_ ports.PayrollRunRepository = (*fakePayrollRuns)(nil)
	_ ports.PayrollTxRunner      = (*fakePayrollRuns)(nil)
)

type runEnv struct {
	*payrollEnv
	runs *fakePayrollRuns
	svc  PayrollRunService
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	base := newPayrollEnv(t)
	// Un dueño de OTRO salón para el aislamiento por tenant.
	base.mems.ByKey["u-other-owner|s-other"] = domain.Membership{
		UserID: "u-other-owner", SalonID: "s-other", Role: domain.RoleOwner,
	}
	runs := newFakePayrollRuns(func() time.Time { return base.when })
	runs.settings = base.settings
	runs.pay, runs.sales = base.pay, base.sales
	env := &runEnv{payrollEnv: base, runs: runs}
	env.svc = PayrollRunService{
		Runs: runs, Tx: runs,
		PayModels: base.pay, Sales: base.sales, Memberships: base.mems, Settings: base.settings,
		Now: func() time.Time { return base.when },
	}
	return env
}

// febSeed deja a Ana con una venta en febrero (comisión 499) para poder crear
// una liquidación del mes cerrado.
func (e *runEnv) febSeed() {
	e.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 3, SalesTotalCents: 3499,
		CommissionBase: 49990, Estimated: true,
	})
}

func (e *runEnv) create(t *testing.T, email string, kind, date string) domain.PayrollRun {
	t.Helper()
	run, err := e.svc.Create(e.actor(t, email), "s-pay", kind, date)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return run
}

// Crear congela la vista previa: después de mover ventas y modelo de pago, las
// líneas guardadas leídas de nuevo son las mismas.
func TestPayrollRunCreate_congela_la_vista_previa(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()

	run := e.create(t, "u-owner", "month", "2026-02-10")
	if run.Status != domain.PayrollRunOpen || run.Kind != domain.PayMonth ||
		run.From != "2026-02-01" || run.To != "2026-02-28" {
		t.Fatalf("corrida creada: %+v", run)
	}
	if run.CreatedBy != "u-owner" || run.PaidAt != nil || run.PaidBy != "" {
		t.Fatalf("cabecera: %+v", run)
	}
	if len(run.Lines) != 1 {
		t.Fatalf("líneas = %d, esperaba 1: %+v", len(run.Lines), run.Lines)
	}
	line := run.Lines[0]
	if line.UserID != "u-prof" || line.Name != "Ana" || line.SalesCount != 3 ||
		line.SalesTotalCents != 3499 || line.CommissionCents != 499 ||
		line.FixedCents != 0 || line.TotalCents != 499 || !line.Estimated {
		t.Fatalf("línea congelada: %+v", line)
	}
	// El período se derivó con el motor de la vista previa en la zona del salón.
	if len(e.sales.windows) == 0 {
		t.Fatal("la creación tiene que usar el motor de la vista previa (ventana de ventas)")
	}
	w := e.sales.windows[0]
	wantFrom := time.Date(2026, 2, 1, 6, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	if !w.from.Equal(wantFrom) || !w.to.Equal(wantTo) {
		t.Fatalf("ventana = [%s, %s)", w.from, w.to)
	}

	// Se mueve todo lo que cambió después: más ventas, otro total y un modelo
	// de pago con fijo.
	e.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 99, SalesTotalCents: 999999,
		CommissionBase: 99999999,
	})
	model, _ := domain.NewMemberPay("s-pay", "u-prof", 100000, "month", true)
	if err := e.pay.SavePayModel(model); err != nil {
		t.Fatalf("modelo posterior: %v", err)
	}

	got, err := e.svc.Get(e.actor(t, "u-owner"), "s-pay", run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0] != line {
		t.Fatalf("la liquidación cambió sola: %+v", got.Lines)
	}
	if got.Totals().CommissionCents != 499 || got.Totals().TotalCents != 499 {
		t.Fatalf("totales congelados: %+v", got.Totals())
	}
}

// Crear exige período terminado, al menos una línea y días libres.
func TestPayrollRunCreate_periodo_abierto_vacio_y_solape(t *testing.T) {
	e := newRunEnv(t)
	owner := e.actor(t, "u-owner")

	// Marzo 2026 sigue en curso con «hoy» 20/3: no se liquida.
	e.febSeed()
	_, err := e.svc.Create(owner, "s-pay", "month", "2026-03-10")
	if err == nil || err.Error() != domain.MsgPayrollPeriodNotClosed {
		t.Fatalf("período en curso: %v", err)
	}
	// Enero no tiene ventas ni fijos: nada para liquidar.
	e.sales.totals["s-pay"] = nil
	_, err = e.svc.Create(owner, "s-pay", "month", "2026-01-10")
	if err == nil || err.Error() != domain.MsgPayrollNothingToPay {
		t.Fatalf("período vacío: %v", err)
	}

	e.febSeed()
	first := e.create(t, "u-owner", "month", "2026-02-10")
	// El mismo período, otra vez.
	if _, err := e.svc.Create(owner, "s-pay", "month", "2026-02-20"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("mismo período: %v", err)
	}
	// Quincena 1-15 de febrero pisa días del mes.
	if _, err := e.svc.Create(owner, "s-pay", "fortnight", "2026-02-03"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("quincena vs mes: %v", err)
	}
	// Semana del 23 de febrero al 1 de marzo también.
	if _, err := e.svc.Create(owner, "s-pay", "week", "2026-02-23"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("semana vs mes: %v", err)
	}
	// Períodos adyacentes: la quincena 1-15 de marzo empieza donde terminó febrero.
	if _, err := e.svc.Create(owner, "s-pay", "fortnight", "2026-03-03"); err != nil {
		t.Fatalf("período adyacente: %v", err)
	}
	// Una SEMANA adentro de esa quincena también se rechaza.
	if _, err := e.svc.Create(owner, "s-pay", "week", "2026-03-10"); err == nil || err.Error() != domain.MsgPayrollRunOverlaps {
		t.Fatalf("semana vs quincena: %v", err)
	}
	// Borrar la abierta libera los días.
	if err := e.svc.Delete(owner, "s-pay", first.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if _, err := e.svc.Create(owner, "s-pay", "month", "2026-02-05"); err != nil {
		t.Fatalf("los días liberados se pueden volver a liquidar: %v", err)
	}
}

// Ajuste: entero con signo, nota obligatoria si no es 0, total no negativo,
// persona de la corrida y solo abierta.
func TestPayrollRunAdjust_reglas(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	owner := e.actor(t, "u-owner")
	run := e.create(t, "u-owner", "month", "2026-02-10")

	got, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 500, "venta mal cargada")
	if err != nil {
		t.Fatalf("ajuste: %v", err)
	}
	line, _ := got.FindLine("u-prof")
	if line.AdjustmentCents != 500 || line.AdjustmentNote != "venta mal cargada" ||
		line.TotalCents != 999 || got.Totals().AdjustmentCents != 500 ||
		got.Totals().TotalCents != 999 {
		t.Fatalf("línea ajustada: %+v / totales %+v", line, got.Totals())
	}
	// Volver a 0 sin nota es válido y borra el ajuste.
	got, err = e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 0, "")
	if err != nil {
		t.Fatalf("volver a 0: %v", err)
	}
	if line, _ := got.FindLine("u-prof"); line.AdjustmentCents != 0 || line.AdjustmentNote != "" || line.TotalCents != 499 {
		t.Fatalf("ajuste en cero: %+v", line)
	}

	// Nota obligatoria si el ajuste no es 0.
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 100, ""); err == nil || err.Error() != domain.MsgPayrollNoteRequired {
		t.Fatalf("sin nota: %v", err)
	}
	// Total nunca negativo (comisión 499).
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", -500, "de más"); err == nil || err.Error() != domain.MsgPayrollNegativeTotal {
		t.Fatalf("total negativo: %v", err)
	}
	// Justo al cero sí.
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", -499, "todo descontado"); err != nil {
		t.Fatalf("hasta cero: %v", err)
	}
	// Nota de 201 y control chars.
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 1, strings.Repeat("ñ", 201)); err == nil {
		t.Fatal("nota de 201 debía rechazarse")
	}
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 1, "a\nb"); err == nil {
		t.Fatal("nota con salto de línea debía rechazarse")
	}
	// Magnitud fuera de ±MaxPayCents.
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", domain.MaxPayCents+1, "tope"); err == nil {
		t.Fatal("ajuste fuera del máximo debía rechazarse")
	}
	// Persona que no está en la corrida: 404.
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-recep", 100, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("persona fuera de la corrida: %v", err)
	}
	// Corrida inexistente: 404.
	if _, err := e.svc.AdjustLine(owner, "s-pay", "nope", "u-prof", 100, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("corrida inexistente: %v", err)
	}

	// Pagar congela: cualquier ajuste posterior es 409 con la frase exacta.
	paid, err := e.svc.Pay(owner, "s-pay", run.ID)
	if err != nil {
		t.Fatalf("pagar: %v", err)
	}
	if !paid.Paid() || paid.PaidAt == nil || paid.PaidBy != "u-owner" {
		t.Fatalf("corrida pagada: %+v", paid)
	}
	if _, err := e.svc.AdjustLine(owner, "s-pay", run.ID, "u-prof", 100, "tarde"); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("ajuste de pagada: %v", err)
	}
}

// Pagar es idempotente en el sentido del contrato: el segundo intento es 409.
// Borrar solo se puede si está abierta.
func TestPayrollRunPay_y_Delete_solo_abierta(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	owner := e.actor(t, "u-owner")

	open := e.create(t, "u-owner", "month", "2026-01-10")
	if err := e.svc.Delete(owner, "s-pay", open.ID); err != nil {
		t.Fatalf("borrar abierta: %v", err)
	}
	if _, err := e.svc.Get(owner, "s-pay", open.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la borrada no debe existir: %v", err)
	}
	if err := e.svc.Delete(owner, "s-pay", open.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar dos veces: %v", err)
	}

	run := e.create(t, "u-owner", "month", "2026-02-10")
	paid, err := e.svc.Pay(owner, "s-pay", run.ID)
	if err != nil {
		t.Fatalf("pagar: %v", err)
	}
	if _, err := e.svc.Pay(owner, "s-pay", run.ID); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("segundo pago: %v", err)
	}
	if err := e.svc.Delete(owner, "s-pay", run.ID); err == nil || err.Error() != domain.MsgPayrollRunPaid {
		t.Fatalf("borrar pagada: %v", err)
	}
	// Sigue estando y pagada.
	got, err := e.svc.Get(owner, "s-pay", run.ID)
	if err != nil || got.Status != domain.PayrollRunPaid || got.PaidAt == nil {
		t.Fatalf("la pagada sigue: %+v, %v (paid %v)", got, err, paid.PaidAt)
	}
}

// Atomicidad del pago: si algo falla DESPUÉS del UPDATE de la corrida dentro de
// la transacción, no cambia nada (ni estado ni líneas).
func TestPayrollRunPay_atomico_si_falla_la_transaccion(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	owner := e.actor(t, "u-owner")
	run := e.create(t, "u-owner", "month", "2026-02-10")
	e.runs.failAfter = func() error { return errors.New("falla simulada después del UPDATE") }

	if _, err := e.svc.Pay(owner, "s-pay", run.ID); err == nil {
		t.Fatal("el pago debía fallar")
	}
	got, err := e.svc.Get(owner, "s-pay", run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Paid() || got.PaidAt != nil || got.PaidBy != "" {
		t.Fatalf("el pago fallido dejó rastro: %+v", got)
	}
	if len(got.Lines) != 1 || got.Lines[0].TotalCents != 499 {
		t.Fatalf("las líneas cambiaron con el pago fallido: %+v", got.Lines)
	}
}

// Listado y detalle: orden, totales, cantidad de líneas y aislamiento por salón.
func TestPayrollRunList_y_Get_tenant(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	owner := e.actor(t, "u-owner")

	// Sin liquidaciones: lista vacía, no null.
	list, err := e.svc.List(owner, "s-pay")
	if err != nil {
		t.Fatalf("listar vacío: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("lista vacía = %#v", list)
	}

	feb := e.create(t, "u-owner", "month", "2026-02-10")
	jan := e.create(t, "u-owner", "month", "2026-01-10")
	list, err = e.svc.List(owner, "s-pay")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 2 || list[0].ID != feb.ID || list[1].ID != jan.ID {
		t.Fatalf("orden (from desc): %+v", list)
	}
	if list[0].LinesCount != 1 || list[0].Totals.CommissionCents != 499 ||
		list[0].Totals.TotalCents != 499 || list[0].Status != domain.PayrollRunOpen {
		t.Fatalf("resumen: %+v", list[0])
	}

	// Un id de la corrida, pero pedido desde OTRO salón: no existe (404).
	if _, err := e.svc.Get(e.mems.ByKey["u-other-owner|s-other"], "s-other", feb.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("corrida de otro salón: %v", err)
	}
}

// D7: solo owner/admin; el resto 403 (sin distinguir recepción, profesional o
// baja), y un actor de otro salón tampoco entra.
func TestPayrollRun_permisos(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	run := e.create(t, "u-owner", "month", "2026-02-10")

	calls := map[string]func(actor domain.Membership) error{
		"list":   func(a domain.Membership) error { _, err := e.svc.List(a, "s-pay"); return err },
		"get":    func(a domain.Membership) error { _, err := e.svc.Get(a, "s-pay", run.ID); return err },
		"create": func(a domain.Membership) error { _, err := e.svc.Create(a, "s-pay", "month", "2026-01-10"); return err },
		"adjust": func(a domain.Membership) error {
			_, err := e.svc.AdjustLine(a, "s-pay", run.ID, "u-prof", 0, "")
			return err
		},
		"pay":    func(a domain.Membership) error { _, err := e.svc.Pay(a, "s-pay", run.ID); return err },
		"delete": func(a domain.Membership) error { return e.svc.Delete(a, "s-pay", run.ID) },
		"export": func(a domain.Membership) error { _, _, err := e.svc.ExportCSV(a, "s-pay", run.ID); return err },
	}
	for name, call := range calls {
		// owner/admin: dejan pasar (el create puede dar 409 por solape, pero no
		// 403).
		for _, actor := range []domain.Membership{
			e.actor(t, "u-owner"), e.actor(t, "u-admin"),
		} {
			if err := call(actor); errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("%s con %s: no debía dar 403 (%v)", name, actor.Role, err)
			}
		}
		// recepción, profesional, dado de baja y dueño de otro salón: 403.
		for _, userID := range []string{"u-recep", "u-prof", "u-gone", "u-other-owner"} {
			actor := e.mems.ByKey[userID+"|s-pay"]
			if userID == "u-other-owner" {
				actor = e.mems.ByKey["u-other-owner|s-other"]
			}
			if err := call(actor); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("%s con %s: esperaba 403, dio %v", name, userID, err)
			}
		}
	}
	// El actor con id de otro salón pero apuntando al salón ajeno tampoco.
	if _, err := e.svc.Create(e.actor(t, "u-prof"), "s-other", "month", "2026-01-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional sobre otro salón: %v", err)
	}
}

// Exportar: nombre seguro, BOM, encabezado y la foto congelada (no la venta de
// hoy).
func TestPayrollRunExport(t *testing.T) {
	e := newRunEnv(t)
	e.febSeed()
	owner := e.actor(t, "u-owner")
	run := e.create(t, "u-owner", "month", "2026-02-10")
	// Se mueve el mundo después de crear; el CSV tiene que salir congelado.
	e.sales.add("s-pay", domain.PayrollSalesTotal{UserID: "u-prof", Name: "Ana", SalesCount: 9, CommissionBase: 999999})

	name, data, err := e.svc.ExportCSV(owner, "s-pay", run.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if name != "nomina-month-2026-02-01-2026-02-28.csv" {
		t.Fatalf("filename = %q", name)
	}
	text := string(data)
	if !strings.HasPrefix(text, "\xef\xbb\xbfProfesional,Estado,Ventas,Total vendido,Comisión,Fijo,Ajuste,Nota del ajuste,Total\r\n") {
		t.Fatalf("CSV sin BOM/encabezado: %q", text)
	}
	if !strings.Contains(text, "\r\nAna,Activo,3,34.99,4.99,0.00,0.00,,4.99\r\n") {
		t.Fatalf("fila congelada: %q", text)
	}
	// Una corrida de otro salón no se exporta: 404.
	if _, _, err := e.svc.ExportCSV(e.mems.ByKey["u-other-owner|s-other"], "s-other", run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("export ajeno: %v", err)
	}
}
