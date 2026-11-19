package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

type mu struct{ byEmail map[string]domain.User }

func (f *mu) Save(u domain.User) error {
	if f.byEmail == nil {
		f.byEmail = map[string]domain.User{}
	}
	f.byEmail[strings.ToLower(u.Email)] = u
	return nil
}
func (f *mu) FindByEmail(e string) (domain.User, error) {
	u, ok := f.byEmail[strings.ToLower(e)]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

type ms struct{ bySlug map[string]domain.Salon }

func (f *ms) Save(s domain.Salon) error {
	if f.bySlug == nil {
		f.bySlug = map[string]domain.Salon{}
	}
	f.bySlug[s.Slug] = s
	return nil
}
func (f *ms) FindBySlug(slug string) (domain.Salon, error) {
	s, ok := f.bySlug[slug]
	if !ok {
		return domain.Salon{}, domain.ErrNotFound
	}
	return s, nil
}

type mm struct{ byKey map[string]domain.Membership }

func (f *mm) Save(m domain.Membership) error {
	if f.byKey == nil {
		f.byKey = map[string]domain.Membership{}
	}
	f.byKey[m.UserID+"|"+m.SalonID] = m
	return nil
}
func (f *mm) Find(u, s string) (domain.Membership, error) {
	m, ok := f.byKey[u+"|"+s]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

func testRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	users, salons, mems := &mu{}, &ms{}, &mm{}
	hasher := security.BcryptHasher{}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	authSvc := app.AuthService{Users: users, Hasher: hasher, Tokens: tokens}
	tenSvc := app.TenantService{Salons: salons, Memberships: mems}
	bookSvc := app.BookingService{Bookings: &mb{}, Memberships: mems}
	r := gin.New()
	RegisterRoutes(r, Deps{
		Auth:        AuthHandler{Auth: authSvc},
		Tenants:     TenantsHandler{Tenants: tenSvc, Memberships: mems},
		Bookings:    BookingHandler{Bookings: bookSvc, Memberships: mems},
		TokenIssuer: tokens,
	})
	return r
}

func doReq(r *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Flujo: register -> login -> crear salón ->
// slug duplicado 409 -> sin token 401.
func TestHTTP_flujo_auth_salon(t *testing.T) {
	r := testRouter()

	w := doReq(r, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"ana@mail.com","password":"secreta"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, "POST", "/api/v1/auth/register", `{"name":"Otra","email":"ana@mail.com","password":"x"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d", w.Code)
	}
	w = doReq(r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login sin token: %s", w.Body.String())
	}
	w = doReq(r, "POST", "/api/v1/salons", `{"name":"Navajas de Oro","slug":"navajas-oro"}`, lr.Token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create salon: %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, "POST", "/api/v1/salons", `{"name":"Copia","slug":"navajas-oro"}`, lr.Token)
	if w.Code != http.StatusConflict {
		t.Fatalf("slug duplicado debe dar 409, dio %d", w.Code)
	}
	w = doReq(r, "POST", "/api/v1/salons", `{"name":"X","slug":"x"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// Fake de reservas en memoria (misma semántica que pg EXCLUDE para el test).
type mb struct{ all []domain.Booking }

func (f *mb) Save(b domain.Booking) error { f.all = append(f.all, b); return nil }
func (f *mb) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	probe := domain.Booking{SalonID: salonID, ProfessionalID: professionalID, StartsAt: start, EndsAt: end, Status: domain.BookingBooked}
	for _, b := range f.all {
		if b.Status == domain.BookingBooked && b.Overlaps(probe) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *mb) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	for _, b := range f.all {
		if b.SalonID == salonID && !b.StartsAt.Before(from) && b.StartsAt.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

// Flujo: login como receptora -> crear reserva 201 -> solape 409 ->
// adyacente 201 -> listado del día trae 2 -> sin token 401.
func TestHTTP_bookings(t *testing.T) {
	r := testRouter()
	doReq(r, "POST", "/api/v1/auth/register", `{"name":"Recep","email":"recep@mail.com","password":"secreta"}`, "")
	lr := doReq(r, "POST", "/api/v1/auth/login", `{"email":"recep@mail.com","password":"secreta"}`, "")
	var lt struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &lt); err != nil || lt.Token == "" {
		t.Fatalf("login: %s", lr.Body.String())
	}
	ws := doReq(r, "POST", "/api/v1/salons", `{"name":"Salón","slug":"salon-http"}`, lt.Token)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ws.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("salón: %s", ws.Body.String())
	}
	p := "/api/v1/salons/" + created.ID + "/bookings"
	b10 := `{"professional_id":"prof1","client_name":"Lía","starts_at":"2026-12-03T10:00:00Z","ends_at":"2026-12-03T11:00:00Z"}`
	if w := doReq(r, "POST", p, b10, lt.Token); w.Code != http.StatusCreated {
		t.Fatalf("crear: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(r, "POST", p, b10, lt.Token); w.Code != http.StatusConflict {
		t.Fatalf("solape debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	b11 := `{"professional_id":"prof1","client_name":"Ana","starts_at":"2026-12-03T11:00:00Z","ends_at":"2026-12-03T12:00:00Z"}`
	if w := doReq(r, "POST", p, b11, lt.Token); w.Code != http.StatusCreated {
		t.Fatalf("adyacente: %d %s", w.Code, w.Body.String())
	}
	w := doReq(r, "GET", p+"?from=2026-12-03T00:00:00Z&to=2026-12-04T00:00:00Z", "", lt.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Bookings []map[string]interface{} `json:"bookings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Bookings) != 2 {
		t.Fatalf("esperaba 2 reservas, dio %s", w.Body.String())
	}
	if w := doReq(r, "GET", p, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}
