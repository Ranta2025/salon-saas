// D6 del contrato F8: la nómina descuenta de `ventas` la parte de paquete de la
// venta (`total_cents - products_cents - packages_cents`). Es la ÚNICA línea
// compartida que F8 toca. Se prueba contra Postgres real y de forma DIFERENCIAL:
//
//	(a) con datos SIN paquetes el resultado es IDÉNTICO al de la consulta
//	    anterior (la de `main`, copiada abajo como referencia);
//	(b) con una venta de paquete en el período, `sales_total` NO la incluye
//	    (sí la cuenta en `sales_count`, igual que las de solo productos) y la
//	    comisión no cambia (un paquete no tiene líneas de servicio).
//
// Se saltea sin SALON_IT=1.
package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// payrollVentasMain es el CTE `ventas` TAL COMO ESTÁ en `main` (F6/D6): la
// referencia contra la que se compara. Solo se usa para sumar el total y contar.
const payrollVentasMain = `
	SELECT professional_id, COUNT(*)::int, SUM(total_cents - products_cents)::bigint
	FROM sales
	WHERE salon_id=$1 AND created_at >= $2 AND created_at < $3
	GROUP BY professional_id`

type refTotals struct {
	count int
	total int64
}

func referenceVentas(t *testing.T, pool *pgxpool.Pool, salonID string, from, to time.Time) map[string]refTotals {
	t.Helper()
	rows, err := pool.Query(context.Background(), payrollVentasMain, salonID, from.UTC(), to.UTC())
	if err != nil {
		t.Fatalf("consulta de referencia: %v", err)
	}
	defer rows.Close()
	out := map[string]refTotals{}
	for rows.Next() {
		var prof string
		var r refTotals
		if err := rows.Scan(&prof, &r.count, &r.total); err != nil {
			t.Fatal(err)
		}
		out[prof] = r
	}
	return out
}

func TestPackagesPayrollIT_ventas_excludes_package_sales(t *testing.T) {
	f := pkgFixtures(t)
	pool, ctx := f.pool, f.ctx
	// Un owner para pedir la vista previa.
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-pkg-owner','Dueña Paquetes','pkgowner@test.com','x')`); err != nil {
		t.Fatal(err)
	}
	owner := domain.Membership{UserID: "u-it-pkg-owner", SalonID: "s-it-pkg", Role: domain.RoleOwner}
	if err := (Memberships{Pool: pool}).Save(owner); err != nil {
		t.Fatal(err)
	}
	// Marzo de 2026 en Auckland (NZDT +13): [28/2 11:00Z, 31/3 11:00Z).
	from := time.Date(2026, 2, 28, 11, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 31, 11, 0, 0, 0, time.UTC)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	// Datos SIN paquetes: dos ventas de servicios y una venta de solo productos.
	insertPaySale(t, ctx, pool, "sale-pkgpay-1", "s-it-pkg", "u-it-pkg-prof", at, 30000,
		paySaleItem{serviceID: f.svcA.ID, qty: 2, priceCent: 15000, pct: intPtr(10)})
	insertPaySale(t, ctx, pool, "sale-pkgpay-2", "s-it-pkg", "u-it-pkg-prof", at.Add(time.Hour), 5000,
		paySaleItem{serviceID: f.svcB.ID, qty: 1, priceCent: 5000, pct: intPtr(20)})
	insertPaySale(t, ctx, pool, "sale-pkgpay-3", "s-it-pkg", "u-it-pkg-prof", at.Add(2*time.Hour), 7000)
	if _, err := pool.Exec(ctx, `UPDATE sales SET products_cents=7000 WHERE id='sale-pkgpay-3'`); err != nil {
		t.Fatal(err)
	}

	repo := PayrollSales{Pool: pool}
	svc := app.PayrollService{
		Pay: PayModels{Pool: pool}, Sales: repo, Memberships: Memberships{Pool: pool}, Settings: Salons{Pool: pool},
		Now: func() time.Time { return time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) },
	}
	previewLine := func() app.PayrollLine {
		t.Helper()
		p, err := svc.Preview(owner, "s-it-pkg", "month", "2026-03-10")
		if err != nil {
			t.Fatalf("vista previa: %v", err)
		}
		for _, l := range p.Lines {
			if l.UserID == "u-it-pkg-prof" {
				return l
			}
		}
		t.Fatalf("la vista previa no trae al profesional: %+v", p.Lines)
		return app.PayrollLine{}
	}

	// (a) SIN paquetes: idéntico a `main`. 3 ventas; total = 30000 + 5000 + (7000 -
	// 7000 de productos) = 35000; base = 2×15000×10 + 5000×20 = 400000 -> 4000.
	beforeRef := referenceVentas(t, pool, "s-it-pkg", from, to)["u-it-pkg-prof"]
	totals, err := repo.CommissionTotals("s-it-pkg", from, to)
	if err != nil || len(totals) != 1 {
		t.Fatalf("agregado sin paquetes: %+v %v", totals, err)
	}
	got := totals[0]
	if got.SalesCount != beforeRef.count || int64(got.SalesTotalCents) != beforeRef.total {
		t.Fatalf("SIN paquetes difiere de main: %d/%d vs %d/%d", got.SalesCount, got.SalesTotalCents, beforeRef.count, beforeRef.total)
	}
	if got.SalesCount != 3 || got.SalesTotalCents != 35000 || got.CommissionBase != 400000 || got.CommissionCents() != 4000 {
		t.Fatalf("números a mano sin paquetes: %+v", got)
	}
	line := previewLine()
	if line.SalesCount != 3 || line.SalesTotalCents != 35000 || line.CommissionCents != 4000 {
		t.Fatalf("vista previa sin paquetes: %+v", line)
	}

	// (b) CON una venta de paquete en el período (60000): entra por el camino real.
	pkg := f.newPackage(t, "Pack nómina", 5, 60000, nil)
	order := f.order(t, pkg, f.lia, "", at.Add(3*time.Hour))
	if err := f.owned.Purchase(order); err != nil {
		t.Fatalf("comprar paquete: %v", err)
	}
	// `main` (sin la línea de F8) incluiría los 60000 en `sales_total`: el test es
	// sensible al cambio.
	withPkgRef := referenceVentas(t, pool, "s-it-pkg", from, to)["u-it-pkg-prof"]
	if withPkgRef.total != 95000 || withPkgRef.count != 4 {
		t.Fatalf("la referencia anterior debía ver el paquete: %+v", withPkgRef)
	}
	totals, err = repo.CommissionTotals("s-it-pkg", from, to)
	if err != nil || len(totals) != 1 {
		t.Fatalf("agregado con paquete: %+v %v", totals, err)
	}
	got = totals[0]
	// La venta del paquete CUENTA como venta (4) pero NO suma a `sales_total` (35000)
	// ni a la base de comisión (no tiene líneas de servicio).
	if got.SalesCount != 4 || got.SalesTotalCents != 35000 || got.CommissionBase != 400000 || got.CommissionCents() != 4000 {
		t.Fatalf("con paquete: %+v", got)
	}
	line = previewLine()
	if line.SalesCount != 4 || line.SalesTotalCents != 35000 || line.CommissionCents != 4000 {
		t.Fatalf("vista previa con paquete: %+v", line)
	}
	// «Mis comisiones» (el agregado de una persona) coincide.
	one, err := repo.CommissionTotalFor("s-it-pkg", "u-it-pkg-prof", from, to)
	if err != nil || one.SalesCount != 4 || one.SalesTotalCents != 35000 {
		t.Fatalf("agregado de una persona: %+v %v", one, err)
	}
	// El dinero del paquete SÍ está en `sales.total_cents` (lo que leen la caja, el
	// dashboard y los exportes): F8 no lo esconde, solo la nómina lo descuenta.
	var total, pkgCents int
	if err := pool.QueryRow(ctx, `SELECT total_cents, packages_cents FROM sales WHERE id=$1`, order.Sale.ID).Scan(&total, &pkgCents); err != nil || total != 60000 || pkgCents != 60000 {
		t.Fatalf("la venta del paquete: %d/%d %v", total, pkgCents, err)
	}
	if sum, err := (Sales{Pool: pool}).SumCents("s-it-pkg", from, to, ""); err != nil || sum != 30000+5000+7000+60000 {
		t.Fatalf("SumCents (caja/dashboard) debe incluir el paquete: %d %v", sum, err)
	}
}
