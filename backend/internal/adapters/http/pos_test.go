package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de POS/catálogo con la misma semántica que pgstore: scoping por salón,
// ErrNotFound, upsert por id. Los handlers son finos, así que esto alcanza para
// probar contrato HTTP, permisos y mapeo de errores a status.

type msvc struct{ all []domain.Service }

func (f *msvc) Save(s domain.Service) error {
	for i := range f.all {
		if f.all[i].ID == s.ID {
			f.all[i] = s
			return nil
		}
	}
	f.all = append(f.all, s)
	return nil
}

func (f *msvc) FindByID(salonID, id string) (domain.Service, error) {
	for _, s := range f.all {
		if s.SalonID == salonID && s.ID == id {
			return s, nil
		}
	}
	return domain.Service{}, domain.ErrNotFound
}

func (f *msvc) List(salonID string, includeInactive bool) ([]domain.Service, error) {
	var out []domain.Service
	for _, s := range f.all {
		if s.SalonID != salonID || (!includeInactive && !s.Active) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

type msale struct {
	all []domain.Sale
	top []domain.TopService
}

func (f *msale) Save(s domain.Sale) error { f.all = append(f.all, s); return nil }
func (f *msale) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	for _, s := range f.all {
		if s.SalonID == salonID && s.ClientOpID != "" && s.ClientOpID == clientOpID {
			return s, nil
		}
	}
	return domain.Sale{}, domain.ErrNotFound
}

func (f *msale) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	var out []domain.Sale
	for _, s := range f.all {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *msale) SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error) {
	total := 0
	for _, s := range f.all {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		if method != "" && s.Method != method {
			continue
		}
		total += s.TotalCents
	}
	return total, nil
}

func (f *msale) TopServices(string, time.Time, time.Time, int) ([]domain.TopService, error) {
	return f.top, nil
}

type mclose struct{ all []domain.CashClose }

func (f *mclose) Save(c domain.CashClose) error { f.all = append(f.all, c); return nil }

func (f *mclose) Last(salonID string) (domain.CashClose, error) {
	var last domain.CashClose
	found := false
	for _, c := range f.all {
		if c.SalonID != salonID {
			continue
		}
		if !found || c.ClosedAt.After(last.ClosedAt) {
			last, found = c, true
		}
	}
	if !found {
		return domain.CashClose{}, domain.ErrNotFound
	}
	return last, nil
}

func (f *mclose) List(salonID string) ([]domain.CashClose, error) {
	var out []domain.CashClose
	for _, c := range f.all {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

var (
	_ ports.ServiceRepository   = (*msvc)(nil)
	_ ports.SaleRepository      = (*msale)(nil)
	_ ports.CashCloseRepository = (*mclose)(nil)
	_ ports.AgendaStats         = (*mb)(nil)
	_ ports.StaffCounter        = (*mm)(nil)
)

// posEnv es el router completo con fakes compartidos, para poder sembrar
// memberships y datos sin pasar por la DB.
type posEnv struct {
	r        *gin.Engine
	users    *mu
	salons   *ms
	mems     *mm
	services *msvc
	sales    *msale
	closes   *mclose
	bookings *mb
	notifs   *mnotif
	clients  *mclients
	store    *mstorage
	events   *mevents
	hub      *realtime.Hub
	// Ronda 2: configuración del salón, administración del equipo y firma de
	// las URLs de fotos.
	settings *msettings
	members  *mmembers
	profs    *mprof
	signer   ports.PhotoURLSigner
	// T12: email de la reserva pública (la confirmación viaja en goroutine,
	// por eso los fakes tienen mutex).
	mailer *mmailer
	emails *mbookingEmails
	// F2d1: horario semanal del salón y de cada persona.
	weekly *mweekly
	// F2d2: ausencias (vacaciones y días libres puntuales).
	timeOff *mtimeoff
	// F2e: tipos y unidades de equipo con capacidad.
	resources *mresources
	// F2g1: invitaciones de equipo (con su reloj para probar el vencimiento).
	inv *minvitations
	d   Deps
}

// deps devuelve lo mismo que monta newPOSEnv, para armar routers alternativos
// (con los middlewares de producción) sin duplicar el cableado.
func (e *posEnv) deps() Deps { return e.d }

func newPOSEnv() *posEnv {
	gin.SetMode(gin.TestMode)
	env := &posEnv{
		users:     &mu{},
		salons:    &ms{},
		mems:      &mm{},
		services:  &msvc{},
		sales:     &msale{},
		closes:    &mclose{},
		bookings:  &mb{},
		notifs:    &mnotif{},
		clients:   &mclients{},
		store:     &mstorage{},
		events:    &mevents{},
		settings:  &msettings{},
		mailer:    &mmailer{},
		emails:    &mbookingEmails{},
		weekly:    &mweekly{},
		timeOff:   &mtimeoff{},
		resources: &mresources{},
	}
	env.resources.bookings = env.bookings
	env.inv = &minvitations{rows: map[string]domain.Invitation{}, users: env.users, mems: env.mems}
	env.members = &mmembers{mems: env.mems, users: env.users}
	env.profs = &mprof{mems: env.mems}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	authSvc := app.AuthService{Users: env.users, Hasher: hasher, Tokens: tokens}
	tenSvc := app.TenantService{Salons: env.salons, Memberships: env.mems}
	notifSvc := app.NotificationService{Notifications: env.notifs, Salons: env.mems, Events: env.events}
	bookSvc := app.BookingService{Bookings: env.bookings, Memberships: env.mems, Events: env.events, Notifier: notifSvc}
	svcSvc := app.ServiceService{Services: env.services, Memberships: env.mems, Professionals: env.profs, Resources: env.resources}
	saleSvc := app.SaleService{Sales: env.sales, Services: env.services, Memberships: env.mems, Events: env.events}
	cashSvc := app.CashCloseService{CashCloses: env.closes, Sales: env.sales, Memberships: env.mems, Tx: &mCashTx{closes: env.closes, sales: env.sales}, Events: env.events, Notifier: notifSvc}
	dashSvc := app.DashboardService{Sales: env.sales, Agenda: env.bookings, Staff: env.mems, Memberships: env.mems}
	clientSvc := app.ClientService{Clients: env.clients, Memberships: env.mems, Photos: env.store, Images: mimages{}}
	meSvc := app.MeService{Users: env.users, Salons: env.salons, Members: env.mems}
	staffSvc := app.StaffService{Staff: env.mems, Memberships: env.mems}
	publicSvc := app.PublicSalonService{
		Salons: env.salons, Services: env.services,
		Settings: env.settings, Hours: env.weekly,
	}
	settingsSvc := app.SettingsService{Settings: env.settings, Memberships: env.mems}
	salonHoursSvc := app.SalonHoursService{Hours: env.weekly, Settings: env.settings, Memberships: env.mems}
	professionalHoursSvc := app.ProfessionalHoursService{Hours: env.weekly, Memberships: env.mems}
	timeOffSvc := app.TimeOffService{TimeOff: env.timeOff, Bookings: env.bookings, Memberships: env.mems}
	// F2d3: cubrir una ausencia (reasignar los turnos de quien falta).
	coverageSvc := app.CoverageService{
		Bookings: env.bookings, Memberships: env.mems, Staff: env.mems,
		Professionals: env.profs, Hours: env.weekly, TimeOff: env.timeOff,
		Services: env.services, Settings: env.settings, Salons: env.salons,
		Tx: &mBookingTx{bookings: env.bookings}, Events: env.events, Notifier: notifSvc,
		Mailer: env.mailer, Emails: env.emails,
	}
	memberSvc := app.MemberService{
		Members: env.members, Memberships: env.mems, Users: env.users,
		Professionals: env.profs, Services: env.services,
	}
	// F2g1: invitaciones de equipo. El reloj sale del fake para poder vencer una
	// invitación sin esperar los 7 días.
	invitationSvc := app.InvitationService{
		Invitations: env.inv, Tx: env.inv, Memberships: env.mems, Users: env.users,
		Salons: env.salons, Settings: env.settings, Mailer: env.mailer,
		AppURL: "http://localhost:8081", Now: env.inv.now,
	}
	invitationPublicSvc := app.InvitationPublicService{
		Invitations: env.inv, Tx: env.inv, Users: env.users, Salons: env.salons,
		Hasher: hasher, Tokens: tokens, Now: env.inv.now,
	}
	availabilitySvc := app.AvailabilityService{
		Salons: env.salons, Services: env.services, Settings: env.settings,
		Staff: env.mems, Bookings: env.bookings, Professionals: env.profs, Hours: env.weekly,
		TimeOff: env.timeOff,
	}
	publicBookingSvc := app.PublicBookingService{
		Salons: env.salons, Services: env.services, Settings: env.settings,
		Staff: env.mems, Bookings: env.bookings, Professionals: env.profs, Hours: env.weekly,
		TimeOff: env.timeOff,
		Events:  env.events, Notifier: notifSvc, Mailer: env.mailer, Emails: env.emails,
	}
	env.signer = security.NewPhotoSigner("test-secret")
	hub := realtime.NewHub(tokens, env.mems, realtime.HubOrigins("http://localhost:5173"))
	env.hub = hub

	env.d = Deps{
		Auth:          AuthHandler{Auth: authSvc},
		Tenants:       TenantsHandler{Tenants: tenSvc, Memberships: env.mems},
		Bookings:      BookingHandler{Bookings: bookSvc, Memberships: env.mems},
		Services:      ServiceHandler{Services: svcSvc, Memberships: env.mems},
		Sales:         SaleHandler{Sales: saleSvc, Memberships: env.mems},
		CashCloses:    CashCloseHandler{CashCloses: cashSvc, Memberships: env.mems},
		Dashboard:     DashboardHandler{Dashboard: dashSvc, Memberships: env.mems},
		Notifications: NotificationHandler{Notifications: notifSvc, Memberships: env.mems},
		Clients:       ClientHandler{Clients: clientSvc, Memberships: env.mems, Photos: env.signer},
		Me:            MeHandler{Me: meSvc},
		Staff:         StaffHandler{Staff: staffSvc, Memberships: env.mems},
		Public:        PublicHandler{Public: publicSvc, Avail: availabilitySvc, Bookings: publicBookingSvc},
		Settings:      SettingsHandler{Settings: settingsSvc, Memberships: env.mems},
		Members:       MemberHandler{Members: memberSvc, Memberships: env.mems},
		Hours: HoursHandler{
			Salon: salonHoursSvc, Professional: professionalHoursSvc, Memberships: env.mems,
		},
		TimeOff:     TimeOffHandler{TimeOff: timeOffSvc, Memberships: env.mems},
		Absences:    AbsenceHandler{Coverage: coverageSvc, Memberships: env.mems},
		Resources:   ResourceHandler{Resources: app.ResourceService{Resources: env.resources, Services: env.services, Memberships: env.mems}, Memberships: env.mems},
		Invitations: InvitationHandler{Invitations: invitationSvc, Public: invitationPublicSvc, Memberships: env.mems},
		WS:          hub.ServeWS,
		TokenIssuer: tokens,
	}
	env.r = gin.New()
	RegisterRoutes(env.r, env.d)
	return env
}

// mCashTx es la transacción con lock (B-09) sobre los fakes: en los tests HTTP
// no hay Postgres, pero el caso de uso recorre el mismo camino.
type mCashTx struct {
	closes *mclose
	sales  *msale
}

type mCashTxView struct{ parent *mCashTx }

func (v mCashTxView) Save(c domain.CashClose) error { return v.parent.closes.Save(c) }
func (v mCashTxView) Last(salonID string) (domain.CashClose, error) {
	return v.parent.closes.Last(salonID)
}
func (v mCashTxView) List(salonID string) ([]domain.CashClose, error) {
	return v.parent.closes.List(salonID)
}
func (v mCashTxView) SumCashCents(salonID string, from, to time.Time) (int, error) {
	return v.parent.sales.SumCents(salonID, from, to, domain.MethodCash)
}

func (t *mCashTx) InSalonTx(salonID string, fn func(ports.CashCloseTx) error) error {
	return fn(mCashTxView{parent: t})
}

var _ ports.SalonTxRunner = (*mCashTx)(nil)

// seedProfessional hace que userID atienda en el salón: el professional_id de
// una venta o reserva tiene que ser personal del salón (B-12).
func seedProfessional(env *posEnv, salonID, userID string) {
	// Attends=true como lo deja domain.NewMembership: un profesional atiende
	// siempre, y la marca tiene que reflejarlo también en la fila.
	_ = env.mems.Save(domain.Membership{
		UserID: userID, SalonID: salonID, Role: domain.RoleProfessional, Attends: true,
	})
}

// register registra+loguea un usuario y devuelve (token, userID).
func (e *posEnv) register(t *testing.T, email string) (string, string) {
	t.Helper()
	if w := doReq(e.r, "POST", "/api/v1/auth/register", `{"name":"`+email+`","email":"`+email+`","password":"secreta123"}`, ""); w.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, w.Code, w.Body.String())
	}
	w := doReq(e.r, "POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"secreta123"}`, "")
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login %s: %s", email, w.Body.String())
	}
	u, ok := e.users.byEmail[email]
	if !ok {
		t.Fatalf("usuario %s no quedó registrado", email)
	}
	return lr.Token, u.ID
}

// salon crea un salón con el token dado y devuelve su id.
func (e *posEnv) salon(t *testing.T, token, slug string) string {
	t.Helper()
	w := doReq(e.r, "POST", "/api/v1/salons", `{"name":"Salón `+slug+`","slug":"`+slug+`"}`, token)
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("crear salón %s: %d %s", slug, w.Code, w.Body.String())
	}
	return out.ID
}

func mustJSON(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("json inválido: %v (%s)", err, body)
	}
}

// --- Servicios ---

func TestHTTP_services(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "servicios")
	base := "/api/v1/salons/" + salonID + "/services"

	// Crear: 201 con la entidad completa (el frontend necesita el id a mano).
	w := doReq(env.r, "POST", base, `{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear servicio: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID            string `json:"id"`
		SalonID       string `json:"salon_id"`
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
		Active        bool   `json:"active"`
	}
	mustJSON(t, w.Body.Bytes(), &created)
	if created.ID == "" || created.SalonID != salonID || created.Name != "Corte" ||
		created.DurationMin != 45 || created.PriceCents != 150000 || created.CommissionPct != 20 || !created.Active {
		t.Fatalf("respuesta de creación incompleta: %s", w.Body.String())
	}

	// Duplicado activo (mismo nombre, otra caja): 409.
	if w := doReq(env.r, "POST", base, `{"name":"corte","duration_min":30,"price_cents":90000,"commission_pct":10}`, ownerTok); w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	// Cuerpo inválido: 400 (dos sabores: tipo malo y regla de dominio).
	if w := doReq(env.r, "POST", base, `{"name":123}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("json con tipo malo debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "POST", base, `{"name":"","duration_min":0}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("servicio inválido debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// No-miembro: 403 (ni leer ni escribir).
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "POST", base, `{"name":"Golpe","duration_min":30,"price_cents":1,"commission_pct":0}`, strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro leyendo debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Profesional miembro: lee sí, escribe no.
	profTok, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := doReq(env.r, "POST", base, `{"name":"Golpe","duration_min":30,"price_cents":1,"commission_pct":0}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional creando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", profTok); w.Code != http.StatusOK {
		t.Fatalf("profesional leyendo: %d %s", w.Code, w.Body.String())
	}

	// Actualizar: 200 y refleja los cambios.
	put := base + "/" + created.ID
	w = doReq(env.r, "PUT", put, `{"name":"Corte y brushing","duration_min":60,"price_cents":180000,"commission_pct":35,"active":false}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("actualizar: %d %s", w.Code, w.Body.String())
	}
	var updated struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
		Active        bool   `json:"active"`
	}
	mustJSON(t, w.Body.Bytes(), &updated)
	if updated.ID != created.ID || updated.Name != "Corte y brushing" || updated.DurationMin != 60 ||
		updated.PriceCents != 180000 || updated.CommissionPct != 35 || updated.Active {
		t.Fatalf("no aplicó el update: %s", w.Body.String())
	}
	// Desactivado: fuera del listado por defecto, adentro con include_inactive.
	var list struct {
		Services []map[string]any `json:"services"`
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Services) != 0 {
		t.Fatalf("sin inactivos esperaba [], dio %s", w.Body.String())
	}
	w = doReq(env.r, "GET", base+"?include_inactive=true", "", ownerTok)
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Services) != 1 {
		t.Fatalf("con inactivos esperaba 1, dio %s", w.Body.String())
	}
	// Servicio inexistente: 404. Profesional editando: 403.
	if w := doReq(env.r, "PUT", base+"/no-existe", `{"name":"X","duration_min":30,"price_cents":1,"commission_pct":0}`, ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PUT", put, `{"name":"X","duration_min":30,"price_cents":1,"commission_pct":0}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional editando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Sin token: 401.
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// --- Ventas ---

func (e *posEnv) seedService(t *testing.T, token, salonID, name string, price int) string {
	t.Helper()
	body := `{"name":"` + name + `","duration_min":45,"price_cents":` + itoa(price) + `,"commission_pct":20}`
	w := doReq(e.r, "POST", "/api/v1/salons/"+salonID+"/services", body, token)
	var out struct {
		ID string `json:"id"`
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("seed service %s: %d %s", name, w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &out)
	return out.ID
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestHTTP_sales(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "pos")
	base := "/api/v1/salons/" + salonID + "/sales"
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)

	seedProfessional(env, salonID, "prof1")

	// El cliente manda su propio total: debe ser ignorado y recalculado.
	body := `{"professional_id":"prof1","client_name":"Lía","method":"cash",` +
		`"total":1,"total_cents":1,` +
		`"items":[{"service_id":"` + svcID + `","qty":2,"unit_price_cents":150000}]}`
	w := doReq(env.r, "POST", base, body, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("registrar venta: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		SaleID string `json:"sale_id"`
		Total  int    `json:"total"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.SaleID == "" || out.Total != 300000 {
		t.Fatalf("total del servidor = 300000, dio %s", w.Body.String())
	}

	// Ventas inválidas: 400 (sin ítems, qty 0, método desconocido, json roto).
	for _, bad := range []string{
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[]}`,
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"` + svcID + `","qty":0,"unit_price_cents":150000}]}`,
		`{"professional_id":"prof1","client_name":"Lía","method":"bitcoin","items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`,
		`{"professional_id":"prof1","client_name":"","method":"cash","items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`,
		`no-json`,
	} {
		if w := doReq(env.r, "POST", base, bad, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("venta inválida %s debe dar 400, dio %d %s", bad, w.Code, w.Body.String())
		}
	}
	// Servicio inexistente: 400 (no 500 ni venta huérfana).
	if w := doReq(env.r, "POST", base, `{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"no-existe","qty":1,"unit_price_cents":1}]}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("servicio inexistente debe dar 400, dio %d %s", w.Code, w.Body.String())
	}

	// Permisos: no-miembro 403, profesional 403.
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "POST", base, body, strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	profTok, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := doReq(env.r, "POST", base, body, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional vendiendo debe dar 403, dio %d %s", w.Code, w.Body.String())
	}

	// Listado: trae la venta con sus ítems y el total en centavos.
	w = doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar ventas: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Sales []struct {
			ID             string `json:"id"`
			SalonID        string `json:"salon_id"`
			ProfessionalID string `json:"professional_id"`
			ClientName     string `json:"client_name"`
			TotalCents     int    `json:"total_cents"`
			Method         string `json:"method"`
			CreatedAt      string `json:"created_at"`
			Items          []struct {
				ServiceID      string `json:"service_id"`
				Qty            int    `json:"qty"`
				UnitPriceCents int    `json:"unit_price_cents"`
			} `json:"items"`
		} `json:"sales"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Sales) != 1 {
		t.Fatalf("esperaba 1 venta, dio %s", w.Body.String())
	}
	s := list.Sales[0]
	if s.ID != out.SaleID || s.SalonID != salonID || s.ProfessionalID != "prof1" || s.ClientName != "Lía" ||
		s.TotalCents != 300000 || s.Method != "cash" || s.CreatedAt == "" {
		t.Fatalf("venta mal serializada: %s", w.Body.String())
	}
	if len(s.Items) != 1 || s.Items[0].ServiceID != svcID || s.Items[0].Qty != 2 || s.Items[0].UnitPriceCents != 150000 {
		t.Fatalf("ítems mal serializados: %s", w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// Ventana sin ventas: [].
	w = doReq(env.r, "GET", base+"?from=2020-01-01T00:00:00Z&to=2020-01-02T00:00:00Z", "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sales":[]`) {
		t.Fatalf("ventana vacía debe dar []: %d %s", w.Code, w.Body.String())
	}
}

// --- Cierre de caja ---

func TestHTTP_cash_closes(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "caja")
	base := "/api/v1/salons/" + salonID + "/cash-closes"
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	seedProfessional(env, salonID, "prof1")

	// Una venta en efectivo de 300000 deja eso en el cajón.
	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/sales",
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"`+svcID+`","qty":2,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta previa: %d %s", w.Code, w.Body.String())
	}

	// Recepción no cierra caja (solo owner/admin): 403.
	recepTok, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})
	if w := doReq(env.r, "POST", base, `{"counted_cents":300000,"note":"turno mañana"}`, recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción cerrando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}

	// Cierre con faltante: 201 {close_id, expected_cash}.
	w = doReq(env.r, "POST", base, `{"counted_cents":298000,"note":"faltó vuelto"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("cerrar caja: %d %s", w.Code, w.Body.String())
	}
	var closed struct {
		CloseID      string `json:"close_id"`
		ExpectedCash int    `json:"expected_cash"`
	}
	mustJSON(t, w.Body.Bytes(), &closed)
	if closed.CloseID == "" || closed.ExpectedCash != 300000 {
		t.Fatalf("esperaba expected_cash=300000, dio %s", w.Body.String())
	}

	// Contado negativo: 400.
	if w := doReq(env.r, "POST", base, `{"counted_cents":-1}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("contado negativo debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// JSON roto: 400.
	if w := doReq(env.r, "POST", base, `{"counted_cents":"dos"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("json roto debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Cerrar dos veces seguidas: el segundo turno arranca sin efectivo previo.
	w = doReq(env.r, "POST", base, `{"counted_cents":0}`, ownerTok)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"expected_cash":0`) {
		t.Fatalf("segundo cierre debe arrancar en 0: %d %s", w.Code, w.Body.String())
	}

	// Listado con la diferencia contable.
	w = doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar cierres: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Closes []struct {
			ID              string `json:"id"`
			SalonID         string `json:"salon_id"`
			OpenedAt        string `json:"opened_at"`
			ClosedAt        string `json:"closed_at"`
			SystemCashCents int    `json:"system_cash_cents"`
			CountedCents    int    `json:"counted_cents"`
			DifferenceCents int    `json:"difference_cents"`
			Note            string `json:"note"`
		} `json:"closes"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Closes) != 2 {
		t.Fatalf("esperaba 2 cierres, dio %s", w.Body.String())
	}
	var sawShortfall bool
	for _, c := range list.Closes {
		if c.SalonID != salonID || c.OpenedAt == "" || c.ClosedAt == "" {
			t.Fatalf("cierre mal serializado: %s", w.Body.String())
		}
		if c.SystemCashCents == 300000 && c.CountedCents == 298000 && c.DifferenceCents == -2000 && c.Note == "faltó vuelto" {
			sawShortfall = true
		}
	}
	if !sawShortfall {
		t.Fatalf("falta el cierre con faltante -2000: %s", w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// --- Dashboard ---

func TestHTTP_dashboard(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "panel")
	base := "/api/v1/salons/" + salonID + "/dashboard"
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)

	// Un profesional en el salón = 720 minutos de capacidad del día.
	_, profID := env.register(t, "profe@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	env.bookings.all = append(env.bookings.all,
		domain.Booking{SalonID: salonID, ProfessionalID: profID, StartsAt: dayStart.Add(9 * time.Hour), EndsAt: dayStart.Add(11 * time.Hour), Status: domain.BookingBooked},
		domain.Booking{SalonID: salonID, ProfessionalID: profID, StartsAt: dayStart.Add(15 * time.Hour), EndsAt: dayStart.Add(16 * time.Hour), Status: domain.BookingCancelled},
	)
	env.sales.top = []domain.TopService{{ServiceID: svcID, Name: "Corte", Qty: 2, RevenueCents: 300000}}

	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/sales",
		`{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","items":[{"service_id":"`+svcID+`","qty":2,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta previa: %d %s", w.Code, w.Body.String())
	}

	w = doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var d struct {
		RevenueToday      int     `json:"revenue_today"`
		AppointmentsToday int     `json:"appointments_today"`
		OccupancyRate     float64 `json:"occupancy_rate"`
		TopServices       []struct {
			ServiceID    string `json:"service_id"`
			Name         string `json:"name"`
			Qty          int    `json:"qty"`
			RevenueCents int    `json:"revenue_cents"`
		} `json:"top_services"`
	}
	mustJSON(t, w.Body.Bytes(), &d)
	if d.RevenueToday != 300000 {
		t.Fatalf("revenue_today = 300000, dio %d", d.RevenueToday)
	}
	if d.AppointmentsToday != 1 {
		t.Fatalf("appointments_today = 1 (la cancelada no cuenta), dio %d", d.AppointmentsToday)
	}
	// 120 min de 720 de un profesional = 0.1667
	if d.OccupancyRate != 0.1667 {
		t.Fatalf("occupancy_rate = 120/720, dio %v", d.OccupancyRate)
	}
	if len(d.TopServices) != 1 || d.TopServices[0].ServiceID != svcID || d.TopServices[0].Qty != 2 || d.TopServices[0].RevenueCents != 300000 {
		t.Fatalf("top_services: %s", w.Body.String())
	}

	// No-miembro: 403. Sin token: 401.
	strangerTok, _ := env.register(t, "stranger@mail.com")
	if w := doReq(env.r, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// La agenda de hoy se lee con el mismo calendario: sin token 401, con
	// token de miembro 200 (regresión del adaptador de reservas intacta).
	day := dayStart.Format(time.RFC3339)
	if w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/bookings?from="+day+"&to="+dayStart.Add(24*time.Hour).Format(time.RFC3339), "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("bookings sigue funcionando: %d %s", w.Code, w.Body.String())
	}
}

// TestHTTP_sale_replay: reintento offline con el mismo client_op_id.
// El segundo POST debe devolver 201 con el MISMO sale_id (no 409, no duplicado).
func TestHTTP_sale_replay(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "replay")
	base := "/api/v1/salons/" + salonID + "/sales"
	svcID := env.seedService(t, ownerTok, salonID, "Corte", 150000)
	seedProfessional(env, salonID, "prof1")

	body := `{"professional_id":"prof1","client_name":"Lía","method":"cash","client_op_id":"op-abc",` +
		`"items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`
	w1 := doReq(env.r, "POST", base, body, ownerTok)
	w2 := doReq(env.r, "POST", base, body, ownerTok)
	if w1.Code != http.StatusCreated || w2.Code != http.StatusCreated {
		t.Fatalf("reintento debe dar 201/201, dio %d/%d (%s | %s)", w1.Code, w2.Code, w1.Body.String(), w2.Body.String())
	}
	var o1, o2 struct {
		SaleID string `json:"sale_id"`
	}
	mustJSON(t, w1.Body.Bytes(), &o1)
	mustJSON(t, w2.Body.Bytes(), &o2)
	if o1.SaleID == "" || o1.SaleID != o2.SaleID {
		t.Fatalf("el replay debe devolver el mismo sale_id: %q vs %q", o1.SaleID, o2.SaleID)
	}
	w := doReq(env.r, "GET", base, "", ownerTok)
	var list struct {
		Sales []struct {
			ID string `json:"id"`
		} `json:"sales"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Sales) != 1 {
		t.Fatalf("debe existir una sola venta, hay %d", len(list.Sales))
	}
}
