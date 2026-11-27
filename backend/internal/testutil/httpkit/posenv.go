package httpkit

import (
	"encoding/json"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/realtime"
	"salon-saas/internal/auth"
	authhttpapi "salon-saas/internal/auth/httpapi"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/bookings"
	"salon-saas/internal/clients"
	clientshttpapi "salon-saas/internal/clients/httpapi"
	"salon-saas/internal/domain"
	"salon-saas/internal/notifications"
	notificationshttpapi "salon-saas/internal/notifications/httpapi"
	"salon-saas/internal/ports"
	"salon-saas/internal/pos"
	poshttpapi "salon-saas/internal/pos/httpapi"
	"salon-saas/internal/reviews"
	reviewshttpapi "salon-saas/internal/reviews/httpapi"
	"salon-saas/internal/schedules"
	scheduleshttpapi "salon-saas/internal/schedules/httpapi"
	"salon-saas/internal/settings"
	settingshttpapi "salon-saas/internal/settings/httpapi"
	"salon-saas/internal/staffing"
	staffinghttpapi "salon-saas/internal/staffing/httpapi"
	"salon-saas/internal/team"
	teamhttpapi "salon-saas/internal/team/httpapi"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	bookingshttpapi "salon-saas/internal/bookings/httpapi"
)

type Services struct{ All []domain.Service }

func (f *Services) Save(s domain.Service) error {
	for i := range f.All {
		if f.All[i].ID == s.ID {
			f.All[i] = s
			return nil
		}
	}
	f.All = append(f.All, s)
	return nil
}

func (f *Services) FindByID(salonID, id string) (domain.Service, error) {
	for _, s := range f.All {
		if s.SalonID == salonID && s.ID == id {
			return s, nil
		}
	}
	return domain.Service{}, domain.ErrNotFound
}

func (f *Services) List(salonID string, includeInactive bool) ([]domain.Service, error) {
	var out []domain.Service
	for _, s := range f.All {
		if s.SalonID != salonID || (!includeInactive && !s.Active) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

type Sales struct {
	All []domain.Sale
	Top []domain.TopService
}

func (f *Sales) Save(s domain.Sale) error { f.All = append(f.All, s); return nil }

func (f *Sales) FindByClientOpID(salonID, clientOpID string) (domain.Sale, error) {
	for _, s := range f.All {
		if s.SalonID == salonID && s.ClientOpID != "" && s.ClientOpID == clientOpID {
			return s, nil
		}
	}
	return domain.Sale{}, domain.ErrNotFound
}

func (f *Sales) ListRange(salonID string, from, to time.Time) ([]domain.Sale, error) {
	var out []domain.Sale
	for _, s := range f.All {
		if s.SalonID != salonID || s.CreatedAt.Before(from) || !s.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *Sales) SumCents(salonID string, from, to time.Time, method domain.PaymentMethod) (int, error) {
	total := 0
	for _, s := range f.All {
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

func (f *Sales) TopServices(string, time.Time, time.Time, int) ([]domain.TopService, error) {
	return f.Top, nil
}

type Closes struct{ All []domain.CashClose }

func (f *Closes) Save(c domain.CashClose) error { f.All = append(f.All, c); return nil }

func (f *Closes) Last(salonID string) (domain.CashClose, error) {
	var last domain.CashClose
	found := false
	for _, c := range f.All {
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

func (f *Closes) List(salonID string) ([]domain.CashClose, error) {
	var out []domain.CashClose
	for _, c := range f.All {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

var (
	_ ports.ServiceRepository   = (*Services)(nil)
	_ ports.SaleRepository      = (*Sales)(nil)
	_ ports.CashCloseRepository = (*Closes)(nil)
	_ ports.AgendaStats         = (*Bookings)(nil)
	_ ports.StaffCounter        = (*Memberships)(nil)
)

// Env es el router completo con fakes compartidos, para poder sembrar
// memberships y datos sin pasar por la DB.
type Env struct {
	Router        *gin.Engine
	Users         *Users
	Salons        *Salons
	Mems          *Memberships
	Services      *Services
	Sales         *Sales
	Closes        *Closes
	Bookings      *Bookings
	Notifications *Notifications
	Clients       *Clients
	Storage       *Storage
	Events        *Events
	Hub           *realtime.Hub
	// Ronda 2: configuración del salón, administración del equipo y firma de
	// las URLs de fotos.
	Settings      *Settings
	MemberDir     *MemberDir
	Professionals *Professionals
	Signer        ports.PhotoURLSigner
	// T12: email de la reserva pública (la confirmación viaja en goroutine,
	// por eso los fakes tienen mutex).
	Mailer        *Mailer
	BookingEmails *BookingEmails
	// F2d1: horario semanal del salón y de cada persona.
	Weekly *Weekly
	// F2d2: ausencias (vacaciones y días libres puntuales).
	TimeOff *TimeOff
	// F2e: tipos y unidades de equipo con capacidad.
	Resources *Resources
	// F2g1: invitaciones de equipo (con su reloj para probar el vencimiento).
	Invitations *Invitations
	// F13: reseñas posvisita (configuración, lista, enlace y pedidos).
	Reviews *Reviews
	Deps    httpapi.Deps
}

func NewPOSEnv() *Env {
	gin.SetMode(gin.TestMode)
	env := &Env{
		Users:         &Users{},
		Salons:        &Salons{},
		Mems:          &Memberships{},
		Services:      &Services{},
		Sales:         &Sales{},
		Closes:        &Closes{},
		Bookings:      &Bookings{},
		Notifications: &Notifications{},
		Clients:       &Clients{},
		Storage:       &Storage{},
		Events:        &Events{},
		Settings:      &Settings{},
		Mailer:        &Mailer{},
		BookingEmails: &BookingEmails{},
		Weekly:        &Weekly{},
		TimeOff:       &TimeOff{},
		Resources:     &Resources{},
		Reviews:       NewReviews(),
	}
	env.Resources.Bookings = env.Bookings
	env.Invitations = &Invitations{Rows: map[string]domain.Invitation{}, Users: env.Users, Mems: env.Mems}
	reviewsSvc := reviews.Service{
		Reviews: env.Reviews, Requests: env.Reviews, Memberships: env.Mems,
		Mailer: env.Mailer, AppURL: "http://localhost:8081",
	}
	env.MemberDir = &MemberDir{
		Mems: env.Mems, Users: env.Users, Bookings: env.Bookings, Invitations: env.Invitations,
	}
	env.Professionals = &Professionals{Mems: env.Mems}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	authSvc := auth.AuthService{Users: env.Users, Hasher: hasher, Tokens: tokens}
	tenSvc := team.TenantService{Salons: env.Salons, Memberships: env.Mems}
	notifSvc := notifications.NotificationService{Notifications: env.Notifications, Salons: env.Mems, Events: env.Events}
	bookSvc := bookings.BookingService{Bookings: env.Bookings, Memberships: env.Mems, Events: env.Events, Notifier: notifSvc}
	svcSvc := staffing.ServiceService{Services: env.Services, Memberships: env.Mems, Professionals: env.Professionals, Resources: env.Resources}
	saleSvc := pos.SaleService{Sales: env.Sales, Services: env.Services, Memberships: env.Mems, Events: env.Events}
	cashSvc := pos.CashCloseService{CashCloses: env.Closes, Sales: env.Sales, Memberships: env.Mems, Tx: &CashTx{Closes: env.Closes, Sales: env.Sales}, Events: env.Events, Notifier: notifSvc}
	dashSvc := pos.DashboardService{Sales: env.Sales, Agenda: env.Bookings, Staff: env.Mems, Memberships: env.Mems}
	clientSvc := clients.ClientService{Clients: env.Clients, Memberships: env.Mems, Photos: env.Storage, Images: Images{}}
	meSvc := auth.MeService{Users: env.Users, Salons: env.Salons, Members: env.Mems}
	staffSvc := team.StaffService{Staff: env.Mems, Memberships: env.Mems}
	publicSvc := settings.PublicSalonService{
		Salons: env.Salons, Services: env.Services,
		Settings: env.Settings, Hours: env.Weekly,
		// F13/D7: la sección aditiva de reseñas de la landing. Va el CASO DE USO
		// (no el doble) porque el promedio con una decimal se redondea ahí, igual
		// que en el binario.
		Reviews: reviewsSvc,
	}
	settingsSvc := settings.SettingsService{Settings: env.Settings, Memberships: env.Mems}
	salonHoursSvc := schedules.SalonHoursService{Hours: env.Weekly, Settings: env.Settings, Memberships: env.Mems}
	professionalHoursSvc := schedules.ProfessionalHoursService{Hours: env.Weekly, Memberships: env.Mems}
	timeOffSvc := schedules.TimeOffService{TimeOff: env.TimeOff, Bookings: env.Bookings, Memberships: env.Mems}
	// F2d3: cubrir una ausencia (reasignar los turnos de quien falta).
	coverageSvc := schedules.CoverageService{
		Bookings: env.Bookings, Memberships: env.Mems, Staff: env.Mems,
		Professionals: env.Professionals, Hours: env.Weekly, TimeOff: env.TimeOff,
		Services: env.Services, Settings: env.Settings, Salons: env.Salons,
		Tx: &BookingTx{Bookings: env.Bookings}, Events: env.Events, Notifier: notifSvc,
		Mailer: env.Mailer, Emails: env.BookingEmails,
	}
	memberSvc := team.MemberService{
		Members: env.MemberDir, Memberships: env.Mems, Users: env.Users,
		Professionals: env.Professionals, Services: env.Services,
		// F2g2: la baja corre en la transacción (desactivar + revocar las
		// invitaciones pendientes que emitió esa persona).
		Tx: env.MemberDir,
	}
	// F2g1: invitaciones de equipo. El reloj sale del fake para poder vencer una
	// invitación sin esperar los 7 días.
	invitationSvc := team.InvitationService{
		Invitations: env.Invitations, Tx: env.Invitations, Memberships: env.Mems, Users: env.Users,
		Salons: env.Salons, Settings: env.Settings, Mailer: env.Mailer,
		AppURL: "http://localhost:8081", Now: env.Invitations.now,
	}
	invitationPublicSvc := team.InvitationPublicService{
		Invitations: env.Invitations, Tx: env.Invitations, Users: env.Users, Salons: env.Salons,
		Hasher: hasher, Tokens: tokens, Now: env.Invitations.now,
	}
	availabilitySvc := bookings.AvailabilityService{
		Salons: env.Salons, Services: env.Services, Settings: env.Settings,
		Staff: env.Mems, Bookings: env.Bookings, Professionals: env.Professionals, Hours: env.Weekly,
		TimeOff: env.TimeOff,
	}
	publicBookingSvc := bookings.PublicBookingService{
		Salons: env.Salons, Services: env.Services, Settings: env.Settings,
		Staff: env.Mems, Bookings: env.Bookings, Professionals: env.Professionals, Hours: env.Weekly,
		TimeOff: env.TimeOff,
		Events:  env.Events, Notifier: notifSvc, Mailer: env.Mailer, Emails: env.BookingEmails,
	}
	env.Signer = security.NewPhotoSigner("test-secret")
	hub := realtime.NewHub(tokens, env.Mems, realtime.HubOrigins("http://localhost:5173"))
	env.Hub = hub

	env.Deps = httpapi.Deps{
		Auth:          authhttpapi.AuthHandler{Auth: authSvc},
		Tenants:       teamhttpapi.TenantsHandler{Tenants: tenSvc, Memberships: env.Mems},
		Bookings:      bookingshttpapi.BookingHandler{Bookings: bookSvc, Memberships: env.Mems},
		Services:      staffinghttpapi.ServiceHandler{Services: svcSvc, Memberships: env.Mems},
		Sales:         poshttpapi.SaleHandler{Sales: saleSvc, Memberships: env.Mems},
		CashCloses:    poshttpapi.CashCloseHandler{CashCloses: cashSvc, Memberships: env.Mems},
		Dashboard:     poshttpapi.DashboardHandler{Dashboard: dashSvc, Memberships: env.Mems},
		Notifications: notificationshttpapi.NotificationHandler{Notifications: notifSvc, Memberships: env.Mems},
		Clients:       clientshttpapi.ClientHandler{Clients: clientSvc, Memberships: env.Mems, Photos: env.Signer},
		Me:            authhttpapi.MeHandler{Me: meSvc},
		Staff:         teamhttpapi.StaffHandler{Staff: staffSvc, Memberships: env.Mems},
		Public:        bookingshttpapi.PublicHandler{Avail: availabilitySvc, Bookings: publicBookingSvc},
		PublicSalon:   settingshttpapi.PublicSalonHandler{Public: publicSvc},
		Settings:      settingshttpapi.SettingsHandler{Settings: settingsSvc, Memberships: env.Mems},
		Members:       teamhttpapi.MemberHandler{Members: memberSvc, Memberships: env.Mems},
		Hours: scheduleshttpapi.HoursHandler{
			Salon: salonHoursSvc, Professional: professionalHoursSvc, Memberships: env.Mems,
		},
		TimeOff:     scheduleshttpapi.TimeOffHandler{TimeOff: timeOffSvc, Memberships: env.Mems},
		Absences:    scheduleshttpapi.AbsenceHandler{Coverage: coverageSvc, Memberships: env.Mems},
		Resources:   staffinghttpapi.ResourceHandler{Resources: staffing.ResourceService{Resources: env.Resources, Services: env.Services, Memberships: env.Mems}, Memberships: env.Mems},
		Invitations: teamhttpapi.InvitationHandler{Invitations: invitationSvc, Public: invitationPublicSvc, Memberships: env.Mems},
		// F13: reseñas posvisita (enlace público y panel). El límite por IP no
		// se cablea en los tests: todas las peticiones vienen de la misma IP.
		Reviews:     reviewshttpapi.ReviewHandler{Service: reviewsSvc, Memberships: env.Mems},
		WS:          hub.ServeWS,
		TokenIssuer: tokens,
	}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)
	return env
}

// CashTx es la transacción con lock (B-09) sobre los fakes: en los tests HTTP
// no hay Postgres, pero el caso de uso recorre el mismo camino.
type CashTx struct {
	Closes *Closes
	Sales  *Sales
}

type CashTxView struct{ Parent *CashTx }

func (v CashTxView) Save(c domain.CashClose) error { return v.Parent.Closes.Save(c) }

func (v CashTxView) Last(salonID string) (domain.CashClose, error) {
	return v.Parent.Closes.Last(salonID)
}

func (v CashTxView) List(salonID string) ([]domain.CashClose, error) {
	return v.Parent.Closes.List(salonID)
}

func (v CashTxView) SumCashCents(salonID string, from, to time.Time) (int, error) {
	return v.Parent.Sales.SumCents(salonID, from, to, domain.MethodCash)
}

func (t *CashTx) InSalonTx(salonID string, fn func(ports.CashCloseTx) error) error {
	return fn(CashTxView{Parent: t})
}

var _ ports.SalonTxRunner = (*CashTx)(nil)

// SeedProfessional hace que userID atienda en el salón: el professional_id de
// una venta o reserva tiene que ser personal del salón (B-12).
func SeedProfessional(env *Env, salonID, userID string) {
	// Attends=true como lo deja domain.NewMembership: un profesional atiende
	// siempre, y la marca tiene que reflejarlo también en la fila.
	_ = env.Mems.Save(domain.Membership{
		UserID: userID, SalonID: salonID, Role: domain.RoleProfessional, Attends: true,
	})
}

// register registra+loguea un usuario y devuelve (token, userID).
func (e *Env) Register(t *testing.T, email string) (string, string) {
	t.Helper()
	if w := Do(e.Router, "POST", "/api/v1/auth/register", `{"name":"`+email+`","email":"`+email+`","password":"secreta123"}`, ""); w.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, w.Code, w.Body.String())
	}
	w := Do(e.Router, "POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"secreta123"}`, "")
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login %s: %s", email, w.Body.String())
	}
	u, ok := e.Users.ByEmail[email]
	if !ok {
		t.Fatalf("usuario %s no quedó registrado", email)
	}
	return lr.Token, u.ID
}

// salon crea un salón con el token dado y devuelve su id.
func (e *Env) Salon(t *testing.T, token, slug string) string {
	t.Helper()
	w := Do(e.Router, "POST", "/api/v1/salons", `{"name":"Salón `+slug+`","slug":"`+slug+`"}`, token)
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("crear salón %s: %d %s", slug, w.Code, w.Body.String())
	}
	return out.ID
}

func MustJSON(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("json inválido: %v (%s)", err, body)
	}
}

func (e *Env) SeedService(t *testing.T, token, salonID, name string, price int) string {
	t.Helper()
	body := `{"name":"` + name + `","duration_min":45,"price_cents":` + Itoa(price) + `,"commission_pct":20}`
	w := Do(e.Router, "POST", "/api/v1/salons/"+salonID+"/services", body, token)
	var out struct {
		ID string `json:"id"`
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("seed service %s: %d %s", name, w.Code, w.Body.String())
	}
	MustJSON(t, w.Body.Bytes(), &out)
	return out.ID
}

func Itoa(n int) string {
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
