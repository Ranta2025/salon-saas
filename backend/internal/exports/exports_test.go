package exports

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// ExportService (F4): permisos owner/admin (D1), rango obligatorio y de hasta
// 366 días con ventana local (D2), tope de filas inyectable (D3), archivo vacío
// con solo la cabecera (D4) y aislamiento por salón (toda lectura lleva
// salon_id).

const exportBOM = "\xef\xbb\xbf"

type exportEnv struct {
	svc     ExportService
	exports *fakeExports
	now     time.Time
}

func newExportEnv(t *testing.T) *exportEnv {
	t.Helper()
	mems := &testutil.FakeMemberships{ByKey: map[string]domain.Membership{}}
	for _, m := range []domain.Membership{
		{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner},
		{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional},
		{UserID: "u-client", SalonID: "s1", Role: domain.RoleClient},
		{UserID: "u-other", SalonID: "s2", Role: domain.RoleOwner},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("seed membresía: %v", err)
		}
	}
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
		"s2": {SalonID: "s2", Timezone: "America/New_York", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	now := time.Date(2026, 5, 5, 3, 0, 0, 0, time.UTC) // 2026-05-04 21:00 en CDMX
	exports := &fakeExports{}
	return &exportEnv{
		svc: ExportService{
			Exports: exports, Memberships: mems, Settings: settings,
			Now: func() time.Time { return now },
		},
		exports: exports,
		now:     now,
	}
}

func (e *exportEnv) actor(userID, salonID string, role domain.Role) domain.Membership {
	return domain.Membership{UserID: userID, SalonID: salonID, Role: role}
}

func exportActor(userID string, role domain.Role) domain.Membership {
	return domain.Membership{UserID: userID, SalonID: "s1", Role: role}
}

// Una venta del salón 1 en el mediodía local del 4 de mayo de 2026.
func (e *exportEnv) saleAt(salonID, id string, at time.Time) {
	e.exports.addSale(salonID, domain.ExportSaleRow{
		CreatedAt: at, SaleID: id, ProfessionalName: "Ana", ClientName: "Lía",
		Method: domain.MethodCash, Items: 1, TotalCents: 100,
	})
}

func TestExportService_permisos_rol_por_rol(t *testing.T) {
	e := newExportEnv(t)
	type call struct {
		name string
		run  func(actor domain.Membership) error
	}
	calls := []call{
		{"ventas", func(a domain.Membership) error {
			_, err := e.svc.Sales(a, "s1", "2026-05-01", "2026-05-04")
			return err
		}},
		{"cierres", func(a domain.Membership) error {
			_, err := e.svc.CashCloses(a, "s1", "2026-05-01", "2026-05-04")
			return err
		}},
		{"clientes", func(a domain.Membership) error {
			_, err := e.svc.Clients(a, "s1")
			return err
		}},
	}
	owners := []domain.Membership{
		exportActor("u-owner", domain.RoleOwner),
		exportActor("u-admin", domain.RoleAdmin),
	}
	for _, c := range calls {
		for _, a := range owners {
			if err := c.run(a); err != nil {
				t.Fatalf("%s de %s: %v", c.name, a.Role, err)
			}
		}
		for _, a := range []domain.Membership{
			exportActor("u-recep", domain.RoleReception),
			exportActor("u-prof", domain.RoleProfessional),
			exportActor("u-client", domain.RoleClient),
		} {
			if err := c.run(a); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("%s de %s: quería ErrForbidden, dio %v", c.name, a.Role, err)
			}
		}
		// Otro salón: el actor no es miembro de s1 con ese salón en el token.
		if err := c.run(e.actor("u-other", "s2", domain.RoleOwner)); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s de otro salón: quería ErrForbidden, dio %v", c.name, err)
		}
	}
	// Un 403 no lee nada de la DB.
	if e.exports.salesCalls+e.exports.closesCalls+e.exports.clientsCalls != 6 {
		t.Fatalf("las lecturas permitidas fueron %d/%d/%d",
			e.exports.salesCalls, e.exports.closesCalls, e.exports.clientsCalls)
	}
}

func TestExportService_rango_invalido(t *testing.T) {
	e := newExportEnv(t)
	e.saleAt("s1", "v1", e.now)
	owner := exportActor("u-owner", domain.RoleOwner)
	for _, c := range [][2]string{
		{"", "2026-05-04"},
		{"2026-05-01", ""},
		{"04/05/2026", "2026-05-04"},
		{"2026-05-04", "2026-05-01"},
		{"2025-01-01", "2026-05-04"}, // > 366 días
	} {
		_, err := e.svc.Sales(owner, "s1", c[0], c[1])
		if err == nil || err.Error() != domain.MsgExportRangeInvalid {
			t.Fatalf("ventas %q..%q: %v", c[0], c[1], err)
		}
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("ventas %q..%q: no es ErrValidation: %v", c[0], c[1], err)
		}
		_, err = e.svc.CashCloses(owner, "s1", c[0], c[1])
		if err == nil || err.Error() != domain.MsgExportRangeInvalid {
			t.Fatalf("cierres %q..%q: %v", c[0], c[1], err)
		}
	}
	// Un 400 no consulta la DB.
	if e.exports.salesCalls != 0 || e.exports.closesCalls != 0 {
		t.Fatalf("un rango inválido leyó: %d/%d", e.exports.salesCalls, e.exports.closesCalls)
	}
}

// D2: la ventana que se le pide a la base es [from 00:00 local, to+1 00:00
// local), no días UTC, y respeta los días de 23 y 25 horas.
func TestExportService_ventana_en_la_zona_del_salon(t *testing.T) {
	e := newExportEnv(t)
	owner := exportActor("u-owner", domain.RoleOwner)
	if _, err := e.svc.Sales(owner, "s1", "2026-03-08", "2026-03-08"); err != nil {
		t.Fatalf("ventas: %v", err)
	}
	wantFrom := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 9, 6, 0, 0, 0, time.UTC)
	if !e.exports.lastFrom.Equal(wantFrom) || !e.exports.lastTo.Equal(wantTo) {
		t.Fatalf("CDMX: [%s, %s), quería [%s, %s)", e.exports.lastFrom, e.exports.lastTo, wantFrom, wantTo)
	}
	// Nueva York en el día de 25 horas: la ventana dura 25 h.
	if _, err := e.svc.CashCloses(e.actor("u-other", "s2", domain.RoleOwner), "s2", "2026-11-01", "2026-11-01"); err != nil {
		t.Fatalf("cierres: %v", err)
	}
	if d := e.exports.lastTo.Sub(e.exports.lastFrom); d != 25*time.Hour {
		t.Fatalf("NY 2026-11-01: ventana de %s", d)
	}
}

// D3: el tope es inyectable; la consulta pide tope + 1 y «hay más» es 400 SIN
// archivo. Exactamente el tope pasa.
func TestExportService_tope_de_filas(t *testing.T) {
	e := newExportEnv(t)
	e.svc.RowCap = 2
	owner := exportActor("u-owner", domain.RoleOwner)
	at := time.Date(2026, 5, 4, 18, 0, 0, 0, time.UTC)
	e.saleAt("s1", "v1", at)
	e.saleAt("s1", "v2", at.Add(-time.Minute))

	file, err := e.svc.Sales(owner, "s1", "2026-05-04", "2026-05-04")
	if err != nil {
		t.Fatalf("exactamente el tope debía pasar: %v", err)
	}
	if file.Rows != 2 || strings.Count(string(file.Data), "\r\n") != 3 {
		t.Fatalf("archivo con 2 filas: rows=%d data=%q", file.Rows, file.Data)
	}
	if e.exports.lastLimit != 3 {
		t.Fatalf("la consulta pidió %d filas, quería tope+1 = 3", e.exports.lastLimit)
	}

	e.saleAt("s1", "v3", at.Add(-2*time.Minute))
	file, err = e.svc.Sales(owner, "s1", "2026-05-04", "2026-05-04")
	if err == nil || err.Error() != domain.MsgExportRowsTooMany {
		t.Fatalf("tope+1: %v", err)
	}
	if file.Name != "" || file.Data != nil || file.Rows != 0 {
		t.Fatalf("un 400 no puede devolver archivo parcial: %+v", file)
	}

	// Cierres: el mismo tope, el mismo mensaje.
	for i := 0; i < 3; i++ {
		e.exports.addClose("s1", domain.ExportCashCloseRow{
			OpenedAt: at, ClosedAt: at.Add(time.Duration(i) * time.Minute),
		})
	}
	if _, err := e.svc.CashCloses(owner, "s1", "2026-05-04", "2026-05-04"); err == nil ||
		err.Error() != domain.MsgExportRowsTooMany {
		t.Fatalf("cierres tope+1: %v", err)
	}

	// Clientes: mensaje propio del contrato.
	for i := 0; i < 3; i++ {
		e.exports.addClient("s1", domain.ExportClientRow{
			Name: "Cliente " + string(rune('A'+i)), CreatedAt: at,
		})
	}
	if _, err := e.svc.Clients(owner, "s1"); err == nil ||
		err.Error() != domain.MsgExportClientsTooMany {
		t.Fatalf("clientes tope+1: %v", err)
	}
}

// D4: sin filas el archivo es la cabecera sola, con 200 (el caso de uso no
// falla). D6: el nombre sale del rango normalizado.
func TestExportService_archivo_vacio_solo_cabecera(t *testing.T) {
	e := newExportEnv(t)
	owner := exportActor("u-owner", domain.RoleOwner)
	file, err := e.svc.Sales(owner, "s1", "2026-05-01", "2026-05-04")
	if err != nil {
		t.Fatalf("ventas vacías: %v", err)
	}
	want := exportBOM + domain.ExportSalesCSVHeader + "\r\n"
	if string(file.Data) != want {
		t.Fatalf("ventas vacías = %q, quería %q", file.Data, want)
	}
	if file.Name != "ventas-2026-05-01_2026-05-04.csv" || file.Rows != 0 {
		t.Fatalf("metadatos: %+v", file)
	}
	file, err = e.svc.CashCloses(owner, "s1", "2026-05-04", "2026-05-04")
	if err != nil || string(file.Data) != exportBOM+domain.ExportCashClosesCSVHeader+"\r\n" {
		t.Fatalf("cierres vacíos: %q %v", file.Data, err)
	}
	if file.Name != "cierres-2026-05-04_2026-05-04.csv" {
		t.Fatalf("nombre de cierres: %q", file.Name)
	}
	file, err = e.svc.Clients(owner, "s1")
	if err != nil || string(file.Data) != exportBOM+domain.ExportClientsCSVHeader+"\r\n" {
		t.Fatalf("clientes vacíos: %q %v", file.Data, err)
	}
}

// D6: `clientes-<hoy local>.csv`. «Hoy» es el día del SALÓN, no UTC.
func TestExportService_clientes_hoy_es_el_dia_local(t *testing.T) {
	e := newExportEnv(t)
	owner := exportActor("u-owner", domain.RoleOwner)
	file, err := e.svc.Clients(owner, "s1")
	if err != nil {
		t.Fatalf("clientes: %v", err)
	}
	// El reloj fijo es 2026-05-05T03:00Z = 2026-05-04 21:00 en CDMX.
	if file.Name != "clientes-2026-05-04.csv" {
		t.Fatalf("nombre = %q", file.Name)
	}
}

// D1/tenant: las filas de otro salón nunca aparecen en el archivo.
func TestExportService_aislamiento_de_tenant(t *testing.T) {
	e := newExportEnv(t)
	at := time.Date(2026, 5, 4, 18, 0, 0, 0, time.UTC)
	e.saleAt("s1", "propia", at)
	e.saleAt("s2", "ajena", at)
	e.exports.addClient("s2", domain.ExportClientRow{Name: "Ajena", CreatedAt: at})
	e.exports.addClose("s2", domain.ExportCashCloseRow{OpenedAt: at, ClosedAt: at, Note: "ajena"})

	owner := exportActor("u-owner", domain.RoleOwner)
	file, err := e.svc.Sales(owner, "s1", "2026-05-04", "2026-05-04")
	if err != nil {
		t.Fatalf("ventas: %v", err)
	}
	if !strings.Contains(string(file.Data), "propia") || strings.Contains(string(file.Data), "ajena") {
		t.Fatalf("ventas con filas de otro salón: %q", file.Data)
	}
	file, err = e.svc.Clients(owner, "s1")
	if err != nil {
		t.Fatalf("clientes: %v", err)
	}
	if strings.Contains(string(file.Data), "Ajena") {
		t.Fatalf("clientes con filas de otro salón: %q", file.Data)
	}
	file, err = e.svc.CashCloses(owner, "s1", "2026-05-04", "2026-05-04")
	if err != nil {
		t.Fatalf("cierres: %v", err)
	}
	if strings.Contains(string(file.Data), "ajena") {
		t.Fatalf("cierres con filas de otro salón: %q", file.Data)
	}
}

// Un error de la base se propaga tal cual (el handler lo vuelve 500), nunca se
// devuelve un archivo inventado.
func TestExportService_propaga_el_error_de_la_lectura(t *testing.T) {
	e := newExportEnv(t)
	boom := errors.New("la base se cayó")
	e.exports.failWith = boom
	owner := exportActor("u-owner", domain.RoleOwner)
	if _, err := e.svc.Sales(owner, "s1", "2026-05-04", "2026-05-04"); !errors.Is(err, boom) {
		t.Fatalf("ventas: %v", err)
	}
	if _, err := e.svc.CashCloses(owner, "s1", "2026-05-04", "2026-05-04"); !errors.Is(err, boom) {
		t.Fatalf("cierres: %v", err)
	}
	if _, err := e.svc.Clients(owner, "s1"); !errors.Is(err, boom) {
		t.Fatalf("clientes: %v", err)
	}
}
