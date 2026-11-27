// Integración contra Postgres real de la ZONA HORARIA CONGELADA (F3.3,
// corrección del bloqueante). Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run TimezoneFreeze -v
//
// El defecto: `payroll_runs` guarda fechas LOCALES y la EXCLUDE compara fechas,
// así que cambiar `salons.timezone` después de una corrida hace que la próxima
// derive OTRA ventana de instantes. Con el salón en Nueva York y 5 ventas
// repartidas en el borde, pasar a Auckland contaba 3 ventas en las DOS corridas
// (8 cobradas por 5 vendidas) y el camino inverso las dejaba en ninguna.
//
// Lo que SOLO se prueba contra la DB: la transacción del PUT que toma el mismo
// advisory lock por salón que la creación (`payroll-write:<salon>`), el
// `EXISTS` de liquidaciones DESPUÉS del lock, y que la creación deriva el
// período con la zona leída ya bajo el lock.
package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/settings"
)

// msgTimezoneFrozen es la frase EXACTA del contrato (literal para que el test
// no pueda pasar copiando la constante del código).
const msgTimezoneFrozen = "No se puede cambiar la zona horaria: ya hay liquidaciones de nómina calculadas con la zona actual"

func timezoneSettingsService(pool *pgxpool.Pool) settings.SettingsService {
	return settings.SettingsService{Settings: Salons{Pool: pool}, Memberships: Memberships{Pool: pool}}
}

// setSalonTimezone es el seed del test: no hay liquidaciones todavía, así que
// escribe directo (la garantía del PUT se prueba con SettingsService.Update).
func setSalonTimezone(t *testing.T, ctx context.Context, pool *pgxpool.Pool, salonID, tz string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE salons SET timezone=$2, open_time='09:00', close_time='19:00', slot_step_min=30 WHERE id=$1`,
		salonID, tz); err != nil {
		t.Fatalf("zona de %s: %v", salonID, err)
	}
}

// seedBoundarySales deja las 5 ventas del experimento del verificador:
//   - sale-tz-a 2026-03-03T15:00Z: semana A en NY y en Auckland;
//   - sale-tz-b 2026-03-08T11:30Z, sale-tz-c 2026-03-08T20:00Z y
//     sale-tz-d 2026-03-09T03:59Z: semana A en NY y semana B en Auckland;
//   - sale-tz-e 2026-03-10T15:00Z: semana B en las dos zonas.
func seedBoundarySales(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	salonID, profID, serviceID string, prefix string) {
	t.Helper()
	sales := []struct {
		suffix string
		at     time.Time
	}{
		{"a", time.Date(2026, 3, 3, 15, 0, 0, 0, time.UTC)},
		{"b", time.Date(2026, 3, 8, 11, 30, 0, 0, time.UTC)},
		{"c", time.Date(2026, 3, 8, 20, 0, 0, 0, time.UTC)},
		{"d", time.Date(2026, 3, 9, 3, 59, 0, 0, time.UTC)},
		{"e", time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)},
	}
	for _, s := range sales {
		insertPaySale(t, ctx, pool, prefix+"-"+s.suffix, salonID, profID, s.at, 1000,
			paySaleItem{serviceID: serviceID, qty: 1, priceCent: 1000, pct: intPtr(10)})
	}
}

// runLineSales cuenta las ventas de la única línea (o la de una persona) de una
// corrida, sin depender del orden del listado.
func runLineSales(t *testing.T, run domain.PayrollRun, userID string) int {
	t.Helper()
	line, ok := run.FindLine(userID)
	if !ok {
		t.Fatalf("la corrida %s no tiene línea de %s: %+v", run.ID, userID, run.Lines)
	}
	return line.SalesCount
}

// (a) El experimento EXACTO del verificador: con una corrida hecha, el PUT que
// cambia la zona responde 409 con la frase del contrato y NO escribe nada (ni
// los otros campos); las ventas de las dos corridas nunca se solapan. El camino
// inverso (Auckland -> Nueva York) tampoco deja ventas en ninguna.
func TestTimezoneFreezeIT_el_experimento_del_verificador(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	svc := payrollRunService(pool)
	settings := timezoneSettingsService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	setSalonTimezone(t, ctx, pool, "s-it-pay", "America/New_York")
	seedBoundarySales(t, ctx, pool, "s-it-pay", "u-it-pay-a", corte.ID, "sale-tz")

	// Corrida A: semana del 2 al 8 de marzo, zona Nueva York.
	runA, err := svc.Create(owner, "s-it-pay", "week", "2026-03-04")
	if err != nil {
		t.Fatalf("corrida A: %v", err)
	}
	if got := runLineSales(t, runA, "u-it-pay-a"); got != 4 {
		t.Fatalf("corrida A (NY) = %d ventas, esperaba 4", got)
	}

	// PUT settings a Auckland cambiando ADEMÁS los otros campos: 409 y NADA se
	// escribe (ni la zona ni el horario).
	_, err = settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "Pacific/Auckland",
		OpenTime: "07:00", CloseTime: "16:00", SlotStepMin: 15,
	})
	if err == nil || !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != msgTimezoneFrozen {
		t.Fatalf("cambiar la zona con una corrida: err = %v, esperaba %q (409)", err, msgTimezoneFrozen)
	}
	after, err := settings.Get(owner, "s-it-pay")
	if err != nil {
		t.Fatalf("leer settings: %v", err)
	}
	if after.Timezone != "America/New_York" || after.OpenTime != "09:00" ||
		after.CloseTime != "19:00" || after.SlotStepMin != 30 {
		t.Fatalf("el PUT rechazado escribió algo: %+v", after)
	}

	// Corrida B: la semana siguiente, con la zona VIEJA (la que vale).
	runB, err := svc.Create(owner, "s-it-pay", "week", "2026-03-11")
	if err != nil {
		t.Fatalf("corrida B: %v", err)
	}
	salesA, salesB := runLineSales(t, runA, "u-it-pay-a"), runLineSales(t, runB, "u-it-pay-a")
	if salesA != 4 || salesB != 1 {
		t.Fatalf("ventas A/B = %d/%d, esperaba 4/1 (con Auckland eran 4/4 y 3 se pagaban dos veces)", salesA, salesB)
	}
	var distinct int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM sales WHERE salon_id='s-it-pay'`).Scan(&distinct); err != nil {
		t.Fatalf("contar ventas: %v", err)
	}
	if distinct != 5 || salesA+salesB != distinct {
		t.Fatalf("ventas distintas = %d, contadas en las corridas = %d (se pisan o se pierden)", distinct, salesA+salesB)
	}

	// (c) Un PUT que MANTIENE la zona y cambia el horario sigue funcionando con
	// corridas presentes.
	same, err := settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "America/New_York",
		OpenTime: "08:00", CloseTime: "18:00", SlotStepMin: 20,
	})
	if err != nil {
		t.Fatalf("mismo huso con corridas: %v", err)
	}
	if same.Timezone != "America/New_York" || same.OpenTime != "08:00" ||
		same.CloseTime != "18:00" || same.SlotStepMin != 20 {
		t.Fatalf("el PUT del mismo huso no aplicó: %+v", same)
	}
}

// (a, camino inverso) Auckland primero y después Nueva York: la corrida B sigue
// contando las ventas y ninguna queda afuera de las dos liquidaciones.
func TestTimezoneFreezeIT_camino_inverso_no_pierde_ventas(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	svc := payrollRunService(pool)
	settings := timezoneSettingsService(pool)

	// El salón 2 con su dueña y su catálogo propio.
	if err := (Memberships{Pool: pool}).Save(domain.Membership{
		UserID: "u-it-pay-c", SalonID: "s-it-pay2", Role: domain.RoleOwner,
	}); err != nil {
		t.Fatalf("dueña del salón 2: %v", err)
	}
	otro, _ := domain.NewService("s-it-pay2", "Corte TZ IT", 45, 150000, 10)
	if err := (Services{Pool: pool}).Save(otro); err != nil {
		t.Fatalf("catálogo del salón 2: %v", err)
	}
	owner2 := payrollRunActor(t, pool, "u-it-pay-c", "s-it-pay2")
	setSalonTimezone(t, ctx, pool, "s-it-pay2", "Pacific/Auckland")
	seedBoundarySales(t, ctx, pool, "s-it-pay2", "u-it-pay-c", otro.ID, "sale-tz2")

	runA, err := svc.Create(owner2, "s-it-pay2", "week", "2026-03-04")
	if err != nil {
		t.Fatalf("corrida A (AKL): %v", err)
	}
	if got := runLineSales(t, runA, "u-it-pay-c"); got != 1 {
		t.Fatalf("corrida A (AKL) = %d ventas, esperaba 1", got)
	}
	if _, err := settings.Update(owner2, "s-it-pay2", domain.SalonSettings{
		SalonID: "s-it-pay2", Timezone: "America/New_York",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}); err == nil || err.Error() != msgTimezoneFrozen {
		t.Fatalf("AKL -> NY con corrida: err = %v, esperaba %q", err, msgTimezoneFrozen)
	}
	runB, err := svc.Create(owner2, "s-it-pay2", "week", "2026-03-11")
	if err != nil {
		t.Fatalf("corrida B (AKL): %v", err)
	}
	salesA, salesB := runLineSales(t, runA, "u-it-pay-c"), runLineSales(t, runB, "u-it-pay-c")
	if salesA != 1 || salesB != 4 || salesA+salesB != 5 {
		t.Fatalf("ventas A/B = %d/%d, esperaba 1/4 (con NY la B quedaba vacía y las ventas en ninguna)", salesA, salesB)
	}
}

// (b) 16 rondas de un cambio de zona compitiendo con una creación. El estado
// final SIEMPRE es consistente:
//   - si el PUT ganó el lock, cambió la zona y la corrida se creó DESPUÉS con la
//     zona nueva (4 ventas serían de la zona vieja y delatarían la carrera);
//   - si la creación ganó, existe la corrida y el PUT fue 409 con la zona vieja.
//
// Nunca queda una corrida derivada con la zona vieja y una zona nueva guardada
// (el estado que paga dos veces), nunca dos corridas y nunca un 500.
func TestTimezoneFreezeIT_carrera_settings_vs_creacion(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	svc := payrollRunService(pool)
	settings := timezoneSettingsService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")
	seedBoundarySales(t, ctx, pool, "s-it-pay", "u-it-pay-a", corte.ID, "sale-race")

	const rounds = 16
	outcomes := map[string]int{}
	for round := 0; round < rounds; round++ {
		// Estado base de la ronda: sin corridas y en Nueva York (seed directo,
		// antes de que empiece la carrera).
		if _, err := pool.Exec(ctx, `DELETE FROM payroll_runs WHERE salon_id='s-it-pay'`); err != nil {
			t.Fatalf("ronda %d limpieza: %v", round, err)
		}
		setSalonTimezone(t, ctx, pool, "s-it-pay", "America/New_York")

		// En las rondas impares el PUT arranca unos milisegundos antes para que
		// gane el lock las dos veces: se prueban las DOS órdenes (la creación
		// después de un cambio aceptado deriva el período con la zona nueva).
		var wg sync.WaitGroup
		var settingsErr, createErr error
		putSettings := func() {
			defer wg.Done()
			_, settingsErr = settings.Update(owner, "s-it-pay", domain.SalonSettings{
				SalonID: "s-it-pay", Timezone: "Pacific/Auckland",
				OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
			})
		}
		createRun := func() {
			defer wg.Done()
			_, createErr = svc.Create(owner, "s-it-pay", "week", "2026-03-04")
		}
		wg.Add(2)
		if round%2 == 0 {
			go putSettings()
			go createRun()
		} else {
			go putSettings()
			time.Sleep(3 * time.Millisecond)
			go createRun()
		}
		wg.Wait()

		if createErr != nil {
			t.Fatalf("ronda %d: la creación no podía fallar (el salón arranca sin corridas): %v", round, createErr)
		}
		switch {
		case settingsErr == nil:
			outcomes["zona cambiada"]++
		case errors.Is(settingsErr, domain.ErrAlreadyExists) && settingsErr.Error() == msgTimezoneFrozen:
			outcomes["409 zona congelada"]++
		default:
			t.Fatalf("ronda %d: el PUT devolvió un error inesperado (¿500?): %v", round, settingsErr)
		}

		var tz string
		if err := pool.QueryRow(ctx, `SELECT timezone FROM salons WHERE id='s-it-pay'`).Scan(&tz); err != nil {
			t.Fatalf("ronda %d: zona final: %v", round, err)
		}
		var runs int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*)::int FROM payroll_runs WHERE salon_id='s-it-pay'`).Scan(&runs); err != nil {
			t.Fatalf("ronda %d: contar corridas: %v", round, err)
		}
		if runs != 1 {
			t.Fatalf("ronda %d: corridas = %d, esperaba exactamente 1", round, runs)
		}
		var runID string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM payroll_runs WHERE salon_id='s-it-pay'`).Scan(&runID); err != nil {
			t.Fatalf("ronda %d: id de la corrida: %v", round, err)
		}
		run, err := svc.Get(owner, "s-it-pay", runID)
		if err != nil {
			t.Fatalf("ronda %d: leer la corrida: %v", round, err)
		}
		// La semana 2-8 de marzo cuenta 4 ventas en NY y 1 en Auckland. Si la
		// corrida existe, sus ventas tienen que corresponder a la zona GUARDADA.
		expected := map[string]int{"America/New_York": 4, "Pacific/Auckland": 1}
		want, ok := expected[tz]
		if !ok {
			t.Fatalf("ronda %d: zona inesperada %q", round, tz)
		}
		if got := runLineSales(t, run, "u-it-pay-a"); got != want {
			t.Fatalf("ronda %d: zona=%s, la corrida contó %d ventas y le corresponden %d "+
				"(la creación derivó el período con una zona que el PUT estaba cambiando)",
				round, tz, got, want)
		}
		if settingsErr != nil && tz != "America/New_York" {
			t.Fatalf("ronda %d: PUT 409 pero la zona cambió a %s", round, tz)
		}
		if settingsErr == nil && tz != "Pacific/Auckland" {
			t.Fatalf("ronda %d: PUT 200 pero la zona quedó %s", round, tz)
		}
	}
	t.Logf("%d rondas: %v", rounds, outcomes)
}

// (d) Borrar la única corrida ABIERTA descongela la zona; una PAGADA la vuelve
// a congelar (abierta o pagada, cualquiera congela).
func TestTimezoneFreezeIT_descongela_al_borrar_la_unica_abierta(t *testing.T) {
	pool, ctx := payrollFixtures(t)
	corte, _, _ := payrollCatalog(t, pool)
	febSales(t, ctx, pool, corte)
	svc := payrollRunService(pool)
	settings := timezoneSettingsService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")

	run, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	if _, err := settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "Pacific/Auckland",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}); err == nil || err.Error() != msgTimezoneFrozen {
		t.Fatalf("con la corrida abierta: err = %v, esperaba %q", err, msgTimezoneFrozen)
	}
	// Se borra la ÚNICA corrida abierta: se descongela.
	if err := svc.Delete(owner, "s-it-pay", run.ID); err != nil {
		t.Fatalf("borrar la abierta: %v", err)
	}
	if _, err := settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "Pacific/Auckland",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}); err != nil {
		t.Fatalf("sin corridas la zona tiene que poder cambiar: %v", err)
	}
	// Una PAGADA vuelve a congelar.
	again, err := svc.Create(owner, "s-it-pay", "month", "2026-02-10")
	if err != nil {
		t.Fatalf("crear de nuevo: %v", err)
	}
	if _, err := svc.Pay(owner, "s-it-pay", again.ID); err != nil {
		t.Fatalf("pagar: %v", err)
	}
	if _, err := settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "America/New_York",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}); err == nil || err.Error() != msgTimezoneFrozen {
		t.Fatalf("con una corrida pagada: err = %v, esperaba %q", err, msgTimezoneFrozen)
	}
}

// (e) PRESERVE: un salón SIN liquidaciones se comporta exactamente como antes
// (los cuatro campos, los mensajes de validación y los permisos).
func TestTimezoneFreezeIT_salon_sin_corridas_sin_cambios(t *testing.T) {
	pool, _ := payrollFixtures(t)
	settings := timezoneSettingsService(pool)
	owner := payrollRunActor(t, pool, "u-it-pay-owner", "s-it-pay")
	profe := payrollRunActor(t, pool, "u-it-pay-a", "s-it-pay")
	ajeno := payrollRunActor(t, pool, "u-it-pay-c", "s-it-pay2")

	// Los cuatro campos se aplican y persisten.
	got, err := settings.Update(owner, "s-it-pay", domain.SalonSettings{
		SalonID: "s-it-pay", Timezone: "America/Bogota",
		OpenTime: "08:00", CloseTime: "17:30", SlotStepMin: 20,
	})
	if err != nil {
		t.Fatalf("PUT sin corridas: %v", err)
	}
	if got.Timezone != "America/Bogota" || got.OpenTime != "08:00" ||
		got.CloseTime != "17:30" || got.SlotStepMin != 20 {
		t.Fatalf("no aplicó los cuatro campos: %+v", got)
	}
	persisted, err := settings.Get(owner, "s-it-pay")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if persisted.Timezone != "America/Bogota" || persisted.OpenTime != "08:00" ||
		persisted.CloseTime != "17:30" || persisted.SlotStepMin != 20 {
		t.Fatalf("no persistió: %+v", persisted)
	}

	// Mensajes de validación EXACTOS (no cambian con la zona congelada).
	cases := []struct {
		name string
		in   domain.SalonSettings
		want string
	}{
		{"zona vacía", domain.SalonSettings{SalonID: "s-it-pay", Timezone: "", OpenTime: "08:00", CloseTime: "17:00", SlotStepMin: 20},
			"validación: la zona horaria es obligatoria"},
		{"zona inexistente", domain.SalonSettings{SalonID: "s-it-pay", Timezone: "Marte/Olympus", OpenTime: "08:00", CloseTime: "17:00", SlotStepMin: 20},
			`validación: zona horaria inválida "Marte/Olympus" (usá un nombre IANA, por ejemplo America/Mexico_City)`},
		{"hora mal formada", domain.SalonSettings{SalonID: "s-it-pay", Timezone: "America/Bogota", OpenTime: "8:00", CloseTime: "17:00", SlotStepMin: 20},
			"validación: la hora de apertura debe tener formato HH:MM (00:00 a 23:59)"},
		{"ventana invertida", domain.SalonSettings{SalonID: "s-it-pay", Timezone: "America/Bogota", OpenTime: "18:00", CloseTime: "17:00", SlotStepMin: 20},
			"validación: la hora de apertura debe ser anterior a la de cierre"},
		{"paso fuera de rango", domain.SalonSettings{SalonID: "s-it-pay", Timezone: "America/Bogota", OpenTime: "08:00", CloseTime: "17:00", SlotStepMin: 0},
			"validación: el paso de turnos debe estar entre 5 y 240 minutos"},
	}
	for _, c := range cases {
		if _, err := settings.Update(owner, "s-it-pay", c.in); err == nil || err.Error() != c.want {
			t.Fatalf("%s: err = %v, esperaba %q", c.name, err, c.want)
		}
	}
	// Una validación fallida no escribe nada.
	after := func() domain.SalonSettings {
		s, err := settings.Get(owner, "s-it-pay")
		if err != nil {
			t.Fatalf("GET final: %v", err)
		}
		return s
	}()
	if after.Timezone != "America/Bogota" || after.OpenTime != "08:00" || after.SlotStepMin != 20 {
		t.Fatalf("una validación fallida escribió: %+v", after)
	}

	// Permisos: el profesional lee pero no edita; quien no es del salón, nada.
	if _, err := settings.Get(profe, "s-it-pay"); err != nil {
		t.Fatalf("el profesional tiene que leer: %v", err)
	}
	if _, err := settings.Update(profe, "s-it-pay", after); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional editando: %v", err)
	}
	if _, err := settings.Get(ajeno, "s-it-pay"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno leyendo: %v", err)
	}
	if _, err := settings.Update(ajeno, "s-it-pay", after); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno escribiendo: %v", err)
	}
}
