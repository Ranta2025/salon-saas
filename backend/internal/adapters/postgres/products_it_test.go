// Integración con Postgres real de productos e inventario (F6, D1/D2).
// Se saltea sin SALON_IT=1. Corre con:
//
//	SALON_IT=1 go test ./internal/adapters/postgres/ -run 'Products|SaleProducts' -v
package pgstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	pospostgres "salon-saas/internal/pos/postgres"
	"salon-saas/internal/testutil/itdb"
)

// productFixtures deja un salón de prueba limpio y migrado (dos pasadas) con
// sus cuentas. Ids fijos para que el test sea re-ejecutable.
func productFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-prod%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-prod%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-prod-owner','Owner Prod','ownerprod@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-prod-prof','Prof Prod','profprod@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-prod','s-it-prod','Salón Productos IT')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-prod2','s-it-prod2','Salón Productos IT 2')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := pgstore.Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-prod-owner", SalonID: "s-it-prod", Role: domain.RoleOwner},
		{UserID: "u-it-prod-prof", SalonID: "s-it-prod", Role: domain.RoleProfessional},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	return pool, ctx
}

func mustProduct(t *testing.T, salonID, name string, price, stock, low int) domain.Product {
	t.Helper()
	p, err := domain.NewProduct(salonID, name, price, stock, low)
	if err != nil {
		t.Fatalf("producto %q: %v", name, err)
	}
	return p
}

func stockOf(t *testing.T, pool *pgxpool.Pool, productID string) int {
	t.Helper()
	var stock int
	if err := pool.QueryRow(context.Background(),
		`SELECT stock FROM products WHERE id=$1`, productID).Scan(&stock); err != nil {
		t.Fatalf("leer stock: %v", err)
	}
	return stock
}

func TestProducts_IT(t *testing.T) {
	pool, _ := productFixtures(t)
	repo := pgstore.Products{Pool: pool}

	shampoo := mustProduct(t, "s-it-prod", "Shampoo IT", 50000, 5, 2)
	initial := domain.StockMovement{
		ID: domain.NewID(), SalonID: "s-it-prod", ProductID: shampoo.ID,
		Delta: 5, Reason: domain.StockInitial, CreatedBy: "u-it-prod-owner",
	}
	if err := repo.Create(shampoo, &initial); err != nil {
		t.Fatalf("crear: %v", err)
	}
	got, err := repo.FindByID("s-it-prod", shampoo.ID)
	if err != nil || got.Stock != 5 || got.Name != "Shampoo IT" || !got.Active {
		t.Fatalf("FindByID: %v %+v", err, got)
	}
	// Scope por tenant: el producto no existe desde otro salón.
	if _, err := repo.FindByID("s-it-prod2", shampoo.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant debe dar ErrNotFound, dio %v", err)
	}

	// Nombre activo duplicado (sin importar mayúsculas): 23505 traducido.
	dup := mustProduct(t, "s-it-prod", "shampoo it", 100, 0, 0)
	if err := repo.Create(dup, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicado activo debe dar ErrAlreadyExists, dio %v", err)
	}
	// Mismo nombre en otro salón: permitido.
	sameOther := mustProduct(t, "s-it-prod2", "Shampoo IT", 100, 0, 0)
	if err := repo.Create(sameOther, nil); err != nil {
		t.Fatalf("mismo nombre en otro salón: %v", err)
	}

	// List ordenado por nombre y scopeado.
	cera := mustProduct(t, "s-it-prod", "Cera IT", 30000, 0, 0)
	if err := repo.Create(cera, nil); err != nil {
		t.Fatalf("crear cera: %v", err)
	}
	list, err := repo.List("s-it-prod")
	if err != nil || len(list) != 2 || list[0].Name != "Cera IT" || list[1].Name != "Shampoo IT" {
		t.Fatalf("List: %v %+v", err, list)
	}
	if other, _ := repo.List("s-it-prod2"); len(other) != 1 {
		t.Fatalf("otro salón debe tener 1, dio %d", len(other))
	}

	// PUT: cambia nombre/precio/umbral/estado y NO el stock.
	up, err := shampoo.WithDetails("Shampoo Premium", 60000, 3, true)
	if err != nil {
		t.Fatalf("dominio update: %v", err)
	}
	if err := repo.Save(up); err != nil {
		t.Fatalf("guardar update: %v", err)
	}
	got, _ = repo.FindByID("s-it-prod", shampoo.ID)
	if got.Name != "Shampoo Premium" || got.PriceCents != 60000 || got.LowStockThreshold != 3 || got.Stock != 5 {
		t.Fatalf("update: %+v", got)
	}
	// Un id de otro salón no se toca (404).
	if err := repo.Save(domain.Product{ID: shampoo.ID, SalonID: "s-it-prod2", Name: "X"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update de otro salón: %v", err)
	}
	// Desactivar libera el nombre.
	off, _ := got.WithDetails(got.Name, got.PriceCents, got.LowStockThreshold, false)
	if err := repo.Save(off); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	renamed := mustProduct(t, "s-it-prod", "shampoo it", 100, 0, 0)
	if err := repo.Create(renamed, nil); err != nil {
		t.Fatalf("nombre libre tras desactivar: %v", err)
	}

	// Ajuste: stock + movimiento en la misma transacción.
	adjusted, err := repo.Adjust("s-it-prod", shampoo.ID, 3, domain.StockRestock, "compra", "u-it-prod-owner")
	if err != nil || adjusted.Stock != 8 {
		t.Fatalf("adjust: %v %+v", err, adjusted)
	}
	if stockOf(t, pool, shampoo.ID) != 8 {
		t.Fatalf("stock persistido: %d", stockOf(t, pool, shampoo.ID))
	}
	// Bajar más de lo que hay: 409 y NADA escrito.
	if _, err := repo.Adjust("s-it-prod", shampoo.ID, -100, domain.StockLoss, "", ""); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("stock negativo debe ser ErrAlreadyExists, dio %v", err)
	}
	if got := stockOf(t, pool, shampoo.ID); got != 8 {
		t.Fatalf("el ajuste fallido no cambia el stock: %d", got)
	}
	// Invariante D2: stock = Σ delta.
	var stock, sum int
	if err := pool.QueryRow(context.Background(),
		`SELECT p.stock, COALESCE(SUM(m.delta),0)::int
		 FROM products p LEFT JOIN stock_movements m
		   ON m.salon_id=p.salon_id AND m.product_id=p.id
		 WHERE p.id=$1 GROUP BY p.stock`, shampoo.ID).Scan(&stock, &sum); err != nil {
		t.Fatalf("invariante: %v", err)
	}
	if stock != sum {
		t.Fatalf("stock %d != Σ delta %d", stock, sum)
	}

	// Libro: más reciente primero y con limit.
	moves, err := repo.ListMovements("s-it-prod", shampoo.ID, 10)
	if err != nil || len(moves) != 2 {
		t.Fatalf("movimientos: %v %+v", err, moves)
	}
	if moves[0].Reason != domain.StockRestock || moves[1].Reason != domain.StockInitial {
		t.Fatalf("orden del libro: %+v", moves)
	}
	if one, _ := repo.ListMovements("s-it-prod", shampoo.ID, 1); len(one) != 1 || one[0].Delta != 3 {
		t.Fatalf("limit 1: %+v", one)
	}
	// Otro salón no ve movimientos.
	if other, _ := repo.ListMovements("s-it-prod2", shampoo.ID, 10); len(other) != 0 {
		t.Fatalf("movimientos de otro salón: %+v", other)
	}

	// La DB es la última barrera: el CHECK impide un stock negativo y un
	// movimiento de cero o con motivo desconocido.
	if _, err := pool.Exec(context.Background(), `UPDATE products SET stock=-1 WHERE id=$1`, shampoo.ID); err == nil {
		t.Fatal("el CHECK tiene que impedir stock negativo")
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO stock_movements(id,salon_id,product_id,delta,reason) VALUES($1,'s-it-prod',$2,0,'correction')`,
		domain.NewID(), shampoo.ID); err == nil {
		t.Fatal("delta 0 no es un movimiento")
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO stock_movements(id,salon_id,product_id,delta,reason) VALUES($1,'s-it-prod',$2,1,'otro')`,
		domain.NewID(), shampoo.ID); err == nil {
		t.Fatal("motivo fuera del catálogo")
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO products(id,salon_id,name,price_cents) VALUES($1,'s-it-prod','Malo',-1)`,
		domain.NewID()); err == nil {
		t.Fatal("precio negativo debe fallar")
	}
}

// La migración 029 re-aplicada sobre datos existentes no cambia NINGUNA fila
// (md5 de sales, sale_items y services idéntico) y las ventas existentes quedan
// con products_cents = 0.
func TestMigration029_overData_IT(t *testing.T) {
	pool, _ := productFixtures(t)
	ctx := context.Background()
	servicesRepo := pgstore.Services{Pool: pool}
	corte, _ := domain.NewService("s-it-prod", "Corte 029", 45, 150000, 30)
	if err := servicesRepo.Save(corte); err != nil {
		t.Fatalf("servicio: %v", err)
	}
	// Venta CLÁSICA (Sales.Save, sin productos): products_cents default 0.
	sale, err := domain.NewSale("s-it-prod", "u-it-prod-prof", "Previa 029", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("venta: %v", err)
	}
	if err := (pospostgres.Sales{Pool: pool}).Save(sale); err != nil {
		t.Fatalf("guardar venta: %v", err)
	}
	if err := (pgstore.Products{Pool: pool}).Create(mustProduct(t, "s-it-prod", "Previo 029", 100, 1, 0), nil); err != nil {
		t.Fatalf("producto previo: %v", err)
	}

	md5Of := func(query string) string {
		t.Helper()
		var sum string
		if err := pool.QueryRow(ctx, query).Scan(&sum); err != nil {
			t.Fatalf("md5: %v", err)
		}
		return sum
	}
	salesMD5 := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM sales t WHERE salon_id LIKE 's-it-prod%'`)
	itemsMD5 := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM sale_items t`)
	servicesMD5 := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM services t WHERE salon_id LIKE 's-it-prod%'`)
	var zero int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sales WHERE salon_id LIKE 's-it-prod%' AND products_cents <> 0`).Scan(&zero); err != nil || zero != 0 {
		t.Fatalf("ventas existentes con products_cents = 0: %d (%v)", zero, err)
	}

	itdb.ApplyMigrations(t, pool) // dos pasadas más de 029 (y de todo)
	if got := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM sales t WHERE salon_id LIKE 's-it-prod%'`); got != salesMD5 {
		t.Fatalf("md5 de sales cambió: %s != %s", got, salesMD5)
	}
	if got := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM sale_items t`); got != itemsMD5 {
		t.Fatalf("md5 de sale_items cambió")
	}
	if got := md5Of(`SELECT md5(COALESCE(string_agg(t::text,'|' ORDER BY t.id),'')) FROM services t WHERE salon_id LIKE 's-it-prod%'`); got != servicesMD5 {
		t.Fatalf("md5 de services cambió")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sales WHERE salon_id LIKE 's-it-prod%' AND products_cents <> 0`).Scan(&zero); err != nil || zero != 0 {
		t.Fatalf("tras re-migrar, products_cents = 0: %d (%v)", zero, err)
	}
}

// El nombre del producto se mide en caracteres (no bytes) y la clave única es
// por salón y por nombre activo: los tres casos quedan en la DB.
func TestProducts_constraints_IT(t *testing.T) {
	pool, _ := productFixtures(t)
	repo := pgstore.Products{Pool: pool}
	long := make([]rune, 0, 121)
	for i := 0; i < 121; i++ {
		long = append(long, 'ñ')
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO products(id,salon_id,name,price_cents) VALUES($1,'s-it-prod',$2,100)`,
		domain.NewID(), string(long)); err == nil {
		t.Fatal("el CHECK de nombre 1..120 tiene que fallar")
	}
	p := mustProduct(t, "s-it-prod", strings.Repeat("ñ", 120), 100, 0, 0)
	if err := repo.Create(p, nil); err != nil {
		t.Fatalf("120 caracteres (aunque sean 240 bytes): %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO products(id,salon_id,name,price_cents) VALUES($1,'s-it-prod','X',10000001)`,
		domain.NewID()); err == nil {
		t.Fatal("precio fuera de rango tiene que fallar")
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO products(id,salon_id,name) VALUES($1,'no-existe','X')`, domain.NewID()); err == nil {
		t.Fatal("FK de salón tiene que fallar")
	}
	_ = fmt.Sprintf("%v", long)
}
