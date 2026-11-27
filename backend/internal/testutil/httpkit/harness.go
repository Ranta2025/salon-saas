package httpkit

import (
	"net/http/httptest"
	"salon-saas/internal/domain"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Users struct{ ByEmail map[string]domain.User }

func (f *Users) Save(u domain.User) error {
	if f.ByEmail == nil {
		f.ByEmail = map[string]domain.User{}
	}
	f.ByEmail[strings.ToLower(u.Email)] = u
	return nil
}

func (f *Users) FindByEmail(e string) (domain.User, error) {
	u, ok := f.ByEmail[strings.ToLower(e)]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

// FindByID: lo usa GET /me (el token trae el userID, no el email).
func (f *Users) FindByID(id string) (domain.User, error) {
	for _, u := range f.ByEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

type Salons struct{ BySlug map[string]domain.Salon }

func (f *Salons) Save(s domain.Salon) error {
	if f.BySlug == nil {
		f.BySlug = map[string]domain.Salon{}
	}
	f.BySlug[s.Slug] = s
	return nil
}

func (f *Salons) FindBySlug(slug string) (domain.Salon, error) {
	s, ok := f.BySlug[slug]
	if !ok {
		return domain.Salon{}, domain.ErrNotFound
	}
	return s, nil
}

// FindByID: lo usa GET /me para el nombre y el slug de cada salón del usuario.
func (f *Salons) FindByID(id string) (domain.Salon, error) {
	for _, s := range f.BySlug {
		if s.ID == id {
			return s, nil
		}
	}
	return domain.Salon{}, domain.ErrNotFound
}

type Memberships struct{ ByKey map[string]domain.Membership }

func (f *Memberships) Save(m domain.Membership) error {
	if f.ByKey == nil {
		f.ByKey = map[string]domain.Membership{}
	}
	// Upsert como pgstore: volver a guardar una membresía la deja ACTIVA.
	m.EndedAt = nil
	f.ByKey[m.UserID+"|"+m.SalonID] = m
	return nil
}

func (f *Memberships) Find(u, s string) (domain.Membership, error) {
	m, ok := f.ByKey[u+"|"+s]
	if !ok || !m.Active() {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// FindMember: la lectura que ve también la baja (F2g2), como la query de
// pgstore: la usan el alta por email y la invitación.
func (f *Memberships) FindMember(u, s string) (domain.Membership, error) {
	m, ok := f.ByKey[u+"|"+s]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// CountProfessionals: capacidad instalada del salón (StaffCounter del dashboard).
func (f *Memberships) CountProfessionals(salonID string) (int, error) {
	n := 0
	for _, m := range f.ByKey {
		if m.SalonID == salonID && m.Role == domain.RoleProfessional && m.Active() {
			n++
		}
	}
	return n, nil
}

// ListByUser: puerto MembershipLister (rooms del websocket, avisos y /me).
func (f *Memberships) ListByUser(userID string) ([]domain.Membership, error) {
	var out []domain.Membership
	for _, m := range f.ByKey {
		if m.UserID == userID && m.Active() {
			out = append(out, m)
		}
	}
	return out, nil
}

// ListStaff: personal que ATIENDE (misma regla que pgstore: profesional
// siempre, owner/admin con attends) con nombre.
func (f *Memberships) ListStaff(salonID string) ([]domain.StaffMember, error) {
	var out []domain.StaffMember
	for _, m := range f.ByKey {
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
func testRouter() *gin.Engine { return NewPOSEnv().Router }

func Do(r *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Fake de reservas en memoria (misma semántica que pg EXCLUDE para el test).
type Bookings struct{ All []domain.Booking }

func (f *Bookings) Save(b domain.Booking) error { f.All = append(f.All, b); return nil }

func (f *Bookings) FindByClientOpID(salonID, clientOpID string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ClientOpID != "" && b.ClientOpID == clientOpID {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}

func (f *Bookings) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	probe := domain.Booking{SalonID: salonID, ProfessionalID: professionalID, StartsAt: start, EndsAt: end, Status: domain.BookingBooked}
	for _, b := range f.All {
		if b.Status == domain.BookingBooked && b.Overlaps(probe) {
			out = append(out, b)
		}
	}
	return out, nil
}

// FindByID: el scope por salón es la autorización del PATCH.
func (f *Bookings) FindByID(salonID, id string) (domain.Booking, error) {
	for _, b := range f.All {
		if b.SalonID == salonID && b.ID == id {
			return b, nil
		}
	}
	return domain.Booking{}, domain.ErrNotFound
}

// Update reemplaza la reserva por id (misma semántica que el UPDATE de pgstore).
func (f *Bookings) Update(b domain.Booking) error {
	for i := range f.All {
		if f.All[i].ID == b.ID && f.All[i].SalonID == b.SalonID {
			f.All[i] = b
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Bookings) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	var out []domain.Booking
	for _, b := range f.All {
		if b.SalonID == salonID && !b.StartsAt.Before(from) && b.StartsAt.Before(to) {
			out = append(out, b)
		}
	}
	return out, nil
}

// StatsRange: agregados de agenda para el dashboard (AgendaStats).
func (f *Bookings) StatsRange(salonID string, from, to time.Time) (int, int, error) {
	count, minutes := 0, 0
	for _, b := range f.All {
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

// SalonSettings siembra la configuración de un salón de prueba (CDMX,
// 09:00-12:00, paso 30) para disponibilidad y reserva pública. Es el helper
// compartido por los contratos HTTP de las funcionalidades que la necesitan.
func SalonSettings(env *Env, salonID string) {
	if env.Settings.BySalon == nil {
		env.Settings.BySalon = map[string]domain.SalonSettings{}
	}
	env.Settings.BySalon[salonID] = domain.SalonSettings{
		SalonID: salonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}
}
