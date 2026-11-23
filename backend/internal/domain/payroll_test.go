package domain_test

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// Períodos (D5) y fórmula de comisión (D2) de la nómina de F3.2. Las fechas
// están escritas a mano: si un cálculo cambia, el test dice cuál.

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("zona %s: %v", name, err)
	}
	return loc
}

// periodOf deriva el período y devuelve las fechas locales inclusivas.
func periodOf(t *testing.T, kind, date, zone string) domain.PayPeriod {
	t.Helper()
	p, err := domain.NewPayPeriod(kind, date, mustLoc(t, zone))
	if err != nil {
		t.Fatalf("%s %s en %s: %v", kind, date, zone, err)
	}
	return p
}

func TestPayPeriod_fechas_locales_inclusivas(t *testing.T) {
	cdmx := "America/Mexico_City"
	cases := []struct {
		name string
		kind string
		date string
		from string
		to   string
	}{
		// Mes calendario y febrero de un año NO bisiesto.
		{"mes de febrero no bisiesto", "month", "2026-02-10", "2026-02-01", "2026-02-28"},
		// Febrero bisiesto: el 29 existe y el período lo incluye entero.
		{"mes de febrero bisiesto", "month", "2024-02-10", "2024-02-01", "2024-02-29"},
		{"mes de 31 días", "month", "2026-12-31", "2026-12-01", "2026-12-31"},
		// Quincenas: 1-15 y 16-fin; el 31 solo aparece en lados de 31 días.
		{"quincena primera de 31 días", "fortnight", "2026-01-05", "2026-01-01", "2026-01-15"},
		{"quincena segunda de 31 días", "fortnight", "2026-01-20", "2026-01-16", "2026-01-31"},
		{"quincena segunda de 30 días", "fortnight", "2026-04-20", "2026-04-16", "2026-04-30"},
		{"quincena segunda de febrero bisiesto", "fortnight", "2024-02-20", "2024-02-16", "2024-02-29"},
		{"quincena segunda de febrero no bisiesto", "fortnight", "2026-02-16", "2026-02-16", "2026-02-28"},
		{"el día 15 es de la primera quincena", "fortnight", "2026-01-15", "2026-01-01", "2026-01-15"},
		{"el día 16 es de la segunda quincena", "fortnight", "2026-01-16", "2026-01-16", "2026-01-31"},
		// Semanas lunes-domingo que cruzan mes y año.
		{"semana que cruza el mes", "week", "2026-01-01", "2025-12-29", "2026-01-04"},
		{"semana que cruza el año", "week", "2026-12-31", "2026-12-28", "2027-01-03"},
		{"semana que empieza un lunes", "week", "2026-03-02", "2026-03-02", "2026-03-08"},
		{"domingo cierra la semana", "week", "2026-03-08", "2026-03-02", "2026-03-08"},
		// Una sola fecha dentro del período alcanza (cualquier día).
		{"mes desde el último día", "month", "2026-03-31", "2026-03-01", "2026-03-31"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := periodOf(t, c.kind, c.date, cdmx)
			if p.FromDate() != c.from || p.ToDate() != c.to {
				t.Fatalf("%s %s = [%s, %s], esperaba [%s, %s]",
					c.kind, c.date, p.FromDate(), p.ToDate(), c.from, c.to)
			}
		})
	}
}

// La ventana interna es [from 00:00 local, (to+1) 00:00 local) en la zona del
// salón: el día del salón, no UTC. Con CDMX (UTC-6 todo el año) la medianoche
// local son las 06:00 UTC.
func TestPayPeriod_ventana_en_zona_del_salon(t *testing.T) {
	p := periodOf(t, "month", "2026-03-15", "America/Mexico_City")
	start, end := p.Window()
	wantStart := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 4, 1, 6, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("inicio de la ventana = %s, esperaba %s", start.UTC(), wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("fin de la ventana = %s, esperaba %s", end.UTC(), wantEnd)
	}
	// El último instante del último día local está DENTRO de [inicio, fin).
	lastMoment := time.Date(2026, 3, 31, 23, 59, 59, 0, start.Location())
	if lastMoment.Before(start) || !lastMoment.Before(end) {
		t.Fatalf("el último instante del período debe estar dentro: %s", lastMoment.UTC())
	}
}

// Cambio de horario de verano: un día de 23 h (marzo) y uno de 25 h (noviembre)
// tienen que entrar ENTEROS y la ventana no puede correrse.
func TestPayPeriod_dia_de_cambio_de_hora_entero(t *testing.T) {
	ny := "America/New_York"

	// Marzo: el domingo 8/3/2026 a las 02:00 el reloj salta a las 03:00 -> el día
	// dura 23 h. La semana que lo contiene va del lunes 2 al domingo 8.
	p := periodOf(t, "week", "2026-03-05", ny)
	if p.FromDate() != "2026-03-02" || p.ToDate() != "2026-03-08" {
		t.Fatalf("semana del cambio de marzo = [%s, %s]", p.FromDate(), p.ToDate())
	}
	start, end := p.Window()
	// 00:00 del 2/3 en EST (UTC-5) y 00:00 del 9/3 en EDT (UTC-4).
	wantStart := time.Date(2026, 3, 2, 5, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("ventana = [%s, %s], esperaba [%s, %s]", start.UTC(), end.UTC(), wantStart, wantEnd)
	}
	// 167 h y no 168: se sumó un día calendario, no 24 h fijas.
	if got := end.Sub(start); got != 167*time.Hour {
		t.Fatalf("la semana del cambio de marzo dura %s, esperaba 167h", got)
	}
	// El último minuto del domingo 8 (23:59 EDT) está DENTRO.
	lastMarch := time.Date(2026, 3, 8, 23, 59, 59, 0, start.Location())
	if lastMarch.Before(start) || !lastMarch.Before(end) {
		t.Fatalf("el 8/3 23:59 local debe estar dentro: %s", lastMarch.UTC())
	}

	// Noviembre: el domingo 1/11/2026 a las 02:00 vuelve a la 01:00 -> 25 h.
	q := periodOf(t, "week", "2026-11-01", ny)
	if q.FromDate() != "2026-10-26" || q.ToDate() != "2026-11-01" {
		t.Fatalf("semana del cambio de noviembre = [%s, %s]", q.FromDate(), q.ToDate())
	}
	qstart, qend := q.Window()
	qWantStart := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC) // EDT (UTC-4)
	qWantEnd := time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)    // EST (UTC-5)
	if !qstart.Equal(qWantStart) || !qend.Equal(qWantEnd) {
		t.Fatalf("ventana de noviembre = [%s, %s], esperaba [%s, %s]",
			qstart.UTC(), qend.UTC(), qWantStart, qWantEnd)
	}
	if got := qend.Sub(qstart); got != 169*time.Hour {
		t.Fatalf("la semana del cambio de noviembre dura %s, esperaba 169h", got)
	}
	// La hora repetida (01:30 local del 1/11) también está dentro.
	repeated := time.Date(2026, 11, 1, 1, 30, 0, 0, qstart.Location())
	if repeated.Before(qstart) || !repeated.Before(qend) {
		t.Fatalf("la hora repetida debe estar dentro: %s", repeated.UTC())
	}
}

// Un período de dos meses seguidos cubre el mes entero, sin importar el día que
// se pida: es la prueba de que la derivación no depende del día enviado.
func TestPayPeriod_no_depende_del_dia_enviado(t *testing.T) {
	loc := mustLoc(t, "America/Mexico_City")
	first := periodOf(t, "month", "2026-05-01", "America/Mexico_City")
	last := periodOf(t, "month", "2026-05-31", "America/Mexico_City")
	if first.FromDate() != last.FromDate() || first.ToDate() != last.ToDate() {
		t.Fatalf("el mes debe ser el mismo desde cualquier día: %s..%s vs %s..%s",
			first.FromDate(), first.ToDate(), last.FromDate(), last.ToDate())
	}
	if !first.From.Equal(last.From) || !first.To.Equal(last.To) {
		t.Fatalf("la ventana debe ser la misma: %s vs %s", first.From.In(loc), last.From.In(loc))
	}
}

func TestPayPeriod_closed(t *testing.T) {
	loc := mustLoc(t, "America/Mexico_City")
	p := periodOf(t, "month", "2026-01-10", "America/Mexico_City")
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"en curso: el último día no está cerrado", time.Date(2026, 1, 31, 23, 59, 0, 0, loc), false},
		{"en curso: un día del medio", time.Date(2026, 1, 10, 12, 0, 0, 0, loc), false},
		{"cerrado: el día siguiente", time.Date(2026, 2, 1, 0, 0, 0, 0, loc), true},
		{"cerrado: mucho después", time.Date(2026, 6, 1, 9, 0, 0, 0, loc), true},
		// El «hoy» es el del salón: 2026-02-01 03:00 UTC sigue siendo 31/1 en CDMX.
		{"cerrado solo cuando ya es otro día LOCAL", time.Date(2026, 2, 1, 3, 0, 0, 0, time.UTC), false},
		{"cerrado a medianoche local exacta", time.Date(2026, 2, 1, 6, 0, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Closed(c.now); got != c.want {
				t.Fatalf("closed(%s) = %v, esperaba %v", c.now, got, c.want)
			}
		})
	}
}

func TestPayPeriod_invalido(t *testing.T) {
	loc := mustLoc(t, "America/Mexico_City")
	cases := []struct{ name, kind, date string }{
		{"tipo desconocido", "day", "2026-01-10"},
		{"tipo vacío", "", "2026-01-10"},
		{"fecha vacía", "month", ""},
		{"fecha con formato raro", "month", "10/01/2026"},
		{"mes inexistente", "month", "2026-13-01"},
		{"día inexistente en febrero", "month", "2026-02-30"},
		{"con hora", "week", "2026-01-10T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := domain.NewPayPeriod(c.kind, c.date, loc)
			if err == nil {
				t.Fatal("esperaba error de validación")
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("esperaba ErrValidation, dio %v", err)
			}
			// El mensaje es parte del contrato: el frontend lo muestra tal cual.
			if err.Error() != "Período inválido" {
				t.Fatalf("mensaje = %q, esperaba %q", err.Error(), "Período inválido")
			}
		})
	}
}

// --- Fórmula D2 ---

// Tres ítems de 333 centavos al 10%: Σ = 9990, /100 con UNA división = 99.
func TestCommission_redondeo_una_sola_division(t *testing.T) {
	items := []domain.CommissionItem{
		{Qty: 1, UnitPriceCents: 333, Pct: 10},
		{Qty: 1, UnitPriceCents: 333, Pct: 10},
		{Qty: 1, UnitPriceCents: 333, Pct: 10},
	}
	if base := domain.CommissionBase(items); base != 9990 {
		t.Fatalf("base = %d, esperaba 9990", base)
	}
	if got := domain.CommissionOf(items); got != 99 {
		t.Fatalf("comisión = %d, esperaba 99", got)
	}
}

// Caso donde dividir por ítem daría OTRO número: 2 ítems de 335 al 10% suman
// 6700 -> 67 centavos. Por ítem: floor(33,5) = 33 y 33+33 = 66. La fórmula
// tiene que dar 67 (una sola división al final).
func TestCommission_redondeo_no_se_acumula(t *testing.T) {
	items := []domain.CommissionItem{
		{Qty: 1, UnitPriceCents: 335, Pct: 10},
		{Qty: 1, UnitPriceCents: 335, Pct: 10},
	}
	if got := domain.CommissionOf(items); got != 67 {
		t.Fatalf("comisión = %d, esperaba 67 (por ítem daría 66)", got)
	}
	// Y el mismo caso en una sola línea con qty=2.
	one := []domain.CommissionItem{{Qty: 2, UnitPriceCents: 335, Pct: 10}}
	if got := domain.CommissionOf(one); got != 67 {
		t.Fatalf("comisión de qty 2 = %d, esperaba 67", got)
	}
}

// Una venta con ítems de porcentajes distintos, un ítem al 0% y cantidades
// mayores: todo entero, sin float.
func TestCommission_porcentajes_distintos_y_cero(t *testing.T) {
	items := []domain.CommissionItem{
		{Qty: 1, UnitPriceCents: 1000, Pct: 30}, // 30000
		{Qty: 2, UnitPriceCents: 500, Pct: 5},   // 5000
		{Qty: 3, UnitPriceCents: 999, Pct: 0},   // 0
	}
	if base := domain.CommissionBase(items); base != 35000 {
		t.Fatalf("base = %d, esperaba 35000", base)
	}
	if got := domain.CommissionOf(items); got != 350 {
		t.Fatalf("comisión = %d, esperaba 350", got)
	}
	// Sin ítems (o todos al 0%) la comisión es 0, no un error.
	if got := domain.CommissionOf(nil); got != 0 {
		t.Fatalf("sin ítems = %d, esperaba 0", got)
	}
}

// Los topes de una línea (1000 × 10.000.000 × 100) entran en int64 y la suma de
// muchas líneas también: no hay float ni desbordamiento en la base.
func TestCommission_enteros_grandes_sin_desbordar(t *testing.T) {
	maxLine := domain.CommissionItem{Qty: domain.MaxSaleItemQty, UnitPriceCents: domain.MaxSaleUnitPriceCents, Pct: 100}
	wantBase := int64(1000) * int64(10_000_000) * int64(100) // 1e12
	if got := domain.CommissionBase([]domain.CommissionItem{maxLine}); got != wantBase {
		t.Fatalf("base de una línea máxima = %d, esperaba %d", got, wantBase)
	}
	three := []domain.CommissionItem{maxLine, maxLine, maxLine}
	if got := domain.CommissionBase(three); got != 3*wantBase {
		t.Fatalf("base de tres líneas máximas = %d, esperaba %d", got, 3*wantBase)
	}
	if got := domain.CommissionOf(three); got != 30_000_000_000 {
		t.Fatalf("comisión de tres líneas máximas = %d, esperaba 30000000000", got)
	}
}

// Foto vs tarifa actual (D1): la foto manda; sin foto se estima con la tarifa
// de HOY y la línea queda marcada. Un 0 guardado es un porcentaje real.
func TestPayrollPct_foto_o_estimado(t *testing.T) {
	twenty := 20
	thirty := 30
	zero := 0
	cases := []struct {
		name      string
		snapshot  *int
		current   int
		wantPct   int
		estimated bool
	}{
		{"foto manda sobre la tarifa actual", &thirty, 20, 30, false},
		{"foto 0 es un porcentaje real", &zero, 20, 0, false},
		{"sin foto estima con la actual", nil, 20, 20, true},
		{"sin foto y tarifa 0", nil, 0, 0, true},
		{"foto igual a la actual sigue sin ser estimada", &twenty, 20, 20, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pct, estimated := domain.PayrollPct(c.snapshot, c.current)
			if pct != c.wantPct || estimated != c.estimated {
				t.Fatalf("PayrollPct = (%d, %v), esperaba (%d, %v)", pct, estimated, c.wantPct, c.estimated)
			}
		})
	}
}

// --- Modelo de pago (D3) ---

func TestMemberPay_default_y_validacion(t *testing.T) {
	d := domain.DefaultMemberPay("s1", "u1")
	if d.FixedCents != 0 || d.FixedPeriod != domain.PayMonth || !d.CommissionEnabled {
		t.Fatalf("el default debe ser solo comisión (0, month, true), dio %+v", d)
	}

	ok, err := domain.NewMemberPay("s1", "u1", 150000, "fortnight", false)
	if err != nil {
		t.Fatalf("modelo válido: %v", err)
	}
	if ok.FixedCents != 150000 || ok.FixedPeriod != domain.PayFortnight || ok.CommissionEnabled {
		t.Fatalf("modelo mal armado: %+v", ok)
	}

	cases := []struct {
		name     string
		fixed    int
		period   string
		wantText string
	}{
		{"negativo", -1, "month", "El sueldo fijo no puede ser negativo"},
		{"arriba del máximo", domain.MaxPayCents + 1, "month",
			"El sueldo fijo no puede superar los 2000000000 centavos"},
		{"período desconocido", 100, "day", "Período de pago desconocido: usá semana, quincena o mes"},
		{"período vacío", 100, "", "Período de pago desconocido: usá semana, quincena o mes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := domain.NewMemberPay("s1", "u1", c.fixed, c.period, true)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("esperaba ErrValidation, dio %v", err)
			}
			if err.Error() != c.wantText {
				t.Fatalf("mensaje = %q, esperaba %q", err.Error(), c.wantText)
			}
		})
	}
	// El máximo exacto SÍ entra.
	if _, err := domain.NewMemberPay("s1", "u1", domain.MaxPayCents, "month", true); err != nil {
		t.Fatalf("el máximo exacto debe entrar: %v", err)
	}
}
