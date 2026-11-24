package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// pkgEnv arma un salón s1 (zona de Auckland, lejos de UTC) con servicio, clienta
// y un reloj fijo.
type pkgEnv struct {
	svc      PackageService
	mems     *fakeMemberships
	services *fakeServices
	clients  *fakeClients
	catalog  *fakePackageCatalog
	owned    *fakeClientPackages
	settings *fakeSettings
	events   *fakeEvents
	clock    time.Time

	owner, admin, recep, prof, client, other domain.Membership
	corte                                    domain.Service
	lia                                      domain.Client
}

func newPkgEnv(t *testing.T) *pkgEnv {
	t.Helper()
	e := &pkgEnv{
		mems:     &fakeMemberships{},
		services: &fakeServices{},
		clients:  &fakeClients{},
		settings: &fakeSettings{bySalon: map[string]domain.SalonSettings{
			"s1": {SalonID: "s1", Timezone: "Pacific/Auckland", OpenTime: "09:00", CloseTime: "18:00", SlotStepMin: 30},
		}},
		events: &fakeEvents{},
		// 11 de marzo 00:30 en Auckland (NZDT +13) = 10 de marzo 11:30 UTC.
		clock: time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC),
	}
	e.catalog = &fakePackageCatalog{services: e.services}
	e.owned = &fakeClientPackages{services: e.services}
	e.svc = PackageService{
		Packages: e.catalog, ClientPackages: e.owned, Services: e.services, Clients: e.clients,
		Memberships: e.mems, Settings: e.settings, Events: e.events,
		Now: func() time.Time { return e.clock },
	}
	e.owner = miembro(e.mems, "owner", "s1", domain.RoleOwner)
	e.admin = miembro(e.mems, "admin", "s1", domain.RoleAdmin)
	e.recep = miembro(e.mems, "recep", "s1", domain.RoleReception)
	e.prof = miembro(e.mems, "prof", "s1", domain.RoleProfessional)
	// El profesional atiende (los profesionales siempre atienden).
	_ = e.mems.Save(domain.Membership{UserID: "prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	e.prof, _ = e.mems.Find("prof", "s1")
	e.client = miembro(e.mems, "client", "s1", domain.RoleClient)
	e.other = miembro(e.mems, "owner2", "s2", domain.RoleOwner)

	svc, err := domain.NewService("s1", "Corte", 45, 15000, 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.services.Save(svc)
	e.corte = svc
	e.lia = e.newClient(t, "s1", "Lía Pérez")
	return e
}

func (e *pkgEnv) newClient(t *testing.T, salonID, name string) domain.Client {
	t.Helper()
	c, err := domain.NewClient(salonID, name, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.clients.Save(c)
	return c
}

func (e *pkgEnv) newCatalogPkg(t *testing.T, name string, sessions, price int, validity *int) domain.ServicePackage {
	t.Helper()
	p, err := e.svc.CreateCatalog(e.owner, "s1", name, e.corte.ID, sessions, price, validity)
	if err != nil {
		t.Fatalf("crear paquete %q: %v", name, err)
	}
	return p
}

func (e *pkgEnv) buy(t *testing.T, p domain.ServicePackage, op string) domain.ClientPackage {
	t.Helper()
	cp, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.MethodCash, op)
	if err != nil {
		t.Fatalf("comprar: %v", err)
	}
	return cp
}

// nothingWritten comprueba que un rechazo no dejó NADA en las tablas de F8.
func (e *pkgEnv) nothingWritten(t *testing.T, what string) {
	t.Helper()
	if len(e.owned.sales) != 0 || len(e.owned.packages) != 0 || len(e.owned.movements) != 0 {
		t.Fatalf("%s escribió: %d ventas, %d paquetes, %d movimientos", what, len(e.owned.sales), len(e.owned.packages), len(e.owned.movements))
	}
}

func wantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("se esperaba el error %q", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error %q (%v), quería %q de la familia %v", err.Error(), err, text, kind)
	}
}

// deactivated deja a la persona como miembro dado de baja.
func (e *pkgEnv) deactivate(userID string) domain.Membership {
	key := userID + "|s1"
	m := e.mems.byKey[key]
	at := e.clock.Add(-time.Hour)
	m.EndedAt = &at
	e.mems.byKey[key] = m
	return m
}

// --- D10: catálogo ---------------------------------------------------------

func TestPackageService_catalog_permissions(t *testing.T) {
	e := newPkgEnv(t)
	for _, actor := range []domain.Membership{e.owner, e.admin} {
		if _, err := e.svc.CreateCatalog(actor, "s1", "Pack "+actor.UserID, e.corte.ID, 5, 1000, nil); err != nil {
			t.Fatalf("%s crea: %v", actor.Role, err)
		}
	}
	p := e.newCatalogPkg(t, "Pack base", 5, 1000, nil)
	// Recepción lista (los necesita para vender) pero no escribe.
	if list, err := e.svc.ListCatalog(e.recep, "s1"); err != nil || len(list) != 3 {
		t.Fatalf("recepción lista: %v %d", err, len(list))
	}
	if _, err := e.svc.CreateCatalog(e.recep, "s1", "X", e.corte.ID, 1, 1, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no crea: %v", err)
	}
	if _, err := e.svc.UpdateCatalog(e.recep, "s1", p.ID, "X", 1, nil, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción no edita: %v", err)
	}
	removed := e.deactivate("admin")
	for _, actor := range []domain.Membership{e.prof, e.client, e.other, removed} {
		if _, err := e.svc.ListCatalog(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no lista: %v", actor.UserID, actor.Role, err)
		}
		if _, err := e.svc.CreateCatalog(actor, "s1", "X", e.corte.ID, 1, 1, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no crea: %v", actor.UserID, actor.Role, err)
		}
		if _, err := e.svc.UpdateCatalog(actor, "s1", p.ID, "X", 1, nil, true); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no edita: %v", actor.UserID, actor.Role, err)
		}
	}
	if len(e.catalog.all) != 3 {
		t.Fatalf("un rechazo escribió: %d", len(e.catalog.all))
	}
}

func TestPackageService_catalog_create_rules(t *testing.T) {
	e := newPkgEnv(t)
	inactive, _ := domain.NewService("s1", "Viejo", 30, 1000, 0)
	inactive.Active = false
	_ = e.services.Save(inactive)
	foreign, _ := domain.NewService("s2", "Ajeno", 30, 1000, 0)
	_ = e.services.Save(foreign)

	// Servicio inexistente, de otro salón o inactivo: 400 «Paquete inválido».
	for _, sid := range []string{"no-existe", foreign.ID, inactive.ID, "a\x00b"} {
		_, err := e.svc.CreateCatalog(e.owner, "s1", "Pack", sid, 5, 1000, nil)
		wantMsg(t, err, domain.ErrValidation, "Paquete inválido")
	}
	if len(e.catalog.all) != 0 {
		t.Fatalf("un 400 escribió")
	}
	// Validaciones del dominio: 400 y nada escrito.
	for _, c := range []struct {
		name            string
		sessions, price int
		validity        *int
		wantText        string
	}{
		{"", 5, 100, nil, "El nombre del paquete es obligatorio"},
		{"Pack\x00", 5, 100, nil, domain.MsgPackageInvalidChars},
		{"Pack\nX", 5, 100, nil, domain.MsgPackageInvalidChars},
		{"Pack", 0, 100, nil, "Las sesiones deben estar entre 1 y 100"},
		{"Pack", 5, 10_000_001, nil, "El precio debe estar entre 0 y 10000000 centavos"},
		{"Pack", 5, 100, pint(0), "La vigencia debe estar entre 1 y 3650 días"},
	} {
		_, err := e.svc.CreateCatalog(e.owner, "s1", c.name, e.corte.ID, c.sessions, c.price, c.validity)
		wantMsg(t, err, domain.ErrValidation, c.wantText)
	}
	if len(e.catalog.all) != 0 {
		t.Fatalf("un 400 escribió")
	}
	// Nombre activo repetido sin importar mayúsculas: 409; el inactivo libera el nombre.
	first := e.newCatalogPkg(t, "Pack Cinco", 5, 1000, nil)
	_, err := e.svc.CreateCatalog(e.admin, "s1", "pack cinco", e.corte.ID, 3, 500, nil)
	wantMsg(t, err, domain.ErrAlreadyExists, "Ya hay un paquete activo con ese nombre")
	if _, err := e.svc.UpdateCatalog(e.owner, "s1", first.ID, "Pack Cinco", 1000, nil, false); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if _, err := e.svc.CreateCatalog(e.admin, "s1", "pack cinco", e.corte.ID, 3, 500, nil); err != nil {
		t.Fatalf("el inactivo libera el nombre: %v", err)
	}
	// El catálogo trae el nombre del servicio.
	list, _ := e.svc.ListCatalog(e.owner, "s1")
	for _, p := range list {
		if p.ServiceName != "Corte" {
			t.Fatalf("falta el nombre del servicio: %+v", p)
		}
	}
}

func TestPackageService_catalog_update_replaces(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, pint(30))
	other := e.newCatalogPkg(t, "Otro", 3, 500, nil)

	// validity nil = SIN vencimiento (reemplaza, no conserva los 30 días).
	got, err := e.svc.UpdateCatalog(e.owner, "s1", p.ID, "Pack nuevo", 2000, nil, true)
	if err != nil || got.ValidityDays != nil || got.Name != "Pack nuevo" || got.PriceCents != 2000 || got.Sessions != 5 || got.ServiceID != e.corte.ID || got.ServiceName != "Corte" {
		t.Fatalf("update: %+v %v", got, err)
	}
	stored, _ := e.catalog.FindByID("s1", p.ID)
	if stored.ValidityDays != nil {
		t.Fatalf("no guardó la vigencia nula")
	}
	// 404 de un paquete inexistente o de otro salón.
	_, err = e.svc.UpdateCatalog(e.owner, "s1", "nope", "X", 1, nil, true)
	wantMsg(t, err, domain.ErrNotFound, "Paquete no encontrado")
	foreign, _ := domain.NewServicePackage("s2", "Ajeno", "svcx", 1, 1, nil)
	_ = e.catalog.Create(foreign)
	_, err = e.svc.UpdateCatalog(e.owner, "s1", foreign.ID, "X", 1, nil, true)
	wantMsg(t, err, domain.ErrNotFound, "Paquete no encontrado")
	// Nombre activo repetido: 409 y no cambia nada.
	_, err = e.svc.UpdateCatalog(e.owner, "s1", p.ID, "OTRO", 1, nil, true)
	wantMsg(t, err, domain.ErrAlreadyExists, "Ya hay un paquete activo con ese nombre")
	// NUL / salto de línea: 400 y no cambia nada.
	_, err = e.svc.UpdateCatalog(e.owner, "s1", p.ID, "A\x00B", 1, nil, true)
	wantMsg(t, err, domain.ErrValidation, domain.MsgPackageInvalidChars)
	after, _ := e.catalog.FindByID("s1", p.ID)
	if after.Name != "Pack nuevo" || after.PriceCents != 2000 {
		t.Fatalf("un rechazo cambió el paquete: %+v", after)
	}
	_ = other
}

// --- D5: comprar ---------------------------------------------------------------

func TestPackageService_purchase_complete(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack 5 cortes", 5, 60000, pint(1))

	cp := e.buy(t, p, "")
	// La venta: total = precio, sin líneas, la clienta y el método.
	if len(e.owned.sales) != 1 {
		t.Fatalf("ventas = %d", len(e.owned.sales))
	}
	s := e.owned.sales[0]
	if s.TotalCents != 60000 || len(s.Items) != 0 || s.ClientName != "Lía Pérez" || s.Method != domain.MethodCash ||
		s.ProfessionalID != "prof" || s.SalonID != "s1" || !s.CreatedAt.Equal(e.clock) {
		t.Fatalf("venta mal armada: %+v", s)
	}
	// El paquete: foto del precio y del nombre, saldo completo, vínculo a la venta.
	if cp.SaleID != s.ID || cp.ClientID != e.lia.ID || cp.PriceCents != 60000 || cp.Name != "Pack 5 cortes" ||
		cp.SessionsTotal != 5 || cp.SessionsLeft != 5 || cp.ServiceName != "Corte" || cp.Expired {
		t.Fatalf("paquete mal armado: %+v", cp)
	}
	// Vigencia de 1 día desde el 11 de marzo local de Auckland: vence a fin del 12
	// (10:59:59.999999 UTC), NO a fin del 11 de UTC.
	want := time.Date(2026, 3, 12, 10, 59, 59, 999_999_000, time.UTC)
	if cp.ExpiresAt == nil || !cp.ExpiresAt.Equal(want) {
		t.Fatalf("vence %v, quería %v", cp.ExpiresAt, want)
	}
	// Un movimiento `purchase` con +sesiones y el invariante.
	if len(e.owned.movements) != 1 || e.owned.movements[0].Reason != domain.PackagePurchase || e.owned.movements[0].Delta != 5 {
		t.Fatalf("movimientos: %+v", e.owned.movements)
	}
	if e.owned.sumDelta(cp.ID) != cp.SessionsLeft {
		t.Fatalf("invariante roto")
	}
	if _, n := e.events.typed("sale.created"); n != 1 {
		t.Fatalf("sale.created = %d", n)
	}

	// SNAPSHOT: cambiar el catálogo después NO mueve lo comprado.
	if _, err := e.svc.UpdateCatalog(e.owner, "s1", p.ID, "Renombrado", 99999, nil, true); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.ListClientPackages(e.recep, "s1", e.lia.ID)
	if err != nil || len(list) != 1 || list[0].PriceCents != 60000 || list[0].Name != "Pack 5 cortes" {
		t.Fatalf("el snapshot se movió: %+v %v", list, err)
	}
	// Sin vigencia: no vence.
	free := e.newCatalogPkg(t, "Sin vencer", 2, 0, nil)
	if got := e.buy(t, free, ""); got.ExpiresAt != nil || got.PriceCents != 0 {
		t.Fatalf("sin vigencia: %+v", got)
	}
}

func TestPackageService_purchase_permissions(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		if _, err := e.svc.Purchase(actor, "s1", e.lia.ID, p.ID, "prof", domain.MethodCard, ""); err != nil {
			t.Fatalf("%s vende: %v", actor.Role, err)
		}
	}
	before := len(e.owned.sales)
	removed := e.deactivate("recep")
	for _, actor := range []domain.Membership{e.prof, e.client, e.other, removed} {
		if _, err := e.svc.Purchase(actor, "s1", e.lia.ID, p.ID, "prof", domain.MethodCard, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no vende: %v", actor.UserID, actor.Role, err)
		}
	}
	if len(e.owned.sales) != before {
		t.Fatalf("un 403 escribió")
	}
}

// Cada rechazo es 400/404 y NO escribe nada (ni venta, ni paquete, ni movimiento).
func TestPackageService_purchase_rejections_write_nothing(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	inactivePkg := e.newCatalogPkg(t, "Inactivo", 5, 1000, nil)
	_, _ = e.svc.UpdateCatalog(e.owner, "s1", inactivePkg.ID, "Inactivo", 1000, nil, false)
	foreignPkg, _ := domain.NewServicePackage("s2", "Ajeno", e.corte.ID, 5, 1000, nil)
	_ = e.catalog.Create(foreignPkg)
	// Paquete cuyo servicio se desactivó después.
	sv2, _ := domain.NewService("s1", "Tinte", 60, 5000, 0)
	_ = e.services.Save(sv2)
	pk2, err := e.svc.CreateCatalog(e.owner, "s1", "Pack tinte", sv2.ID, 3, 9000, nil)
	if err != nil {
		t.Fatal(err)
	}
	sv2.Active = false
	_ = e.services.Save(sv2)
	foreignClient := e.newClient(t, "s2", "Clienta ajena")

	for _, pid := range []string{"no-existe", inactivePkg.ID, foreignPkg.ID, pk2.ID, "x\x00y", ""} {
		_, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, pid, "prof", domain.MethodCash, "")
		wantMsg(t, err, domain.ErrValidation, "Paquete inválido")
	}
	// Clienta de otro salón o inexistente: 404 con el texto de las rutas de clientes.
	for _, cid := range []string{foreignClient.ID, "no-existe", "", "a\x00b"} {
		_, err := e.svc.Purchase(e.recep, "s1", cid, p.ID, "prof", domain.MethodCash, "")
		wantMsg(t, err, domain.ErrNotFound, "Ficha no encontrada")
	}
	// Profesional: validada igual que el POS (existe, es del salón y atiende).
	for _, pr := range []string{"", "ghost", "owner2", "recep", "a\x00b"} {
		if _, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, pr, domain.MethodCash, ""); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("profesional %q: %v", pr, err)
		}
	}
	// Método de pago desconocido y client_op_id inválido.
	if _, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.PaymentMethod("bitcoin"), ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("método: %v", err)
	}
	_, err = e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.MethodCash, "a\x00b")
	wantMsg(t, err, domain.ErrValidation, "El identificador de la operación no es válido")
	e.nothingWritten(t, "una compra rechazada")
}

// El dueño que atiende (attends) es un profesional válido, igual que en el POS.
func TestPackageService_purchase_professional_like_pos(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	// El owner sin attends NO atiende (regla F2a): rechazado como en el POS.
	if _, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "owner", domain.MethodCash, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("owner sin attends: %v", err)
	}
	_ = e.mems.Save(domain.Membership{UserID: "owner", SalonID: "s1", Role: domain.RoleOwner, Attends: true})
	if _, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "owner", domain.MethodCash, ""); err != nil {
		t.Fatalf("owner que atiende: %v", err)
	}
}

func TestPackageService_purchase_replay_by_op(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	first := e.buy(t, p, "op-compra")
	// El catálogo cambia y el paquete se desactiva: el repetido devuelve la
	// compra ORIGINAL igual (no se rompe porque el catálogo cambió).
	_, _ = e.svc.UpdateCatalog(e.owner, "s1", p.ID, "Pack", 7777, nil, false)
	second, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.MethodCard, "op-compra")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.ID != first.ID || second.SaleID != first.SaleID || second.PriceCents != 1000 {
		t.Fatalf("el replay no devolvió la original: %+v vs %+v", second, first)
	}
	if len(e.owned.sales) != 1 || len(e.owned.packages) != 1 || len(e.owned.movements) != 1 {
		t.Fatalf("el replay creó otra compra: %d/%d/%d", len(e.owned.sales), len(e.owned.packages), len(e.owned.movements))
	}
	// El mismo client_op_id para OTRA clienta: 409, no devuelve la compra ajena.
	other := e.newClient(t, "s1", "Otra Clienta")
	_, err = e.svc.Purchase(e.recep, "s1", other.ID, p.ID, "prof", domain.MethodCash, "op-compra")
	wantMsg(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
	if len(e.owned.sales) != 1 {
		t.Fatalf("escribió")
	}
}

// La carrera: otro pedido con el MISMO client_op_id gana entre el replay y el
// insert; el índice único rechaza este y se devuelve la compra que ganó.
func TestPackageService_purchase_replay_race(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	loc, _ := time.LoadLocation("Pacific/Auckland")
	e.owned.beforePurchase = func() {
		o, err := domain.NewPackagePurchase(p, e.lia, "prof", domain.MethodCash, "op-race", "x", e.clock, loc)
		if err != nil {
			t.Error(err)
			return
		}
		e.owned.mu.Lock()
		e.owned.sales = append(e.owned.sales, o.Sale)
		e.owned.packages = append(e.owned.packages, o.ClientPackage)
		e.owned.movements = append(e.owned.movements, o.Movement)
		e.owned.mu.Unlock()
	}
	cp, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.MethodCash, "op-race")
	if err != nil {
		t.Fatalf("la carrera perdida debe devolver la compra ganadora: %v", err)
	}
	if len(e.owned.sales) != 1 || cp.ID != e.owned.packages[0].ID {
		t.Fatalf("no devolvió la ganadora: %+v (%d ventas)", cp, len(e.owned.sales))
	}
}

// Una DB caída no deja nada y el error se propaga (nunca se come).
func TestPackageService_purchase_failure_writes_nothing(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	boom := errors.New("db caída")
	e.owned.failPurchase = boom
	if _, err := e.svc.Purchase(e.recep, "s1", e.lia.ID, p.ID, "prof", domain.MethodCash, "op-x"); !errors.Is(err, boom) {
		t.Fatalf("debe propagar el error: %v", err)
	}
	e.nothingWritten(t, "una compra con la DB caída")
	if _, n := e.events.typed("sale.created"); n != 0 {
		t.Fatalf("publicó una venta que no existe")
	}
}

// --- D7: consumir -----------------------------------------------------------------

func TestPackageService_redeem(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 2, 1000, nil)
	cp := e.buy(t, p, "")

	got, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "prof", " hoy corte ", "")
	if err != nil || got.SessionsLeft != 1 || got.ServiceName != "Corte" {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	m := e.owned.movements[len(e.owned.movements)-1]
	if m.Reason != domain.PackageRedeem || m.Delta != -1 || m.Note != "hoy corte" || m.ProfessionalID != "prof" || m.Actor != "recep" {
		t.Fatalf("movimiento: %+v", m)
	}
	// Consumir NO es una venta: sigue habiendo UNA sola.
	if len(e.owned.sales) != 1 {
		t.Fatalf("consumir creó una venta")
	}
	// Sin profesional ni nota también vale (son opcionales).
	if got, err = e.svc.Redeem(e.owner, "s1", e.lia.ID, cp.ID, "", "", ""); err != nil || got.SessionsLeft != 0 {
		t.Fatalf("redeem sin extras: %+v %v", got, err)
	}
	if e.owned.sumDelta(cp.ID) != 0 {
		t.Fatalf("invariante roto: Σ = %d", e.owned.sumDelta(cp.ID))
	}
	// Sin saldo: 409 con el texto del contrato y nada escrito.
	n := len(e.owned.movements)
	_, err = e.svc.Redeem(e.admin, "s1", e.lia.ID, cp.ID, "", "", "")
	wantMsg(t, err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete")
	if len(e.owned.movements) != n {
		t.Fatalf("un 409 escribió")
	}
}

func TestPackageService_redeem_permissions_and_isolation(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	cp := e.buy(t, p, "")
	removed := e.deactivate("admin")
	for _, actor := range []domain.Membership{e.prof, e.client, e.other, removed} {
		if _, err := e.svc.Redeem(actor, "s1", e.lia.ID, cp.ID, "", "", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no consume: %v", actor.UserID, actor.Role, err)
		}
	}
	// Otra clienta del MISMO salón, un id inexistente, otro salón y un NUL: 404.
	other := e.newClient(t, "s1", "Otra")
	foreign := e.newClient(t, "s2", "Ajena")
	for _, c := range []struct{ client, pkg string }{
		{other.ID, cp.ID}, {e.lia.ID, "nope"}, {foreign.ID, cp.ID}, {e.lia.ID, "a\x00b"}, {"a\x00b", cp.ID},
	} {
		_, err := e.svc.Redeem(e.recep, "s1", c.client, c.pkg, "", "", "")
		wantMsg(t, err, domain.ErrNotFound, "Paquete no encontrado")
	}
	// Una persona de OTRO salón tampoco lo consume actuando en el suyo.
	if _, err := e.svc.Redeem(e.other, "s2", e.lia.ID, cp.ID, "", "", ""); err == nil {
		t.Fatalf("el salón 2 consumió un paquete del 1")
	}
	// Nota con NUL o salto, profesional inválido, op inválido: 400 y nada escrito.
	for _, c := range []struct{ prof, note, op string }{
		{"", "a\x00b", ""}, {"", "a\nb", ""}, {"", strings.Repeat("x", 201), ""},
		{"ghost", "", ""}, {"recep", "", ""}, {"a\x00b", "", ""}, {"", "", "a\x00b"},
	} {
		if _, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, c.prof, c.note, c.op); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("redeem %+v: %v", c, err)
		}
	}
	if len(e.owned.movements) != 1 || e.owned.packages[0].SessionsLeft != 5 {
		t.Fatalf("un rechazo escribió: %d movimientos, saldo %d", len(e.owned.movements), e.owned.packages[0].SessionsLeft)
	}
}

func TestPackageService_redeem_expired(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 3, 1000, pint(1))
	cp := e.buy(t, p, "") // vence 2026-03-12T10:59:59.999999Z
	// Un instante antes del vencimiento todavía se consume.
	e.clock = time.Date(2026, 3, 12, 10, 59, 59, 0, time.UTC)
	if _, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", ""); err != nil {
		t.Fatalf("vigente: %v", err)
	}
	// Pasado el vencimiento: 409 «El paquete venció» y no descuenta.
	e.clock = time.Date(2026, 3, 12, 11, 0, 0, 0, time.UTC)
	_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "")
	wantMsg(t, err, domain.ErrAlreadyExists, "El paquete venció")
	if e.owned.packages[0].SessionsLeft != 2 || len(e.owned.movements) != 2 {
		t.Fatalf("un vencido descontó: saldo %d", e.owned.packages[0].SessionsLeft)
	}
	// La lectura lo marca vencido.
	list, _ := e.svc.ListClientPackages(e.recep, "s1", e.lia.ID)
	if len(list) != 1 || !list[0].Expired {
		t.Fatalf("expired: %+v", list)
	}
}

func TestPackageService_redeem_replay_by_op(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 1, 1000, nil)
	cp := e.buy(t, p, "")
	first, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "op-1")
	if err != nil || first.SessionsLeft != 0 {
		t.Fatalf("primero: %+v %v", first, err)
	}
	// El repetido (aunque ya no quede saldo) devuelve el estado actual SIN
	// descontar ni dar 409.
	again, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "op-1")
	if err != nil || again.SessionsLeft != 0 || again.ID != cp.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if len(e.owned.movements) != 2 {
		t.Fatalf("el replay escribió: %d movimientos", len(e.owned.movements))
	}
	// Otro op id sí choca con el saldo agotado.
	_, err = e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "op-2")
	wantMsg(t, err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete")
}

// --- D9: ajustar --------------------------------------------------------------------

func TestPackageService_adjust(t *testing.T) {
	e := newPkgEnv(t)
	p := e.newCatalogPkg(t, "Pack", 5, 1000, nil)
	cp := e.buy(t, p, "")
	_, _ = e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "")
	_, _ = e.svc.Redeem(e.recep, "s1", e.lia.ID, cp.ID, "", "", "")

	// Corrige un consumo equivocado: +1.
	got, err := e.svc.Adjust(e.admin, "s1", e.lia.ID, cp.ID, 1, " se descontó de más ")
	if err != nil || got.SessionsLeft != 4 {
		t.Fatalf("ajuste: %+v %v", got, err)
	}
	m := e.owned.movements[len(e.owned.movements)-1]
	if m.Reason != domain.PackageAdjust || m.Delta != 1 || m.Note != "se descontó de más" || m.Actor != "admin" {
		t.Fatalf("movimiento: %+v", m)
	}
	if e.owned.sumDelta(cp.ID) != 4 {
		t.Fatalf("invariante: %d", e.owned.sumDelta(cp.ID))
	}
	n := len(e.owned.movements)
	// Límites: no pasa del total ni baja de 0 (409), sin nota 400, cero 400.
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 2, "x")
	wantMsg(t, err, domain.ErrAlreadyExists, "El ajuste deja el saldo fuera de rango")
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, -5, "x")
	wantMsg(t, err, domain.ErrAlreadyExists, "El ajuste deja el saldo fuera de rango")
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 1<<40, "x")
	wantMsg(t, err, domain.ErrAlreadyExists, "El ajuste deja el saldo fuera de rango")
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 1, "")
	wantMsg(t, err, domain.ErrValidation, "Escribí el motivo del ajuste")
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 0, "x")
	wantMsg(t, err, domain.ErrValidation, "El ajuste no puede ser cero")
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 1, "a\nb")
	wantMsg(t, err, domain.ErrValidation, domain.MsgPackageInvalidChars)
	_, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, "nope", 1, "x")
	wantMsg(t, err, domain.ErrNotFound, "Paquete no encontrado")
	if len(e.owned.movements) != n || e.owned.packages[0].SessionsLeft != 4 {
		t.Fatalf("un rechazo escribió")
	}
	// Bordes que SÍ valen: bajar a 0 y volver a subir al total.
	if got, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, -4, "cierre"); err != nil || got.SessionsLeft != 0 {
		t.Fatalf("a 0: %+v %v", got, err)
	}
	if got, err = e.svc.Adjust(e.owner, "s1", e.lia.ID, cp.ID, 5, "reabre"); err != nil || got.SessionsLeft != 5 {
		t.Fatalf("al total: %+v %v", got, err)
	}
	// Solo owner/admin.
	removed := e.deactivate("owner")
	for _, actor := range []domain.Membership{e.recep, e.prof, e.client, e.other, removed} {
		if _, err := e.svc.Adjust(actor, "s1", e.lia.ID, cp.ID, 1, "x"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no ajusta: %v", actor.UserID, actor.Role, err)
		}
	}
}

// --- D8: leer ----------------------------------------------------------------------------

func TestPackageService_reads(t *testing.T) {
	e := newPkgEnv(t)
	short := e.newCatalogPkg(t, "Corto", 1, 100, pint(1))
	long := e.newCatalogPkg(t, "Largo", 5, 500, nil)
	spent := e.newCatalogPkg(t, "Gastado", 1, 50, nil)
	a := e.buy(t, short, "")
	e.clock = e.clock.Add(time.Minute)
	b := e.buy(t, long, "")
	e.clock = e.clock.Add(time.Minute)
	c := e.buy(t, spent, "")
	_, _ = e.svc.Redeem(e.recep, "s1", e.lia.ID, c.ID, "", "", "")
	// Dos días después: «Corto» venció, «Gastado» está agotado.
	e.clock = e.clock.Add(48 * time.Hour)

	list, err := e.svc.ListClientPackages(e.recep, "s1", e.lia.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("lista: %v %d", err, len(list))
	}
	// Los vigentes con saldo primero.
	if list[0].ID != b.ID {
		t.Fatalf("el vigente con saldo va primero: %+v", list)
	}
	byID := map[string]domain.ClientPackage{}
	for _, cp := range list {
		byID[cp.ID] = cp
	}
	if !byID[a.ID].Expired || byID[b.ID].Expired || byID[c.ID].Expired {
		t.Fatalf("expired mal: %+v", byID)
	}
	// Movimientos: más nuevos primero.
	mv, err := e.svc.Movements(e.owner, "s1", e.lia.ID, c.ID)
	if err != nil || len(mv) != 2 || mv[0].Reason != domain.PackageRedeem || mv[1].Reason != domain.PackagePurchase {
		t.Fatalf("movimientos: %+v %v", mv, err)
	}
	// Permisos: owner/admin/recepción; el resto 403.
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		if _, err := e.svc.ListClientPackages(actor, "s1", e.lia.ID); err != nil {
			t.Fatalf("%s lista: %v", actor.Role, err)
		}
		if _, err := e.svc.Movements(actor, "s1", e.lia.ID, c.ID); err != nil {
			t.Fatalf("%s ve movimientos: %v", actor.Role, err)
		}
	}
	removed := e.deactivate("owner")
	for _, actor := range []domain.Membership{e.prof, e.client, e.other, removed} {
		if _, err := e.svc.ListClientPackages(actor, "s1", e.lia.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no lista: %v", actor.UserID, actor.Role, err)
		}
		if _, err := e.svc.Movements(actor, "s1", e.lia.ID, c.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s/%s no ve movimientos: %v", actor.UserID, actor.Role, err)
		}
	}
	// Aislamiento: clienta de otro salón / inexistente = 404 de ficha; el paquete
	// de OTRA clienta o inexistente = 404 de paquete.
	foreign := e.newClient(t, "s2", "Ajena")
	for _, cid := range []string{foreign.ID, "nope", "a\x00b"} {
		_, err := e.svc.ListClientPackages(e.recep, "s1", cid)
		wantMsg(t, err, domain.ErrNotFound, "Ficha no encontrada")
	}
	other := e.newClient(t, "s1", "Otra")
	for _, q := range []struct{ client, pkg string }{{other.ID, c.ID}, {e.lia.ID, "nope"}, {foreign.ID, c.ID}, {e.lia.ID, "a\x00b"}} {
		_, err := e.svc.Movements(e.recep, "s1", q.client, q.pkg)
		wantMsg(t, err, domain.ErrNotFound, "Paquete no encontrado")
	}
	if empty, err := e.svc.ListClientPackages(e.recep, "s1", other.ID); err != nil || len(empty) != 0 {
		t.Fatalf("clienta sin paquetes: %v %d", err, len(empty))
	}
}
