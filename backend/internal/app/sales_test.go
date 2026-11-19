package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// Catalogo mínimo para validar ítems contra el catálogo del salón.
func catalogo(t *testing.T, services *fakeServices, salonID string) {
	t.Helper()
	for _, s := range []struct {
		name  string
		price int
	}{{"Corte", 150000}, {"Brushing", 80000}} {
		svc, err := domain.NewService(salonID, s.name, 45, s.price, 20)
		if err != nil {
			t.Fatalf("catálogo: %v", err)
		}
		if err := services.Save(svc); err != nil {
			t.Fatalf("catálogo save: %v", err)
		}
	}
}

func svcIDByName(t *testing.T, services *fakeServices, salonID, name string) string {
	t.Helper()
	list, err := services.List(salonID, true)
	if err != nil {
		t.Fatalf("listar catálogo: %v", err)
	}
	for _, s := range list {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("no hay servicio %q en %s", name, salonID)
	return ""
}

func TestSaleService_register_total_en_servidor(t *testing.T) {
	sales := &fakeSales{}
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := SaleService{Sales: sales, Services: services, Memberships: mems}
	catalogo(t, services, "s1")
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	corte := svcIDByName(t, services, "s1", "Corte")
	brushing := svcIDByName(t, services, "s1", "Brushing")

	// No hay parámetro total: el total nace de los ítems, siempre.
	sale, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 2, UnitPriceCents: 150000},
		{ServiceID: brushing, Qty: 1, UnitPriceCents: 80000},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("registrar: %v", err)
	}
	if sale.TotalCents != 380000 {
		t.Fatalf("total servidor = 380000, dio %d", sale.TotalCents)
	}
	if len(sales.all) != 1 || sales.all[0].ID != sale.ID {
		t.Fatalf("no persistió: %+v", sales.all)
	}
	if len(sale.Items) != 2 {
		t.Fatalf("ítems: %+v", sale.Items)
	}
}

func TestSaleService_permisos(t *testing.T) {
	sales := &fakeSales{}
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := SaleService{Sales: sales, Services: services, Memberships: mems}
	catalogo(t, services, "s1")
	corte := svcIDByName(t, services, "s1", "Corte")
	items := []domain.SaleItem{{ServiceID: corte, Qty: 1, UnitPriceCents: 150000}}

	// owner, admin y recepción venden.
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleReception} {
		actor := miembro(mems, "u-"+string(role), "s1", role)
		if _, err := svc.RegisterSale(actor, "s1", "prof1", "Lía", items, domain.MethodCard); err != nil {
			t.Fatalf("%s debe poder vender: %v", role, err)
		}
	}
	// Profesional y cliente no venden (el POS es de recepción).
	for _, role := range []domain.Role{domain.RoleProfessional, domain.RoleClient} {
		actor := miembro(mems, "u-"+string(role), "s1", role)
		if _, err := svc.RegisterSale(actor, "s1", "prof1", "Lía", items, domain.MethodCard); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe vender, dio %v", role, err)
		}
	}
	// Miembro de otro salón: 403 en ambos sentidos.
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := svc.RegisterSale(ajeno, "s1", "prof1", "Lía", items, domain.MethodCard); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}

func TestSaleService_rechaza_ventas_invalidas(t *testing.T) {
	sales := &fakeSales{}
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := SaleService{Sales: sales, Services: services, Memberships: mems}
	catalogo(t, services, "s1")
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	corte := svcIDByName(t, services, "s1", "Corte")

	// Sin ítems: no hay venta que registrar (no hay venta en $0 fantasma).
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", nil, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("sin ítems debe dar Validation, dio %v", err)
	}
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("lista vacía debe dar Validation, dio %v", err)
	}
	// qty 0 no es una venta.
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{{ServiceID: corte, Qty: 0, UnitPriceCents: 150000}}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("qty 0 debe dar Validation, dio %v", err)
	}
	// Servicio inexistente y servicio de otro tenant: 400, nunca venta huérfana.
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{{ServiceID: "no-existe", Qty: 1, UnitPriceCents: 1}}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inexistente debe dar Validation, dio %v", err)
	}
	otro, _ := domain.NewService("s2", "Corte ajeno", 30, 99900, 10)
	_ = services.Save(otro)
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{{ServiceID: otro.ID, Qty: 1, UnitPriceCents: 1}}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio de otro salón debe dar Validation, dio %v", err)
	}
	// Método fuera de catálogo.
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{{ServiceID: corte, Qty: 1, UnitPriceCents: 1}}, domain.PaymentMethod("bitcoin")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("método inválido debe dar Validation, dio %v", err)
	}
	if len(sales.all) != 0 {
		t.Fatalf("ninguna venta inválida debe persistirse, hay %d", len(sales.all))
	}
}

func TestSaleService_list_solo_del_salon(t *testing.T) {
	sales := &fakeSales{}
	services := &fakeServices{}
	mems := &fakeMemberships{}
	svc := SaleService{Sales: sales, Services: services, Memberships: mems}
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)

	now := time.Now().UTC()
	sales.all = append(sales.all,
		domain.Sale{ID: "v1", SalonID: "s1", TotalCents: 100, Method: domain.MethodCash, CreatedAt: now},
		domain.Sale{ID: "v2", SalonID: "s2", TotalCents: 999, Method: domain.MethodCash, CreatedAt: now},
	)
	got, err := svc.ListSales(recep, "s1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(got) != 1 || got[0].ID != "v1" {
		t.Fatalf("esperaba solo la venta de s1, dio %+v", got)
	}
	// Un profesional NO ve la facturación del salón: es información sensible.
	if _, err := svc.ListSales(profe, "s1", now.Add(-time.Hour), now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional no debe listar ventas, dio %v", err)
	}
	// Ventana vacía: no trae lo de afuera.
	if got, _ := svc.ListSales(recep, "s1", now.Add(-3*time.Hour), now.Add(-2*time.Hour)); len(got) != 0 {
		t.Fatalf("fuera de ventana no debe traer nada, dio %d", len(got))
	}
	// No-miembro: 403.
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := svc.ListSales(ajeno, "s1", now.Add(-time.Hour), now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}

// TestPlata_solo_manage_y_recepcion: la regla de dinero vive en una función
// (puedeVerPlata) y aplica igual a ventas, cierres y panel.
func TestPlata_solo_manage_y_recepcion(t *testing.T) {
	mems := &fakeMemberships{}
	sales := &fakeSales{}
	services := &fakeServices{}
	closes := &fakeCashCloses{}
	agenda := &fakeAgenda{}
	staff := fakeStaff{count: 2}

	saleSvc := SaleService{Sales: sales, Services: services, Memberships: mems}
	closeSvc := CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems}
	dashSvc := DashboardService{Sales: sales, Agenda: agenda, Staff: staff, Memberships: mems}

	now := time.Now().UTC()
	from, to := now.Add(-time.Hour), now.Add(time.Hour)

	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	cliente := miembro(mems, "u-cli", "s1", domain.RoleClient)
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	dueña := miembro(mems, "u-due", "s1", domain.RoleOwner)

	for _, m := range []domain.Membership{profe, cliente} {
		if _, err := saleSvc.ListSales(m, "s1", from, to); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("ventas(%s) debe dar Forbidden, dio %v", m.Role, err)
		}
		if _, err := closeSvc.ListCloses(m, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("cierres(%s) debe dar Forbidden, dio %v", m.Role, err)
		}
		if _, err := dashSvc.Today(m, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("dashboard(%s) debe dar Forbidden, dio %v", m.Role, err)
		}
	}
	for _, m := range []domain.Membership{recep, dueña} {
		if _, err := saleSvc.ListSales(m, "s1", from, to); err != nil {
			t.Fatalf("ventas(%s) debe poder leer: %v", m.Role, err)
		}
		if _, err := closeSvc.ListCloses(m, "s1"); err != nil {
			t.Fatalf("cierres(%s) debe poder leer: %v", m.Role, err)
		}
		if _, err := dashSvc.Today(m, "s1"); err != nil {
			t.Fatalf("dashboard(%s) debe poder leer: %v", m.Role, err)
		}
	}
}
