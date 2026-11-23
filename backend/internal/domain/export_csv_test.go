package domain

import (
	"strings"
	"testing"
	"time"
)

// Contrato F4 (exportar CSV de ventas, cierres de caja y clientes): cabeceras y
// orden de columnas EXACTOS (D2/D4/D6), BOM y CRLF, fórmulas neutralizadas en
// toda celda de TEXTO (D5) y números crudos (una diferencia negativa sigue
// siendo un número), fechas en la zona del salón y rango de 366 días (D2).

const exportTestBOM = "\xef\xbb\xbf"

// headerLine devuelve la primera línea del archivo (sin BOM, con CRLF).
func headerLine(t *testing.T, data []byte) string {
	t.Helper()
	s := string(data)
	if !strings.HasPrefix(s, exportTestBOM) {
		t.Fatalf("el archivo no empieza con BOM: %q", s[:10])
	}
	i := strings.Index(s, "\r\n")
	if i < 0 {
		t.Fatalf("la cabecera no termina en CRLF: %q", s)
	}
	return s[len(exportTestBOM):i]
}

// rowsOf devuelve las filas completas (cada una con su CRLF) después de la
// cabecera.
func rowsOf(t *testing.T, data []byte) []string {
	t.Helper()
	s := strings.TrimPrefix(string(data), exportTestBOM)
	i := strings.Index(s, "\r\n")
	if i < 0 {
		t.Fatalf("sin cabecera: %q", s)
	}
	rest := s[i+2:]
	if rest == "" {
		return nil
	}
	rows := strings.SplitAfter(rest, "\r\n")
	if rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// exportLoc carga una zona IANA para el test (mustLoc ya existe en expense_test).
func exportLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("zona %s: %v", name, err)
	}
	return loc
}

// Cabecera EXACTA de los tres archivos, en el orden del contrato.
func TestExportCSV_cabeceras_exactas(t *testing.T) {
	loc := exportLoc(t, "America/Mexico_City")
	sales := SalesCSV(nil, loc)
	closes := CashClosesCSV(nil, loc)
	clients := ClientsCSV(nil, loc)

	want := map[string]string{
		"ventas":   "Fecha,Venta,Profesional,Cliente,Método,Ítems,Total",
		"cierres":  "Apertura,Cierre,Efectivo del sistema,Efectivo contado,Diferencia,Nota",
		"clientes": "Nombre,Teléfono,Email,Notas,Alta",
	}
	got := map[string]string{
		"ventas":   headerLine(t, sales),
		"cierres":  headerLine(t, closes),
		"clientes": headerLine(t, clients),
	}
	for name, w := range want {
		if got[name] != w {
			t.Fatalf("%s: cabecera = %q, quería %q", name, got[name], w)
		}
	}
	// Sin filas: SOLO la cabecera (200 con cabecera, D4).
	for name, data := range map[string][]byte{"ventas": sales, "cierres": closes, "clientes": clients} {
		if rows := rowsOf(t, data); len(rows) != 0 {
			t.Fatalf("%s vacío: filas = %q", name, rows)
		}
	}
}

// Una fila por venta con el orden del contrato y los montos como decimales con
// punto (CentsDecimal), el método en español y los ítems crudos.
func TestExportCSV_fila_de_venta(t *testing.T) {
	loc := exportLoc(t, "America/Mexico_City")
	at := time.Date(2026, 5, 4, 15, 30, 0, 0, time.UTC) // 09:30 local
	data := SalesCSV([]ExportSaleRow{{
		CreatedAt: at, SaleID: "abc123", ProfessionalName: "Ana Pérez",
		ClientName: "Lía Gómez", Method: MethodCash, Items: 3, TotalCents: 123450,
	}}, loc)
	rows := rowsOf(t, data)
	if len(rows) != 1 {
		t.Fatalf("filas = %q", rows)
	}
	want := "2026-05-04 09:30,abc123,Ana Pérez,Lía Gómez,Efectivo,3,1234.50\r\n"
	if rows[0] != want {
		t.Fatalf("fila = %q, quería %q", rows[0], want)
	}
	if label := PaymentMethodLabel(MethodCard); label != "Tarjeta" {
		t.Fatalf("card = %q", label)
	}
	if label := PaymentMethodLabel(MethodTransfer); label != "Transferencia" {
		t.Fatalf("transfer = %q", label)
	}
}

// Fila de cierre: apertura y cierre locales, tres montos y la nota.
func TestExportCSV_fila_de_cierre(t *testing.T) {
	loc := exportLoc(t, "America/Mexico_City")
	open := time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC)   // 09:00 local
	close := time.Date(2026, 5, 4, 23, 30, 0, 0, time.UTC) // 17:30 local
	data := CashClosesCSV([]ExportCashCloseRow{{
		OpenedAt: open, ClosedAt: close, SystemCashCents: 100050,
		CountedCents: 10000, DifferenceCents: -5, Note: "faltante",
	}}, loc)
	rows := rowsOf(t, data)
	want := "2026-05-04 09:00,2026-05-04 17:30,1000.50,100.00,-0.05,faltante\r\n"
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("fila = %q, quería %q", rows, want)
	}
}

// Fila de cliente: Alta es el DÍA local de created_at.
func TestExportCSV_fila_de_cliente(t *testing.T) {
	loc := exportLoc(t, "America/Mexico_City")
	// 2026-05-05 03:00 UTC = 2026-05-04 21:00 local: el alta es el día 4.
	created := time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC)
	data := ClientsCSV([]ExportClientRow{{
		Name: "Lía Gómez", Phone: "11 5555-1234", Email: "lia@mail.com",
		Notes: "alergia al nickel", CreatedAt: created,
	}}, loc)
	rows := rowsOf(t, data)
	want := "Lía Gómez,11 5555-1234,lia@mail.com,alergia al nickel,2026-05-04\r\n"
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("fila = %q, quería %q", rows, want)
	}
}

// D5: TODA celda de texto pasa por CSVCell (apostrofo de neutralización y
// comillas RFC 4180). La tabla fija el valor de entrada y la celda EXACTA que
// tiene que salir.
func TestExportCSV_neutraliza_formulas_en_texto(t *testing.T) {
	loc := time.UTC
	at := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	cases := []struct{ in, want string }{
		{"=cmd|' /C calc'!A0", `'=cmd|' /C calc'!A0`},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@SUM", "'@SUM"},
		{"＝1", "'＝1"},
		{" =1", "' =1"},
		{"\tTAB", "'\tTAB"},
		{"Ana\nPérez", "\"Ana\nPérez\""},
		{`Di "comillas"`, `"Di ""comillas"""`},
		{"Lía, con coma", "\"Lía, con coma\""},
	}
	salesLine := func(prof, client string) string {
		return "2026-05-04 00:00,id1," + prof + "," + client + ",Efectivo,1,0.01\r\n"
	}
	closesLine := func(note string) string {
		return "2026-05-04 00:00,2026-05-04 00:00,1.00,1.00,0.00," + note + "\r\n"
	}
	clientsLine := func(name, phone, email, notes string) string {
		return name + "," + phone + "," + email + "," + notes + ",2026-05-04\r\n"
	}
	for _, c := range cases {
		// Ventas: profesional y cliente.
		got := rowsOf(t, SalesCSV([]ExportSaleRow{{
			CreatedAt: at, SaleID: "id1", ProfessionalName: c.in, ClientName: "Lía",
			Method: MethodCash, Items: 1, TotalCents: 1,
		}}, loc))
		if len(got) != 1 || got[0] != salesLine(c.want, "Lía") {
			t.Fatalf("venta/profesional %q: %q", c.in, got)
		}
		got = rowsOf(t, SalesCSV([]ExportSaleRow{{
			CreatedAt: at, SaleID: "id1", ProfessionalName: "Ana", ClientName: c.in,
			Method: MethodCash, Items: 1, TotalCents: 1,
		}}, loc))
		if len(got) != 1 || got[0] != salesLine("Ana", c.want) {
			t.Fatalf("venta/cliente %q: %q", c.in, got)
		}
		// Cierres: la nota.
		got = rowsOf(t, CashClosesCSV([]ExportCashCloseRow{{
			OpenedAt: at, ClosedAt: at, SystemCashCents: 100, CountedCents: 100,
			Note: c.in,
		}}, loc))
		if len(got) != 1 || got[0] != closesLine(c.want) {
			t.Fatalf("cierre/nota %q: %q", c.in, got)
		}
		// Clientes: nombre, teléfono, email y notas.
		if got := rowsOf(t, ClientsCSV([]ExportClientRow{{
			Name: c.in, Phone: "123", Email: "a@b.com", CreatedAt: at,
		}}, loc)); len(got) != 1 || got[0] != clientsLine(c.want, "123", "a@b.com", "") {
			t.Fatalf("cliente/nombre %q: %q", c.in, got)
		}
		if got := rowsOf(t, ClientsCSV([]ExportClientRow{{
			Name: "Ana", Phone: c.in, Email: "a@b.com", CreatedAt: at,
		}}, loc)); len(got) != 1 || got[0] != clientsLine("Ana", c.want, "a@b.com", "") {
			t.Fatalf("cliente/teléfono %q: %q", c.in, got)
		}
		if got := rowsOf(t, ClientsCSV([]ExportClientRow{{
			Name: "Ana", Phone: "123", Email: c.in, CreatedAt: at,
		}}, loc)); len(got) != 1 || got[0] != clientsLine("Ana", "123", c.want, "") {
			t.Fatalf("cliente/email %q: %q", c.in, got)
		}
		if got := rowsOf(t, ClientsCSV([]ExportClientRow{{
			Name: "Ana", Phone: "123", Email: "a@b.com", Notes: c.in, CreatedAt: at,
		}}, loc)); len(got) != 1 || got[0] != clientsLine("Ana", "123", "a@b.com", c.want) {
			t.Fatalf("cliente/notas %q: %q", c.in, got)
		}
	}
	// El teléfono que empieza con + sale con la comilla simple (D5, deliberado).
	got := rowsOf(t, ClientsCSV([]ExportClientRow{{
		Name: "Ana", Phone: "+54 11 5555-1234", Email: "a@b.com", CreatedAt: at,
	}}, loc))
	if len(got) != 1 || !strings.Contains(got[0], ",'+54 11 5555-1234,") {
		t.Fatalf("teléfono con +: %q", got)
	}
}

// D5: los NÚMEROS no se neutralizan. Una diferencia negativa sigue siendo un
// número y se puede sumar en la planilla.
func TestExportCSV_numeros_crudos(t *testing.T) {
	at := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	rows := rowsOf(t, CashClosesCSV([]ExportCashCloseRow{{
		OpenedAt: at, ClosedAt: at, SystemCashCents: 500, CountedCents: 0,
		DifferenceCents: -500, Note: "",
	}}, time.UTC))
	if len(rows) != 1 || rows[0] != "2026-05-04 00:00,2026-05-04 00:00,5.00,0.00,-5.00,\r\n" {
		t.Fatalf("diferencia negativa: %q", rows)
	}
	// Ítems crudos (0 y > 9 no se tocan).
	rows = rowsOf(t, SalesCSV([]ExportSaleRow{{
		CreatedAt: at, SaleID: "id1", ProfessionalName: "Ana", ClientName: "Lía",
		Method: MethodCash, Items: 12, TotalCents: 0,
	}}, time.UTC))
	if len(rows) != 1 || rows[0] != "2026-05-04 00:00,id1,Ana,Lía,Efectivo,12,0.00\r\n" {
		t.Fatalf("ítems: %q", rows)
	}
}

// D2: las fechas del archivo se escriben en la zona del SALÓN.
func TestExportCSV_fechas_en_la_zona_del_salon(t *testing.T) {
	at := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC)
	cases := map[string]string{
		"America/Mexico_City": "2026-03-08 00:30",
		"America/New_York":    "2026-03-08 01:30",
		"Pacific/Auckland":    "2026-03-08 19:30",
		"UTC":                 "2026-03-08 06:30",
	}
	for zone, want := range cases {
		loc := exportLoc(t, zone)
		rows := rowsOf(t, SalesCSV([]ExportSaleRow{{
			CreatedAt: at, SaleID: "id1", ProfessionalName: "Ana", ClientName: "Lía",
			Method: MethodCash, Items: 1, TotalCents: 1,
		}}, loc))
		if len(rows) != 1 || !strings.HasPrefix(rows[0], want+",") {
			t.Fatalf("%s: %q, quería empezar con %q", zone, rows, want)
		}
	}
	// El día del alta cruza el cambio de día local (el 23 h y el 25 h incluidos).
	ny := exportLoc(t, "America/New_York")
	casesDay := []struct {
		utc  time.Time
		want string
	}{
		{time.Date(2026, 3, 8, 4, 59, 0, 0, time.UTC), "2026-03-07"},  // 23:59 EST
		{time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), "2026-03-08"},   // 00:00 EST
		{time.Date(2026, 11, 1, 3, 59, 0, 0, time.UTC), "2026-10-31"}, // 23:59 EDT
		{time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC), "2026-11-01"},  // 00:00 EDT
	}
	for _, c := range casesDay {
		got := ExportDay(c.utc, ny)
		if got != c.want {
			t.Fatalf("ExportDay(%s, NY) = %q, quería %q", c.utc, got, c.want)
		}
	}
}

// D6: el nombre del archivo se sanea con csvSafe: nada de un salto de línea ni
// una comilla puede inyectar una cabecera en Content-Disposition.
func TestExportCSV_filenames_saneados(t *testing.T) {
	if got := SalesCSVFilename("2026-01-01", "2026-01-31"); got != "ventas-2026-01-01_2026-01-31.csv" {
		t.Fatalf("ventas = %q", got)
	}
	if got := CashClosesCSVFilename("2026-01-01", "2026-01-31"); got != "cierres-2026-01-01_2026-01-31.csv" {
		t.Fatalf("cierres = %q", got)
	}
	if got := ClientsCSVFilename("2026-01-31"); got != "clientes-2026-01-31.csv" {
		t.Fatalf("clientes = %q", got)
	}
	// Entrada hostil: CR, LF, comillas, espacios, punto y comas desaparecen.
	for _, in := range []string{"2026-01-01\r\nX-Injected: 1", `2026"; rm -rf /`, "2026-01-01 00:00"} {
		name := SalesCSVFilename(in, "2026-01-31")
		if strings.ContainsAny(name, "\r\n\"'; ") {
			t.Fatalf("%q -> %q: carácter inseguro en el nombre", in, name)
		}
		if !strings.HasPrefix(name, "ventas-") || !strings.HasSuffix(name, "_2026-01-31.csv") {
			t.Fatalf("%q -> %q: forma inesperada", in, name)
		}
	}
}

// D2: rango obligatorio, formato YYYY-MM-DD, to >= from y hasta 366 días; la
// ventana es [from 00:00 local, to+1 00:00 local) y es correcta en los días de
// 23 y 25 horas.
func TestExportRange_validacion(t *testing.T) {
	invalidMsg := "Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días"
	loc := exportLoc(t, "America/New_York")
	bad := [][2]string{
		{"", "2026-01-31"},
		{"2026-01-01", ""},
		{"", ""},
		{"2026-1-1", "2026-01-31"},
		{"ayer", "hoy"},
		{"2026-02-30", "2026-03-01"},
		{"2026-01-31", "2026-01-01"},
		{"2026-01-01", "2027-01-02"}, // 367 días
		{"2026-01-01T00:00:00Z", "2026-01-31"},
	}
	for _, c := range bad {
		if _, err := ExportRange(c[0], c[1], loc); err == nil {
			t.Fatalf("%q..%q debía ser inválido", c[0], c[1])
		} else if err.Error() != invalidMsg {
			t.Fatalf("%q..%q: mensaje = %q", c[0], c[1], err.Error())
		}
	}
	// 366 días exactos pasan; un solo día también.
	if _, err := ExportRange("2026-01-01", "2027-01-01", loc); err != nil {
		t.Fatalf("366 días debían pasar: %v", err)
	}
	w, err := ExportRange("2026-03-08", "2026-03-08", loc)
	if err != nil || w.FromDate != "2026-03-08" || w.ToDate != "2026-03-08" {
		t.Fatalf("to == from: %+v %v", w, err)
	}
}

func TestExportRange_ventana_en_dias_de_23_y_25_horas(t *testing.T) {
	ny := exportLoc(t, "America/New_York")
	cases := []struct {
		day       string
		from, to  time.Time
		durationH int
	}{
		// 2026-03-08: el día local de 23 h (arranca EST, termina EDT).
		{"2026-03-08",
			time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC), 23},
		// 2026-11-01: el día local de 25 h (arranca EDT, termina EST).
		{"2026-11-01",
			time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC),
			time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC), 25},
	}
	for _, c := range cases {
		w, err := ExportRange(c.day, c.day, ny)
		if err != nil {
			t.Fatalf("%s: %v", c.day, err)
		}
		if !w.From.Equal(c.from) || !w.To.Equal(c.to) {
			t.Fatalf("%s: ventana = [%s, %s), quería [%s, %s)",
				c.day, w.From, w.To, c.from, c.to)
		}
		if got := int(w.To.Sub(w.From).Hours()); got != c.durationH {
			t.Fatalf("%s: duración = %dh, quería %dh", c.day, got, c.durationH)
		}
	}
	// México (sin horario de verano desde 2022) y Auckland: medianoche local.
	mx := exportLoc(t, "America/Mexico_City")
	w, err := ExportRange("2026-06-01", "2026-06-02", mx)
	if err != nil {
		t.Fatalf("mx: %v", err)
	}
	if !w.From.Equal(time.Date(2026, 6, 1, 6, 0, 0, 0, time.UTC)) ||
		!w.To.Equal(time.Date(2026, 6, 3, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("mx ventana = [%s, %s)", w.From, w.To)
	}
	akl := exportLoc(t, "Pacific/Auckland")
	w, err = ExportRange("2026-03-08", "2026-03-08", akl)
	if err != nil {
		t.Fatalf("akl: %v", err)
	}
	if !w.From.Equal(time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC)) ||
		!w.To.Equal(time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("akl ventana = [%s, %s)", w.From, w.To)
	}
}
