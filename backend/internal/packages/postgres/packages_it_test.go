// Integración con Postgres real de los paquetes prepagos (F8, D3..D10). Se
// saltea sin SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: la
// compra en UNA transacción, el invariante sessions_left = Σ delta, el UPDATE
// condicional del consumo, la idempotencia por los índices únicos y el
// aislamiento por salón.
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	clientspostgres "salon-saas/internal/clients/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

type pkgFix struct {
	pool    *pgxpool.Pool
	ctx     context.Context
	catalog Packages
	owned   ClientPackages
	// s1 es el salón de las pruebas, s2 el de OTRO tenant.
	svcA, svcB, svcOther domain.Service
	lia, otherClient     domain.Client
	liaTwo               domain.Client
}

// pkgFixtures deja un escenario limpio: dos salones, un profesional en cada uno,
// servicios y una clienta por salón (más una segunda en el salón 1).
func pkgFixtures(t *testing.T) *pkgFix {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-pkg%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-pkg%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pkg-prof','Prof Paquetes','pkgprof@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pkg-prof2','Prof Paquetes 2','pkgprof2@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-pkg','s-it-pkg','Salón Paquetes IT','Pacific/Auckland')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-pkg2','s-it-pkg2','Salón Paquetes IT 2','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := pgstore.Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-pkg-prof", SalonID: "s-it-pkg", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-it-pkg-prof2", SalonID: "s-it-pkg2", Role: domain.RoleProfessional, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	f := &pkgFix{pool: pool, ctx: ctx, catalog: Packages{Pool: pool}, owned: ClientPackages{Pool: pool}}
	services := pgstore.Services{Pool: pool}
	mk := func(salon, name string) domain.Service {
		s, err := domain.NewService(salon, name, 45, 15000, 10)
		if err != nil {
			t.Fatalf("servicio: %v", err)
		}
		if err := services.Save(s); err != nil {
			t.Fatalf("guardar servicio: %v", err)
		}
		return s
	}
	f.svcA, f.svcB, f.svcOther = mk("s-it-pkg", "Corte A"), mk("s-it-pkg", "Tinte B"), mk("s-it-pkg2", "Corte Ajeno")
	clients := clientspostgres.Clients{Pool: pool}
	mc := func(salon, name string) domain.Client {
		c, err := domain.NewClient(salon, name, "", "", "")
		if err != nil {
			t.Fatalf("clienta: %v", err)
		}
		if err := clients.Save(c); err != nil {
			t.Fatalf("guardar clienta: %v", err)
		}
		return c
	}
	f.lia, f.liaTwo, f.otherClient = mc("s-it-pkg", "Lía Paquetes"), mc("s-it-pkg", "Mora Paquetes"), mc("s-it-pkg2", "Clienta Ajena")
	return f
}

func (f *pkgFix) newPackage(t *testing.T, name string, sessions, price int, validity *int) domain.ServicePackage {
	t.Helper()
	p, err := domain.NewServicePackage("s-it-pkg", name, f.svcA.ID, sessions, price, validity)
	if err != nil {
		t.Fatalf("paquete %q: %v", name, err)
	}
	if err := f.catalog.Create(p); err != nil {
		t.Fatalf("crear paquete %q: %v", name, err)
	}
	return p
}

func (f *pkgFix) order(t *testing.T, p domain.ServicePackage, c domain.Client, op string, at time.Time) domain.PackagePurchaseOrder {
	t.Helper()
	o, err := domain.NewPackagePurchase(p, c, "u-it-pkg-prof", domain.MethodCard, op, "u-it-pkg-prof", at, time.UTC)
	if err != nil {
		t.Fatalf("armar compra: %v", err)
	}
	return o
}

// buy compra un paquete nuevo de `sessions` sesiones (sin vigencia) para Lía.
func (f *pkgFix) buy(t *testing.T, sessions int, price int, validity *int) domain.ClientPackage {
	t.Helper()
	p := f.newPackage(t, "Pack "+domain.NewID()[:8], sessions, price, validity)
	o := f.order(t, p, f.lia, "", time.Now().UTC())
	if err := f.owned.Purchase(o); err != nil {
		t.Fatalf("comprar: %v", err)
	}
	return o.ClientPackage
}

func (f *pkgFix) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	return itdb.CountRows(t, f.pool, q, args...)
}

func (f *pkgFix) left(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT sessions_left FROM client_packages WHERE id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("saldo: %v", err)
	}
	return n
}

func (f *pkgFix) sum(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(SUM(delta),0)::int FROM package_movements WHERE client_package_id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("Σ delta: %v", err)
	}
	return n
}

// invariant: sessions_left = Σ delta y dentro de 0..sessions_total (D4).
func (f *pkgFix) invariant(t *testing.T, id string) {
	t.Helper()
	var left, total int
	if err := f.pool.QueryRow(f.ctx, `SELECT sessions_left, sessions_total FROM client_packages WHERE id=$1`, id).Scan(&left, &total); err != nil {
		t.Fatalf("invariante: %v", err)
	}
	if s := f.sum(t, id); left != s || left < 0 || left > total {
		t.Fatalf("invariante roto: sessions_left=%d Σdelta=%d total=%d", left, s, total)
	}
}

func (f *pkgFix) redeem(id string, op string) (domain.ClientPackage, error) {
	return f.owned.Redeem(domain.PackageRedeemRequest{
		SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: id, ClientOpID: op,
		Actor: "u-it-pkg-prof", Now: time.Now().UTC(),
	})
}

func wantDomainMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil || !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error %v, quería %q de la familia %v", err, text, kind)
	}
}

// --- Catálogo (D3/D10) -----------------------------------------------------------

func TestPackagesIT_catalog(t *testing.T) {
	f := pkgFixtures(t)
	ten := 90
	a := f.newPackage(t, "Pack Cinco", 5, 60000, &ten)
	b := f.newPackage(t, "Pack Sin Vencer", 3, 0, nil)

	got, err := f.catalog.FindByID("s-it-pkg", a.ID)
	if err != nil || got.Name != "Pack Cinco" || got.Sessions != 5 || got.PriceCents != 60000 || got.ValidityDays == nil || *got.ValidityDays != 90 || !got.Active || got.ServiceID != f.svcA.ID {
		t.Fatalf("FindByID: %+v %v", got, err)
	}
	if got, _ := f.catalog.FindByID("s-it-pkg", b.ID); got.ValidityDays != nil || got.PriceCents != 0 {
		t.Fatalf("la vigencia nula y el precio 0 se guardan tal cual: %+v", got)
	}
	// Aislamiento: el otro salón no lo ve, no lo lista y no lo edita.
	if _, err := f.catalog.FindByID("s-it-pkg2", a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón encontró el paquete: %v", err)
	}
	if list, _ := f.catalog.List("s-it-pkg2"); len(list) != 0 {
		t.Fatalf("otro salón lista paquetes ajenos: %+v", list)
	}
	hijack := a
	hijack.SalonID = "s-it-pkg2"
	hijack.Name = "Robado"
	if err := f.catalog.Save(hijack); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Save de otro salón: %v", err)
	}
	if got, _ := f.catalog.FindByID("s-it-pkg", a.ID); got.Name != "Pack Cinco" {
		t.Fatalf("otro salón modificó el paquete: %+v", got)
	}
	// Listado por nombre con el nombre del servicio.
	list, err := f.catalog.List("s-it-pkg")
	if err != nil || len(list) != 2 || list[0].Name != "Pack Cinco" || list[1].Name != "Pack Sin Vencer" || list[0].ServiceName != "Corte A" {
		t.Fatalf("List: %+v %v", list, err)
	}
	// Nombre activo repetido (sin importar mayúsculas): 409; el inactivo libera.
	dup, _ := domain.NewServicePackage("s-it-pkg", "PACK cinco", f.svcA.ID, 2, 100, nil)
	if err := f.catalog.Create(dup); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("nombre repetido: %v", err)
	}
	off, _ := a.WithDetails(a.Name, a.PriceCents, nil, false)
	if err := f.catalog.Save(off); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if got, _ := f.catalog.FindByID("s-it-pkg", a.ID); got.Active || got.ValidityDays != nil {
		t.Fatalf("Save no reemplazó la vigencia nula ni el estado: %+v", got)
	}
	if err := f.catalog.Create(dup); err != nil {
		t.Fatalf("el inactivo libera el nombre: %v", err)
	}
	// Reactivar el viejo choca con el nuevo activo: 409 y no cambia.
	on, _ := off.WithDetails(off.Name, off.PriceCents, nil, true)
	if err := f.catalog.Save(on); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("reactivar con nombre tomado: %v", err)
	}
	// El mismo nombre en OTRO salón es válido.
	foreign, _ := domain.NewServicePackage("s-it-pkg2", "Pack Cinco", f.svcOther.ID, 2, 100, nil)
	if err := f.catalog.Create(foreign); err != nil {
		t.Fatalf("mismo nombre en otro salón: %v", err)
	}
	// Servicio inexistente: nunca un 500 con SQL crudo.
	ghost, _ := domain.NewServicePackage("s-it-pkg", "Fantasma", "no-existe", 2, 100, nil)
	if err := f.catalog.Create(ghost); err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inexistente debe ser 400: %v", err)
	}
	// Un NUL en el nombre ya lo frena el dominio; la DB es la última barrera de
	// los saltos de línea.
	bad := b
	bad.ID, bad.Name = domain.NewID(), "a\nb"
	if err := f.catalog.Create(bad); err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("salto de línea en el nombre debe ser 400 desde el CHECK: %v", err)
	}
}

// --- Comprar (D5) ------------------------------------------------------------------

func TestPackagesIT_purchase_is_one_transaction(t *testing.T) {
	f := pkgFixtures(t)
	validity := 30
	p := f.newPackage(t, "Pack 5 cortes", 5, 60000, &validity)
	// 2026-03-10 11:30 UTC = 11 de marzo 00:30 en Auckland.
	at := time.Date(2026, 3, 10, 11, 30, 0, 123_456_000, time.UTC)
	loc, _ := time.LoadLocation("Pacific/Auckland")
	o, err := domain.NewPackagePurchase(p, f.lia, "u-it-pkg-prof", domain.MethodTransfer, "op-compra-1", "u-it-pkg-prof", at, loc)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.owned.Purchase(o); err != nil {
		t.Fatalf("Purchase: %v", err)
	}

	// La VENTA: total = precio, packages_cents = precio, sin sale_items, la clienta
	// por id, el nombre de la ficha, el método y el profesional.
	var total, pkgCents, productsCents int
	var clientID, clientName, method, prof, op string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT total_cents, packages_cents, products_cents, client_id, client_name, method, professional_id, client_op_id
		 FROM sales WHERE id=$1 AND salon_id='s-it-pkg'`, o.Sale.ID).
		Scan(&total, &pkgCents, &productsCents, &clientID, &clientName, &method, &prof, &op); err != nil {
		t.Fatalf("leer venta: %v", err)
	}
	if total != 60000 || pkgCents != 60000 || productsCents != 0 || clientID != f.lia.ID || clientName != "Lía Paquetes" ||
		method != "transfer" || prof != "u-it-pkg-prof" || op != "op-compra-1" {
		t.Fatalf("venta guardada: %d %d %d %s %q %s %s %s", total, pkgCents, productsCents, clientID, clientName, method, prof, op)
	}
	if n := f.count(t, `SELECT count(*) FROM sale_items WHERE sale_id=$1`, o.Sale.ID); n != 0 {
		t.Fatalf("una compra de paquete no tiene sale_items: %d", n)
	}
	// El PAQUETE de la clienta: snapshot, saldo completo, vencimiento a fin del día
	// local de Auckland (11/3 + 30 días = 10/4 23:59:59.999999 NZST = 11:59:59.999999Z).
	var name string
	var price, sessTotal, left int
	var expires *time.Time
	var saleID, svcID, pkgID string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT name, price_cents, sessions_total, sessions_left, expires_at, sale_id, service_id, package_id
		 FROM client_packages WHERE id=$1 AND salon_id='s-it-pkg' AND client_id=$2`, o.ClientPackage.ID, f.lia.ID).
		Scan(&name, &price, &sessTotal, &left, &expires, &saleID, &svcID, &pkgID); err != nil {
		t.Fatalf("leer paquete: %v", err)
	}
	wantExpiry := time.Date(2026, 4, 10, 11, 59, 59, 999_999_000, time.UTC)
	if name != "Pack 5 cortes" || price != 60000 || sessTotal != 5 || left != 5 || saleID != o.Sale.ID || svcID != f.svcA.ID || pkgID != p.ID ||
		expires == nil || !expires.Equal(wantExpiry) {
		t.Fatalf("paquete guardado: %q %d %d %d %v %s", name, price, sessTotal, left, expires, saleID)
	}
	// El MOVIMIENTO de compra y el invariante.
	var delta int
	var reason string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT delta, reason FROM package_movements WHERE client_package_id=$1`, o.ClientPackage.ID).Scan(&delta, &reason); err != nil || delta != 5 || reason != "purchase" {
		t.Fatalf("movimiento de compra: %d %s %v", delta, reason, err)
	}
	f.invariant(t, o.ClientPackage.ID)

	// SNAPSHOT: cambiar el catálogo (precio, nombre, desactivar) no mueve lo comprado.
	renamed, _ := p.WithDetails("Otro nombre", 1, nil, false)
	if err := f.catalog.Save(renamed); err != nil {
		t.Fatal(err)
	}
	list, err := f.owned.ListByClient("s-it-pkg", f.lia.ID, at)
	if err != nil || len(list) != 1 || list[0].Name != "Pack 5 cortes" || list[0].PriceCents != 60000 || list[0].ServiceName != "Corte A" ||
		list[0].ExpiresAt == nil || !list[0].ExpiresAt.Equal(wantExpiry) || !list[0].PurchasedAt.Equal(at) {
		t.Fatalf("snapshot: %+v %v", list, err)
	}
}

// Un rechazo a mitad de camino NO deja nada: ni la venta, ni el paquete, ni el
// movimiento (una sola transacción), y el tenant de la clienta o del paquete se
// verifica en el INSERT, no solo en el caso de uso.
func TestPackagesIT_purchase_failures_write_nothing(t *testing.T) {
	f := pkgFixtures(t)
	p := f.newPackage(t, "Pack", 5, 1000, nil)
	now := time.Now().UTC()
	snapshot := func() [4]int {
		return [4]int{
			f.count(t, `SELECT count(*) FROM sales WHERE salon_id LIKE 's-it-pkg%'`),
			f.count(t, `SELECT count(*) FROM client_packages WHERE salon_id LIKE 's-it-pkg%'`),
			f.count(t, `SELECT count(*) FROM package_movements WHERE salon_id LIKE 's-it-pkg%'`),
			f.count(t, `SELECT count(*) FROM sale_items`),
		}
	}
	before := snapshot()

	// Clienta de OTRO salón con un paquete de este: la venta entra primero, el
	// paquete no; todo se deshace.
	cross := f.order(t, p, f.otherClient, "", now)
	if err := f.owned.Purchase(cross); err == nil {
		t.Fatalf("la clienta de otro salón compró")
	}
	// Paquete de OTRO salón con una clienta de este.
	foreignPkg, _ := domain.NewServicePackage("s-it-pkg2", "Ajeno", f.svcOther.ID, 2, 100, nil)
	if err := f.catalog.Create(foreignPkg); err != nil {
		t.Fatal(err)
	}
	cross2 := f.order(t, p, f.lia, "", now)
	cross2.ClientPackage.PackageID = foreignPkg.ID
	if err := f.owned.Purchase(cross2); err == nil {
		t.Fatalf("el paquete de otro salón se vendió")
	}
	// Movimiento inválido (delta 0): rompe el CHECK DESPUÉS de insertar venta y
	// paquete -> se deshace todo.
	broken := f.order(t, p, f.lia, "", now)
	broken.Movement.Delta = 0
	if err := f.owned.Purchase(broken); err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("un CHECK debe ser 400: %v", err)
	}
	// Precio de la venta incoherente con packages_cents (> total): no aplica acá,
	// pero un nombre con salto de línea en el snapshot sí rompe el CHECK.
	nl := f.order(t, p, f.lia, "", now)
	nl.ClientPackage.Name = "a\nb"
	if err := f.owned.Purchase(nl); err == nil {
		t.Fatalf("snapshot con salto de línea")
	}
	if after := snapshot(); after != before {
		t.Fatalf("un rechazo dejó escrito algo: antes %v, después %v", before, after)
	}
}

func TestPackagesIT_purchase_replay_by_op(t *testing.T) {
	f := pkgFixtures(t)
	p := f.newPackage(t, "Pack", 5, 1000, nil)
	now := time.Now().UTC()
	first := f.order(t, p, f.lia, "op-replay", now)
	if err := f.owned.Purchase(first); err != nil {
		t.Fatalf("primera compra: %v", err)
	}
	// El repetido: el índice único de sales lo frena con 409 y NO deja nada.
	again := f.order(t, p, f.lia, "op-replay", now)
	if err := f.owned.Purchase(again); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("el repetido debe ser ErrAlreadyExists: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM sales WHERE client_op_id='op-replay' AND salon_id='s-it-pkg'`); n != 1 {
		t.Fatalf("ventas del op = %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM client_packages WHERE salon_id='s-it-pkg'`); n != 1 {
		t.Fatalf("paquetes = %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM package_movements WHERE salon_id='s-it-pkg'`); n != 1 {
		t.Fatalf("movimientos = %d", n)
	}
	// La compra ORIGINAL se resuelve por el op id.
	got, err := f.owned.FindPurchaseByOp("s-it-pkg", "op-replay")
	if err != nil || got.ID != first.ClientPackage.ID || got.SaleID != first.Sale.ID || got.ClientID != f.lia.ID || got.ServiceName != "Corte A" {
		t.Fatalf("FindPurchaseByOp: %+v %v", got, err)
	}
	// Op desconocido: ErrNotFound; en otro salón: ErrNotFound (scope).
	if _, err := f.owned.FindPurchaseByOp("s-it-pkg", "op-nadie"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("op desconocido: %v", err)
	}
	if _, err := f.owned.FindPurchaseByOp("s-it-pkg2", "op-replay"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("op de otro salón: %v", err)
	}
	// Una venta del POS (sin paquete) con ese op id: el id ya se usó para OTRA cosa.
	if err := (pgstore.Sales{Pool: f.pool}).Save(domain.Sale{
		ID: domain.NewID(), SalonID: "s-it-pkg", ProfessionalID: "u-it-pkg-prof", ClientName: "Mostrador",
		Items: []domain.SaleItem{{ServiceID: f.svcA.ID, Qty: 1, UnitPriceCents: 100}}, TotalCents: 100,
		Method: domain.MethodCash, ClientOpID: "op-pos", CreatedAt: now,
	}); err != nil {
		t.Fatalf("venta del POS: %v", err)
	}
	if _, err := f.owned.FindPurchaseByOp("s-it-pkg", "op-pos"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("op de una venta que no es un paquete: %v", err)
	}
	// Las ventas del POS siguen con packages_cents = 0 (PRESERVE).
	var pc int
	if err := f.pool.QueryRow(f.ctx, `SELECT packages_cents FROM sales WHERE client_op_id='op-pos'`).Scan(&pc); err != nil || pc != 0 {
		t.Fatalf("venta del POS packages_cents = %d %v", pc, err)
	}
}

// --- Consumir (D7) ------------------------------------------------------------------

func TestPackagesIT_redeem(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 2, 1000, nil)
	now := time.Now().UTC()

	got, err := f.owned.Redeem(domain.PackageRedeemRequest{
		SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: cp.ID, ProfessionalID: "u-it-pkg-prof",
		Note: "primer corte", Actor: "u-it-pkg-prof", Now: now,
	})
	if err != nil || got.SessionsLeft != 1 || got.ID != cp.ID || got.ServiceName != "Corte A" || got.SessionsTotal != 2 {
		t.Fatalf("redeem: %+v %v", got, err)
	}
	var delta int
	var reason, note, prof, actor string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT delta, reason, COALESCE(note,''), COALESCE(professional_id,''), COALESCE(actor,'')
		 FROM package_movements WHERE client_package_id=$1 AND reason='redeem'`, cp.ID).Scan(&delta, &reason, &note, &prof, &actor); err != nil ||
		delta != -1 || note != "primer corte" || prof != "u-it-pkg-prof" || actor != "u-it-pkg-prof" {
		t.Fatalf("movimiento: %d %s %q %s %s %v", delta, reason, note, prof, actor, err)
	}
	// Consumir NO es una venta: sigue habiendo la de la compra.
	if n := f.count(t, `SELECT count(*) FROM sales WHERE salon_id='s-it-pkg'`); n != 1 {
		t.Fatalf("consumir creó una venta: %d", n)
	}
	f.invariant(t, cp.ID)
	if _, err := f.redeem(cp.ID, ""); err != nil {
		t.Fatalf("segunda sesión: %v", err)
	}
	// Agotado: 409 con el texto del contrato y NO escribe nada.
	moves := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID)
	_, err = f.redeem(cp.ID, "")
	wantDomainMsg(t, err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete")
	if n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID); n != moves {
		t.Fatalf("un 409 escribió un movimiento")
	}
	f.invariant(t, cp.ID)
}

func TestPackagesIT_redeem_not_found_and_isolation(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 3, 1000, nil)
	now := time.Now().UTC()
	req := func(salon, client, id string) domain.PackageRedeemRequest {
		return domain.PackageRedeemRequest{SalonID: salon, ClientID: client, ClientPackageID: id, Actor: "x", Now: now}
	}
	for name, r := range map[string]domain.PackageRedeemRequest{
		"inexistente":         req("s-it-pkg", f.lia.ID, "no-existe"),
		"otra clienta":        req("s-it-pkg", f.liaTwo.ID, cp.ID),
		"otro salón":          req("s-it-pkg2", f.lia.ID, cp.ID),
		"clienta de otro":     req("s-it-pkg2", f.otherClient.ID, cp.ID),
		"clienta inexistente": req("s-it-pkg", "nadie", cp.ID),
	} {
		if _, err := f.owned.Redeem(r); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s: debe ser ErrNotFound, dio %v", name, err)
		}
	}
	if got := f.left(t, cp.ID); got != 3 {
		t.Fatalf("un 404 descontó: %d", got)
	}
	if n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID); n != 1 {
		t.Fatalf("un 404 escribió movimientos: %d", n)
	}
	// Un client_op_id de un 404 no queda «quemado»: el movimiento se deshizo.
	_, _ = f.owned.Redeem(domain.PackageRedeemRequest{SalonID: "s-it-pkg", ClientID: f.liaTwo.ID, ClientPackageID: cp.ID, ClientOpID: "op-404", Actor: "x", Now: now})
	if n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_op_id='op-404'`); n != 0 {
		t.Fatalf("el 404 dejó el op id usado")
	}
	if _, err := f.redeem(cp.ID, "op-404"); err != nil {
		t.Fatalf("el op id sigue libre: %v", err)
	}
}

func TestPackagesIT_redeem_expiry(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 3, 1000, nil)
	expires := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := f.pool.Exec(f.ctx, `UPDATE client_packages SET expires_at=$2 WHERE id=$1`, cp.ID, expires); err != nil {
		t.Fatal(err)
	}
	at := func(now time.Time) (domain.ClientPackage, error) {
		return f.owned.Redeem(domain.PackageRedeemRequest{SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: cp.ID, Actor: "x", Now: now})
	}
	if _, err := at(expires.Add(-time.Microsecond)); err != nil {
		t.Fatalf("un microsegundo antes: %v", err)
	}
	// En el instante exacto del vencimiento ya NO vale (expires_at > now).
	_, err := at(expires)
	wantDomainMsg(t, err, domain.ErrAlreadyExists, "El paquete venció")
	_, err = at(expires.Add(time.Hour))
	wantDomainMsg(t, err, domain.ErrAlreadyExists, "El paquete venció")
	if f.left(t, cp.ID) != 2 || f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID) != 2 {
		t.Fatalf("un vencido escribió")
	}
	f.invariant(t, cp.ID)
	// Agotado Y vencido: gana «No quedan sesiones» (no hay nada que consumir).
	if _, err := f.pool.Exec(f.ctx, `UPDATE client_packages SET sessions_left=0 WHERE id=$1`, cp.ID); err != nil {
		t.Fatal(err)
	}
	_, err = at(expires.Add(time.Hour))
	wantDomainMsg(t, err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete")
	// La lectura marca el vencido/agotado y los vigentes con saldo van primero.
	live := f.buy(t, 2, 1000, nil)
	list, _ := f.owned.ListByClient("s-it-pkg", f.lia.ID, expires.Add(time.Hour))
	if len(list) != 2 || list[0].ID != live.ID || list[1].ID != cp.ID {
		t.Fatalf("orden de la lista: %+v", list)
	}
}

func TestPackagesIT_redeem_replay_by_op(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 2, 1000, nil)
	first, err := f.redeem(cp.ID, "op-r1")
	if err != nil || first.SessionsLeft != 1 {
		t.Fatalf("primero: %+v %v", first, err)
	}
	// El repetido devuelve el estado ACTUAL sin descontar.
	again, err := f.redeem(cp.ID, "op-r1")
	if err != nil || again.SessionsLeft != 1 || again.ID != cp.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if f.left(t, cp.ID) != 1 || f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1 AND reason='redeem'`, cp.ID) != 1 {
		t.Fatalf("el replay descontó otra vez")
	}
	// Agotado: el repetido sigue siendo un replay (no un 409 de saldo).
	if _, err := f.redeem(cp.ID, "op-r2"); err != nil {
		t.Fatal(err)
	}
	again, err = f.redeem(cp.ID, "op-r2")
	if err != nil || again.SessionsLeft != 0 {
		t.Fatalf("replay con el paquete agotado: %+v %v", again, err)
	}
	_, err = f.redeem(cp.ID, "op-r3")
	wantDomainMsg(t, err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete")
	// El mismo op id sobre OTRO paquete o de otra clienta: 409, no descuenta.
	other := f.buy(t, 2, 1000, nil)
	_, err = f.redeem(other.ID, "op-r1")
	wantDomainMsg(t, err, domain.ErrAlreadyExists, domain.MsgPackageOpReused)
	_, err = f.owned.Redeem(domain.PackageRedeemRequest{SalonID: "s-it-pkg", ClientID: f.liaTwo.ID, ClientPackageID: cp.ID, ClientOpID: "op-r1", Actor: "x", Now: time.Now().UTC()})
	wantDomainMsg(t, err, domain.ErrAlreadyExists, domain.MsgPackageOpReused)
	if f.left(t, other.ID) != 2 {
		t.Fatalf("un op reutilizado descontó")
	}
	// El mismo op id en otro SALÓN es independiente (índice por salón).
	f.invariant(t, cp.ID)
	f.invariant(t, other.ID)
}

// --- Ajustar (D9) ------------------------------------------------------------------

func TestPackagesIT_adjust(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 5, 1000, nil)
	now := time.Now().UTC()
	adj := func(client, id string, delta int) (domain.ClientPackage, error) {
		return f.owned.Adjust(domain.PackageAdjustRequest{
			SalonID: "s-it-pkg", ClientID: client, ClientPackageID: id, Delta: delta,
			Note: "corrección", Actor: "u-it-pkg-prof", Now: now,
		})
	}
	if _, err := f.redeem(cp.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := adj(f.lia.ID, cp.ID, 1)
	if err != nil || got.SessionsLeft != 5 || got.ServiceName != "Corte A" {
		t.Fatalf("ajuste +1: %+v %v", got, err)
	}
	var note, actor string
	if err := f.pool.QueryRow(f.ctx, `SELECT note, actor FROM package_movements WHERE client_package_id=$1 AND reason='adjust'`, cp.ID).Scan(&note, &actor); err != nil || note != "corrección" || actor != "u-it-pkg-prof" {
		t.Fatalf("movimiento de ajuste: %q %q %v", note, actor, err)
	}
	f.invariant(t, cp.ID)
	n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID)
	// Fuera de rango (más que el total, bajo cero): 409 y no escribe.
	for _, d := range []int{1, 10, -6, -100} {
		_, err := adj(f.lia.ID, cp.ID, d)
		wantDomainMsg(t, err, domain.ErrAlreadyExists, "El ajuste deja el saldo fuera de rango")
	}
	if f.left(t, cp.ID) != 5 || f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1`, cp.ID) != n {
		t.Fatalf("un ajuste rechazado escribió")
	}
	// Bordes que SÍ valen: a 0 y de vuelta al total.
	if got, err := adj(f.lia.ID, cp.ID, -5); err != nil || got.SessionsLeft != 0 {
		t.Fatalf("a 0: %+v %v", got, err)
	}
	if got, err := adj(f.lia.ID, cp.ID, 5); err != nil || got.SessionsLeft != 5 {
		t.Fatalf("al total: %+v %v", got, err)
	}
	// 404: otra clienta, inexistente.
	for _, c := range []struct{ client, id string }{{f.liaTwo.ID, cp.ID}, {f.lia.ID, "nope"}} {
		if _, err := adj(c.client, c.id, 1); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ajuste ajeno: %v", err)
		}
	}
	// Otro salón.
	if _, err := f.owned.Adjust(domain.PackageAdjustRequest{SalonID: "s-it-pkg2", ClientID: f.lia.ID, ClientPackageID: cp.ID, Delta: 1, Note: "x", Now: now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ajuste desde otro salón: %v", err)
	}
	f.invariant(t, cp.ID)
}

// --- Leer (D8) ----------------------------------------------------------------------

func TestPackagesIT_lists(t *testing.T) {
	f := pkgFixtures(t)
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	mkAt := func(name string, sessions int, at time.Time, validity *int) domain.ClientPackage {
		p := f.newPackage(t, name, sessions, 100, validity)
		o := f.order(t, p, f.lia, "", at)
		if err := f.owned.Purchase(o); err != nil {
			t.Fatal(err)
		}
		return o.ClientPackage
	}
	one := 1
	expiring := mkAt("Vence", 3, base, &one)                   // vence a fin del 11/1
	spent := mkAt("Agotado", 1, base.Add(time.Hour), nil)      // sin saldo
	live := mkAt("Vigente", 5, base.Add(2*time.Hour), nil)     // vigente con saldo
	newest := mkAt("Más nuevo", 2, base.Add(3*time.Hour), nil) // vigente con saldo, más nuevo
	if _, err := f.owned.Redeem(domain.PackageRedeemRequest{SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: spent.ID, Actor: "x", Now: base.Add(4 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Mucho después: «Vence» venció.
	now := base.Add(72 * time.Hour)
	list, err := f.owned.ListByClient("s-it-pkg", f.lia.ID, now)
	if err != nil || len(list) != 4 {
		t.Fatalf("lista: %v %d", err, len(list))
	}
	// Vigentes con saldo primero (el más nuevo antes), después el resto.
	if list[0].ID != newest.ID || list[1].ID != live.ID {
		t.Fatalf("orden: %s %s ...", list[0].Name, list[1].Name)
	}
	rest := map[string]bool{list[2].ID: true, list[3].ID: true}
	if !rest[expiring.ID] || !rest[spent.ID] {
		t.Fatalf("el vencido y el agotado van al final: %+v", list)
	}
	// Otra clienta y otro salón no ven nada.
	for _, c := range []struct{ salon, client string }{{"s-it-pkg", f.liaTwo.ID}, {"s-it-pkg2", f.lia.ID}} {
		got, err := f.owned.ListByClient(c.salon, c.client, now)
		if err != nil || len(got) != 0 {
			t.Fatalf("lista ajena: %+v %v", got, err)
		}
	}
	// Movimientos: más nuevo primero; ajenos 404.
	mv, err := f.owned.ListMovements("s-it-pkg", f.lia.ID, spent.ID)
	if err != nil || len(mv) != 2 || mv[0].Reason != domain.PackageRedeem || mv[0].Delta != -1 || mv[1].Reason != domain.PackagePurchase || mv[1].Delta != 1 {
		t.Fatalf("movimientos: %+v %v", mv, err)
	}
	for _, c := range []struct{ salon, client, id string }{
		{"s-it-pkg", f.liaTwo.ID, spent.ID}, {"s-it-pkg2", f.lia.ID, spent.ID}, {"s-it-pkg", f.lia.ID, "nope"},
	} {
		if _, err := f.owned.ListMovements(c.salon, c.client, c.id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("movimientos ajenos %+v: %v", c, err)
		}
	}
	// Tope de 200: 205 movimientos de ajuste ±1 alternados en un paquete.
	big := f.buy(t, 2, 1, nil)
	for i := 0; i < 100; i++ {
		for _, d := range []int{-1, 1} {
			if _, err := f.owned.Adjust(domain.PackageAdjustRequest{SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: big.ID, Delta: d, Note: strings.Repeat("n", 3), Now: base}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := func() error {
		_, err := f.owned.Adjust(domain.PackageAdjustRequest{SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: big.ID, Delta: -1, Note: "extra", Now: base})
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	mv, err = f.owned.ListMovements("s-it-pkg", f.lia.ID, big.ID)
	if err != nil || len(mv) != domain.MaxPackageListed {
		t.Fatalf("tope de movimientos: %d %v", len(mv), err)
	}
	f.invariant(t, big.ID)
}
