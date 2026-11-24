// F5 por HTTP: la ficha (GET/PUT), el historial con el dinero por rol y el
// client_id opcional del POS. Los fakes viven acá; el router se rearma con el
// historial cableado (el env de pos_test no lo conoce).
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

type fakeHistoryRepo struct {
	data domain.ClientHistoryData
	err  error
}

func (f *fakeHistoryRepo) History(string, string, time.Time, int) (domain.ClientHistoryData, error) {
	if f.err != nil {
		return domain.ClientHistoryData{}, f.err
	}
	return f.data, nil
}

// historyClients es la ficha con semántica de UPSERT por id (como pgstore), sin
// fotos: el fake de realtime_clients_test.go acumula filas y la ficha editada
// quedaría tapada por la vieja al releer.
type historyClients struct{ all []domain.Client }

func (f *historyClients) Save(c domain.Client) error {
	for i := range f.all {
		if f.all[i].ID == c.ID {
			f.all[i] = c
			return nil
		}
	}
	f.all = append(f.all, c)
	return nil
}

func (f *historyClients) List(salonID string) ([]domain.Client, error) {
	var out []domain.Client
	for _, c := range f.all {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *historyClients) FindByID(salonID, id string) (domain.Client, error) {
	for _, c := range f.all {
		if c.SalonID == salonID && c.ID == id {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *historyClients) FindByPhone(salonID, phone string) (domain.Client, error) {
	for _, c := range f.all {
		if c.SalonID == salonID && domain.NormalizePhone(c.Phone) == phone {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *historyClients) FindByName(salonID, name string) (domain.Client, error) {
	for _, c := range f.all {
		if c.SalonID == salonID && c.Name == name {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *historyClients) SavePhoto(domain.ClientPhoto) error { return nil }
func (f *historyClients) ListPhotos(string, string) ([]domain.ClientPhoto, error) {
	return nil, nil
}
func (f *historyClients) DeletePhoto(string, string, string) error { return domain.ErrNotFound }
func (f *historyClients) FindPhoto(string, string, string) (domain.ClientPhoto, error) {
	return domain.ClientPhoto{}, domain.ErrNotFound
}
func (f *historyClients) PhotoByID(string) (domain.ClientPhoto, error) {
	return domain.ClientPhoto{}, domain.ErrNotFound
}

type fakeHTTPLinks struct {
	bookings int
	sales    int
	// saleClient es el vínculo REAL por venta (lo que SaleClientID devuelve).
	saleClient map[string]string
	// failSales hace que el UPDATE de vínculo de ventas falle de verdad (el
	// servicio de ventas del handler es una COPIA: se inyecta el fallo acá).
	failSales bool
}

func (f *fakeHTTPLinks) LinkBookingByID(string, string, string) error { f.bookings++; return nil }
func (f *fakeHTTPLinks) LinkBookingByPhone(string, string) error      { f.bookings++; return nil }
func (f *fakeHTTPLinks) LinkSaleByID(_, saleID, clientID string) error {
	if f.failSales {
		return errors.New("db caída")
	}
	f.sales++
	if f.saleClient == nil {
		f.saleClient = map[string]string{}
	}
	f.saleClient[saleID] = clientID
	return nil
}
func (f *fakeHTTPLinks) SaleClientID(_, saleID string) (string, error) {
	return f.saleClient[saleID], nil
}

// newHistoryEnv rearma el router con el historial y los vínculos cableados.
func newHistoryEnv() (*posEnv, *fakeHistoryRepo, *fakeHTTPLinks) {
	env := newPOSEnv()
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	last := time.Date(2026, 3, 18, 23, 59, 59, 0, time.UTC)
	next := time.Date(2026, 4, 2, 15, 0, 0, 0, time.UTC)
	history := &fakeHistoryRepo{data: domain.ClientHistoryData{
		Visits: 4, NoShows: 1, Cancelled: 2, Upcoming: 1,
		LastDoneAt: &last, NextBookedAt: &next,
		Bookings: []domain.ClientHistoryBooking{
			{ID: "b1", StartsAt: last, Status: domain.BookingDone, ServiceName: "Corte", ProfessionalName: "Ana"},
		},
		Spend: domain.ClientSpend{SalesCount: 2, TotalCents: 123400},
		Sales: []domain.ClientHistorySale{
			{ID: "sa1", CreatedAt: last, TotalCents: 100000, Method: domain.MethodCash},
			{ID: "sa2", CreatedAt: last, TotalCents: 23400, Method: domain.MethodCard},
		},
	}}
	links := &fakeHTTPLinks{}

	clients := env.d.Clients
	clients.Clients = app.ClientService{
		Clients: &historyClients{}, Memberships: env.mems, Photos: env.store, Images: mimages{},
	}
	clients.History = app.ClientHistoryService{
		Clients: env.clients, Source: history, Memberships: env.mems, Settings: env.settings,
		Now: func() time.Time { return now },
	}
	env.d.Clients = clients

	saleSvc := env.d.Sales.Sales
	saleSvc.Clients = env.clients
	saleSvc.ClientLinks = links
	env.d.Sales = SaleHandler{Sales: saleSvc, Memberships: env.mems}

	env.r = gin.New()
	RegisterRoutes(env.r, env.d)
	return env, history, links
}

func seedClient(t *testing.T, env *posEnv, salonID string) domain.Client {
	t.Helper()
	// El fake guarda la ficha directo: el POST ya está probado en otros tests.
	c, err := domain.NewClientWithAllergies(salonID, "Lía Fernández", "11 5555-1234", "lia@mail.com", "nota", "nickel")
	if err != nil {
		t.Fatalf("ficha: %v", err)
	}
	if err := env.clients.Save(c); err != nil {
		t.Fatalf("guardar ficha: %v", err)
	}
	return c
}

func okErr(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("json de error inválido: %v (%s)", err, body)
	}
	return out.Error
}

// D5: roles en GET y PUT, las cinco claves obligatorias, el NUL, el tope de
// alergias y el aislamiento de tenant. Un 400 no escribe nada.
func TestHTTP_client_get_put(t *testing.T) {
	env, _, _ := newHistoryEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "fichas")
	base := "/api/v1/salons/" + salonID + "/clients"

	// Alta con alergias (campo nuevo del POST).
	w := doReq(env.r, "POST", base, `{"name":"Lía Fernández","phone":"11 5555-1234","email":"lia@mail.com","notes":"nota","allergies":"nickel"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	mustJSON(t, w.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" || created["allergies"] != "nickel" {
		t.Fatalf("la ficha creada debe traer allergies: %s", w.Body.String())
	}
	// El listado agrega alergias sin quitar nada.
	w = doReq(env.r, "GET", base, "", ownerTok)
	var list struct {
		Clients []map[string]any `json:"clients"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Clients) != 1 || list.Clients[0]["allergies"] != "nickel" || list.Clients[0]["notes"] != "nota" {
		t.Fatalf("listado con alergias: %s", w.Body.String())
	}

	// GET por id: cualquier miembro activo, incluido el rol cliente.
	one := base + "/" + id
	cliTok, cliID := env.register(t, "cli@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: cliID, SalonID: salonID, Role: domain.RoleClient})
	profTok, profID := env.register(t, "prof@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	for _, tok := range []string{ownerTok, cliTok, profTok} {
		if w := doReq(env.r, "GET", one, "", tok); w.Code != http.StatusOK {
			t.Fatalf("GET ficha: %d %s", w.Code, w.Body.String())
		}
	}
	if w := doReq(env.r, "GET", one, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
	stranger, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", one, "", stranger); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base+"/nope", "", ownerTok); w.Code != http.StatusNotFound || okErr(t, w.Body.Bytes()) != "Ficha no encontrada" {
		t.Fatalf("ficha inexistente: %d %s", w.Code, w.Body.String())
	}

	// PUT incompleto (falta allergies): 400 exacto y no escribe.
	w = doReq(env.r, "PUT", one, `{"name":"Cambio","phone":"555","email":"","notes":""}`, ownerTok)
	if w.Code != http.StatusBadRequest || okErr(t, w.Body.Bytes()) != "Faltan datos de la ficha: nombre, teléfono, email, notas y alergias" {
		t.Fatalf("PUT incompleto: %d %s", w.Code, w.Body.String())
	}
	// 500 caracteres entran; 501 (multibyte) es 400 exacto y no escribe.
	w = doReq(env.r, "PUT", one, `{"name":"Lía","phone":"555","email":"","notes":"","allergies":"`+strings.Repeat("a", 500)+`"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("500 caracteres: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "PUT", one, `{"name":"Cambio","phone":"555","email":"","notes":"","allergies":"`+strings.Repeat("á", 501)+`"}`, ownerTok)
	if w.Code != http.StatusBadRequest || okErr(t, w.Body.Bytes()) != "Las alergias no pueden superar los 500 caracteres" {
		t.Fatalf("501 caracteres: %d %s", w.Code, w.Body.String())
	}
	// Un NUL es 400, nunca 500.
	w = doReq(env.r, "PUT", one, `{"name":"Lía\u0000X","phone":"555","email":"","notes":"","allergies":""}`, ownerTok)
	if w.Code != http.StatusBadRequest || okErr(t, w.Body.Bytes()) != "La ficha tiene caracteres no válidos" {
		t.Fatalf("NUL: %d %s", w.Code, w.Body.String())
	}
	// Profesional no edita.
	if w := doReq(env.r, "PUT", one, `{"name":"X","phone":"","email":"","notes":"","allergies":""}`, profTok); w.Code != http.StatusForbidden ||
		okErr(t, w.Body.Bytes()) != "sin permiso" {
		t.Fatalf("profesional editando: %d %s", w.Code, w.Body.String())
	}
	// Nada de lo rechazado se escribió: sigue el PUT válido de 500.
	w = doReq(env.r, "GET", one, "", ownerTok)
	var after map[string]any
	mustJSON(t, w.Body.Bytes(), &after)
	if after["name"] != "Lía" || after["allergies"] != strings.Repeat("a", 500) {
		t.Fatalf("un 400 no debe escribir: %s", w.Body.String())
	}
	// PUT completo: reemplaza los cinco campos.
	w = doReq(env.r, "PUT", one, `{"name":"Lía G.","phone":"11 4444-0000","email":"LIA@mail.com","notes":"otra","allergies":"tinte"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT válido: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &after)
	if after["name"] != "Lía G." || after["email"] != "lia@mail.com" || after["allergies"] != "tinte" {
		t.Fatalf("PUT no aplicado: %s", w.Body.String())
	}

	// Aislamiento de tenant: la ficha de otro salón es 404 en GET, PUT y historial.
	otherTok, _ := env.register(t, "other@mail.com")
	otherSalon := env.salon(t, otherTok, "otro")
	for _, req := range []struct{ method, path, body string }{
		{"GET", "/api/v1/salons/" + otherSalon + "/clients/" + id, ""},
		{"PUT", "/api/v1/salons/" + otherSalon + "/clients/" + id, `{"name":"X","phone":"","email":"","notes":"","allergies":""}`},
		{"GET", "/api/v1/salons/" + otherSalon + "/clients/" + id + "/history", ""},
	} {
		w := doReq(env.r, req.method, req.path, req.body, otherTok)
		if w.Code != http.StatusNotFound || okErr(t, w.Body.Bytes()) != "Ficha no encontrada" {
			t.Fatalf("%s cross-tenant: %d %s", req.method, w.Code, w.Body.String())
		}
	}
}

// D6: cualquier miembro ve las visitas y los turnos; spend/sales solo quien
// puede ver la plata (para el profesional vienen en null, no en cero).
func TestHTTP_client_history_roles(t *testing.T) {
	env, _, _ := newHistoryEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "historial")
	_ = env.settings.SaveSettings(domain.SalonSettings{SalonID: salonID, Timezone: "America/Mexico_City"})
	c := seedClient(t, env, salonID)
	path := "/api/v1/salons/" + salonID + "/clients/" + c.ID + "/history"

	recepTok, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})
	profTok, profID := env.register(t, "prof@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	cliTok, cliID := env.register(t, "cli@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: cliID, SalonID: salonID, Role: domain.RoleClient})
	type historyResp struct {
		ClientID string         `json:"client_id"`
		Summary  map[string]any `json:"summary"`
		Bookings []map[string]any
		Spend    map[string]any `json:"spend"`
		Sales    []map[string]any
	}
	// owner y recepción ven el dinero.
	for _, tok := range []string{ownerTok, recepTok} {
		w := doReq(env.r, "GET", path, "", tok)
		if w.Code != http.StatusOK {
			t.Fatalf("historial: %d %s", w.Code, w.Body.String())
		}
		var out historyResp
		mustJSON(t, w.Body.Bytes(), &out)
		if out.ClientID != c.ID || out.Spend == nil || out.Spend["total_cents"] != float64(123400) || len(out.Sales) != 2 {
			t.Fatalf("debe ver el dinero: %s", w.Body.String())
		}
		if out.Summary["visits"] != float64(4) || out.Summary["last_visit"] != "2026-03-18" ||
			out.Summary["timezone"] != "America/Mexico_City" || out.Summary["next_visit"] == nil {
			t.Fatalf("resumen: %s", w.Body.String())
		}
		if len(out.Bookings) != 1 || out.Bookings[0]["service_name"] != "Corte" {
			t.Fatalf("turnos: %s", w.Body.String())
		}
	}
	// Profesional y rol cliente: visitas sí, dinero null.
	for _, tok := range []string{profTok, cliTok} {
		w := doReq(env.r, "GET", path, "", tok)
		if w.Code != http.StatusOK {
			t.Fatalf("historial sin dinero: %d %s", w.Code, w.Body.String())
		}
		var raw map[string]any
		mustJSON(t, w.Body.Bytes(), &raw)
		if v, ok := raw["spend"]; !ok || v != nil {
			t.Fatalf("spend debe ser null: %s", w.Body.String())
		}
		if v, ok := raw["sales"]; !ok || v != nil {
			t.Fatalf("sales debe ser null: %s", w.Body.String())
		}
		if raw["summary"].(map[string]any)["visits"] != float64(4) {
			t.Fatalf("las visitas se ven siempre: %s", w.Body.String())
		}
	}
	// Sin token 401; ajeno 403; ficha de otro salón 404.
	if w := doReq(env.r, "GET", path, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
	stranger, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", path, "", stranger); w.Code != http.StatusForbidden {
		t.Fatalf("ajeno: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/clients/nope/history", "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("ficha inexistente en historial: %d %s", w.Code, w.Body.String())
	}
}

// D3-c: el POS acepta client_id opcional; una ficha de otro salón es 400 con el
// texto exacto y no guarda la venta; la respuesta trae client_id.
func TestHTTP_pos_client_id(t *testing.T) {
	env, _, links := newHistoryEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "pos-ficha")
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	seedProfessional(env, salonID, "prof1")
	c := seedClient(t, env, salonID)
	base := "/api/v1/salons/" + salonID + "/sales"
	body := func(clientID, name string) string {
		return `{"professional_id":"prof1","client_name":"` + name + `","method":"cash","client_id":"` + clientID + `",` +
			`"items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`
	}

	// Con ficha y nombre vacío: se completa con el nombre de la ficha.
	w := doReq(env.r, "POST", base, body(c.ID, ""), ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta con ficha: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		SaleID   string `json:"sale_id"`
		ClientID string `json:"client_id"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.SaleID == "" || out.ClientID != c.ID {
		t.Fatalf("respuesta con client_id: %s", w.Body.String())
	}
	if links.sales != 1 {
		t.Fatalf("la venta debe quedar vinculada: %d", links.sales)
	}
	// Sin client_id: se comporta como siempre y la respuesta trae "".
	w = doReq(env.r, "POST", base, `{"professional_id":"prof1","client_name":"Ana","method":"card","items":[{"service_id":"`+svcID+`","qty":1,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta sin ficha: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.ClientID != "" {
		t.Fatalf("sin client_id la respuesta trae \"\": %s", w.Body.String())
	}

	// Ficha de otro salón: 400 «Ficha de clienta inválida» y NO se guarda.
	otherTok, otherID := env.register(t, "other@mail.com")
	otherSalon := env.salon(t, otherTok, "pos-otro")
	ajena, _ := domain.NewClient(otherSalon, "Ajena", "555", "", "")
	_ = env.clients.Save(ajena)
	_ = otherID
	antes := len(env.sales.all)
	w = doReq(env.r, "POST", base, body(ajena.ID, "X"), ownerTok)
	if w.Code != http.StatusBadRequest || okErr(t, w.Body.Bytes()) != "Ficha de clienta inválida" {
		t.Fatalf("ficha de otro salón: %d %s", w.Code, w.Body.String())
	}
	if len(env.sales.all) != antes {
		t.Fatalf("una ficha inválida no debe guardar la venta")
	}

	// Un fallo del vínculo no tumba la venta (best-effort).
	links.failSales = true
	w = doReq(env.r, "POST", base, body(c.ID, "Lía"), ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("fallo del vínculo: %d %s", w.Code, w.Body.String())
	}
	// R4-001 (revisión nativa): la respuesta dice el vínculo REAL de la venta, no el
	// que se pidió. Con el vínculo caído la venta quedó sin ficha: client_id "".
	var failed struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &failed); err != nil {
		t.Fatalf("respuesta: %v %s", err, w.Body.String())
	}
	if failed.ClientID != "" {
		t.Fatalf("el vínculo falló: client_id tenía que ser \"\" y fue %q", failed.ClientID)
	}
}
