package products

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// producto siembra un producto en el fake y devuelve su id.
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

func newProductEnv() (*testutil.FakeProducts, *testutil.FakeMemberships, *testutil.FakeEvents, *testutil.FakeStockNotifier, ProductService) {
	inv := &testutil.FakeProducts{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeStockNotifier{}
	svc := ProductService{Products: inv, Memberships: mems, Events: events, Notifier: notifier}
	return inv, mems, events, notifier, svc
}

// D7: leer el catálogo y el stock = owner/admin/recepción; ESCRIBIR (alta,
// edición, ajuste y movimientos) = solo owner/admin. Profesional y cliente 403.
// Un actor de otro salón es 403.
func TestProductService_permissions(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	admin := testutil.Miembro(mems, "admin", "s1", domain.RoleAdmin)
	recep := testutil.Miembro(mems, "recep", "s1", domain.RoleReception)
	prof := testutil.Miembro(mems, "prof", "s1", domain.RoleProfessional)
	client := testutil.Miembro(mems, "client", "s1", domain.RoleClient)
	otherSalon := testutil.Miembro(mems, "owner2", "s2", domain.RoleOwner)
	producto(t, inv, "s1", "Shampoo", 1000, 3, 1)

	for _, actor := range []domain.Membership{owner, admin} {
		if _, err := svc.Create(actor, "s1", "Nuevo "+actor.UserID, 100, nil, nil); err != nil {
			t.Fatalf("%s crea: %v", actor.Role, err)
		}
	}
	// Recepción lista (ve el stock) pero no escribe ni ve movimientos.
	if list, err := svc.List(recep, "s1"); err != nil || len(list) == 0 {
		t.Fatalf("recepción lista: %v %d", err, len(list))
	}
	if _, err := svc.Create(recep, "s1", "Cera", 100, nil, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no crea: %v", err)
	}
	if _, err := svc.Update(recep, "s1", inv.All[0].ID, "X", 1, 0, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no edita: %v", err)
	}
	if _, err := svc.Adjust(recep, "s1", inv.All[0].ID, 1, "restock", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no ajusta: %v", err)
	}
	if _, err := svc.Movements(recep, "s1", inv.All[0].ID, 50); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no ve movimientos: %v", err)
	}
	for _, actor := range []domain.Membership{prof, client, otherSalon} {
		if _, err := svc.List(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no lista: %v", actor.Role, err)
		}
		if _, err := svc.Create(actor, "s1", "X", 1, nil, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no crea: %v", actor.Role, err)
		}
	}
}

// Alta: stock y umbral opcionales (0 por defecto) y movimiento `initial` si el
// stock inicial no es cero. Un nombre activo repetido (sin importar
// mayúsculas) es 409 con el texto del contrato.
func TestProductService_create(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)

	p, err := svc.Create(owner, "s1", "  Shampoo ", 150000, nil, nil)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	if p.Name != "Shampoo" || p.Stock != 0 || p.LowStockThreshold != 0 || !p.Active {
		t.Fatalf("defaults: %+v", p)
	}
	if len(inv.Movements) != 0 {
		t.Fatalf("stock 0 no escribe movimiento: %+v", inv.Movements)
	}
	stock, low := 4, 2
	withStock, err := svc.Create(owner, "s1", "Cera", 5000, &stock, &low)
	if err != nil {
		t.Fatalf("crear con stock: %v", err)
	}
	if len(inv.Movements) != 1 || inv.Movements[0].Delta != 4 || inv.Movements[0].Reason != domain.StockInitial {
		t.Fatalf("movimiento inicial: %+v", inv.Movements)
	}
	if withStock.Stock != 4 || withStock.LowStockThreshold != 2 {
		t.Fatalf("stock/umbral: %+v", withStock)
	}

	_, err = svc.Create(owner, "s1", "shampoo", 100, nil, nil)
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgProductNameTaken {
		t.Fatalf("nombre repetido: %v", err)
	}
	// Desactivado libera el nombre.
	if _, err := svc.Update(owner, "s1", p.ID, p.Name, p.PriceCents, p.LowStockThreshold, false); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if _, err := svc.Create(owner, "s1", "shampoo", 200, nil, nil); err != nil {
		t.Fatalf("nombre libre: %v", err)
	}
	// Validación de dominio propagada.
	if _, err := svc.Create(owner, "s1", "  ", 1, nil, nil); err == nil || err.Error() != domain.MsgProductNameRequired {
		t.Fatalf("nombre vacío: %v", err)
	}
	// No se puede crear en otro salón ni con un producto de otro tenant.
	if _, err := svc.Create(owner, "s2", "X", 1, nil, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}
}

// PUT reemplaza los cuatro campos y NO toca el stock; un id ajeno es 404.
func TestProductService_update(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	p := producto(t, inv, "s1", "Shampoo", 1000, 7, 1)
	producto(t, inv, "s1", "Cera", 5000, 2, 0)
	other := producto(t, inv, "s2", "Mascarilla", 5000, 2, 0)

	up, err := svc.Update(owner, "s1", p.ID, "Acondicionador", 2000, 3, true)
	if err != nil {
		t.Fatalf("editar: %v", err)
	}
	if up.Name != "Acondicionador" || up.PriceCents != 2000 || up.LowStockThreshold != 3 || up.Stock != 7 {
		t.Fatalf("campos: %+v", up)
	}
	if _, err := svc.Update(owner, "s1", p.ID, "Cera", 1, 0, true); err == nil {
		t.Fatal("no puede robar el nombre de otro producto")
	}
	if _, err := svc.Update(owner, "s1", other.ID, "X", 1, 0, true); !errors.Is(err, domain.ErrNotFound) || err.Error() != domain.MsgProductNotFound {
		t.Fatalf("producto de otro salón: %v", err)
	}
	if _, err := svc.Update(owner, "s2", p.ID, "X", 1, 0, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("actor de otro salón: %v", err)
	}
}

// D8: el aviso de stock bajo sale UNA sola vez al cruzar el umbral (y publica
// product.updated). Si ya estaba bajo, no hay aviso nuevo.
func TestProductService_adjust_lowStockNotice(t *testing.T) {
	inv, mems, events, notifier, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	// stock 6, umbral 5: bajar 1 cruza.
	p := producto(t, inv, "s1", "Shampoo", 1000, 6, 5)

	up, err := svc.Adjust(owner, "s1", p.ID, -1, "loss", "se rompió")
	if err != nil {
		t.Fatalf("ajuste: %v", err)
	}
	if up.Stock != 5 || !up.Low() {
		t.Fatalf("quedó stock %d low=%v", up.Stock, up.Low())
	}
	if len(notifier.All) != 1 || notifier.All[0].Title != "Stock bajo" {
		t.Fatalf("aviso de stock bajo: %+v", notifier.All)
	}
	if !strings.Contains(notifier.All[0].Body, "Shampoo") || !strings.Contains(notifier.All[0].Body, "5") {
		t.Fatalf("el aviso nombra el producto y el stock: %q", notifier.All[0].Body)
	}
	if len(events.All) != 1 || events.All[0].Type != "product.updated" {
		t.Fatalf("evento product.updated: %+v", events.All)
	}

	// Ya estaba bajo: otro ajuste NO avisa de nuevo.
	if _, err := svc.Adjust(owner, "s1", p.ID, -1, "loss", ""); err != nil {
		t.Fatalf("segundo ajuste: %v", err)
	}
	if len(notifier.All) != 1 || len(events.All) != 1 {
		t.Fatalf("no debe avisar dos veces: %d avisos, %d eventos", len(notifier.All), len(events.All))
	}

	// Reponer por encima del umbral no avisa; volver a cruzar SÍ (otra vez).
	if _, err := svc.Adjust(owner, "s1", p.ID, 10, "restock", ""); err != nil {
		t.Fatalf("reponer: %v", err)
	}
	if _, err := svc.Adjust(owner, "s1", p.ID, -10, "loss", ""); err != nil {
		t.Fatalf("volver a cruzar: %v", err)
	}
	if len(notifier.All) != 2 {
		t.Fatalf("un segundo cruce avisa otra vez: %d", len(notifier.All))
	}
}

func TestProductService_adjust_validation(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	p := producto(t, inv, "s1", "Shampoo", 1000, 2, 0)

	for _, tc := range []struct {
		delta  int
		reason string
		want   string
		kind   error
	}{
		{0, "restock", domain.MsgProductAdjustZero, domain.ErrValidation},
		{1, "initial", domain.MsgProductAdjustReason, domain.ErrValidation},
		{1, "sale", domain.MsgProductAdjustReason, domain.ErrValidation},
		{1, "otro", domain.MsgProductAdjustReason, domain.ErrValidation},
		{-1, "restock", domain.MsgProductAdjustSign, domain.ErrValidation},
		{1, "loss", domain.MsgProductAdjustSign, domain.ErrValidation},
		{-3, "loss", domain.MsgProductNegativeStock, domain.ErrAlreadyExists},
	} {
		_, err := svc.Adjust(owner, "s1", p.ID, tc.delta, tc.reason, "")
		if err == nil || err.Error() != tc.want || !errors.Is(err, tc.kind) {
			t.Fatalf("delta=%d reason=%s: %v, quería %q", tc.delta, tc.reason, err, tc.want)
		}
	}
	// Nada de eso escribió un movimiento ni cambió el stock.
	if len(inv.Movements) != 1 || inv.All[0].Stock != 2 {
		t.Fatalf("un ajuste inválido no escribe: %+v stock=%d", inv.Movements, inv.All[0].Stock)
	}
	if _, err := svc.Adjust(owner, "s1", "no-existe", 1, "restock", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("producto inexistente: %v", err)
	}
}

// Movimientos: más reciente primero, limit 1..100 (default 50) y 404 del
// producto de otro salón.
func TestProductService_movements(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	owner := testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
	p := producto(t, inv, "s1", "Shampoo", 1000, 5, 0)
	for i := 0; i < 3; i++ {
		if _, err := svc.Adjust(owner, "s1", p.ID, 1, "restock", ""); err != nil {
			t.Fatalf("ajuste %d: %v", i, err)
		}
	}
	mv, err := svc.Movements(owner, "s1", p.ID, 0)
	if err != nil || len(mv) != 4 {
		t.Fatalf("default 50: %v %d", err, len(mv))
	}
	if mv[0].Reason != domain.StockRestock || mv[len(mv)-1].Reason != domain.StockInitial {
		t.Fatalf("más reciente primero: %+v", mv)
	}
	if got, err := svc.Movements(owner, "s1", p.ID, 2); err != nil || len(got) != 2 {
		t.Fatalf("limit 2: %v %d", err, len(got))
	}
	for _, bad := range []int{-1, 101} {
		if _, err := svc.Movements(owner, "s1", p.ID, bad); err == nil || err.Error() != domain.MsgProductLimit {
			t.Fatalf("limit %d: %v", bad, err)
		}
	}
	foreign := producto(t, inv, "s2", "Cera", 5000, 1, 0)
	if _, err := svc.Movements(owner, "s1", foreign.ID, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("producto de otro salón: %v", err)
	}
}

// Listado: activos e inactivos ordenados por nombre, con `low` calculado.
func TestProductService_list(t *testing.T) {
	inv, mems, _, _, svc := newProductEnv()
	mems.Save(domain.Membership{UserID: "recep", SalonID: "s1", Role: domain.RoleReception})
	recep, _ := mems.Find("recep", "s1")
	producto(t, inv, "s1", "Zeta", 100, 0, 0)
	producto(t, inv, "s1", "Alfa", 100, 2, 2)
	p := producto(t, inv, "s1", "Mica", 100, 9, 0)
	if _, err := svc.Update(recepActorOwner(t, mems), "s1", p.ID, "Mica", 100, 0, false); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	list, err := svc.List(recep, "s1")
	if err != nil || len(list) != 3 {
		t.Fatalf("listar: %v %d", err, len(list))
	}
	if list[0].Name != "Alfa" || list[1].Name != "Mica" || list[2].Name != "Zeta" {
		t.Fatalf("orden: %+v", list)
	}
	if !list[0].Low() || list[1].Low() || list[2].Low() {
		t.Fatalf("low: %+v", list)
	}
}

// recepActorOwner siembra un owner para las operaciones de escritura del test.
func recepActorOwner(t *testing.T, mems *testutil.FakeMemberships) domain.Membership {
	t.Helper()
	return testutil.Miembro(mems, "owner", "s1", domain.RoleOwner)
}
