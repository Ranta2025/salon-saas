// Integración con Postgres real de la venta CON productos (F6, D1/D3/D4/D6/D8).
// Se saltea sin SALON_IT=1.
package pgstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	payrollpostgres "salon-saas/internal/payroll/postgres"
	"salon-saas/internal/pos"
	pospostgres "salon-saas/internal/pos/postgres"
	"salon-saas/internal/testutil/itdb"
)

// saleProductFixture siembra catálogo de servicios y productos en s-it-prod.
func saleProductFixture(t *testing.T) (*pgxpool.Pool, context.Context, pgstore.Services, pgstore.Products, pospostgres.SaleProducts) {
	t.Helper()
	pool, ctx := productFixtures(t)
	return pool, ctx, pgstore.Services{Pool: pool}, pgstore.Products{Pool: pool}, pospostgres.SaleProducts{Pool: pool}
}

type productSpec struct {
	name  string
	price int
	stock int
	low   int
}

func seedProducts(t *testing.T, repo pgstore.Products, salonID string, specs ...productSpec) []domain.Product {
	t.Helper()
	out := make([]domain.Product, 0, len(specs))
	for _, s := range specs {
		p := mustProduct(t, salonID, s.name, s.price, s.stock, s.low)
		var initial *domain.StockMovement
		if p.Stock > 0 {
			initial = &domain.StockMovement{ID: domain.NewID(), SalonID: salonID, ProductID: p.ID, Delta: p.Stock, Reason: domain.StockInitial}
		}
		if err := repo.Create(p, initial); err != nil {
			t.Fatalf("crear %q: %v", s.name, err)
		}
		out = append(out, p)
	}
	return out
}

func sumMovements(t *testing.T, pool *pgxpool.Pool, productID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(delta),0)::int FROM stock_movements WHERE product_id=$1`, productID).Scan(&n); err != nil {
		t.Fatalf("sumar movimientos: %v", err)
	}
	return n
}

func TestSaleWithProducts_IT(t *testing.T) {
	pool, _, services, products, sales := saleProductFixture(t)
	ctx := context.Background()

	corte, err := domain.NewService("s-it-prod", "Corte F6", 45, 150000, 40)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := services.Save(corte); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	seed := seedProducts(t, products, "s-it-prod",
		productSpec{"Shampoo F6", 50000, 10, 2},
		productSpec{"Cera F6", 30000, 5, 0},
		productSpec{"Casi F6", 10000, 1, 0},
	)
	shampoo, cera, casi := seed[0], seed[1], seed[2]

	// Venta SOLO de productos: total = Σ líneas, products_cents = total.
	only, err := domain.NewSaleWithProducts("s-it-prod", "u-it-prod-prof", "Lía F6", nil, []domain.SaleProduct{
		{ProductID: shampoo.ID, Qty: 2, UnitPriceCents: shampoo.PriceCents},
		{ProductID: cera.ID, Qty: 1, UnitPriceCents: cera.PriceCents},
	}, domain.MethodCard)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if crossed, err := sales.SaveWithProducts(only); err != nil || len(crossed) != 0 {
		t.Fatalf("guardar venta de productos: %v (cruzados %+v)", err, crossed)
	}
	var total, productsCents int
	if err := pool.QueryRow(ctx, `SELECT total_cents, products_cents FROM sales WHERE id=$1`, only.ID).Scan(&total, &productsCents); err != nil {
		t.Fatalf("leer venta: %v", err)
	}
	if total != 130000 || productsCents != 130000 {
		t.Fatalf("venta guardada: total=%d productos=%d", total, productsCents)
	}
	if n := itdb.CountRows(t, pool, `SELECT count(*) FROM sale_products WHERE sale_id=$1`, only.ID); n != 2 {
		t.Fatalf("sale_products = 2, dio %d", n)
	}
	if got := stockOf(t, pool, shampoo.ID); got != 8 {
		t.Fatalf("stock shampoo = 8, dio %d", got)
	}
	if sumMovements(t, pool, shampoo.ID) != 8 {
		t.Fatalf("el libro no cuadra con el stock")
	}
	// Un movimiento `sale` por línea, apuntando a la venta.
	if n := itdb.CountRows(t, pool, `SELECT count(*) FROM stock_movements WHERE sale_id=$1 AND reason='sale' AND delta < 0`, only.ID); n != 2 {
		t.Fatalf("movimientos de venta = 2, dio %d", n)
	}

	// Foto del precio: cambiar el catálogo después NO mueve la venta.
	up, err := shampoo.WithDetails(shampoo.Name, 99000, shampoo.LowStockThreshold, true)
	if err != nil {
		t.Fatalf("dominio update: %v", err)
	}
	if err := products.Save(up); err != nil {
		t.Fatalf("cambiar precio: %v", err)
	}
	var unit int
	if err := pool.QueryRow(ctx, `SELECT unit_price_cents FROM sale_products WHERE sale_id=$1 AND product_id=$2`, only.ID, shampoo.ID).Scan(&unit); err != nil {
		t.Fatalf("precio de la línea: %v", err)
	}
	if unit != 50000 {
		t.Fatalf("precio congelado en la línea: %d", unit)
	}
	if total = 0; pool.QueryRow(ctx, `SELECT total_cents FROM sales WHERE id=$1`, only.ID).Scan(&total) != nil || total != 130000 {
		t.Fatalf("el total de la venta cambió: %d", total)
	}

	// Venta MIXTA: total = servicios + productos.
	mixed, err := domain.NewSaleWithProducts("s-it-prod", "u-it-prod-prof", "Lía F6", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: corte.PriceCents, CommissionPct: &corte.CommissionPct},
	}, []domain.SaleProduct{{ProductID: shampoo.ID, Qty: 1, UnitPriceCents: shampoo.PriceCents}}, domain.MethodCash)
	if err != nil {
		t.Fatalf("dominio mixta: %v", err)
	}
	if _, err := sales.SaveWithProducts(mixed); err != nil {
		t.Fatalf("guardar mixta: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT total_cents, products_cents FROM sales WHERE id=$1`, mixed.ID).Scan(&total, &productsCents); err != nil {
		t.Fatalf("leer mixta: %v", err)
	}
	if total != 200000 || productsCents != 50000 {
		t.Fatalf("mixta: total=%d productos=%d", total, productsCents)
	}
	if n := itdb.CountRows(t, pool, `SELECT count(*) FROM sale_items WHERE sale_id=$1`, mixed.ID); n != 1 {
		t.Fatalf("sale_items de la mixta = 1, dio %d", n)
	}

	// Sin stock: 409 con el nombre y NADA guardado (ni la venta ni el descuento).
	salesBefore := itdb.CountRows(t, pool, `SELECT count(*) FROM sales WHERE salon_id='s-it-prod'`)
	_, err = sales.SaveWithProducts(domain.SaleWithProducts{
		Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "X", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
		Products:      []domain.SaleProduct{{ProductID: casi.ID, Qty: 2, UnitPriceCents: 10000}},
		ProductsCents: 20000,
	})
	if err == nil || err.Error() != domain.NoStockMessage("Casi F6") || !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("sin stock: %v", err)
	}
	if got := itdb.CountRows(t, pool, `SELECT count(*) FROM sales WHERE salon_id='s-it-prod'`); got != salesBefore {
		t.Fatalf("la venta fallida no se guarda: %d -> %d", salesBefore, got)
	}
	if got := stockOf(t, pool, casi.ID); got != 1 {
		t.Fatalf("el stock no cambia: %d", got)
	}

	// El segundo producto no alcanza: el PRIMERO queda sin descontar y no hay venta.
	shampooBefore := stockOf(t, pool, shampoo.ID)
	_, err = sales.SaveWithProducts(domain.SaleWithProducts{
		Sale: domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "X", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
		Products: []domain.SaleProduct{
			{ProductID: shampoo.ID, Qty: 1, UnitPriceCents: shampoo.PriceCents},
			{ProductID: casi.ID, Qty: 2, UnitPriceCents: 10000},
		},
		ProductsCents: 70000,
	})
	if err == nil {
		t.Fatal("el segundo producto no alcanza")
	}
	if got := stockOf(t, pool, shampoo.ID); got != shampooBefore {
		t.Fatalf("el primer producto quedó descontado: %d -> %d", shampooBefore, got)
	}
	if got := itdb.CountRows(t, pool, `SELECT count(*) FROM sales WHERE salon_id='s-it-prod'`); got != salesBefore {
		t.Fatalf("la venta fallida no se guarda: %d -> %d", salesBefore, got)
	}

	// Un producto inactivo no se vende (400 «Producto inválido»).
	off, _ := shampoo.WithDetails(shampoo.Name, shampoo.PriceCents, shampoo.LowStockThreshold, false)
	if err := products.Save(off); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	_, err = sales.SaveWithProducts(domain.SaleWithProducts{
		Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "X", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
		Products:      []domain.SaleProduct{{ProductID: shampoo.ID, Qty: 1, UnitPriceCents: 50000}},
		ProductsCents: 50000,
	})
	if err == nil || err.Error() != domain.MsgProductInvalid || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("inactivo: %v", err)
	}

	// D6: la nómina ve SOLO la parte de servicios de una venta mixta.
	now := time.Now().UTC()
	from, to := now.Add(-2*time.Hour), now.Add(2*time.Hour)
	payroll := payrollpostgres.PayrollSales{Pool: pool}
	totals, err := payroll.CommissionTotals("s-it-prod", from, to)
	if err != nil || len(totals) != 1 {
		t.Fatalf("nómina: %v %+v", err, totals)
	}
	got := totals[0]
	if got.SalesCount != 2 || got.SalesTotalCents != 150000 {
		t.Fatalf("sales_count/total de servicios: %d/%d", got.SalesCount, got.SalesTotalCents)
	}
	if got.CommissionBase != 1*150000*40 {
		t.Fatalf("la comisión solo sale de los ítems de servicio: %d", got.CommissionBase)
	}
}

func TestSaleWithProducts_concurrency_IT(t *testing.T) {
	pool, _, _, products, sales := saleProductFixture(t)

	// (a) N ventas simultáneas del MISMO producto con stock S: exactamente S
	// unidades vendidas, el resto 409, stock final 0 = Σ movimientos.
	limited := seedProducts(t, products, "s-it-prod", productSpec{"Limitado", 10000, 4, 0})[0]
	const buyers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, rejected := 0, 0
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := sales.SaveWithProducts(domain.SaleWithProducts{
				Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "C", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
				Products:      []domain.SaleProduct{{ProductID: limited.ID, Qty: 1, UnitPriceCents: 10000}},
				ProductsCents: 10000,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrAlreadyExists) && err.Error() == domain.NoStockMessage("Limitado"):
				rejected++
			default:
				t.Errorf("error inesperado en la carrera: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 4 || rejected != 6 {
		t.Fatalf("vendidas %d / rechazadas %d, quería 4/6", ok, rejected)
	}
	if got := stockOf(t, pool, limited.ID); got != 0 {
		t.Fatalf("stock final = 0, dio %d", got)
	}
	if sumMovements(t, pool, limited.ID) != 0 {
		t.Fatalf("stock != Σ movimientos: %d", sumMovements(t, pool, limited.ID))
	}
	if n := itdb.CountRows(t, pool, `SELECT COALESCE(SUM(qty),0) FROM sale_products WHERE product_id=$1`, limited.ID); n != 4 {
		t.Fatalf("unidades vendidas = 4, dio %d", n)
	}

	// (b) Orden cruzado A,B / B,A en paralelo: sin interbloqueo ni 500.
	ab := seedProducts(t, products, "s-it-prod",
		productSpec{"Cruzada A", 1000, 100, 0},
		productSpec{"Cruzada B", 2000, 100, 0},
	)
	var wg2 sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			lines := []domain.SaleProduct{
				{ProductID: ab[0].ID, Qty: 1, UnitPriceCents: 1000},
				{ProductID: ab[1].ID, Qty: 1, UnitPriceCents: 2000},
			}
			if i%2 == 1 {
				lines[0], lines[1] = lines[1], lines[0]
			}
			if _, err := sales.SaveWithProducts(domain.SaleWithProducts{
				Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "C", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
				Products:      lines,
				ProductsCents: 3000,
			}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg2.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("orden cruzado devolvió error: %v", err)
	}
	for _, p := range ab {
		if got := stockOf(t, pool, p.ID); got != 92 {
			t.Fatalf("%s: stock 92, dio %d", p.Name, got)
		}
	}

	// (c) 16 pedidos con el MISMO client_op_id: UN solo descuento. En el
	// adaptador, uno gana y los otros reciben ErrAlreadyExists (el caso de uso
	// devuelve la venta original).
	op := seedProducts(t, products, "s-it-prod", productSpec{"Idempotente", 1000, 5, 0})[0]
	const same = 16
	var wg3 sync.WaitGroup
	var mu3 sync.Mutex
	wins := 0
	for i := 0; i < same; i++ {
		wg3.Add(1)
		go func() {
			defer wg3.Done()
			swp := domain.SaleWithProducts{
				Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "C", Method: domain.MethodCash, ClientOpID: "op-f6", CreatedAt: time.Now().UTC()},
				Products:      []domain.SaleProduct{{ProductID: op.ID, Qty: 1, UnitPriceCents: 1000}},
				ProductsCents: 1000,
			}
			_, err := sales.SaveWithProducts(swp)
			mu3.Lock()
			defer mu3.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, domain.ErrAlreadyExists):
			default:
				t.Errorf("error inesperado con el mismo op id: %v", err)
			}
		}()
	}
	wg3.Wait()
	if wins != 1 {
		t.Fatalf("ganó %d vez/veces, quería 1", wins)
	}
	if got := stockOf(t, pool, op.ID); got != 4 {
		t.Fatalf("descontó %d vez/veces (stock %d)", 5-got, got)
	}
	if n := itdb.CountRows(t, pool, `SELECT count(*) FROM sales WHERE salon_id='s-it-prod' AND client_op_id='op-f6'`); n != 1 {
		t.Fatalf("ventas con el mismo op id = 1, dio %d", n)
	}

	// (f) Ajustes en paralelo con ventas: stock = Σ delta.
	mixedUse := seedProducts(t, products, "s-it-prod", productSpec{"Mixto", 1000, 50, 0})[0]
	var wg4 sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg4.Add(1)
		go func() {
			defer wg4.Done()
			_, _ = sales.SaveWithProducts(domain.SaleWithProducts{
				Sale:          domain.Sale{ID: domain.NewID(), SalonID: "s-it-prod", ProfessionalID: "u-it-prod-prof", ClientName: "C", Method: domain.MethodCash, CreatedAt: time.Now().UTC()},
				Products:      []domain.SaleProduct{{ProductID: mixedUse.ID, Qty: 2, UnitPriceCents: 1000}},
				ProductsCents: 2000,
			})
		}()
	}
	for i := 0; i < 5; i++ {
		wg4.Add(1)
		go func() {
			defer wg4.Done()
			_, _ = products.Adjust("s-it-prod", mixedUse.ID, 1, domain.StockRestock, "", "u-it-prod-owner")
		}()
	}
	wg4.Wait()
	if got, sum := stockOf(t, pool, mixedUse.ID), sumMovements(t, pool, mixedUse.ID); got != sum {
		t.Fatalf("invariante roto: stock %d != Σ delta %d", got, sum)
	}

	// (g) App completa: 16 pedidos con el MISMO client_op_id devuelven la MISMA
	// venta y descuentan UNA vez.
	mems := pgstore.Memberships{Pool: pool}
	owner, _ := mems.Find("u-it-prod-owner", "s-it-prod")
	idem := seedProducts(t, products, "s-it-prod", productSpec{"Idempotente App", 1000, 5, 0})[0]
	svc := pos.SaleService{
		Sales: pospostgres.Sales{Pool: pool}, Services: pgstore.Services{Pool: pool}, Memberships: mems,
		Products: products, ProductSales: sales,
	}
	var wg5 sync.WaitGroup
	ids := make(chan string, same)
	errCh := make(chan error, same)
	for i := 0; i < same; i++ {
		wg5.Add(1)
		go func() {
			defer wg5.Done()
			sale, err := svc.RegisterSaleWithProducts(owner, "s-it-prod", "u-it-prod-prof", "C", "",
				nil, []domain.SaleProduct{{ProductID: idem.ID, Qty: 1}}, domain.MethodCash, "op-f6-app")
			if err != nil {
				errCh <- err
				return
			}
			ids <- sale.ID
		}()
	}
	wg5.Wait()
	close(ids)
	close(errCh)
	for err := range errCh {
		t.Fatalf("app con el mismo op id: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("devolvió %d ventas distintas, quería 1", len(seen))
	}
	if got := stockOf(t, pool, idem.ID); got != 4 {
		t.Fatalf("la app descontó de más: stock %d", got)
	}
}
