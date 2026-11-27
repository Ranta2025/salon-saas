// Lecturas masivas de la exportación CSV contra Postgres real (F4, SALON_IT=1).
// Lo que la prueba unitaria no puede ver: la unión con users (nombre de la
// cuenta), la suma de cantidades de los ítems, los bordes del día LOCAL en
// zonas con horario de verano (día de 23 h y de 25 h) y el aislamiento por
// salón.
//
// Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run ExportsIT -v
package postgres

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

// exportCleanup deja los salones de la prueba vacíos y sin filas hijas.
func exportCleanup(t *testing.T, pool *pgxpool.Pool, salons ...string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM sales WHERE salon_id = ANY($1)`, // los ítems caen por CASCADE
		`DELETE FROM cash_closes WHERE salon_id = ANY($1)`,
		`DELETE FROM clients WHERE salon_id = ANY($1)`,
		`DELETE FROM services WHERE salon_id = ANY($1)`,
		`DELETE FROM memberships WHERE salon_id = ANY($1)`,
	} {
		if _, err := pool.Exec(ctx, q, salons); err != nil {
			t.Fatalf("limpieza %q: %v", q, err)
		}
	}
}

// exportSeedUser crea (o actualiza el nombre de) una cuenta.
func exportSeedUser(t *testing.T, pool *pgxpool.Pool, id, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users(id,name,email,pass_hash) VALUES($1,$2,$3,'x')
		 ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name`,
		id, name, id+"@f4w.test")
	if err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
}

func exportSeedSalon(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO salons(id,slug,name) VALUES($1,$1,'Salón export IT')
		 ON CONFLICT (id) DO NOTHING`, id)
	if err != nil {
		t.Fatalf("seed salon %s: %v", id, err)
	}
}

func exportSeedService(t *testing.T, pool *pgxpool.Pool, id, salonID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO services(id,salon_id,name,duration_min,price_cents,commission_pct)
		 VALUES($1,$2,'Servicio export',$3,1000,0) ON CONFLICT (id) DO NOTHING`,
		id, salonID, 30)
	if err != nil {
		t.Fatalf("seed service %s: %v", id, err)
	}
}

// exportSeedSale inserta la venta (y un ítem con la cantidad pedida).
func exportSeedSale(t *testing.T, pool *pgxpool.Pool, id, salonID, professionalID, serviceID string, at time.Time, qty int) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,created_at)
		 VALUES($1,$2,$3,'Lía export',$4,'card',$5)`,
		id, salonID, professionalID, 150000, at)
	if err != nil {
		t.Fatalf("seed sale %s: %v", id, err)
	}
	if serviceID != "" && qty > 0 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents) VALUES($1,$2,$3,$4,1000)`,
			id+"-i1", id, serviceID, qty); err != nil {
			t.Fatalf("seed item %s: %v", id, err)
		}
	}
}

func exportSeedClose(t *testing.T, pool *pgxpool.Pool, id, salonID string, opened, closed time.Time, system, counted int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO cash_closes(id,salon_id,opened_at,closed_at,system_cash_cents,counted_cents,difference_cents,note)
		 VALUES($1,$2,$3,$4,$5,$6,$7,'nota')`,
		id, salonID, opened, closed, system, counted, counted-system); err != nil {
		t.Fatalf("seed close %s: %v", id, err)
	}
}

func exportSeedClient(t *testing.T, pool *pgxpool.Pool, id, salonID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO clients(id,salon_id,name,phone,email,notes,created_at)
		 VALUES($1,$2,$3,'11 5555-1234','c@f4w.test','nota',$4)`,
		id, salonID, name, time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed client %s: %v", id, err)
	}
}

func exportZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("zona %s: %v", name, err)
	}
	return loc
}

// La lectura real: ventana local, orden del contrato, nombre profesional unido
// desde users, suma de cantidades, tenant isolation y LIMIT.
func TestExportsIT_ventas_cierres_y_clientes(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()

	const salon, other = "s-f4w-main", "s-f4w-other"
	exportSeedSalon(t, pool, salon)
	exportSeedSalon(t, pool, other)
	exportCleanup(t, pool, salon, other)
	exportSeedUser(t, pool, "u-f4w-ana", "Ana Profesional")
	// Un usuario sin nombre: la celda Profesional sale vacía. La FK
	// sales.professional_id -> users hace imposible una venta sin fila en users,
	// así que este es el único camino real al «vacío» del contrato; el SQL usa
	// LEFT JOIN + COALESCE para cubrir las dos formas.
	exportSeedUser(t, pool, "u-f4w-vacio", "")
	exportSeedService(t, pool, "svc-f4w", salon)
	exportSeedService(t, pool, "svc-f4w-o", other)

	// Dentro de la ventana del 4 de mayo local (CDMX), dos ventas.
	at1 := time.Date(2026, 5, 4, 18, 0, 0, 0, time.UTC)
	at2 := time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC)
	exportSeedSale(t, pool, "f4w-v1", salon, "u-f4w-ana", "svc-f4w", at1, 2)
	exportSeedSale(t, pool, "f4w-v2", salon, "u-f4w-vacio", "", at2, 0)
	// El 5 local a las 01:00: fuera de la ventana.
	exportSeedSale(t, pool, "f4w-v3", salon, "u-f4w-ana", "svc-f4w",
		time.Date(2026, 5, 5, 7, 0, 0, 0, time.UTC), 1)
	// Otro salón, dentro de la ventana: nunca puede aparecer.
	exportSeedSale(t, pool, "f4w-x1", other, "u-f4w-ana", "svc-f4w-o", at1, 1)
	// Segunda línea de la venta 1: la suma de cantidades es 2 + 3 = 5.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents)
		 VALUES('f4w-v1-i2','f4w-v1','svc-f4w',3,1000)`); err != nil {
		t.Fatalf("segunda línea: %v", err)
	}

	exportSeedClose(t, pool, "f4w-c1", salon,
		time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC), time.Date(2026, 5, 4, 23, 30, 0, 0, time.UTC), 10000, 9995)
	// 2026-05-05T05:59Z = 2026-05-04 23:59 local: dentro, y es el primero.
	exportSeedClose(t, pool, "f4w-c2", salon,
		time.Date(2026, 5, 4, 20, 0, 0, 0, time.UTC), time.Date(2026, 5, 5, 5, 59, 0, 0, time.UTC), 100, 100)
	// 2026-05-05T06:00Z = 2026-05-05 00:00 local: fuera.
	exportSeedClose(t, pool, "f4w-c3", salon,
		time.Date(2026, 5, 5, 4, 0, 0, 0, time.UTC), time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC), 100, 100)
	exportSeedClose(t, pool, "f4w-x2", other,
		time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC), time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC), 100, 100)

	exportSeedClient(t, pool, "f4w-cl1", salon, "Zoe")
	exportSeedClient(t, pool, "f4w-cl2", salon, "Ana")
	exportSeedClient(t, pool, "f4w-x3", other, "Ajena")

	loc := exportZone(t, "America/Mexico_City")
	window, err := domain.ExportRange("2026-05-04", "2026-05-04", loc)
	if err != nil {
		t.Fatalf("rango: %v", err)
	}
	repo := Exports{Pool: pool}

	sales, err := repo.ListExportSales(salon, window.From, window.To, domain.ExportRowsCap+1)
	if err != nil {
		t.Fatalf("ventas: %v", err)
	}
	if len(sales) != 2 {
		t.Fatalf("ventas en la ventana = %d (%+v)", len(sales), sales)
	}
	if sales[0].SaleID != "f4w-v1" || sales[1].SaleID != "f4w-v2" {
		t.Fatalf("orden (created_at DESC) = %q, %q", sales[0].SaleID, sales[1].SaleID)
	}
	if sales[0].ProfessionalName != "Ana Profesional" || sales[0].Items != 5 ||
		sales[0].TotalCents != 150000 || sales[0].Method != domain.MethodCard {
		t.Fatalf("venta 1: %+v", sales[0])
	}
	if sales[1].ProfessionalName != "" || sales[1].Items != 0 {
		t.Fatalf("venta 2 (profesional sin nombre): %+v", sales[1])
	}
	for _, s := range sales {
		if s.SaleID == "f4w-v3" || s.SaleID == "f4w-x1" {
			t.Fatalf("se coló una venta fuera de la ventana o de otro salón: %+v", s)
		}
	}
	// El LIMIT es del contrato: el caso de uso pide tope + 1 y compara.
	if one, err := repo.ListExportSales(salon, window.From, window.To, 1); err != nil || len(one) != 1 {
		t.Fatalf("LIMIT 1: %d %v", len(one), err)
	}

	closes, err := repo.ListExportCashCloses(salon, window.From, window.To, domain.ExportRowsCap+1)
	if err != nil {
		t.Fatalf("cierres: %v", err)
	}
	if len(closes) != 2 {
		t.Fatalf("cierres = %d (%+v)", len(closes), closes)
	}
	// El más reciente primero: el de las 23:59 locales del 4 (05:59Z del 5).
	if !closes[0].ClosedAt.Equal(time.Date(2026, 5, 5, 5, 59, 0, 0, time.UTC)) {
		t.Fatalf("el cierre más reciente no vino primero: %+v", closes)
	}
	if closes[0].DifferenceCents != 0 || closes[1].DifferenceCents != -5 {
		t.Fatalf("diferencias = %d, %d", closes[0].DifferenceCents, closes[1].DifferenceCents)
	}

	clients, err := repo.ListExportClients(salon, domain.ExportRowsCap+1)
	if err != nil {
		t.Fatalf("clientes: %v", err)
	}
	if len(clients) != 2 || clients[0].Name != "Ana" || clients[1].Name != "Zoe" {
		t.Fatalf("clientes (orden por nombre) = %+v", clients)
	}
	if clients[0].Email != "c@f4w.test" || clients[0].Phone != "11 5555-1234" {
		t.Fatalf("ficha: %+v", clients[0])
	}
	if one, err := repo.ListExportClients(salon, 1); err != nil || len(one) != 1 {
		t.Fatalf("clientes LIMIT 1: %d %v", len(one), err)
	}
}

// Bordes del día local: 23:59:59 y 00:00:00 locales entran/NO entran según la
// ventana, en CDMX, en Nueva York con su día de 23 h y el de 25 h, y en
// Auckland. Vale para ventas y para cierres.
func TestExportsIT_bordes_de_dia_local(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()

	cases := []struct {
		zone string
		day  string
		// duración del día local en horas (0 = no se controla)
		hours int
	}{
		{"America/Mexico_City", "2026-06-01", 24},
		{"America/New_York", "2026-03-08", 23}, // arranca el horario de verano
		{"America/New_York", "2026-11-01", 25}, // termina el horario de verano
		{"Pacific/Auckland", "2026-03-08", 24},
	}
	for i, c := range cases {
		loc := exportZone(t, c.zone)
		window, err := domain.ExportRange(c.day, c.day, loc)
		if err != nil {
			t.Fatalf("%s %s: %v", c.zone, c.day, err)
		}
		if c.hours != 0 {
			if d := window.To.Sub(window.From); d != time.Duration(c.hours)*time.Hour {
				t.Fatalf("%s %s: ventana de %s, quería %dh", c.zone, c.day, d, c.hours)
			}
		}
		idx := strconv.Itoa(i)
		salon := "s-f4w-edge-" + idx
		exportSeedSalon(t, pool, salon)
		exportCleanup(t, pool, salon)
		exportSeedUser(t, pool, "u-f4w-edge", "Ana Edge")
		service := "svc-f4w-edge-" + idx
		exportSeedService(t, pool, service, salon)
		repo := Exports{Pool: pool}

		// Justo antes de la ventana, el primer instante, el último y el primero
		// de la ventana siguiente.
		points := []struct {
			suffix string
			at     time.Time
			in     bool
		}{
			{"before", window.From.Add(-time.Second), false},
			{"start", window.From, true},
			{"end", window.To.Add(-time.Second), true},
			{"next", window.To, false},
		}
		for _, p := range points {
			id := "f4w-edge-" + idx + "-" + p.suffix
			exportSeedSale(t, pool, id, salon, "u-f4w-edge", service, p.at, 1)
			exportSeedClose(t, pool, id+"-c", salon, p.at.Add(-time.Hour), p.at, 100, 100)
		}
		sales, err := repo.ListExportSales(salon, window.From, window.To, domain.ExportRowsCap+1)
		if err != nil {
			t.Fatalf("%s %s ventas: %v", c.zone, c.day, err)
		}
		if len(sales) != 2 {
			t.Fatalf("%s %s: ventas en la ventana = %d (%+v)", c.zone, c.day, len(sales), sales)
		}
		for _, s := range sales {
			if s.SaleID != "f4w-edge-"+idx+"-start" && s.SaleID != "f4w-edge-"+idx+"-end" {
				t.Fatalf("%s %s: se coló %s", c.zone, c.day, s.SaleID)
			}
		}
		closes, err := repo.ListExportCashCloses(salon, window.From, window.To, domain.ExportRowsCap+1)
		if err != nil {
			t.Fatalf("%s %s cierres: %v", c.zone, c.day, err)
		}
		if len(closes) != 2 {
			t.Fatalf("%s %s: cierres en la ventana = %d (%+v)", c.zone, c.day, len(closes), closes)
		}
	}
}

// Hallazgo del verificador independiente: las lecturas masivas usaban
// context.Background() sin tope. Con la tabla bloqueada (o un plan malo después
// de una carga masiva) la consulta quedaba viva en Postgres reteniendo una de las
// conexiones del pool aunque el cliente ya se hubiera ido. Ahora cada lectura
// tiene su propio tope y termina con error.
func TestExportsIT_la_consulta_tiene_tope_de_tiempo(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	ctx := context.Background()

	// Otra transacción tiene la tabla bloqueada: cualquier SELECT queda esperando.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir la transacción bloqueante: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `LOCK TABLE sales IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("bloquear sales: %v", err)
	}

	repo := Exports{Pool: pool, Timeout: 300 * time.Millisecond}
	from := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := repo.ListExportSales("s-f4-timeout", from, from.Add(24*time.Hour), 10)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("con la tabla bloqueada la lectura tenía que fallar por el tope de tiempo")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("tardó %v: el tope de 300 ms no se aplicó", elapsed)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("la lectura sigue esperando el lock: no tiene tope de tiempo")
	}
}
