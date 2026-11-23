package domain

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// Liquidaciones de nómina (F3.3, D6/D8 del contrato F3): el ajuste con signo,
// el total que nunca queda negativo y el CSV que neutraliza la inyección de
// fórmulas. Todo puro: sin DB, sin HTTP.

// El decimal del CSV sale de CENTAVOS ENTEROS, sin float: 5 centavos es 0.05 y
// 123.456.789 centavos es 1234567.89 exacto.
func TestCentsDecimal(t *testing.T) {
	cases := []struct {
		cents int
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{99, "0.99"},
		{100, "1.00"},
		{101, "1.01"},
		{123456789, "1234567.89"},
		{-5, "-0.05"},
		{-250, "-2.50"},
		{MaxPayCents, "20000000.00"},
		{-MaxPayCents, "-20000000.00"},
	}
	for _, c := range cases {
		if got := CentsDecimal(c.cents); got != c.want {
			t.Fatalf("CentsDecimal(%d) = %q, esperaba %q", c.cents, got, c.want)
		}
	}
}

// D8: toda celda de TEXTO que empieza con = + - @ tab o CR se prefija con una
// comilla simple; además se escapan comillas, comas y saltos de línea (RFC 4180).
func TestCSVCell_neutraliza_formulas(t *testing.T) {
	cases := []struct{ in, want string }{
		{"=SUM(A1:A9)", "'=SUM(A1:A9)"},
		{"+1+1", "'+1+1"},
		{"-2", "'-2"},
		{"@cmd", "'@cmd"},
		{"\tAna", "'\tAna"},
		{"\rAna", "\"'\rAna\""},
		// Texto normal, con unicode: sin comilla ni comillas.
		{"Ana", "Ana"},
		{"Ñandú", "Ñandú"},
		{"", ""},
		// Comillas, comas y saltos: se escapan SIEMPRE (aunque no sean fórmula).
		{"a,b", "\"a,b\""},
		{"a\"b", "\"a\"\"b\""},
		{"a\nb", "\"a\nb\""},
		// Una fórmula con coma lleva la comilla Y las comillas.
		{"=A1,B1", "\"'=A1,B1\""},
		// Un espacio inicial NO salva la celda: decide el primer carácter no
		// blanco (ver payroll_csv_whitespace_test.go, regla endurecida de F3.3).
		{" =SUM(A1)", "' =SUM(A1)"},
	}
	for _, c := range cases {
		if got := CSVCell(c.in); got != c.want {
			t.Fatalf("CSVCell(%q) = %q, esperaba %q", c.in, got, c.want)
		}
	}
}

// runCSV es una liquidación cerrada con las líneas que usan los tests del CSV.
func runCSV(lines ...PayrollLine) PayrollRun {
	now := time.Date(2026, 3, 2, 15, 4, 5, 0, time.UTC)
	return PayrollRun{
		ID: "run-1", SalonID: "s-1", Kind: PayMonth,
		From: "2026-02-01", To: "2026-02-28", Status: PayrollRunPaid,
		CreatedBy: "u-owner", CreatedAt: now, PaidAt: &now, PaidBy: "u-owner",
		Lines: lines,
	}
}

func TestPayrollCSV_BOM_encabezado_y_decimales(t *testing.T) {
	run := runCSV(
		PayrollLine{
			UserID: "u-a", Name: "Ana", Active: true, SalesCount: 3,
			SalesTotalCents: 3499, CommissionCents: 499, FixedCents: 100000,
			AdjustmentCents: 0, TotalCents: 100499,
		},
		PayrollLine{
			UserID: "u-b", Name: "Beto", Active: false, SalesCount: 0,
			SalesTotalCents: 0, CommissionCents: 0, FixedCents: 0,
			AdjustmentCents: -1250, AdjustmentNote: "Corrección", TotalCents: 0,
		},
	)
	got := string(PayrollCSV(run))
	if !strings.HasPrefix(got, "\xef\xbb\xbf") {
		t.Fatal("el CSV tiene que empezar con el BOM UTF-8")
	}
	body := strings.TrimPrefix(got, "\xef\xbb\xbf")
	rows := strings.Split(body, "\r\n")
	if len(rows) < 4 {
		t.Fatalf("filas del CSV: %q", rows)
	}
	if rows[0] != PayrollCSVHeader {
		t.Fatalf("encabezado = %q, esperaba %q", rows[0], PayrollCSVHeader)
	}
	if rows[0] != "Profesional,Estado,Ventas,Total vendido,Comisión,Fijo,Ajuste,Nota del ajuste,Total" {
		t.Fatalf("el encabezado del contrato cambió: %q", rows[0])
	}
	want1 := "Ana,Activo,3,34.99,4.99,1000.00,0.00,,1004.99"
	if rows[1] != want1 {
		t.Fatalf("fila de Ana = %q, esperaba %q", rows[1], want1)
	}
	want2 := "Beto,Dado de baja,0,0.00,0.00,0.00,-12.50,Corrección,0.00"
	if rows[2] != want2 {
		t.Fatalf("fila de Beto = %q, esperaba %q", rows[2], want2)
	}
	if strings.HasSuffix(body, "\r\n") != true {
		t.Fatal("cada fila (incluida la última) termina en CRLF")
	}
}

// Nombres y notas maliciosos: la celda sale neutralizada. Se parsea con
// encoding/csv (no con un split por coma): si las comillas del RFC 4180
// faltaran, el nombre con coma partiría la fila y el lector lo delata.
func TestPayrollCSV_neutraliza_nombres_y_notas(t *testing.T) {
	run := runCSV(
		PayrollLine{UserID: "u-a", Name: "=SUM(A1:A9)", Active: true},
		PayrollLine{UserID: "u-b", Name: "+Ana", Active: true},
		PayrollLine{UserID: "u-c", Name: "-Beto", Active: true},
		PayrollLine{UserID: "u-d", Name: "@Cmd|' /C calc'!A0", Active: true},
		PayrollLine{UserID: "u-e", Name: "\tTab", Active: true},
		PayrollLine{UserID: "u-f", Name: "\rCR", Active: true},
		PayrollLine{UserID: "u-g", Name: "Ruiz, Ana", Active: true},
		PayrollLine{UserID: "u-h", Name: "Dijo \"hola\"", Active: true},
		PayrollLine{UserID: "u-i", Name: "=Ajuste,\"x\"", Active: true, TotalCents: 100,
			AdjustmentCents: 100, AdjustmentNote: "=1+1"},
		PayrollLine{UserID: "u-j", Name: "Ñandú", Active: true},
	)
	body := strings.TrimPrefix(string(PayrollCSV(run)), "\xef\xbb\xbf")
	reader := csv.NewReader(strings.NewReader(body))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("el CSV no se puede parsear (¿comillas rotas?): %v", err)
	}
	if len(records) != 11 {
		t.Fatalf("filas = %d, esperaba 11", len(records))
	}
	wantNames := []string{
		"'=SUM(A1:A9)", "'+Ana", "'-Beto", "'@Cmd|' /C calc'!A0",
		"'\tTab", "'\rCR", "Ruiz, Ana", "Dijo \"hola\"", "'=Ajuste,\"x\"", "Ñandú",
	}
	for i, want := range wantNames {
		row := records[i+1]
		if len(row) != 9 {
			t.Fatalf("fila %d con %d columnas (el nombre no está entre comillas): %q", i+1, len(row), row)
		}
		if row[0] != want {
			t.Fatalf("fila %d: nombre = %q, esperaba %q", i+1, row[0], want)
		}
	}
	// La nota también se neutraliza.
	if note := records[9][7]; note != "'=1+1" {
		t.Fatalf("nota = %q, esperaba %q", note, "'=1+1")
	}
}

// El nombre del archivo se arma SOLO con el tipo y las dos fechas validadas:
// nada del salón, del nombre ni del ajuste puede inyectar cabeceras.
func TestPayrollCSVFilename(t *testing.T) {
	cases := []struct {
		kind           PayPeriodKind
		from, to, want string
	}{
		{PayMonth, "2026-02-01", "2026-02-28", "nomina-month-2026-02-01-2026-02-28.csv"},
		{PayWeek, "2026-03-09", "2026-03-15", "nomina-week-2026-03-09-2026-03-15.csv"},
		{PayFortnight, "2026-03-16", "2026-03-31", "nomina-fortnight-2026-03-16-2026-03-31.csv"},
		// Basura: se descarta lo que no sea [a-z0-9-] (una comilla o un CR no
		// llegan nunca a la cabecera).
		{PayPeriodKind("month\r\nX"), "2026-02-01\"; x=1", "2026-02-28", "nomina-monthx-2026-02-01x1-2026-02-28.csv"},
	}
	for _, c := range cases {
		if got := PayrollCSVFilename(c.kind, c.from, c.to); got != c.want {
			t.Fatalf("PayrollCSVFilename(%q,%q,%q) = %q, esperaba %q", c.kind, c.from, c.to, got, c.want)
		}
	}
}

// El ajuste: OBLIGATORIO, entero con signo, dentro de ±MaxPayCents, con nota
// obligatoria si no es 0, de hasta 200 caracteres y sin saltos de línea.
func TestNewPayrollAdjustment(t *testing.T) {
	if _, _, err := NewPayrollAdjustment(0, ""); err != nil {
		t.Fatalf("ajuste 0 sin nota: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(0, "nota opcional"); err != nil {
		t.Fatalf("ajuste 0 con nota: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(5000, "venta mal cargada"); err != nil {
		t.Fatalf("ajuste con nota: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(-5000, "descuento"); err != nil {
		t.Fatalf("ajuste negativo con nota: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(MaxPayCents, "tope"); err != nil {
		t.Fatalf("ajuste en el máximo: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(-MaxPayCents, "tope"); err != nil {
		t.Fatalf("ajuste en el mínimo: %v", err)
	}

	// Nota obligatoria si el ajuste no es 0 (ausente, vacía o solo espacios).
	for _, note := range []string{"", "   "} {
		_, _, err := NewPayrollAdjustment(1, note)
		if err == nil || err.Error() != "Explicá el ajuste en una nota" {
			t.Fatalf("nota %q: err = %v", note, err)
		}
	}
	// Tope del ajuste.
	for _, v := range []int{MaxPayCents + 1, -MaxPayCents - 1} {
		if _, _, err := NewPayrollAdjustment(v, "nota"); err == nil {
			t.Fatalf("ajuste %d debía rechazarse", v)
		}
	}
	// Largo de la nota: 200 sí, 201 no (se mide en runes).
	if _, _, err := NewPayrollAdjustment(1, strings.Repeat("ñ", 200)); err != nil {
		t.Fatalf("nota de 200: %v", err)
	}
	if _, _, err := NewPayrollAdjustment(1, strings.Repeat("ñ", 201)); err == nil {
		t.Fatal("nota de 201 debía rechazarse")
	}
	// Sin saltos de línea ni caracteres de control.
	for _, note := range []string{"a\nb", "a\rb", "a\tb", "a\u2028b", string([]byte{0x07})} {
		if _, _, err := NewPayrollAdjustment(1, note); err == nil {
			t.Fatalf("nota %q debía rechazarse", note)
		}
	}
}

// La línea: total = comisión + fijo + ajuste, y NUNCA negativo.
func TestPayrollLine_WithAdjustment(t *testing.T) {
	line := PayrollLine{UserID: "u-a", Name: "Ana", CommissionCents: 500, FixedCents: 1000, TotalCents: 1500}
	got, err := line.WithAdjustment(-1500, "todo descontado")
	if err != nil {
		t.Fatalf("ajuste al total exacto: %v", err)
	}
	if got.AdjustmentCents != -1500 || got.TotalCents != 0 ||
		got.AdjustmentNote != "todo descontado" ||
		got.CommissionCents != 500 || got.FixedCents != 1000 {
		t.Fatalf("línea ajustada: %+v", got)
	}

	_, err = line.WithAdjustment(-1501, "de más")
	if err == nil || err.Error() != "El ajuste dejaría el total en negativo" {
		t.Fatalf("err = %v", err)
	}
	// La línea original no se toca.
	if line.AdjustmentCents != 0 || line.TotalCents != 1500 {
		t.Fatalf("WithAdjustment no puede mutar la original: %+v", line)
	}
}

// Totales de la corrida: la suma de las líneas con el ajuste incluido.
func TestPayrollRun_Totals(t *testing.T) {
	run := runCSV(
		PayrollLine{UserID: "u-a", Name: "Ana", CommissionCents: 499, FixedCents: 100000, AdjustmentCents: 100, TotalCents: 100599},
		PayrollLine{UserID: "u-b", Name: "Beto", CommissionCents: 200, FixedCents: 0, AdjustmentCents: -50, TotalCents: 150},
	)
	got := run.Totals()
	if got.CommissionCents != 699 || got.FixedCents != 100000 ||
		got.AdjustmentCents != 50 || got.TotalCents != 100749 {
		t.Fatalf("totales = %+v", got)
	}
	if !run.Paid() || run.Open() {
		t.Fatalf("estado pagado mal interpretado: %+v", run.Status)
	}
}

// Una corrida se arma con el período ya derivado y sus líneas validadas.
func TestNewPayrollRun(t *testing.T) {
	p := PayPeriod{Kind: PayMonth,
		From: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)}
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	lines := []PayrollLine{{UserID: "u-a", Name: "Ana", CommissionCents: 499, TotalCents: 499}}
	run, err := NewPayrollRun("run-1", "s-1", p, "u-owner", now, lines)
	if err != nil {
		t.Fatalf("corrida válida: %v", err)
	}
	if run.From != "2026-02-01" || run.To != "2026-02-28" ||
		run.Kind != PayMonth || run.Status != PayrollRunOpen || !run.Open() || run.Paid() {
		t.Fatalf("corrida armada mal: %+v", run)
	}
	if run.PaidAt != nil {
		t.Fatalf("una abierta no tiene paid_at: %v", run.PaidAt)
	}

	// Sin líneas no hay liquidación (el caso de uso da el mensaje del contrato;
	// el constructor no arma una corrida vacía).
	if _, err := NewPayrollRun("run-2", "s-1", p, "u-owner", now, nil); err == nil {
		t.Fatal("una corrida sin líneas debía rechazarse")
	}
	// Dos líneas de la misma persona romperían la PK: se rechazan antes.
	dup := []PayrollLine{
		{UserID: "u-a", Name: "Ana", TotalCents: 0},
		{UserID: "u-a", Name: "Ana", TotalCents: 0},
	}
	if _, err := NewPayrollRun("run-3", "s-1", p, "u-owner", now, dup); err == nil {
		t.Fatal("dos líneas de la misma persona debían rechazarse")
	}
}
