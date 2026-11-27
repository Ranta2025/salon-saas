package pos

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

func TestCashCloseService_cierra_con_ventana_del_dia(t *testing.T) {
	sales := &fakeSales{}
	closes := &testutil.FakeCashCloses{}
	mems := &testutil.FakeMemberships{}
	// Zona explícita UTC: la ventana sin cierre previo es la medianoche local,
	// que en UTC es la medianoche UTC. (Con la default del salón, CDMX, la
	// ventana arranca a medianoche de CDMX; ver cash_closes_timezone_test.go.)
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "UTC", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	svc := CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems, Settings: settings}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)

	now := time.Now().UTC()
	sales.All = append(sales.All,
		// Efectivo de hoy: es el que espera el cajón.
		domain.Sale{ID: "v-cash", SalonID: "s1", TotalCents: 250000, Method: domain.MethodCash, CreatedAt: now},
		// Tarjeta de hoy: no está en el cajón.
		domain.Sale{ID: "v-card", SalonID: "s1", TotalCents: 400000, Method: domain.MethodCard, CreatedAt: now},
		// Efectivo de anteayer: ya se cerró (o no corresponde a esta caja).
		domain.Sale{ID: "v-viejo", SalonID: "s1", TotalCents: 999999, Method: domain.MethodCash, CreatedAt: now.Add(-48 * time.Hour)},
		// Efectivo de otro salón: jamás suma.
		domain.Sale{ID: "v-otro", SalonID: "s2", TotalCents: 777777, Method: domain.MethodCash, CreatedAt: now},
	)

	c, err := svc.CloseCash(owner, "s1", 248000, "faltó vuelto")
	if err != nil {
		t.Fatalf("cerrar: %v", err)
	}
	if c.SystemCashCents != 250000 {
		t.Fatalf("sistema debe ser solo efectivo de hoy (250000), dio %d", c.SystemCashCents)
	}
	if c.CountedCents != 248000 || c.DifferenceCents != -2000 {
		t.Fatalf("contado/diferencia: %d / %d", c.CountedCents, c.DifferenceCents)
	}
	if c.Note != "faltó vuelto" {
		t.Fatalf("nota: %q", c.Note)
	}
	// Sin cierre previo la caja abre a las 00:00 UTC de hoy.
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !c.OpenedAt.Equal(dayStart) {
		t.Fatalf("apertura sin cierre previo = %v, esperaba %v", c.OpenedAt, dayStart)
	}
	if !c.ClosedAt.After(c.OpenedAt) && !c.ClosedAt.Equal(c.OpenedAt) {
		t.Fatalf("cierre no puede ser antes de la apertura: %v", c.ClosedAt)
	}
	if len(closes.All) != 1 {
		t.Fatalf("debe persistir el cierre, hay %d", len(closes.All))
	}
}

func TestCashCloseService_segundo_cierre_arranca_del_anterior(t *testing.T) {
	sales := &fakeSales{}
	closes := &testutil.FakeCashCloses{}
	mems := &testutil.FakeMemberships{}
	svc := CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)

	now := time.Now().UTC()
	anterior := now.Add(-2 * time.Hour)
	closes.All = append(closes.All, domain.CashClose{
		ID: "c0", SalonID: "s1", OpenedAt: anterior.Add(-8 * time.Hour), ClosedAt: anterior,
		SystemCashCents: 100000, CountedCents: 100000, DifferenceCents: 0,
	})
	sales.All = append(sales.All,
		// Antes de la apertura del turno actual: ya contabilizado en c0.
		domain.Sale{ID: "v-previo", SalonID: "s1", TotalCents: 100000, Method: domain.MethodCash, CreatedAt: anterior.Add(-time.Hour)},
		// Turno actual.
		domain.Sale{ID: "v-turno", SalonID: "s1", TotalCents: 50000, Method: domain.MethodCash, CreatedAt: now},
	)

	c, err := svc.CloseCash(owner, "s1", 50500, "")
	if err != nil {
		t.Fatalf("segundo cierre: %v", err)
	}
	if !c.OpenedAt.Equal(anterior) {
		t.Fatalf("la apertura debe ser el cierre anterior (%v), dio %v", anterior, c.OpenedAt)
	}
	if c.SystemCashCents != 50000 {
		t.Fatalf("sistema del turno = 50000, dio %d", c.SystemCashCents)
	}
	if c.DifferenceCents != 500 {
		t.Fatalf("sobrante 500, dio %d", c.DifferenceCents)
	}
}

func TestCashCloseService_permisos_y_validacion(t *testing.T) {
	sales := &fakeSales{}
	closes := &testutil.FakeCashCloses{}
	mems := &testutil.FakeMemberships{}
	svc := CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems}

	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)
	admin := testutil.Miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	for _, actor := range []domain.Membership{recep, profe, ajeno} {
		if _, err := svc.CloseCash(actor, "s1", 1000, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe cerrar caja, dio %v", actor.Role, err)
		}
	}
	// owner y admin sí.
	if _, err := svc.CloseCash(admin, "s1", 1000, ""); err != nil {
		t.Fatalf("admin debe cerrar: %v", err)
	}
	if _, err := svc.CloseCash(owner, "s1", 1000, ""); err != nil {
		t.Fatalf("owner debe cerrar: %v", err)
	}
	// Contado negativo no es un cierre válido.
	if _, err := svc.CloseCash(owner, "s1", -1, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("contado negativo debe dar Validation, dio %v", err)
	}
	// Listado scopeado por salón.
	list, err := svc.ListCloses(owner, "s1")
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba 2 cierres en s1, dio %d", len(list))
	}
	if _, err := svc.ListCloses(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}
