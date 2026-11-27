package httpapi_test

import (
	"fmt"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/exports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Contrato HTTP de la exportación CSV (F4): las tres rutas, los permisos de D1
// rol por rol, el rango y el tope de D2/D3 con sus mensajes EXACTOS, las
// cabeceras de D6 y la única línea de log de D7.

// exportHTTPNow es el reloj fijo: 2026-05-05T03:00Z = 2026-05-04 21:00 en CDMX.
var exportHTTPNow = time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC)

type exportHTTPEnv struct {
	*httpkit.Env
	Exports *httpkit.Exports
	svc     *exports.ExportService
	logs    []string
}

func (e *exportHTTPEnv) logf(format string, args ...any) {
	e.logs = append(e.logs, fmt.Sprintf(format, args...))
}

// rebuild vuelve a montar el router con el handler de export actual (el tope de
// filas cambia entre pruebas).
func (e *exportHTTPEnv) rebuild() {
	d := e.Env.Deps
	d.Exports = httpapi.ExportHandler{Exports: *e.svc, Memberships: e.Mems, Logf: e.logf}
	r := gin.New()
	httpapi.RegisterRoutes(r, d)
	e.Env.Router = r
}

func newExportHTTPEnv(t *testing.T) *exportHTTPEnv {
	t.Helper()
	base := httpkit.NewPOSEnv()
	repo := httpkit.NewExports()
	svc := &exports.ExportService{
		Exports: repo, Memberships: base.Mems, Settings: base.Settings,
		Now: func() time.Time { return exportHTTPNow },
	}
	e := &exportHTTPEnv{Env: base, Exports: repo, svc: svc}
	e.rebuild()
	return e
}

// member registra un usuario y lo deja en el salón con el rol pedido.
func (e *exportHTTPEnv) member(t *testing.T, email, salonID string, role domain.Role) (string, string) {
	t.Helper()
	tok, uid := e.Register(t, email)
	if err := e.Mems.Save(domain.Membership{UserID: uid, SalonID: salonID, Role: role, Attends: role == domain.RoleProfessional}); err != nil {
		t.Fatalf("membership %s: %v", email, err)
	}
	return tok, uid
}

func (e *exportHTTPEnv) exportPath(salonID, file string) string {
	return "/api/v1/salons/" + salonID + "/export/" + file
}

// seedExportData deja datos del salón en los tres fakes (y basura de otro salón).
func (e *exportHTTPEnv) seedExportData(salonID, otherSalon string) {
	e.Exports.AddSale(salonID, domain.ExportSaleRow{
		CreatedAt: time.Date(2026, 5, 4, 18, 0, 0, 0, time.UTC), SaleID: "v1",
		ProfessionalName: "Ana", ClientName: "Lía", Method: domain.MethodCard,
		Items: 2, TotalCents: 150000,
	})
	e.Exports.AddSale(salonID, domain.ExportSaleRow{
		CreatedAt: time.Date(2026, 5, 3, 15, 0, 0, 0, time.UTC), SaleID: "v2",
		ProfessionalName: "Ana", ClientName: "Zoe", Method: domain.MethodCash,
		Items: 1, TotalCents: 5000,
	})
	// Fuera de la ventana 2026-05-01..2026-05-04 (es el 5 local).
	e.Exports.AddSale(salonID, domain.ExportSaleRow{
		CreatedAt: time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC), SaleID: "v3",
		ProfessionalName: "Ana", ClientName: "Fuera", Method: domain.MethodCash,
		Items: 1, TotalCents: 100,
	})
	e.Exports.AddSale(otherSalon, domain.ExportSaleRow{
		CreatedAt: time.Date(2026, 5, 4, 18, 0, 0, 0, time.UTC), SaleID: "ajena",
		ProfessionalName: "Otra", ClientName: "Ajena", Method: domain.MethodCash,
		Items: 1, TotalCents: 999,
	})
	e.Exports.AddClose(salonID, domain.ExportCashCloseRow{
		OpenedAt:        time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC),
		ClosedAt:        time.Date(2026, 5, 4, 23, 30, 0, 0, time.UTC),
		SystemCashCents: 10005, CountedCents: 10000, DifferenceCents: -5,
		Note: "=cmd|' /C calc'!A0",
	})
	e.Exports.AddClose(otherSalon, domain.ExportCashCloseRow{
		OpenedAt: time.Date(2026, 5, 4, 15, 0, 0, 0, time.UTC),
		ClosedAt: time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC), Note: "ajena",
	})
	e.Exports.AddClient(salonID, domain.ExportClientRow{
		Name: "Zoe", Phone: "11 2222-3333", Email: "zoe@mail.com",
		CreatedAt: time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC),
	})
	e.Exports.AddClient(salonID, domain.ExportClientRow{
		Name: "Lía", Phone: "+54 11 5555-1234", Email: "lia@mail.com",
		Notes: "alergia al nickel", CreatedAt: time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC),
	})
	e.Exports.AddClient(otherSalon, domain.ExportClientRow{
		Name: "Ajena", CreatedAt: time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC),
	})
}

// Las tres rutas: 200, cabeceras de D6 y el CSV exacto (BOM, cabecera, filas en
// el orden del contrato, fechas locales y fórmulas neutralizadas).
func TestHTTP_export_csv_los_tres_archivos(t *testing.T) {
	e := newExportHTTPEnv(t)
	ownerTok, ownerID := e.member(t, "duena@export.com", "", domain.RoleOwner)
	salonID := e.Salon(t, ownerTok, "export")
	// El salón nace con la dueña; el segundo miembro es el profesional del seed.
	_, profID := e.Register(t, "profe@export.com")
	if err := e.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional, Attends: true}); err != nil {
		t.Fatalf("profesional: %v", err)
	}
	otherSalon := "salon-ajeno"
	e.seedExportData(salonID, otherSalon)

	cases := []struct {
		name, file, query string
		wantName          string
		wantBody          string
		logType           string
		rows              int
	}{
		{
			"ventas", "sales.csv", "?from=2026-05-01&to=2026-05-04",
			"ventas-2026-05-01_2026-05-04.csv",
			"\xef\xbb\xbf" + domain.ExportSalesCSVHeader + "\r\n" +
				"2026-05-04 12:00,v1,Ana,Lía,Tarjeta,2,1500.00\r\n" +
				"2026-05-03 09:00,v2,Ana,Zoe,Efectivo,1,50.00\r\n",
			"ventas", 2,
		},
		{
			"cierres", "cash-closes.csv", "?from=2026-05-01&to=2026-05-04",
			"cierres-2026-05-01_2026-05-04.csv",
			"\xef\xbb\xbf" + domain.ExportCashClosesCSVHeader + "\r\n" +
				"2026-05-04 09:00,2026-05-04 17:30,100.05,100.00,-0.05,'=cmd|' /C calc'!A0\r\n",
			"cierres", 1,
		},
		{
			"clientes", "clients.csv", "",
			"clientes-2026-05-04.csv",
			"\xef\xbb\xbf" + domain.ExportClientsCSVHeader + "\r\n" +
				"Lía,'+54 11 5555-1234,lia@mail.com,alergia al nickel,2026-05-04\r\n" +
				"Zoe,11 2222-3333,zoe@mail.com,,2026-05-04\r\n",
			"clientes", 2,
		},
	}
	for _, c := range cases {
		e.logs = nil
		w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, c.file)+c.query, "", ownerTok)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
			t.Fatalf("%s: Content-Type = %q", c.name, ct)
		}
		if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="`+c.wantName+`"` {
			t.Fatalf("%s: Content-Disposition = %q", c.name, cd)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("%s: Cache-Control = %q", c.name, cc)
		}
		if x := w.Header().Get("X-Content-Type-Options"); x != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q", c.name, x)
		}
		if got := w.Body.String(); got != c.wantBody {
			t.Fatalf("%s: cuerpo =\n%q\nquería\n%q", c.name, got, c.wantBody)
		}
		// D7: UNA línea, con tipo, salón, usuario y filas; sin datos personales
		// ni montos.
		if len(e.logs) != 1 {
			t.Fatalf("%s: %d líneas de log, quería 1 (%q)", c.name, len(e.logs), e.logs)
		}
		wantLog := fmt.Sprintf("export csv: tipo=%s salon=%s usuario=%s filas=%d", c.logType, salonID, ownerID, c.rows)
		if e.logs[0] != wantLog {
			t.Fatalf("%s: log = %q, quería %q", c.name, e.logs[0], wantLog)
		}
		for _, leak := range []string{"Lía", "lia@mail.com", "5555", "1500.00", "100.05", "calc"} {
			if strings.Contains(e.logs[0], leak) {
				t.Fatalf("%s: el log filtra %q: %q", c.name, leak, e.logs[0])
			}
		}
	}
}

// D1: owner/admin 200; recepción, profesional y cliente 403 «sin permiso»; sin
// token 401; otro salón 403/404.
func TestHTTP_export_csv_permisos_rol_por_rol(t *testing.T) {
	e := newExportHTTPEnv(t)
	ownerTok, _ := e.member(t, "rol-owner@export.com", "", domain.RoleOwner)
	salonID := e.Salon(t, ownerTok, "roles") // la dueña queda como owner de ese salón
	adminTok, _ := e.member(t, "rol-admin@export.com", salonID, domain.RoleAdmin)
	recepTok, _ := e.member(t, "rol-recep@export.com", salonID, domain.RoleReception)
	profTok, _ := e.member(t, "rol-prof@export.com", salonID, domain.RoleProfessional)
	clientTok, _ := e.member(t, "rol-client@export.com", salonID, domain.RoleClient)
	// Dueño de OTRO salón: no es miembro de este.
	_, _ = e.Register(t, "rol-ajeno@export.com")

	otherOwnerTok, _ := e.Register(t, "rol-otro@export.com")
	otherSalon := e.Salon(t, otherOwnerTok, "otro-salon")
	e.seedExportData(salonID, otherSalon)

	files := []struct{ file, query string }{
		{"sales.csv", "?from=2026-05-01&to=2026-05-04"},
		{"cash-closes.csv", "?from=2026-05-01&to=2026-05-04"},
		{"clients.csv", ""},
	}
	for _, f := range files {
		path := e.exportPath(salonID, f.file) + f.query
		for _, ok := range []struct {
			name, tok string
		}{{"owner", ownerTok}, {"admin", adminTok}} {
			if w := httpkit.Do(e.Router, "GET", path, "", ok.tok); w.Code != http.StatusOK {
				t.Fatalf("%s de %s: %d %s", f.file, ok.name, w.Code, w.Body.String())
			}
		}
		for _, no := range []struct {
			name, tok string
		}{
			{"recepción", recepTok}, {"profesional", profTok}, {"cliente", clientTok},
		} {
			w := httpkit.Do(e.Router, "GET", path, "", no.tok)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s de %s: %d %s", f.file, no.name, w.Code, w.Body.String())
			}
			var body map[string]string
			httpkit.MustJSON(t, w.Body.Bytes(), &body)
			if body["error"] != "sin permiso" {
				t.Fatalf("%s de %s: error = %q", f.file, no.name, body["error"])
			}
			if strings.HasPrefix(w.Body.String(), "\xef\xbb\xbf") {
				t.Fatalf("%s de %s: devolvió un CSV a quien no puede exportar", f.file, no.name)
			}
		}
		// Sin token: 401.
		if w := httpkit.Do(e.Router, "GET", path, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s sin token: %d", f.file, w.Code)
		}
		// Pedir el salón ajeno con el token del dueño de s1: 403 o 404.
		w := httpkit.Do(e.Router, "GET", e.exportPath(otherSalon, f.file)+f.query, "", ownerTok)
		if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Fatalf("%s de otro salón: %d %s", f.file, w.Code, w.Body.String())
		}
	}
}

// D2: rango obligatorio, bien formado, to >= from y hasta 366 días. D3: el tope
// es 400 sin CSV parcial. Los mensajes son los EXACTOS del contrato.
func TestHTTP_export_csv_rango_y_tope(t *testing.T) {
	e := newExportHTTPEnv(t)
	ownerTok, _ := e.member(t, "rango@export.com", "", domain.RoleOwner)
	salonID := e.Salon(t, ownerTok, "rango")
	e.seedExportData(salonID, "otro-rango")

	badRanges := []string{
		"",
		"?from=2026-05-01",
		"?to=2026-05-04",
		"?from=2026-05-04&to=2026-05-01",
		"?from=01/05/2026&to=04/05/2026",
		"?from=2025-01-01&to=2026-05-04", // 489 días
	}
	for _, q := range badRanges {
		for _, file := range []string{"sales.csv", "cash-closes.csv"} {
			w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, file)+q, "", ownerTok)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s %q: %d %s", file, q, w.Code, w.Body.String())
			}
			var body map[string]string
			httpkit.MustJSON(t, w.Body.Bytes(), &body)
			if body["error"] != domain.MsgExportRangeInvalid {
				t.Fatalf("%s %q: error = %q", file, q, body["error"])
			}
			if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/csv") {
				t.Fatalf("%s %q: un 400 salió como CSV", file, q)
			}
		}
	}
	// 366 días exactos pasan.
	if w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, "sales.csv")+"?from=2026-01-01&to=2027-01-01", "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("366 días: %d %s", w.Code, w.Body.String())
	}

	// D3: con un tope de 1, dos ventas son 400 SIN archivo parcial.
	e.svc.RowCap = 1
	e.rebuild()
	e.logs = nil
	w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, "sales.csv")+"?from=2026-05-01&to=2026-05-04", "", ownerTok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("tope: %d %s", w.Code, w.Body.String())
	}
	var body map[string]string
	httpkit.MustJSON(t, w.Body.Bytes(), &body)
	if body["error"] != domain.MsgExportRowsTooMany {
		t.Fatalf("tope: error = %q", body["error"])
	}
	if body := w.Body.String(); strings.Contains(body, domain.ExportSalesCSVHeader) || strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Fatalf("un 400 devolvió parte del CSV: %q", body)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("un 400 no debe anunciar un archivo: %q", cd)
	}
	if len(e.logs) != 0 {
		t.Fatalf("un 400 no es un export: log = %q", e.logs)
	}
	// El LIMIT que se pide es tope + 1 (la consulta sabe si hay más).
	if len(e.Exports.Limits) == 0 || e.Exports.Limits[len(e.Exports.Limits)-1] != 2 {
		t.Fatalf("LIMIT = %v, quería tope+1 = 2", e.Exports.Limits)
	}

	// Cierres y clientes tienen sus propios mensajes de tope.
	if w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, "cash-closes.csv")+"?from=2026-05-01&to=2026-05-04", "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("1 cierre con tope 1 debía pasar: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		e.Exports.AddClose(salonID, domain.ExportCashCloseRow{
			OpenedAt: time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
			ClosedAt: time.Date(2026, 5, 4, 11+i, 0, 0, 0, time.UTC),
		})
	}
	w = httpkit.Do(e.Router, "GET", e.exportPath(salonID, "cash-closes.csv")+"?from=2026-05-01&to=2026-05-04", "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body["error"] != domain.MsgExportRowsTooMany {
		t.Fatalf("cierres sobre el tope: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(e.Router, "GET", e.exportPath(salonID, "clients.csv"), "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body["error"] != domain.MsgExportClientsTooMany {
		t.Fatalf("clientes sobre el tope: %d %s", w.Code, w.Body.String())
	}
}

// D4: sin filas en el rango = 200 con SOLO la cabecera (nunca 404 ni vacío).
func TestHTTP_export_csv_rango_vacio_solo_cabecera(t *testing.T) {
	e := newExportHTTPEnv(t)
	ownerTok, _ := e.member(t, "vacio@export.com", "", domain.RoleOwner)
	salonID := e.Salon(t, ownerTok, "vacio")
	w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, "sales.csv")+"?from=2026-05-01&to=2026-05-01", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("ventas vacías: %d %s", w.Code, w.Body.String())
	}
	want := "\xef\xbb\xbf" + domain.ExportSalesCSVHeader + "\r\n"
	if w.Body.String() != want {
		t.Fatalf("ventas vacías = %q, quería %q", w.Body.String(), want)
	}
}

// El nombre del archivo sale saneado: no hay forma de inyectar una cabecera en
// Content-Disposition con lo que llega por query string.
func TestHTTP_export_csv_nombre_sin_inyeccion(t *testing.T) {
	e := newExportHTTPEnv(t)
	ownerTok, _ := e.member(t, "nombre@export.com", "", domain.RoleOwner)
	salonID := e.Salon(t, ownerTok, "nombre")
	// Una fecha con CRLF no es un rango válido: 400 y ninguna cabecera armada.
	w := httpkit.Do(e.Router, "GET", e.exportPath(salonID, "sales.csv")+"?from=2026-05-01%0d%0aX-Injected:%201&to=2026-05-04", "", ownerTok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CRLF en from: %d %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("Content-Disposition inyectada: %q", cd)
	}
	// Un nombre válido no lleva nada más que el alfabeto seguro.
	w = httpkit.Do(e.Router, "GET", e.exportPath(salonID, "sales.csv")+"?from=2026-05-01&to=2026-05-04", "", ownerTok)
	cd := w.Header().Get("Content-Disposition")
	if cd != `attachment; filename="ventas-2026-05-01_2026-05-04.csv"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}
