// Integración contra Postgres real de la FOTO de comisión en cada venta
// (F3.1, S-F3.1-1 y S-F3.1-2). Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run CommissionSnapshot
//
// Lo que se prueba acá SOLO se puede probar con la DB de verdad: que la columna
// existe con su CHECK, que la venta nueva copia la tarifa vigente ítem por ítem,
// que editar el catálogo después no mueve lo vendido, que el repetido no
// duplica la foto, que dos ventas simultáneas con un cambio de porcentaje en
// medio guardan un valor coherente, y que una línea que falla no deja venta ni
// foto parcial.
package pgstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
)

// commissionFixtures migra, limpia y deja un salón con recepción + profesional.
func commissionFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	applyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-foto%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-foto%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-foto-recep','Recep IT','foto-recep@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-foto-prof','Prof IT','foto-prof@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES('s-it-foto','s-it-foto','Salón Foto IT')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-foto-recep", SalonID: "s-it-foto", Role: domain.RoleReception},
		{UserID: "u-it-foto-prof", SalonID: "s-it-foto", Role: domain.RoleProfessional, Attends: true},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	return pool, ctx
}

// foto lee la comisión guardada de cada línea de una venta, por servicio, como
// TEXTO: así se distingue NULL de 0 (que es un porcentaje real, no "sin foto").
func foto(t *testing.T, ctx context.Context, pool *pgxpool.Pool, saleID string) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT service_id, COALESCE(commission_pct::text,'NULL') FROM sale_items
		 WHERE sale_id=$1`, saleID)
	if err != nil {
		t.Fatalf("leer foto de la venta: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var svcID, v string
		if err := rows.Scan(&svcID, &v); err != nil {
			t.Fatalf("scan foto: %v", err)
		}
		out[svcID] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("filas de la foto: %v", err)
	}
	return out
}

// saleService arma el caso de uso REAL contra la DB real.
func saleService(pool *pgxpool.Pool) pos.SaleService {
	return pos.SaleService{
		Sales:       Sales{Pool: pool},
		Services:    Services{Pool: pool},
		Memberships: Memberships{Pool: pool},
	}
}

func TestSaleCommissionSnapshot_IT(t *testing.T) {
	pool, ctx := commissionFixtures(t)
	services := Services{Pool: pool}
	svc := saleService(pool)

	corte, _ := domain.NewService("s-it-foto", "Corte Foto", 45, 150000, 30)
	gratis, _ := domain.NewService("s-it-foto", "Cortesía Foto", 30, 0, 0)
	for _, s := range []domain.Service{corte, gratis} {
		if err := services.Save(s); err != nil {
			t.Fatalf("catálogo: %v", err)
		}
	}
	recep, err := Memberships{Pool: pool}.Find("u-it-foto-recep", "s-it-foto")
	if err != nil {
		t.Fatalf("actor: %v", err)
	}

	// (1) Venta NUEVA: dos servicios con porcentajes distintos, cada ítem con su
	// foto. (3) Comisión 0 se guarda como 0, NO como NULL.
	sale, err := svc.RegisterSaleWithOp(recep, "s-it-foto", "u-it-foto-prof", "Lía foto", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000},
		{ServiceID: gratis.ID, Qty: 2, UnitPriceCents: 0},
	}, domain.MethodCash, "op-foto-1")
	if err != nil {
		t.Fatalf("venta nueva: %v", err)
	}
	if got := foto(t, ctx, pool, sale.ID); len(got) != 2 || got[gratis.ID] != "0" || got[corte.ID] != "30" {
		t.Fatalf("foto por ítem = corte 30 / cortesía 0, dio %v", got)
	}
	// Y la venta que devuelve el caso de uso lleva la misma foto.
	if sale.Items[0].CommissionPct == nil || *sale.Items[0].CommissionPct != 30 {
		t.Fatalf("la entidad vendida debe traer la foto 30: %v", sale.Items[0].CommissionPct)
	}

	// (2) El dueño edita el catálogo: lo vendido NO se mueve (se lee la fila).
	up, _ := corte.WithDetails(corte.Name, corte.DurationMin, corte.PriceCents, 99, true)
	if err := services.Save(up); err != nil {
		t.Fatalf("editar catálogo: %v", err)
	}
	if got := foto(t, ctx, pool, sale.ID); got[corte.ID] != "30" || got[gratis.ID] != "0" {
		t.Fatalf("editar el catálogo cambió la foto vieja: %v", got)
	}

	// (4) Repetido por client_op_id: devuelve la venta ORIGINAL con su foto
	// original, sin tocar ni duplicar líneas (el catálogo ya vale 99).
	replay, err := svc.RegisterSaleWithOp(recep, "s-it-foto", "u-it-foto-prof", "Lía foto", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000},
	}, domain.MethodCash, "op-foto-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ID != sale.ID || len(replay.Items) != 2 {
		t.Fatalf("el replay debe devolver la venta original: %+v", replay)
	}
	// El repo devuelve las líneas ordenadas por id (no por orden de la venta),
	// así que la foto se busca por servicio.
	pctDe := func(items []domain.SaleItem, serviceID string) *int {
		for _, it := range items {
			if it.ServiceID == serviceID {
				return it.CommissionPct
			}
		}
		t.Fatalf("el replay no trae el servicio %s: %+v", serviceID, items)
		return nil
	}
	if p := pctDe(replay.Items, corte.ID); p == nil || *p != 30 {
		t.Fatalf("el replay debe traer la foto original (30), dio %v", p)
	}
	if p := pctDe(replay.Items, gratis.ID); p == nil || *p != 0 {
		t.Fatalf("el replay debe traer la foto 0 de la cortesía, dio %v", p)
	}
	if got := foto(t, ctx, pool, sale.ID); len(got) != 2 || got[corte.ID] != "30" {
		t.Fatalf("el replay duplicó o movió la foto: %v", got)
	}
	var ventas int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sales WHERE salon_id='s-it-foto' AND client_op_id='op-foto-1'`).Scan(&ventas); err != nil {
		t.Fatalf("contar ventas del op id: %v", err)
	}
	if ventas != 1 {
		t.Fatalf("el replay no debe crear otra venta, hay %d", ventas)
	}

	// La venta NUEVA posterior sí lleva la tarifa vigente (99).
	nueva, err := svc.RegisterSale(recep, "s-it-foto", "u-it-foto-prof", "Ana foto", []domain.SaleItem{
		{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000},
	}, domain.MethodCard)
	if err != nil {
		t.Fatalf("venta posterior: %v", err)
	}
	if got := foto(t, ctx, pool, nueva.ID); len(got) != 1 || got[corte.ID] != "99" {
		t.Fatalf("la venta nueva debe llevar 99, dio %v", got)
	}

	// El CHECK de la DB sigue de guardia: 101 no entra ni por SQL directo.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents,commission_pct)
		 VALUES('si-it-foto-bad',$1,$2,1,1000,101)`, sale.ID, corte.ID); err == nil {
		t.Fatal("el CHECK debe rechazar 101")
	}
}

// (6) Una línea que falla deja la venta sin rastro: ni venta ni foto parcial.
// El camino es el repo directo, porque el caso de uso valida el catálogo antes
// y acá lo que importa es la transacción de la DB.
func TestSaleCommissionSnapshot_item_fallido_no_deja_nada_IT(t *testing.T) {
	pool, ctx := commissionFixtures(t)
	services := Services{Pool: pool}
	repo := Sales{Pool: pool}

	ok, _ := domain.NewService("s-it-foto", "Corte Rollback", 45, 150000, 40)
	if err := services.Save(ok); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	pct := 40
	bad, err := domain.NewSale("s-it-foto", "u-it-foto-prof", "Fantasma", []domain.SaleItem{
		{ServiceID: ok.ID, Qty: 1, UnitPriceCents: 150000, CommissionPct: &pct},
		{ServiceID: "svc-no-existe-foto", Qty: 1, UnitPriceCents: 1000, CommissionPct: &pct},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Save(bad); err == nil {
		t.Fatal("la venta con un ítem inexistente debe fallar")
	}
	var sales, items int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sales WHERE id=$1`, bad.ID).Scan(&sales); err != nil {
		t.Fatalf("contar ventas: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sale_items WHERE sale_id=$1`, bad.ID).Scan(&items); err != nil {
		t.Fatalf("contar ítems: %v", err)
	}
	if sales != 0 || items != 0 {
		t.Fatalf("no debe quedar venta ni foto parcial: ventas=%d ítems=%d", sales, items)
	}
}

// (5) Dos ventas simultáneas mientras se edita el porcentaje: cada línea guarda
// un valor coherente (viejo o nuevo), nunca NULL y nunca un valor fuera de los
// dos posibles. La foto se copia con el UPDATE del catálogo concurrente.
func TestSaleCommissionSnapshot_concurrente_con_cambio_de_tarifa_IT(t *testing.T) {
	pool, ctx := commissionFixtures(t)
	services := Services{Pool: pool}
	svc := saleService(pool)

	const viejo, nuevo = 25, 75
	corte, _ := domain.NewService("s-it-foto", "Corte Carrera", 45, 150000, viejo)
	if err := services.Save(corte); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	recep, err := Memberships{Pool: pool}.Find("u-it-foto-recep", "s-it-foto")
	if err != nil {
		t.Fatalf("actor: %v", err)
	}

	var wg sync.WaitGroup
	ids := make([]string, 2)
	errs := make([]error, 2)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := svc.RegisterSale(recep, "s-it-foto", "u-it-foto-prof", fmt.Sprintf("Cliente %d", i),
				[]domain.SaleItem{{ServiceID: corte.ID, Qty: 1, UnitPriceCents: 150000}}, domain.MethodCash)
			ids[i], errs[i] = s.ID, err
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// El admin cambia la tarifa mientras las ventas están en vuelo.
		up, _ := corte.WithDetails(corte.Name, corte.DurationMin, corte.PriceCents, nuevo, true)
		if err := services.Save(up); err != nil {
			t.Errorf("editar catálogo en paralelo: %v", err)
		}
	}()
	wg.Wait()

	// La tarifa quedó como la dejó el UPDATE.
	if got, _ := services.FindByID("s-it-foto", corte.ID); got.CommissionPct != nuevo {
		t.Fatalf("la tarifa debe quedar en %d, dio %d", nuevo, got.CommissionPct)
	}
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatalf("venta %d: %v", i, errs[i])
		}
		got := foto(t, ctx, pool, id)
		pct, ok := got[corte.ID]
		if len(got) != 1 || !ok {
			t.Fatalf("venta %d: esperaba 1 línea del corte, dio %v", i, got)
		}
		if pct != fmt.Sprint(viejo) && pct != fmt.Sprint(nuevo) {
			t.Fatalf("venta %d: foto %q no es coherente (debía ser %d o %d)", i, pct, viejo, nuevo)
		}
	}
}

// Una venta anterior a F3.1 (foto NULL) se lee como "sin foto": el repo NO la
// completa con 0, porque 0 es un porcentaje real y F3.2 tiene que poder
// distinguir "sin foto" de "comisión 0".
func TestSaleCommissionSnapshot_legacy_null_se_lee_sin_foto_IT(t *testing.T) {
	pool, ctx := commissionFixtures(t)
	services := Services{Pool: pool}
	repo := Sales{Pool: pool}

	viejo, _ := domain.NewService("s-it-foto", "Corte Viejo", 45, 150000, 20)
	if err := services.Save(viejo); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	// Venta "de antes de F3.1": se inserta sin la columna, como la dejó 005.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,client_op_id,created_at)
		 VALUES('sale-it-foto-legacy','s-it-foto','u-it-foto-prof','Vieja',150000,'cash','op-foto-legacy',now())`); err != nil {
		t.Fatalf("venta legacy: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents)
		 VALUES('si-it-foto-legacy','sale-it-foto-legacy',$1,1,150000)`, viejo.ID); err != nil {
		t.Fatalf("ítem legacy: %v", err)
	}
	got, err := repo.FindByClientOpID("s-it-foto", "op-foto-legacy")
	if err != nil {
		t.Fatalf("leer venta legacy: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].CommissionPct != nil {
		t.Fatalf("una venta sin foto debe volver nil, dio %v", got.Items[0].CommissionPct)
	}
	list, err := repo.ListRange("s-it-foto", got.CreatedAt.Add(-time.Hour), got.CreatedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 1 || list[0].Items[0].CommissionPct != nil {
		t.Fatalf("ListRange debe devolver la línea sin foto: %+v", list)
	}
}
