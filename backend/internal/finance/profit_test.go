// Caso de uso del reporte de ganancia neta (F3b, D1/D2/D5/D6): permisos, el
// período en la zona del salón, la ventana que pide al repo y el armado del
// reporte (ceros, neto y totales).
package finance

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// fakeProfit devuelve la foto sembrada y guarda la ventana que le pidieron.
type fakeProfit struct {
	data domain.ProfitData
	err  error
	call struct {
		salonID, timezone string
		from, to          time.Time
	}
	calls int
}

func (f *fakeProfit) DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error) {
	f.calls++
	f.call.salonID, f.call.timezone, f.call.from, f.call.to = salonID, timezone, from, to
	if f.err != nil {
		return domain.ProfitData{}, f.err
	}
	return f.data, nil
}

var _ ports.ProfitRepository = (*fakeProfit)(nil)

type profitEnv struct {
	repo     *fakeProfit
	settings *testutil.FakeSettings
	mems     *testutil.FakeMemberships
	svc      ProfitService
}

func newProfitEnv() *profitEnv {
	mems := &testutil.FakeMemberships{ByKey: map[string]domain.Membership{}}
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/New_York", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	for _, m := range []domain.Membership{
		{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner},
		{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "u-pro", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner},
	} {
		mems.ByKey[m.UserID+"|"+m.SalonID] = m
	}
	repo := &fakeProfit{}
	return &profitEnv{
		repo: repo, settings: settings, mems: mems,
		svc: ProfitService{
			Profit: repo, Memberships: mems, Settings: settings,
			Now: func() time.Time { return expenseNow },
		},
	}
}

func (e *profitEnv) actor(t *testing.T, userID string) domain.Membership {
	t.Helper()
	m, err := e.mems.Find(userID, "s1")
	if err != nil {
		t.Fatalf("actor %s: %v", userID, err)
	}
	return m
}

// D5: solo owner y administración ven ganancias; el resto recibe 403.
func TestProfitService_permisos_por_rol(t *testing.T) {
	e := newProfitEnv()
	for _, tc := range []struct {
		user string
		want bool
	}{
		{"u-owner", true}, {"u-admin", true},
		{"u-recep", false}, {"u-pro", false},
	} {
		_, err := e.svc.Report(e.actor(t, tc.user), "s1", "month", "2026-03-10")
		if tc.want && err != nil {
			t.Fatalf("%s debería ver ganancias: %v", tc.user, err)
		}
		if !tc.want && !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debería ver ganancias: %v", tc.user, err)
		}
	}
	ajeno := domain.Membership{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner}
	if _, err := e.svc.Report(ajeno, "s1", "month", "2026-03-10"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("un dueño de otro salón: %v", err)
	}
}

func TestProfitService_reporte_y_ventana(t *testing.T) {
	e := newProfitEnv()
	e.repo.data = domain.ProfitData{
		RevenueByDay:  map[string]int{"2026-03-02": 1000},
		ExpensesByDay: map[string]int{"2026-03-05": 4000},
		PayrollByDay:  map[string]int{"2026-03-08": 500},
		ExpensesByCategory: []domain.ExpenseCategoryTotal{
			{Category: domain.ExpenseOther, TotalCents: 4000},
		},
	}
	owner := e.actor(t, "u-owner")
	report, err := e.svc.Report(owner, "s1", "week", "2026-03-08")
	if err != nil {
		t.Fatalf("reporte: %v", err)
	}
	if report.Period.FromDate() != "2026-03-02" || report.Period.ToDate() != "2026-03-08" {
		t.Fatalf("período = %s..%s", report.Period.FromDate(), report.Period.ToDate())
	}
	if len(report.Days) != 7 || report.Totals.NetCents != -3500 ||
		report.Totals.RevenueCents != 1000 || report.Totals.ExpensesCents != 4000 ||
		report.Totals.PayrollCents != 500 {
		t.Fatalf("reporte = %+v", report)
	}
	// La ventana es [00:00 local del 2/3, 00:00 local del 9/3) en la zona del
	// salón, y la zona viaja al SQL para agrupar por día LOCAL.
	if e.repo.call.salonID != "s1" || e.repo.call.timezone != "America/New_York" {
		t.Fatalf("llamada = %+v", e.repo.call)
	}
	if want := time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC); !e.repo.call.from.Equal(want) {
		t.Fatalf("from = %s, esperaba %s", e.repo.call.from.UTC(), want)
	}
	if want := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC); !e.repo.call.to.Equal(want) {
		t.Fatalf("to = %s, esperaba %s", e.repo.call.to.UTC(), want)
	}

	// Sin `date` es HOY en la zona del salón (20/3 12:00 en NY).
	if _, err := e.svc.Report(owner, "s1", "day", ""); err != nil {
		t.Fatalf("día de hoy: %v", err)
	}
	if want := time.Date(2026, 3, 20, 4, 0, 0, 0, time.UTC); !e.repo.call.from.Equal(want) {
		t.Fatalf("hoy = %s, esperaba %s", e.repo.call.from.UTC(), want)
	}
}

// Un salón sin configuración usa la zona de fábrica; un período inválido es 400
// con la frase del contrato y no llega a consultar.
func TestProfitService_periodo_invalido_y_salon_vacio(t *testing.T) {
	e := newProfitEnv()
	owner := e.actor(t, "u-owner")
	for _, tc := range []struct{ kind, date string }{
		{"", "2026-03-10"}, {"fortnight", "2026-03-10"}, {"day", "10/03/2026"},
		{"week", "2026-13-01"},
	} {
		if _, err := e.svc.Report(owner, "s1", tc.kind, tc.date); err == nil ||
			err.Error() != "Período inválido" || !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q %q = %v", tc.kind, tc.date, err)
		}
	}
	if e.repo.calls != 0 {
		t.Fatalf("un período inválido no consulta: %d llamadas", e.repo.calls)
	}

	// Salón sin fila de settings: la zona por defecto (CDMX) y un reporte de ceros.
	delete(e.settings.BySalon, "s1")
	report, err := e.svc.Report(owner, "s1", "day", "2026-03-10")
	if err != nil {
		t.Fatalf("salón sin settings: %v", err)
	}
	if e.repo.call.timezone != domain.DefaultTimezone {
		t.Fatalf("zona = %q, esperaba %q", e.repo.call.timezone, domain.DefaultTimezone)
	}
	if len(report.Days) != 1 || report.Totals != (domain.ProfitTotals{}) || report.Days[0].NetCents != 0 {
		t.Fatalf("reporte vacío = %+v", report)
	}

	// Un error del adaptador se propaga tal cual (nunca se inventa un reporte).
	e.repo.err = errors.New("db caída")
	if _, err := e.svc.Report(owner, "s1", "day", "2026-03-10"); err == nil || err.Error() != "db caída" {
		t.Fatalf("error del repo = %v", err)
	}
}
