package pos

import (
	"errors"
	"fmt"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/products"
	"salon-saas/internal/testutil"
)

// producto siembra un producto en el fake y devuelve su ficha (la copia vive en
// internal/products: los tests del POS la necesitan mientras la venta con
// productos viva acá).
func producto(t *testing.T, inv *testutil.FakeProducts, salonID, name string, price, stock, low int) domain.Product {
	t.Helper()
	p, err := domain.NewProduct(salonID, name, price, stock, low)
	if err != nil {
		t.Fatalf("producto %q: %v", name, err)
	}
	var initial *domain.StockMovement
	if stock > 0 {
		initial = &domain.StockMovement{
			ID: domain.NewID(), SalonID: salonID, ProductID: p.ID,
			Delta: stock, Reason: domain.StockInitial,
		}
	}
	if err := inv.Create(p, initial); err != nil {
		t.Fatalf("guardar producto %q: %v", name, err)
	}
	return p
}

// ventaEnv arma un POS con catálogo de servicios y productos del salón "s1".
type ventaEnv struct {
	sales    *fakeSales
	productS *testutil.FakeProductSales
	inv      *testutil.FakeProducts
	services *testutil.FakeServices
	mems     *testutil.FakeMemberships
	events   *testutil.FakeEvents
	notifier *testutil.FakeStockNotifier
	svc      SaleService
}

func newVentaEnv(t *testing.T) *ventaEnv {
	t.Helper()
	inv := &testutil.FakeProducts{}
	sales := &fakeSales{}
	mems := &testutil.FakeMemberships{}
	services := &testutil.FakeServices{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeStockNotifier{}
	productS := &testutil.FakeProductSales{Inv: inv, Sales: sales}
	catalogo(t, services, "s1")
	testutil.Miembro(mems, "recep", "s1", domain.RoleReception)
	testutil.Miembro(mems, "prof", "s1", domain.RoleProfessional)
	return &ventaEnv{
		sales: sales, productS: productS, inv: inv, services: services,
		mems: mems, events: events, notifier: notifier,
		svc: SaleService{
			Sales: sales, Services: services, Memberships: mems, Events: events,
			Products: inv, ProductSales: productS, Notifier: notifier,
		},
	}
}

func (e *ventaEnv) line(productID string, qty int) domain.SaleProduct {
	return domain.SaleProduct{ProductID: productID, Qty: qty}
}

// D5: una venta solo de productos es válida; el total sale del catálogo y el
// stock baja con su movimiento `sale`.
func TestSaleServiceWithProducts_productsOnly(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	shampoo := producto(t, env.inv, "s1", "Shampoo", 50000, 5, 0)
	cera := producto(t, env.inv, "s1", "Cera", 30000, 2, 0)

	sale, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "",
		nil, []domain.SaleProduct{env.line(shampoo.ID, 2), env.line(cera.ID, 1)}, domain.MethodCard, "")
	if err != nil {
		t.Fatalf("venta de productos: %v", err)
	}
	if sale.TotalCents != 130000 {
		t.Fatalf("total = %d, quería 130000", sale.TotalCents)
	}
	if len(env.sales.All) != 1 || len(env.productS.All) != 1 {
		t.Fatalf("no guardó la venta: %+v %+v", env.sales.All, env.productS.All)
	}
	if env.productS.All[0].ProductsCents != 130000 || len(env.productS.All[0].Products) != 2 {
		t.Fatalf("líneas de producto guardadas: %+v", env.productS.All[0])
	}
	got, _ := env.inv.FindByID("s1", shampoo.ID)
	if got.Stock != 3 {
		t.Fatalf("stock del shampoo = 3, dio %d", got.Stock)
	}
	gotCera, _ := env.inv.FindByID("s1", cera.ID)
	if gotCera.Stock != 1 {
		t.Fatalf("stock de la cera = 1, dio %d", gotCera.Stock)
	}
	saleMoves := 0
	for _, m := range env.inv.Movements {
		if m.Reason == domain.StockSale {
			saleMoves++
			if m.SaleID != sale.ID {
				t.Fatalf("el movimiento tiene que apuntar a la venta: %+v", m)
			}
		}
	}
	if saleMoves != 2 {
		t.Fatalf("un movimiento `sale` por línea: %d", saleMoves)
	}
	// El evento sale.created sigue saliendo con el total real.
	if len(env.events.All) != 1 || env.events.All[0].Type != "sale.created" {
		t.Fatalf("evento: %+v", env.events.All)
	}
}

// D5: una venta mixta suma servicios + productos; el precio SIEMPRE sale del
// catálogo del servidor (un precio enviado se ignora) y se congela.
func TestSaleServiceWithProducts_mixedAndSnapshot(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	corte := svcIDByName(t, env.services, "s1", "Corte")
	shampoo := producto(t, env.inv, "s1", "Shampoo", 50000, 3, 0)

	sale, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "",
		[]domain.SaleItem{{ServiceID: corte, Qty: 1, UnitPriceCents: 1}},
		// Precio manipulado por el cliente: se ignora.
		[]domain.SaleProduct{{ProductID: shampoo.ID, Qty: 2, UnitPriceCents: 1}},
		domain.MethodCash, "")
	if err != nil {
		t.Fatalf("venta mixta: %v", err)
	}
	if sale.TotalCents != 250000 {
		t.Fatalf("total = %d, quería 150000 + 100000", sale.TotalCents)
	}
	if env.productS.All[0].ProductsCents != 100000 {
		t.Fatalf("products_cents = %d", env.productS.All[0].ProductsCents)
	}
	if env.productS.All[0].Products[0].UnitPriceCents != 50000 {
		t.Fatalf("precio congelado del catálogo: %+v", env.productS.All[0].Products[0])
	}

	// Cambiar el precio después NO toca la venta guardada.
	owner := testutil.Miembro(env.mems, "owner", "s1", domain.RoleOwner)
	svc := products.ProductService{Products: env.inv, Memberships: env.mems}
	if _, err := svc.Update(owner, "s1", shampoo.ID, "Shampoo", 90000, 0, true); err != nil {
		t.Fatalf("cambiar precio: %v", err)
	}
	if env.productS.All[0].Products[0].UnitPriceCents != 50000 || env.productS.All[0].TotalCents != 250000 {
		t.Fatalf("la venta cambió con el catálogo: %+v", env.productS.All[0])
	}
}

// D5: un producto repetido en el pedido se SUMA en una sola línea.
func TestSaleServiceWithProducts_repeatedLinesSummed(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	shampoo := producto(t, env.inv, "s1", "Shampoo", 10000, 10, 0)

	sale, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "",
		nil, []domain.SaleProduct{env.line(shampoo.ID, 2), env.line(shampoo.ID, 3)}, domain.MethodCash, "")
	if err != nil {
		t.Fatalf("venta: %v", err)
	}
	if sale.TotalCents != 50000 {
		t.Fatalf("total = %d, quería 5 x 10000", sale.TotalCents)
	}
	if len(env.productS.All[0].Products) != 1 || env.productS.All[0].Products[0].Qty != 5 {
		t.Fatalf("se suma en una línea: %+v", env.productS.All[0].Products)
	}
	got, _ := env.inv.FindByID("s1", shampoo.ID)
	if got.Stock != 5 {
		t.Fatalf("stock = 5, dio %d", got.Stock)
	}
}

// D5: tope de 50 líneas de producto (después de sumar repetidos).
func TestSaleServiceWithProducts_lineCap(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	lines := make([]domain.SaleProduct, 0, 51)
	for i := 0; i < 51; i++ {
		p := producto(t, env.inv, "s1", fmt.Sprintf("P%02d", i), 100, 1, 0)
		lines = append(lines, env.line(p.ID, 1))
	}
	_, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil, lines, domain.MethodCash, "")
	if err == nil || err.Error() != domain.MsgProductLines {
		t.Fatalf("51 líneas: %v", err)
	}
	if len(env.sales.All) != 0 || len(env.productS.All) != 0 {
		t.Fatalf("no se guarda nada: %+v", env.sales.All)
	}
}

// Producto inexistente, de otro salón o inactivo: 400 «Producto inválido» y no
// se guarda nada. Sin stock: 409 con el nombre, y tampoco se guarda nada.
func TestSaleServiceWithProducts_invalidAndNoStock(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	ajeno := producto(t, env.inv, "s2", "Ajeno", 1000, 10, 0)
	inactivo := producto(t, env.inv, "s1", "Viejo", 1000, 10, 0)
	owner := testutil.Miembro(env.mems, "owner", "s1", domain.RoleOwner)
	if _, err := (products.ProductService{Products: env.inv, Memberships: env.mems}).Update(owner, "s1", inactivo.ID, "Viejo", 1000, 0, false); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	casi := producto(t, env.inv, "s1", "Casi", 1000, 1, 0)

	for _, id := range []string{"no-existe", ajeno.ID, inactivo.ID} {
		_, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil,
			[]domain.SaleProduct{env.line(id, 1)}, domain.MethodCash, "")
		if err == nil || err.Error() != domain.MsgProductInvalid || !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("producto %q: %v", id, err)
		}
	}
	// El segundo producto no alcanza: se deshace TODO (el primero tampoco baja).
	_, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil,
		[]domain.SaleProduct{env.line(casi.ID, 2)}, domain.MethodCash, "")
	if err == nil || err.Error() != domain.NoStockMessage("Casi") || !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin stock: %v", err)
	}
	if got, _ := env.inv.FindByID("s1", casi.ID); got.Stock != 1 {
		t.Fatalf("el stock no cambia: %d", got.Stock)
	}
	if len(env.sales.All) != 0 || len(env.productS.All) != 0 {
		t.Fatalf("no se guarda la venta fallida: %+v", env.sales.All)
	}
}

// D4: el repetido por client_op_id devuelve la venta original SIN descontar
// otra vez.
func TestSaleServiceWithProducts_idempotent(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	shampoo := producto(t, env.inv, "s1", "Shampoo", 10000, 5, 0)
	lines := []domain.SaleProduct{env.line(shampoo.ID, 2)}

	first, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil, lines, domain.MethodCash, "op-1")
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	second, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil, lines, domain.MethodCash, "op-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.ID != first.ID || second.TotalCents != first.TotalCents {
		t.Fatalf("replay distinto: %+v vs %+v", second, first)
	}
	if got, _ := env.inv.FindByID("s1", shampoo.ID); got.Stock != 3 {
		t.Fatalf("descontó dos veces: stock %d", got.Stock)
	}
	if len(env.sales.All) != 1 {
		t.Fatalf("una sola venta: %d", len(env.sales.All))
	}
}

// D7: vender productos es `puedeVender`: recepción/owner/admin sí; el
// profesional no. Un actor de otro salón no.
func TestSaleServiceWithProducts_permissions(t *testing.T) {
	env := newVentaEnv(t)
	shampoo := producto(t, env.inv, "s1", "Shampoo", 10000, 5, 0)
	lines := []domain.SaleProduct{env.line(shampoo.ID, 1)}
	recep, _ := env.mems.Find("recep", "s1")
	prof, _ := env.mems.Find("prof", "s1")
	other := testutil.Miembro(env.mems, "otro", "s2", domain.RoleOwner)

	for _, actor := range []domain.Membership{prof, other} {
		if _, err := env.svc.RegisterSaleWithProducts(actor, "s1", "prof", "Lía", "", nil, lines, domain.MethodCash, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: %v", actor.Role, err)
		}
	}
	// Owner y admin también venden.
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin} {
		actor := testutil.Miembro(env.mems, "u-"+string(role), "s1", role)
		if _, err := env.svc.RegisterSaleWithProducts(actor, "s1", "prof", "Lía", "", nil, lines, domain.MethodCash, ""); err != nil {
			t.Fatalf("%s vende: %v", role, err)
		}
	}
	_ = recep
}

// D8: el aviso de stock bajo de una venta sale UNA vez al cruzar el umbral.
func TestSaleServiceWithProducts_lowStockNotice(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	shampoo := producto(t, env.inv, "s1", "Shampoo", 10000, 3, 2)

	if _, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil,
		[]domain.SaleProduct{env.line(shampoo.ID, 1)}, domain.MethodCash, ""); err != nil {
		t.Fatalf("venta: %v", err)
	}
	if len(env.notifier.All) != 1 || env.notifier.All[0].Title != "Stock bajo" {
		t.Fatalf("aviso: %+v", env.notifier.All)
	}
	var updated bool
	for _, ev := range env.events.All {
		if ev.Type == "product.updated" {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("falta product.updated: %+v", env.events.All)
	}
	// Segunda venta: ya estaba bajo, no avisa de nuevo.
	if _, err := env.svc.RegisterSaleWithProducts(recep, "s1", "prof", "Lía", "", nil,
		[]domain.SaleProduct{env.line(shampoo.ID, 1)}, domain.MethodCash, ""); err != nil {
		t.Fatalf("segunda venta: %v", err)
	}
	if len(env.notifier.All) != 1 {
		t.Fatalf("avisó dos veces: %d", len(env.notifier.All))
	}
}

// PRESERVE: una venta SIN productos sigue por Sales.Save (sin tocar productos).
func TestSaleService_withoutProducts_untouched(t *testing.T) {
	env := newVentaEnv(t)
	recep, _ := env.mems.Find("recep", "s1")
	corte := svcIDByName(t, env.services, "s1", "Corte")
	sale, err := env.svc.RegisterSale(recep, "s1", "prof", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 1, UnitPriceCents: 150000},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("venta clásica: %v", err)
	}
	if sale.TotalCents != 150000 || len(env.sales.All) != 1 || len(env.productS.All) != 0 {
		t.Fatalf("la venta clásica no toca productos: %+v %+v", env.sales.All, env.productS.All)
	}
}
