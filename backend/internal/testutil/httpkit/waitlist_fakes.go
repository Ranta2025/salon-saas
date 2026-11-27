package httpkit

import (
	"errors"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Fakes de la lista de espera para los tests HTTP (F7). Reproducen la semántica
// del adaptador de Postgres que importa en HTTP: entrada activa idéntica única
// (índice parcial), día como "YYYY-MM-DD" filtrado por >= hoy, baja
// idempotente con UPDATE condicional y aislamiento por salón.
type Waitlist struct {
	Mu      sync.Mutex
	Entries []domain.WaitlistEntry
	// serviceNames/professionalNames son los JOIN de la pantalla del personal.
	ServiceNames      map[string]string
	ProfessionalNames map[string]string
}

func (f *Waitlist) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, e := range f.Entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day && strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *Waitlist) Create(e domain.WaitlistEntry) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, other := range f.Entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day &&
			strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	f.Entries = append(f.Entries, e)
	return nil
}

// --- Transacción del alta (D4/D6) -------------------------------------------
// InWaitlistTx toma el mutex del fake (hace de advisory lock) y entrega una
// transacción con las primitivas del alta; las del barrido no las usa ningún test
// HTTP y devuelven error si alguien las llama por error.

type WaitlistTx struct{ Repo *Waitlist }

func (f *Waitlist) InWaitlistTx(fn func(tx ports.WaitlistTx) error) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return fn(WaitlistTx{Repo: f})
}

func (t WaitlistTx) LockWrites(string) error { return nil }

func (t WaitlistTx) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	for _, e := range t.Repo.Entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day && strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (t WaitlistTx) CountActiveByEmail(salonID, email, fromDay string) (int, error) {
	n := 0
	for _, e := range t.Repo.Entries {
		if e.Active() && e.SalonID == salonID && strings.EqualFold(e.Email, email) && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t WaitlistTx) CountActiveBySalon(salonID, fromDay string) (int, error) {
	n := 0
	for _, e := range t.Repo.Entries {
		if e.Active() && e.SalonID == salonID && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t WaitlistTx) Create(e domain.WaitlistEntry) error {
	for _, other := range t.Repo.Entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day && strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	t.Repo.Entries = append(t.Repo.Entries, e)
	return nil
}

func (t WaitlistTx) MarkLeft(id string, now time.Time) error {
	for i := range t.Repo.Entries {
		if t.Repo.Entries[i].ID == id {
			left := now.UTC()
			t.Repo.Entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

var ErrWaitlistSweep = errors.New("el barrido no se usa en los tests HTTP")

func (t WaitlistTx) CountRetained(domain.WaitlistGroup, time.Time) (int, error) {
	return 0, ErrWaitlistSweep
}

func (t WaitlistTx) Claim(domain.WaitlistGroup, time.Time, int) ([]domain.WaitlistEntry, error) {
	return nil, ErrWaitlistSweep
}

func (t WaitlistTx) SetToken(string, string) error { return ErrWaitlistSweep }

func (f *Waitlist) FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, e := range f.Entries {
		if e.TokenHash != "" && e.TokenHash == tokenHash {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *Waitlist) Leave(tokenHash string, now time.Time) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for i := range f.Entries {
		if f.Entries[i].TokenHash == tokenHash && f.Entries[i].LeftAt == nil {
			left := now.UTC()
			f.Entries[i].LeftAt = &left
			return nil
		}
	}
	return nil
}

func (f *Waitlist) ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.WaitlistStaffEntry
	for _, e := range f.Entries {
		if e.SalonID != salonID || !e.Active() || e.Day < fromDay {
			continue
		}
		out = append(out, domain.WaitlistStaffEntry{
			WaitlistEntry:    e,
			ServiceName:      f.ServiceNames[e.ServiceID],
			ProfessionalName: f.ProfessionalNames[e.ProfessionalID],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (f *Waitlist) RemoveByID(salonID, id string, now time.Time) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for i := range f.Entries {
		if f.Entries[i].ID == id && f.Entries[i].SalonID == salonID && f.Entries[i].LeftAt == nil {
			left := now.UTC()
			f.Entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Waitlist) ListGroups(now time.Time) ([]domain.WaitlistGroup, error) {
	// El barrido no se ejerce por HTTP: se prueba en app y en los IT.
	return nil, nil
}

func (f *Waitlist) MarkNotified(id string, now time.Time) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for i := range f.Entries {
		if f.Entries[i].ID == id {
			notified := now.UTC()
			f.Entries[i].NotifiedAt = &notified
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.WaitlistRepository = (*Waitlist)(nil)

// WaitlistHTTPEnv es el entorno HTTP de F7: un salón con su servicio «Corte» de
// 45 minutos, una profesional que lo hace y los cinco roles.
type WaitlistHTTPEnv struct {
	*Env
	Wl           *Waitlist
	Owner        string
	Admin        string
	Reception    string
	Professional string
	Client       string
	SalonID      string
	Slug         string
	ServiceID    string
	Loc          *time.Location
}

func NewWaitlistHTTPEnv(t *testing.T) *WaitlistHTTPEnv {
	t.Helper()
	env := NewPOSEnv()
	wl := &Waitlist{
		ServiceNames:      map[string]string{},
		ProfessionalNames: map[string]string{},
	}
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	out := &WaitlistHTTPEnv{Env: env, Wl: wl, Slug: "lista-espera", Loc: loc}

	out.Owner, _ = env.Register(t, "duena@mail.com")
	out.SalonID = env.Salon(t, out.Owner, out.Slug)
	var adminID, receptionID, profID, clientID string
	out.Admin, adminID = env.Register(t, "admin-lista@mail.com")
	out.Reception, receptionID = env.Register(t, "recepcion-lista@mail.com")
	out.Professional, profID = env.Register(t, "profe-lista@mail.com")
	out.Client, clientID = env.Register(t, "cliente-lista@mail.com")
	SeedRole(t, env, out.SalonID, adminID, domain.RoleAdmin)
	SeedRole(t, env, out.SalonID, receptionID, domain.RoleReception)
	SeedRole(t, env, out.SalonID, profID, domain.RoleProfessional)
	SeedRole(t, env, out.SalonID, clientID, domain.RoleClient)
	// La profesional hace el servicio: la disponibilidad y el alta la miran.
	if env.Professionals.ByService == nil {
		env.Professionals.ByService = map[string][]string{}
	}

	// El servicio se crea por la API para tener el id real.
	w := Do(env.Router, "POST", "/api/v1/salons/"+out.SalonID+"/services",
		`{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, out.Owner)
	if w.Code != 201 {
		t.Fatalf("crear servicio: %d %s", w.Code, w.Body.String())
	}
	var svc struct {
		ID string `json:"id"`
	}
	MustJSON(t, w.Body.Bytes(), &svc)
	out.ServiceID = svc.ID
	// byService con la clave real del servicio (la de arriba es un comodín que
	// Professionals solo usa cuando NO hay clave exacta: se registra la exacta).
	env.Professionals.ByService[out.SalonID+"|"+svc.ID] = []string{profID}
	wl.ServiceNames[svc.ID] = "Corte"
	wl.ProfessionalNames[profID] = "profe-lista@mail.com"

	out.closedSalon(t)
	out.wire(t, func(d *httpapi.Deps) {})
	return out
}

// wire rearma el router con la lista cableada y deja que el test ajuste las
// dependencias (los rate limits propios, por ejemplo).
func (e *WaitlistHTTPEnv) wire(t *testing.T, mutate func(d *httpapi.Deps)) {
	t.Helper()
	d := e.Deps
	d.Waitlist = httpapi.WaitlistHandler{
		Service: app.WaitlistService{
			Waitlist: e.Wl, Tx: e.Wl,
			Salons: e.Salons, Services: e.Services, Professionals: e.Professionals,
			Settings: e.Settings, Availability: app.AvailabilityService{
				Salons: e.Salons, Services: e.Services, Settings: e.Settings,
				Staff: e.Mems, Bookings: e.Bookings, Professionals: e.Professionals,
				Hours: e.Weekly, TimeOff: e.TimeOff,
			},
			Memberships: e.Mems,
		},
		Memberships: e.Mems,
	}
	if mutate != nil {
		mutate(&d)
	}
	e.Deps = d
	e.Router = gin.New()
	httpapi.RegisterRoutes(e.Router, e.Deps)
}

// routerWith devuelve un router nuevo con las dependencias ya ajustadas.
func (e *WaitlistHTTPEnv) RouterWith(t *testing.T, mutate func(d *httpapi.Deps)) *gin.Engine {
	t.Helper()
	e.wire(t, mutate)
	return e.Router
}

// closedSalon deja la ventana en 09:00-09:30: un servicio de 45 minutos no entra
// y el día queda SIN horarios (es el caso en el que la lista sirve).
func (e *WaitlistHTTPEnv) closedSalon(t *testing.T) {
	t.Helper()
	if e.Settings.BySalon == nil {
		e.Settings.BySalon = map[string]domain.SalonSettings{}
	}
	e.Settings.BySalon[e.SalonID] = domain.SalonSettings{
		SalonID: e.SalonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "09:30", SlotStepMin: 30,
	}
}

// openSalon deja una ventana amplia: el día vuelve a tener horarios libres.
func (e *WaitlistHTTPEnv) OpenSalon(t *testing.T) {
	t.Helper()
	e.Settings.BySalon[e.SalonID] = domain.SalonSettings{
		SalonID: e.SalonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}
}

// closedDay es un día futuro (dentro de la ventana de 30) sin horarios.
func (e *WaitlistHTTPEnv) ClosedDay(t *testing.T) string {
	t.Helper()
	return time.Now().In(e.Loc).AddDate(0, 0, 2).Format("2006-01-02")
}

// openDay es otro día futuro; con la ventana amplia tiene horarios libres.
func (e *WaitlistHTTPEnv) OpenDay(t *testing.T) string {
	t.Helper()
	return time.Now().In(e.Loc).AddDate(0, 0, 3).Format("2006-01-02")
}

// seedEntry deja una entrada con su token EN CLARO (el repo guarda el hash) y
// devuelve el token.
func (e *WaitlistHTTPEnv) SeedEntry(t *testing.T, salonID, serviceID, day, name, email string) string {
	t.Helper()
	_, token := e.Seed(t, salonID, serviceID, "", day, name, email, "")
	return token
}

// seedEntryWithID es igual pero devuelve el id de la entrada.
func (e *WaitlistHTTPEnv) SeedEntryWithID(t *testing.T, salonID, serviceID, professionalID, day, name, email, phone string) string {
	t.Helper()
	entry, _ := e.Seed(t, salonID, serviceID, professionalID, day, name, email, phone)
	return entry.ID
}

func (e *WaitlistHTTPEnv) Seed(t *testing.T, salonID, serviceID, professionalID, day, name, email, phone string) (domain.WaitlistEntry, string) {
	t.Helper()
	contact, err := domain.NewWaitlistContact(name, email, phone)
	if err != nil {
		t.Fatalf("contacto: %v", err)
	}
	entry, err := domain.NewWaitlistEntry(salonID, serviceID, professionalID, day, contact, time.Now().UTC())
	if err != nil {
		t.Fatalf("entrada: %v", err)
	}
	token, err := domain.NewWaitlistToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	entry, err = entry.WithTokenHash(domain.HashWaitlistToken(token))
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if err := e.Wl.Create(entry); err != nil {
		t.Fatalf("guardar entrada: %v", err)
	}
	return entry, token
}

// markNotified deja la entrada como avisada (el estado `notified` de D3).
func (e *WaitlistHTTPEnv) MarkNotified(t *testing.T, id string) {
	t.Helper()
	e.Wl.Mu.Lock()
	defer e.Wl.Mu.Unlock()
	for i := range e.Wl.Entries {
		if e.Wl.Entries[i].ID == id {
			at := time.Now().UTC()
			e.Wl.Entries[i].NotifyClaimedAt = &at
			e.Wl.Entries[i].NotifiedAt = &at
			return
		}
	}
	t.Fatalf("no existe la entrada %s", id)
}

func (e *WaitlistHTTPEnv) EntryCount() int {
	e.Wl.Mu.Lock()
	defer e.Wl.Mu.Unlock()
	return len(e.Wl.Entries)
}
