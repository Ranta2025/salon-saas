package httpapi

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la lista de espera para los tests HTTP (F7). Reproducen la semántica
// del adaptador de Postgres que importa en HTTP: entrada activa idéntica única
// (índice parcial), día como "YYYY-MM-DD" filtrado por >= hoy, baja
// idempotente con UPDATE condicional y aislamiento por salón.
type mwaitlist struct {
	mu      sync.Mutex
	entries []domain.WaitlistEntry
	// serviceNames/professionalNames son los JOIN de la pantalla del personal.
	serviceNames      map[string]string
	professionalNames map[string]string
}

func (f *mwaitlist) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day && strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *mwaitlist) Create(e domain.WaitlistEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, other := range f.entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day &&
			strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	f.entries = append(f.entries, e)
	return nil
}

// --- Transacción del alta (D4/D6) -------------------------------------------
// InWaitlistTx toma el mutex del fake (hace de advisory lock) y entrega una
// transacción con las primitivas del alta; las del barrido no las usa ningún test
// HTTP y devuelven error si alguien las llama por error.

type mwaitlistTx struct{ repo *mwaitlist }

func (f *mwaitlist) InWaitlistTx(fn func(tx ports.WaitlistTx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(mwaitlistTx{repo: f})
}

func (t mwaitlistTx) LockWrites(string) error { return nil }

func (t mwaitlistTx) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && e.ServiceID == serviceID &&
			e.ProfessionalID == professionalID && e.Day == day && strings.EqualFold(e.Email, email) {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (t mwaitlistTx) CountActiveByEmail(salonID, email, fromDay string) (int, error) {
	n := 0
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && strings.EqualFold(e.Email, email) && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t mwaitlistTx) CountActiveBySalon(salonID, fromDay string) (int, error) {
	n := 0
	for _, e := range t.repo.entries {
		if e.Active() && e.SalonID == salonID && e.Day >= fromDay {
			n++
		}
	}
	return n, nil
}

func (t mwaitlistTx) Create(e domain.WaitlistEntry) error {
	for _, other := range t.repo.entries {
		if other.Active() && other.SalonID == e.SalonID && other.ServiceID == e.ServiceID &&
			other.ProfessionalID == e.ProfessionalID && other.Day == e.Day && strings.EqualFold(other.Email, e.Email) {
			return domain.ErrAlreadyExists
		}
	}
	t.repo.entries = append(t.repo.entries, e)
	return nil
}

func (t mwaitlistTx) MarkLeft(id string, now time.Time) error {
	for i := range t.repo.entries {
		if t.repo.entries[i].ID == id {
			left := now.UTC()
			t.repo.entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

var errMWaitlistSweep = errors.New("el barrido no se usa en los tests HTTP")

func (t mwaitlistTx) CountRetained(domain.WaitlistGroup, time.Time) (int, error) {
	return 0, errMWaitlistSweep
}

func (t mwaitlistTx) Claim(domain.WaitlistGroup, time.Time, int) ([]domain.WaitlistEntry, error) {
	return nil, errMWaitlistSweep
}

func (t mwaitlistTx) SetToken(string, string) error { return errMWaitlistSweep }

func (f *mwaitlist) FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.TokenHash != "" && e.TokenHash == tokenHash {
			return e, nil
		}
	}
	return domain.WaitlistEntry{}, domain.ErrNotFound
}

func (f *mwaitlist) Leave(tokenHash string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		if f.entries[i].TokenHash == tokenHash && f.entries[i].LeftAt == nil {
			left := now.UTC()
			f.entries[i].LeftAt = &left
			return nil
		}
	}
	return nil
}

func (f *mwaitlist) ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.WaitlistStaffEntry
	for _, e := range f.entries {
		if e.SalonID != salonID || !e.Active() || e.Day < fromDay {
			continue
		}
		out = append(out, domain.WaitlistStaffEntry{
			WaitlistEntry:    e,
			ServiceName:      f.serviceNames[e.ServiceID],
			ProfessionalName: f.professionalNames[e.ProfessionalID],
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

func (f *mwaitlist) RemoveByID(salonID, id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		if f.entries[i].ID == id && f.entries[i].SalonID == salonID && f.entries[i].LeftAt == nil {
			left := now.UTC()
			f.entries[i].LeftAt = &left
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *mwaitlist) ListGroups(now time.Time) ([]domain.WaitlistGroup, error) {
	// El barrido no se ejerce por HTTP: se prueba en app y en los IT.
	return nil, nil
}

func (f *mwaitlist) MarkNotified(id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.entries {
		if f.entries[i].ID == id {
			notified := now.UTC()
			f.entries[i].NotifiedAt = &notified
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.WaitlistRepository = (*mwaitlist)(nil)

// waitlistHTTPEnv es el entorno HTTP de F7: un salón con su servicio «Corte» de
// 45 minutos, una profesional que lo hace y los cinco roles.
type waitlistHTTPEnv struct {
	*posEnv
	wl           *mwaitlist
	owner        string
	admin        string
	reception    string
	professional string
	client       string
	salonID      string
	slug         string
	serviceID    string
	loc          *time.Location
}

func newWaitlistHTTPEnv(t *testing.T) *waitlistHTTPEnv {
	t.Helper()
	env := newPOSEnv()
	wl := &mwaitlist{
		serviceNames:      map[string]string{},
		professionalNames: map[string]string{},
	}
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	out := &waitlistHTTPEnv{posEnv: env, wl: wl, slug: "lista-espera", loc: loc}

	out.owner, _ = env.register(t, "duena@mail.com")
	out.salonID = env.salon(t, out.owner, out.slug)
	var adminID, receptionID, profID, clientID string
	out.admin, adminID = env.register(t, "admin-lista@mail.com")
	out.reception, receptionID = env.register(t, "recepcion-lista@mail.com")
	out.professional, profID = env.register(t, "profe-lista@mail.com")
	out.client, clientID = env.register(t, "cliente-lista@mail.com")
	seedRole(t, env, out.salonID, adminID, domain.RoleAdmin)
	seedRole(t, env, out.salonID, receptionID, domain.RoleReception)
	seedRole(t, env, out.salonID, profID, domain.RoleProfessional)
	seedRole(t, env, out.salonID, clientID, domain.RoleClient)
	// La profesional hace el servicio: la disponibilidad y el alta la miran.
	if env.profs.byService == nil {
		env.profs.byService = map[string][]string{}
	}

	// El servicio se crea por la API para tener el id real.
	w := doReq(env.r, "POST", "/api/v1/salons/"+out.salonID+"/services",
		`{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, out.owner)
	if w.Code != 201 {
		t.Fatalf("crear servicio: %d %s", w.Code, w.Body.String())
	}
	var svc struct {
		ID string `json:"id"`
	}
	mustJSON(t, w.Body.Bytes(), &svc)
	out.serviceID = svc.ID
	// byService con la clave real del servicio (la de arriba es un comodín que
	// mprof solo usa cuando NO hay clave exacta: se registra la exacta).
	env.profs.byService[out.salonID+"|"+svc.ID] = []string{profID}
	wl.serviceNames[svc.ID] = "Corte"
	wl.professionalNames[profID] = "profe-lista@mail.com"

	out.closedSalon(t)
	out.wire(t, func(d *Deps) {})
	return out
}

// wire rearma el router con la lista cableada y deja que el test ajuste las
// dependencias (los rate limits propios, por ejemplo).
func (e *waitlistHTTPEnv) wire(t *testing.T, mutate func(d *Deps)) {
	t.Helper()
	d := e.d
	d.Waitlist = WaitlistHandler{
		Service: app.WaitlistService{
			Waitlist: e.wl, Tx: e.wl,
			Salons: e.salons, Services: e.services, Professionals: e.profs,
			Settings: e.settings, Availability: app.AvailabilityService{
				Salons: e.salons, Services: e.services, Settings: e.settings,
				Staff: e.mems, Bookings: e.bookings, Professionals: e.profs,
				Hours: e.weekly, TimeOff: e.timeOff,
			},
			Memberships: e.mems,
		},
		Memberships: e.mems,
	}
	if mutate != nil {
		mutate(&d)
	}
	e.d = d
	e.r = gin.New()
	RegisterRoutes(e.r, e.d)
}

// routerWith devuelve un router nuevo con las dependencias ya ajustadas.
func (e *waitlistHTTPEnv) routerWith(t *testing.T, mutate func(d *Deps)) *gin.Engine {
	t.Helper()
	e.wire(t, mutate)
	return e.r
}

// closedSalon deja la ventana en 09:00-09:30: un servicio de 45 minutos no entra
// y el día queda SIN horarios (es el caso en el que la lista sirve).
func (e *waitlistHTTPEnv) closedSalon(t *testing.T) {
	t.Helper()
	if e.settings.bySalon == nil {
		e.settings.bySalon = map[string]domain.SalonSettings{}
	}
	e.settings.bySalon[e.salonID] = domain.SalonSettings{
		SalonID: e.salonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "09:30", SlotStepMin: 30,
	}
}

// openSalon deja una ventana amplia: el día vuelve a tener horarios libres.
func (e *waitlistHTTPEnv) openSalon(t *testing.T) {
	t.Helper()
	e.settings.bySalon[e.salonID] = domain.SalonSettings{
		SalonID: e.salonID, Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "12:00", SlotStepMin: 30,
	}
}

// closedDay es un día futuro (dentro de la ventana de 30) sin horarios.
func (e *waitlistHTTPEnv) closedDay(t *testing.T) string {
	t.Helper()
	return time.Now().In(e.loc).AddDate(0, 0, 2).Format("2006-01-02")
}

// openDay es otro día futuro; con la ventana amplia tiene horarios libres.
func (e *waitlistHTTPEnv) openDay(t *testing.T) string {
	t.Helper()
	return time.Now().In(e.loc).AddDate(0, 0, 3).Format("2006-01-02")
}

// seedEntry deja una entrada con su token EN CLARO (el repo guarda el hash) y
// devuelve el token.
func (e *waitlistHTTPEnv) seedEntry(t *testing.T, salonID, serviceID, day, name, email string) string {
	t.Helper()
	_, token := e.seed(t, salonID, serviceID, "", day, name, email, "")
	return token
}

// seedEntryWithID es igual pero devuelve el id de la entrada.
func (e *waitlistHTTPEnv) seedEntryWithID(t *testing.T, salonID, serviceID, professionalID, day, name, email, phone string) string {
	t.Helper()
	entry, _ := e.seed(t, salonID, serviceID, professionalID, day, name, email, phone)
	return entry.ID
}

func (e *waitlistHTTPEnv) seed(t *testing.T, salonID, serviceID, professionalID, day, name, email, phone string) (domain.WaitlistEntry, string) {
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
	if err := e.wl.Create(entry); err != nil {
		t.Fatalf("guardar entrada: %v", err)
	}
	return entry, token
}

// markNotified deja la entrada como avisada (el estado `notified` de D3).
func (e *waitlistHTTPEnv) markNotified(t *testing.T, id string) {
	t.Helper()
	e.wl.mu.Lock()
	defer e.wl.mu.Unlock()
	for i := range e.wl.entries {
		if e.wl.entries[i].ID == id {
			at := time.Now().UTC()
			e.wl.entries[i].NotifyClaimedAt = &at
			e.wl.entries[i].NotifiedAt = &at
			return
		}
	}
	t.Fatalf("no existe la entrada %s", id)
}

func (e *waitlistHTTPEnv) entryCount() int {
	e.wl.mu.Lock()
	defer e.wl.mu.Unlock()
	return len(e.wl.entries)
}
