package payroll

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// Fakes de la nómina (F3.2): el modelo de pago de cada miembro y los agregados
// de ventas por profesional. Misma semántica que pgstore: scope por salón, el
// default de D3 cuando no hay fila, ErrNotFound para quien no es del salón.

type fakePayModels struct {
	// members es el universo de personas del salón (activas y dadas de baja).
	members []domain.MemberPayView
	// rows es el modelo guardado, por "salón|persona". Sin fila = default.
	rows map[string]domain.MemberPay
}

func newFakePayModels(members ...domain.MemberPayView) *fakePayModels {
	return &fakePayModels{members: members, rows: map[string]domain.MemberPay{}}
}

func payKey(salonID, userID string) string { return salonID + "|" + userID }

func (f *fakePayModels) member(salonID, userID string) (domain.MemberPayView, bool) {
	for _, m := range f.members {
		if m.SalonID == salonID && m.UserID == userID {
			return m, true
		}
	}
	return domain.MemberPayView{}, false
}

func (f *fakePayModels) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	out := []domain.MemberPayView{}
	for _, m := range f.members {
		if m.SalonID != salonID {
			continue
		}
		if row, ok := f.rows[payKey(salonID, m.UserID)]; ok {
			m.MemberPay = row
		} else {
			m.MemberPay = domain.DefaultMemberPay(salonID, m.UserID)
		}
		out = append(out, m)
	}
	return out, nil
}

func (f *fakePayModels) FindPayModel(salonID, userID string) (domain.MemberPayView, error) {
	m, ok := f.member(salonID, userID)
	if !ok {
		return domain.MemberPayView{}, domain.ErrNotFound
	}
	if row, ok := f.rows[payKey(salonID, userID)]; ok {
		m.MemberPay = row
	} else {
		m.MemberPay = domain.DefaultMemberPay(salonID, userID)
	}
	return m, nil
}

func (f *fakePayModels) SavePayModel(p domain.MemberPay) error {
	if _, ok := f.member(p.SalonID, p.UserID); !ok {
		// La FK compuesta: una persona de otro salón no se guarda.
		return domain.ErrNotFound
	}
	f.rows[payKey(p.SalonID, p.UserID)] = p
	return nil
}

var _ ports.MemberPayRepository = (*fakePayModels)(nil)

type payWindow struct {
	salonID  string
	from, to time.Time
}

type fakePayrollSales struct {
	totals map[string][]domain.PayrollSalesTotal // por salón
	// windows guarda las ventanas pedidas, para probar el día del salón.
	windows []payWindow
	// askedFor registra las personas de CommissionTotalFor («Mis comisiones» no
	// puede leer los números de otra persona).
	askedFor []string
}

func newFakePayrollSales() *fakePayrollSales {
	return &fakePayrollSales{totals: map[string][]domain.PayrollSalesTotal{}}
}

func (f *fakePayrollSales) add(salonID string, t domain.PayrollSalesTotal) {
	f.totals[salonID] = append(f.totals[salonID], t)
}

func (f *fakePayrollSales) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	f.windows = append(f.windows, payWindow{salonID: salonID, from: from, to: to})
	return f.totals[salonID], nil
}

func (f *fakePayrollSales) CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error) {
	f.windows = append(f.windows, payWindow{salonID: salonID, from: from, to: to})
	f.askedFor = append(f.askedFor, userID)
	for _, t := range f.totals[salonID] {
		if t.UserID == userID {
			return t, nil
		}
	}
	return domain.PayrollSalesTotal{UserID: userID}, nil
}

var _ ports.PayrollSalesRepository = (*fakePayrollSales)(nil)

// payrollEnv arma el escenario: salón de CDMX, owner, admin, dos profesionales
// (uno activo y uno dado de baja) y recepción.
type payrollEnv struct {
	svc      PayrollService
	paySvc   PayModelService
	pay      *fakePayModels
	sales    *fakePayrollSales
	mems     *testutil.FakeMemberships
	settings *testutil.FakeSettings
	when     time.Time
}

func newPayrollEnv(t *testing.T) *payrollEnv {
	t.Helper()
	const salon = "s-pay"
	// Las filas se cargan directo (Save reactivaría la baja, como el upsert real).
	mems := &testutil.FakeMemberships{ByKey: map[string]domain.Membership{}}
	for _, m := range []domain.Membership{
		{UserID: "u-owner", SalonID: salon, Role: domain.RoleOwner},
		{UserID: "u-admin", SalonID: salon, Role: domain.RoleAdmin},
		{UserID: "u-prof", SalonID: salon, Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-recep", SalonID: salon, Role: domain.RoleReception},
		{UserID: "u-gone", SalonID: salon, Role: domain.RoleProfessional, Attends: true, EndedAt: ptrTime(time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC))},
	} {
		mems.ByKey[m.UserID+"|"+m.SalonID] = m
	}
	pay := newFakePayModels(
		domain.MemberPayView{MemberPay: domain.DefaultMemberPay(salon, "u-owner"), Name: "Dueña", Role: domain.RoleOwner, Active: true},
		domain.MemberPayView{MemberPay: domain.DefaultMemberPay(salon, "u-admin"), Name: "Admin", Role: domain.RoleAdmin, Active: true},
		domain.MemberPayView{MemberPay: domain.DefaultMemberPay(salon, "u-prof"), Name: "Ana", Role: domain.RoleProfessional, Active: true},
		domain.MemberPayView{MemberPay: domain.DefaultMemberPay(salon, "u-recep"), Name: "Recep", Role: domain.RoleReception, Active: true},
		domain.MemberPayView{MemberPay: domain.DefaultMemberPay(salon, "u-gone"), Name: "Beto", Role: domain.RoleProfessional, Active: false},
	)
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		salon: {SalonID: salon, Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	when := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)
	env := &payrollEnv{
		pay: pay, sales: newFakePayrollSales(), mems: mems, settings: settings, when: when,
	}
	env.svc = PayrollService{
		Pay: pay, Sales: env.sales, Memberships: mems, Settings: settings,
		Now: func() time.Time { return when },
	}
	env.paySvc = PayModelService{Pay: pay, Memberships: mems}
	return env
}

func ptrTime(t time.Time) *time.Time { return &t }

func (e *payrollEnv) actor(t *testing.T, userID string) domain.Membership {
	t.Helper()
	m, err := e.mems.Find(userID, "s-pay")
	if err != nil {
		t.Fatalf("actor %s: %v", userID, err)
	}
	return m
}

// La ventana llega al repositorio como el día LOCAL del salón: medianoche local
// del primer día y del día siguiente al último, en la zona del salón (CDMX,
// UTC-6 todo el año). Nunca el día UTC.
func TestPayrollPreview_periodo_en_zona_del_salon(t *testing.T) {
	env := newPayrollEnv(t)
	owner := env.actor(t, "u-owner")

	got, err := env.svc.Preview(owner, "s-pay", "month", "2026-03-15")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got.Period.FromDate() != "2026-03-01" || got.Period.ToDate() != "2026-03-31" {
		t.Fatalf("período = %s..%s", got.Period.FromDate(), got.Period.ToDate())
	}
	if got.Closed {
		t.Fatal("marzo con «hoy» 20/3 no está cerrado")
	}
	if len(env.sales.windows) != 1 {
		t.Fatalf("esperaba una consulta de ventas, hubo %d", len(env.sales.windows))
	}
	w := env.sales.windows[0]
	wantFrom := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC)
	if w.salonID != "s-pay" {
		t.Fatalf("la consulta debe ir scopeada al salón, fue %q", w.salonID)
	}
	if !w.from.Equal(wantFrom) || !w.to.Equal(wantTo) {
		t.Fatalf("ventana = [%s, %s), esperaba [%s, %s)", w.from.UTC(), w.to.UTC(), wantFrom, wantTo)
	}

	// Un período terminado responde closed: true.
	old, err := env.svc.Preview(owner, "s-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("preview de febrero: %v", err)
	}
	if !old.Closed {
		t.Fatalf("febrero con «hoy» 20/3 debe estar cerrado: %+v", old.Period)
	}
}

// Líneas: comisión (con la división de D2), fijo del mes solo para el activo,
// totales y el orden por total descendente y después por nombre.
func TestPayrollPreview_arma_lineas_y_totales(t *testing.T) {
	env := newPayrollEnv(t)
	// Ana: 3 ventas, base 49990 -> 499 centavos, una línea sin foto (estimada).
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 3, SalesTotalCents: 3499,
		CommissionBase: 49990, Estimated: true,
	})
	// Beto (dado de baja): 1 venta, base 20000 -> 200. Fijo 0 porque no está activo.
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-gone", Name: "Beto", SalesCount: 1, SalesTotalCents: 2000, CommissionBase: 20000,
	})
	// Dueña: sin ventas, fijo del mes de 1000,00 (comisión apagada).
	dm, _ := domain.NewMemberPay("s-pay", "u-owner", 100000, "month", false)
	if err := env.pay.SavePayModel(dm); err != nil {
		t.Fatalf("modelo de la dueña: %v", err)
	}
	// Admin: sin ventas y sin fijo -> no aparece.
	// Recepción: sin ventas y sin fijo -> no aparece.

	got, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(got.Lines) != 3 {
		t.Fatalf("esperaba 3 líneas, dio %d: %+v", len(got.Lines), got.Lines)
	}
	// Orden: total desc y después nombre.
	wantOrder := []string{"u-owner", "u-prof", "u-gone"}
	for i, id := range wantOrder {
		if got.Lines[i].UserID != id {
			t.Fatalf("línea %d = %s, esperaba %s (%+v)", i, got.Lines[i].UserID, id, got.Lines)
		}
	}
	ownerLine, ana, beto := got.Lines[0], got.Lines[1], got.Lines[2]
	if ownerLine.FixedCents != 100000 || ownerLine.CommissionCents != 0 || ownerLine.TotalCents != 100000 {
		t.Fatalf("línea de la dueña: %+v", ownerLine)
	}
	if ana.Name != "Ana" || !ana.Active || ana.SalesCount != 3 || ana.SalesTotalCents != 3499 ||
		ana.CommissionCents != 499 || ana.FixedCents != 0 || ana.TotalCents != 499 || !ana.Estimated {
		t.Fatalf("línea de Ana: %+v", ana)
	}
	if beto.Active || beto.CommissionCents != 200 || beto.FixedCents != 0 || beto.TotalCents != 200 || beto.Estimated {
		t.Fatalf("línea de Beto (baja): %+v", beto)
	}
	// Totales = suma de las líneas.
	if got.CommissionCents != 699 || got.FixedCents != 100000 || got.TotalCents != 100699 {
		t.Fatalf("totales: comisión %d, fijo %d, total %d", got.CommissionCents, got.FixedCents, got.TotalCents)
	}
}

// El fijo se paga UNA vez y solo si el tipo liquidado es el del miembro y la
// persona está activa (D4).
func TestPayrollPreview_fijo_solo_del_tipo_y_activo(t *testing.T) {
	env := newPayrollEnv(t)
	week, _ := domain.NewMemberPay("s-pay", "u-prof", 50000, "week", false)
	if err := env.pay.SavePayModel(week); err != nil {
		t.Fatalf("modelo: %v", err)
	}
	// Beto (dado de baja) tiene un fijo del mes guardado: no se paga.
	goneFixed, _ := domain.NewMemberPay("s-pay", "u-gone", 90000, "month", false)
	if err := env.pay.SavePayModel(goneFixed); err != nil {
		t.Fatalf("modelo de la baja: %v", err)
	}

	// Liquidando el mes, el fijo semanal de Ana no aplica y no tiene ventas: no
	// aparece.
	month, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("preview del mes: %v", err)
	}
	if len(month.Lines) != 0 {
		t.Fatalf("el fijo semanal no se paga en un mes y la baja no cobra fijo: %+v", month.Lines)
	}

	// Liquidando la semana del 9/3 al 15/3 aparece el fijo semanal de Ana.
	wk, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "week", "2026-03-10")
	if err != nil {
		t.Fatalf("preview de la semana: %v", err)
	}
	if len(wk.Lines) != 1 || wk.Lines[0].UserID != "u-prof" {
		t.Fatalf("esperaba solo el fijo de Ana: %+v", wk.Lines)
	}
	if wk.Lines[0].FixedCents != 50000 || wk.Lines[0].CommissionCents != 0 || wk.Lines[0].TotalCents != 50000 {
		t.Fatalf("línea semanal de Ana: %+v", wk.Lines[0])
	}
	if wk.FixedCents != 50000 || wk.TotalCents != 50000 {
		t.Fatalf("totales de la semana: %+v", wk)
	}
}

// Comisión apagada (fijo puro): su comisión es 0 pero sus ventas siguen
// contando en cantidad y total.
func TestPayrollPreview_comision_deshabilitada(t *testing.T) {
	env := newPayrollEnv(t)
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 2, SalesTotalCents: 5000, CommissionBase: 25000,
	})
	pure, _ := domain.NewMemberPay("s-pay", "u-prof", 70000, "month", false)
	if err := env.pay.SavePayModel(pure); err != nil {
		t.Fatalf("modelo: %v", err)
	}
	got, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(got.Lines) != 1 {
		t.Fatalf("esperaba 1 línea: %+v", got.Lines)
	}
	l := got.Lines[0]
	if l.CommissionCents != 0 || l.FixedCents != 70000 || l.TotalCents != 70000 {
		t.Fatalf("fijo puro: %+v", l)
	}
	if l.SalesCount != 2 || l.SalesTotalCents != 5000 {
		t.Fatalf("las ventas deben seguir contando: %+v", l)
	}
	if got.CommissionCents != 0 || got.FixedCents != 70000 {
		t.Fatalf("totales: %+v", got)
	}
}

// Un profesional con ventas que ya no está en el equipo aparece igual: la
// comisión de lo que vendió no se pierde.
func TestPayrollPreview_vendedor_sin_miembro(t *testing.T) {
	env := newPayrollEnv(t)
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-fantasma", Name: "Vieja", SalesCount: 1, SalesTotalCents: 1000, CommissionBase: 3000,
	})
	got, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0].UserID != "u-fantasma" {
		t.Fatalf("el vendedor sin membresía debe aparecer: %+v", got.Lines)
	}
	if got.Lines[0].Active || got.Lines[0].CommissionCents != 30 || got.Lines[0].Name != "Vieja" {
		t.Fatalf("línea del vendedor sin membresía: %+v", got.Lines[0])
	}
}

// Otra persona del salón no aparece en «Mis comisiones» y el repositorio se
// consulta por persona: la lectura nunca trae las ventas de los demás.
func TestPayrollMe_solo_lo_propio(t *testing.T) {
	env := newPayrollEnv(t)
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 3, SalesTotalCents: 3499, CommissionBase: 49990, Estimated: true,
	})
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-gone", Name: "Beto", SalesCount: 9, SalesTotalCents: 900000, CommissionBase: 9000000,
	})

	got, err := env.svc.Me(env.actor(t, "u-prof"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if got.SalesCount != 3 || got.SalesTotalCents != 3499 || got.CommissionCents != 499 || !got.Estimated {
		t.Fatalf("mis comisiones: %+v", got)
	}
	if got.Period.FromDate() != "2026-03-01" || got.Period.ToDate() != "2026-03-31" {
		t.Fatalf("período: %+v", got.Period)
	}
	// La consulta es por persona y no hay ninguna lectura de todos.
	if len(env.sales.askedFor) != 1 || env.sales.askedFor[0] != "u-prof" {
		t.Fatalf("la lectura debe ser solo de quien llama: %v", env.sales.askedFor)
	}
	// Y la ventana también es la del salón.
	if w := env.sales.windows[len(env.sales.windows)-1]; !w.from.Equal(time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("ventana de «mis comisiones» = %s", w.from.UTC())
	}

	// Comisión apagada: el profesional cobra fijo y su comisión se informa 0.
	pure, _ := domain.NewMemberPay("s-pay", "u-prof", 70000, "month", false)
	if err := env.pay.SavePayModel(pure); err != nil {
		t.Fatalf("modelo: %v", err)
	}
	got, err = env.svc.Me(env.actor(t, "u-prof"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("me con comisión apagada: %v", err)
	}
	if got.CommissionCents != 0 || got.SalesCount != 3 || got.SalesTotalCents != 3499 {
		t.Fatalf("con la comisión apagada: %+v", got)
	}
}

func TestPayroll_permisos(t *testing.T) {
	env := newPayrollEnv(t)
	const salon = "s-pay"

	// Recepción y profesional no ven la nómina; la baja no entra.
	for _, uid := range []string{"u-recep", "u-prof"} {
		if _, err := env.svc.Preview(env.actor(t, uid), salon, "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s en preview: esperaba Forbidden, dio %v", uid, err)
		}
	}
	// Un profesional SÍ entra a «mis comisiones».
	if _, err := env.svc.Me(env.actor(t, "u-prof"), salon, "month", "2026-03-10"); err != nil {
		t.Fatalf("profesional en /me: %v", err)
	}
	// Recepción no ve ni las suyas (D7).
	if _, err := env.svc.Me(env.actor(t, "u-recep"), salon, "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción en /me: esperaba Forbidden, dio %v", err)
	}
	// Dado de baja: no entra a ninguna de las dos.
	gone := domain.Membership{UserID: "u-gone", SalonID: salon, Role: domain.RoleProfessional}
	if _, err := env.svc.Preview(gone, salon, "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("baja en preview: esperaba Forbidden, dio %v", err)
	}
	if _, err := env.svc.Me(gone, salon, "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("baja en /me: esperaba Forbidden, dio %v", err)
	}
	// Dueño y administración ven todo; el dueño también sus propias comisiones.
	if _, err := env.svc.Preview(env.actor(t, "u-admin"), salon, "month", "2026-03-10"); err != nil {
		t.Fatalf("admin en preview: %v", err)
	}
	if _, err := env.svc.Me(env.actor(t, "u-owner"), salon, "month", "2026-03-10"); err != nil {
		t.Fatalf("dueña en /me: %v", err)
	}
	// Otro salón (o el salón equivocado en el token) no entra.
	other := domain.Membership{UserID: "u-owner", SalonID: "s-otro", Role: domain.RoleOwner}
	if _, err := env.svc.Preview(other, salon, "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón en preview: esperaba Forbidden, dio %v", err)
	}
}

func TestPayroll_periodo_invalido(t *testing.T) {
	env := newPayrollEnv(t)
	owner := env.actor(t, "u-owner")
	for _, c := range []struct{ kind, date string }{
		{"day", "2026-03-10"}, {"month", "2026-13-01"}, {"", "2026-03-10"}, {"month", ""},
	} {
		_, err := env.svc.Preview(owner, "s-pay", c.kind, c.date)
		if !errors.Is(err, domain.ErrValidation) || err.Error() != "Período inválido" {
			t.Fatalf("preview %q/%q: esperaba «Período inválido», dio %v", c.kind, c.date, err)
		}
		_, err = env.svc.Me(env.actor(t, "u-prof"), "s-pay", c.kind, c.date)
		if !errors.Is(err, domain.ErrValidation) || err.Error() != "Período inválido" {
			t.Fatalf("me %q/%q: esperaba «Período inválido», dio %v", c.kind, c.date, err)
		}
	}
}

// --- Modelo de pago ---

func TestPayModels_list_incluye_la_baja_y_el_default(t *testing.T) {
	env := newPayrollEnv(t)
	saved, _ := domain.NewMemberPay("s-pay", "u-prof", 30000, "fortnight", true)
	if err := env.pay.SavePayModel(saved); err != nil {
		t.Fatalf("modelo: %v", err)
	}
	list, err := env.paySvc.List(env.actor(t, "u-owner"), "s-pay")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("esperaba los 5 miembros (con la baja), dio %d: %+v", len(list), list)
	}
	byID := map[string]domain.MemberPayView{}
	for _, v := range list {
		byID[v.UserID] = v
	}
	if got := byID["u-prof"]; got.FixedCents != 30000 || got.FixedPeriod != domain.PayFortnight || !got.CommissionEnabled {
		t.Fatalf("modelo guardado: %+v", got)
	}
	// Sin fila: 0, mes, comisión encendida.
	if got := byID["u-admin"]; got.FixedCents != 0 || got.FixedPeriod != domain.PayMonth || !got.CommissionEnabled {
		t.Fatalf("default: %+v", got)
	}
	// La baja se lista con active=false.
	if got := byID["u-gone"]; got.Active || got.Name != "Beto" || got.Role != domain.RoleProfessional {
		t.Fatalf("la baja debe aparecer con active=false: %+v", got)
	}
	// Permisos: recepción y profesional no ven los modelos.
	for _, uid := range []string{"u-recep", "u-prof"} {
		if _, err := env.paySvc.List(env.actor(t, uid), "s-pay"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: esperaba Forbidden, dio %v", uid, err)
		}
	}
	if _, err := env.paySvc.List(env.actor(t, "u-admin"), "s-pay"); err != nil {
		t.Fatalf("admin: %v", err)
	}
}

func TestPayModelUpdate_valida_guarda_y_404(t *testing.T) {
	env := newPayrollEnv(t)
	owner := env.actor(t, "u-owner")

	got, err := env.paySvc.Update(owner, "s-pay", "u-prof", 120000, "week", false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.UserID != "u-prof" || got.Name != "Ana" || got.FixedCents != 120000 ||
		got.FixedPeriod != domain.PayWeek || got.CommissionEnabled || !got.Active {
		t.Fatalf("respuesta del update: %+v", got)
	}
	// Quedó guardado y se puede volver a leer.
	again, err := env.pay.FindPayModel("s-pay", "u-prof")
	if err != nil {
		t.Fatalf("releer: %v", err)
	}
	if again.FixedCents != 120000 || again.CommissionEnabled {
		t.Fatalf("no quedó guardado: %+v", again)
	}

	// Una persona que no es del salón: 404, sin escribir nada.
	if _, err := env.paySvc.Update(owner, "s-pay", "u-de-otro", 1000, "month", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("persona de otro salón: esperaba NotFound, dio %v", err)
	}

	// Validaciones (400 en español).
	for _, c := range []struct {
		fixed  int
		period string
		want   string
	}{
		{-1, "month", "El sueldo fijo no puede ser negativo"},
		{domain.MaxPayCents + 1, "month", "El sueldo fijo no puede superar los 2000000000 centavos"},
		{100, "day", "Período de pago desconocido: usá semana, quincena o mes"},
	} {
		_, err := env.paySvc.Update(owner, "s-pay", "u-prof", c.fixed, c.period, true)
		if !errors.Is(err, domain.ErrValidation) || err.Error() != c.want {
			t.Fatalf("update %d/%q: esperaba %q, dio %v", c.fixed, c.period, c.want, err)
		}
	}
	// Nada de eso se guardó: sigue el 120000 de la primera escritura.
	keep, _ := env.pay.FindPayModel("s-pay", "u-prof")
	if keep.FixedCents != 120000 {
		t.Fatalf("una validación fallida no debe escribir: %+v", keep)
	}

	// Recepción y profesional no escriben (403), aunque el pedido sea válido.
	for _, uid := range []string{"u-recep", "u-prof"} {
		if _, err := env.paySvc.Update(env.actor(t, uid), "s-pay", "u-prof", 1, "month", true); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: esperaba Forbidden, dio %v", uid, err)
		}
	}
	// Y la autorización gana a la validación: recepción con monto inválido es 403.
	if _, err := env.paySvc.Update(env.actor(t, "u-recep"), "s-pay", "u-prof", -5, "mes", true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción con monto inválido: esperaba Forbidden, dio %v", err)
	}
}
