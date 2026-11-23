package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

// FindByID: lo usa GET /me (el token trae el userID, no el email).
func (f *mu) FindByID(id string) (domain.User, error) {
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
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

// FindByID: lo usa GET /me para el nombre y el slug de cada salón del usuario.
func (f *ms) FindByID(id string) (domain.Salon, error) {
	for _, s := range f.bySlug {
		if s.ID == id {
			return s, nil
		}
	}
	return domain.Salon{}, domain.ErrNotFound
}

type mm struct{ byKey map[string]domain.Membership }

func (f *mm) Save(m domain.Membership) error {
	if f.byKey == nil {
		f.byKey = map[string]domain.Membership{}
	}
	// Upsert como pgstore: volver a guardar una membresía la deja ACTIVA.
	m.EndedAt = nil
	f.byKey[m.UserID+"|"+m.SalonID] = m
	return nil
}
func (f *mm) Find(u, s string) (domain.Membership, error) {
	m, ok := f.byKey[u+"|"+s]
	if !ok || !m.Active() {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// CountProfessionals: capacidad instalada del salón (StaffCounter del dashboard).
func (f *mm) CountProfessionals(salonID string) (int, error) {
	n := 0
	for _, m := range f.byKey {
		if m.SalonID == salonID && m.Role == domain.RoleProfessional && m.Active() {
			n++
		}
	}
	return n, nil
}

// ListByUser: puerto MembershipLister (rooms del websocket, avisos y /me).
func (f *mm) ListByUser(userID string) ([]domain.Membership, error) {
	var out []domain.Membership
	for _, m := range f.byKey {
		if m.UserID == userID && m.Active() {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListStaff: personal que ATIENDE (misma regla que pgstore: profesional
// siempre, owner/admin con attends) con nombre.
func (f *mm) ListStaff(salonID string) ([]domain.StaffMember, error) {
	var out []domain.StaffMember
	for _, m := range f.byKey {
		if m.SalonID == salonID && m.Bookable() {
			out = append(out, domain.StaffMember{
				ID: m.UserID, Name: m.UserID, Role: m.Role,
				Attends: m.Attends, Trade: m.Trade,
			})
		}
	}
	return out, nil
}

// testRouter arma el router completo con fakes en memoria (ver pos_test.go).
func testRouter() *gin.Engine { return newPOSEnv().r }

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

	w := doReq(r, "POST", "/api/v1/auth/register", `{"name":"Ana","email":"ana@mail.com","password":"secreta123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w = doReq(r, "POST", "/api/v1/auth/register", `{"name":"Otra","email":"ana@mail.com","password":"otraClave1"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d", w.Code)
	}
	w = doReq(r, "POST", "/api/v1/auth/login", `{"email":"ana@mail.com","password":"secreta123"}`, "")
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
func (f *mb) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	for _, b := range f.all {
		if b.SalonID == salonID && b.ClientOpID != "" && b.ClientOpID == clientOpID {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}
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

// FindByID: el scope por salón es la autorización del PATCH.
func (f *mb) FindByID(salonID, id string) (domain.Booking, error) {
	for _, b := range f.all {
		if b.SalonID == salonID && b.ID == id {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}

// Update reemplaza la reserva por id (misma semántica que el UPDATE de pgstore).
func (f *mb) Update(b domain.Booking) error {
	for i := range f.all {
		if f.all[i].ID == b.ID && f.all[i].SalonID == b.SalonID {
			f.all[i] = b
			return nil
		}
	}
	return domain.ErrNotFound
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

// StatsRange: agregados de agenda para el dashboard (AgendaStats).
func (f *mb) StatsRange(salonID string, from, to time.Time) (int, int, error) {
	count, minutes := 0, 0
	for _, b := range f.all {
		if b.SalonID != salonID || b.StartsAt.Before(from) || !b.StartsAt.Before(to) {
			continue
		}
		if b.Status != domain.BookingBooked && b.Status != domain.BookingDone {
			continue
		}
		count++
		minutes += int(b.EndsAt.Sub(b.StartsAt).Minutes())
	}
	return count, minutes, nil
}

// Flujo: login como receptora -> crear reserva 201 -> solape 409 ->
// adyacente 201 -> listado del día trae 2 -> sin token 401.
func TestHTTP_bookings(t *testing.T) {
	env := newPOSEnv()
	r := env.r
	doReq(r, "POST", "/api/v1/auth/register", `{"name":"Recep","email":"recep@mail.com","password":"secreta123"}`, "")
	lr := doReq(r, "POST", "/api/v1/auth/login", `{"email":"recep@mail.com","password":"secreta123"}`, "")
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
	// B-12: el profesional del turno tiene que atender en el salón.
	seedProfessional(env, created.ID, "prof1")
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
