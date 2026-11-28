package app

import (
	"errors"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
	"salon-saas/internal/team"
	"salon-saas/internal/testutil"
)

// B-04: el precio NO lo pone el cliente, lo pone el catálogo. Y un servicio
// inactivo no se vende (ya no está en la carta).
func TestSaleService_precio_del_catalogo_y_servicio_activo(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	mems := &testutil.FakeMemberships{}
	svc := pos.SaleService{Sales: sales, Services: services, Memberships: mems}
	testutil.Catalogo(t, services, "s1")
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	corte := testutil.SvcIDByName(t, services, "s1", "Corte")

	// El cliente manda 1 centavo por un corte de 150000: se cobra el catálogo.
	sale, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 2, UnitPriceCents: 1},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("registrar: %v", err)
	}
	if sale.TotalCents != 300000 {
		t.Fatalf("total con precio de catálogo = 300000, dio %d", sale.TotalCents)
	}
	for _, it := range sale.Items {
		if it.UnitPriceCents != 150000 {
			t.Fatalf("el ítem debe guardar el precio del catálogo, dio %d", it.UnitPriceCents)
		}
	}

	// Un precio absurdo en el pedido se rechaza aunque el catálogo mande: el
	// pedido se valida tal como llegó (B-03).
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 1, UnitPriceCents: 2_000_000_000},
	}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("precio de 2e9 debe dar ErrValidation, dio %v", err)
	}

	// Servicio inactivo: 400, no se vende.
	inactivo, _ := domain.NewService("s1", "Keratina", 90, 500000, 10)
	inactivo, _ = inactivo.WithDetails(inactivo.Name, inactivo.DurationMin, inactivo.PriceCents, inactivo.CommissionPct, false)
	if err := services.Save(inactivo); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: inactivo.ID, Qty: 1, UnitPriceCents: 500000},
	}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inactivo debe dar ErrValidation, dio %v", err)
	}
}

// B-12: el professional_id tiene que ser alguien que atiende EN ESE salón.
func TestSaleService_profesional_debe_ser_del_salon(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	mems := &testutil.FakeMemberships{}
	svc := pos.SaleService{Sales: sales, Services: services, Memberships: mems}
	testutil.Catalogo(t, services, "s1")
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	corte := testutil.SvcIDByName(t, services, "s1", "Corte")
	items := []domain.SaleItem{{ServiceID: corte, Qty: 1, UnitPriceCents: 150000}}

	// Inexistente en el salón.
	if _, err := svc.RegisterSale(recep, "s1", "fantasma", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional inexistente debe dar ErrValidation, dio %v", err)
	}
	// Miembro del salón pero con rol que no atiende (recepción).
	testutil.Miembro(mems, "u-otra-recep", "s1", domain.RoleReception)
	if _, err := svc.RegisterSale(recep, "s1", "u-otra-recep", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("recepción no puede ser el profesional, dio %v", err)
	}
	// Profesional de OTRO salón: tampoco.
	testutil.Miembro(mems, "u-prof2", "s2", domain.RoleProfessional)
	if _, err := svc.RegisterSale(recep, "s1", "u-prof2", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional de otro salón debe dar ErrValidation, dio %v", err)
	}
	// Los tres roles que atienden sí pasan (F2a: el dueño y la administración
	// con la marca de "atiende clientes", como hasta ahora).
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleProfessional} {
		uid := "u-" + string(role)
		_ = mems.Save(domain.Membership{UserID: uid, SalonID: "s1", Role: role, Attends: true})
		if _, err := svc.RegisterSale(recep, "s1", uid, "Lía", items, domain.MethodCash); err != nil {
			t.Fatalf("%s debe poder ser el profesional de la venta: %v", role, err)
		}
	}
	// F2a: el dueño que no atiende ya no es el profesional de una venta.
	_ = mems.Save(domain.Membership{UserID: "u-duenio-escritorio", SalonID: "s1", Role: domain.RoleOwner})
	if _, err := svc.RegisterSale(recep, "s1", "u-duenio-escritorio", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("el dueño sin attends no atiende: %v", err)
	}
}

func TestBookingService_profesional_debe_ser_del_salon(t *testing.T) {
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: &testutil.FakeBookings{}, Memberships: mems}
	rec := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	start := time.Now().UTC().Add(24 * time.Hour)

	if _, err := svc.CreateBooking(rec, "s1", "fantasma", "Lía", "", start, start.Add(time.Hour)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional inexistente debe dar ErrValidation, dio %v", err)
	}
	testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.CreateBooking(rec, "s1", "u-prof", "Lía", "", start, start.Add(time.Hour)); err != nil {
		t.Fatalf("profesional del salón debe poder agendarse: %v", err)
	}
}

// B-09: el cierre corre en la transacción con lock del salón; sin Tx no hay
// cierre (producción siempre lo configura, los fakes de test pueden no hacerlo).
func TestCashCloseService_usa_la_transaccion_con_lock(t *testing.T) {
	sales := &testutil.FakeSales{}
	mems := &testutil.FakeMemberships{}
	closes := &testutil.FakeCashCloses{}
	tx := &fakeSalonTx{closes: closes, sales: sales}
	svc := pos.CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems, Tx: tx}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)
	prof := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	_ = prof

	sales.All = append(sales.All, domain.Sale{
		ID: "v1", SalonID: "s1", TotalCents: 300000, Method: domain.MethodCash,
		CreatedAt: time.Now().UTC(),
	})
	c, err := svc.CloseCash(owner, "s1", 300000, "")
	if err != nil {
		t.Fatalf("cerrar caja: %v", err)
	}
	if tx.locks != 1 || tx.lastSalonID != "s1" {
		t.Fatalf("el cierre debe tomar el lock del salón una vez: %+v", tx)
	}
	if c.SystemCashCents != 300000 {
		t.Fatalf("expected cash = 300000, dio %d", c.SystemCashCents)
	}
	if len(closes.All) != 1 {
		t.Fatalf("debe quedar un cierre, hay %d", len(closes.All))
	}
}

// --- Endpoints nuevos: /me, /staff, /public/salons/:slug ---

func TestStaffService(t *testing.T) {
	staff := &fakeStaffLister{BySalon: map[string][]domain.StaffMember{
		"s1": {
			{ID: "u-owner", Name: "Ana", Role: domain.RoleOwner, Attends: true},
			{ID: "u-prof", Name: "Bruno", Role: domain.RoleProfessional},
			// F2a: la administración sin la marca no atiende y no se lista.
			{ID: "u-admin", Name: "Carla", Role: domain.RoleAdmin},
			{ID: "u-recep", Name: "Dani", Role: domain.RoleReception, Attends: true},
		},
	}}
	mems := &testutil.FakeMemberships{}
	svc := team.StaffService{Staff: staff, Memberships: mems}
	_ = mems.Save(domain.Membership{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception})

	list, err := svc.List(domain.Membership{UserID: "u-recep", SalonID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("staff: %v", err)
	}
	if len(list) != 2 || list[0].ID != "u-owner" || list[1].ID != "u-prof" {
		t.Fatalf("esperaba dueño (con attends) y profesional, dio %+v", list)
	}
	// No-miembro: 403 (no se filtra el equipo de otro salón).
	if _, err := svc.List(domain.Membership{UserID: "u-ajeno", SalonID: "s2"}, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro debe dar ErrForbidden, dio %v", err)
	}
}
