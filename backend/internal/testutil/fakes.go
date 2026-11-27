// Package testutil reúne los dobles de prueba que necesitan los módulos por
// funcionalidad (`internal/<feature>/...`). Los dobles de `_test.go` de un
// paquete no se pueden importar desde otro, así que los que usa más de una
// funcionalidad viven acá con nombres exportados.
//
// Cada doble es una COPIA fiel del que hoy vive en `internal/app/*_fakes_test.go`
// (misma semántica que pgstore: scope por salón, ErrNotFound, upsert por id). Las
// copias se van a poder borrar cuando el paquete `app` se disuelva y sus tests se
// muden a cada funcionalidad. Este paso es solo mover: no cambia comportamiento.
package testutil

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeMemberships es el repositorio de membresías en memoria: la misma
// semántica que el upsert de pgstore (la fila que vuelve a entrar al equipo
// queda ACTIVA, ended_at NULL) y Find no ve a quien fue dado de baja.
type FakeMemberships struct{ ByKey map[string]domain.Membership }

func (f *FakeMemberships) Save(m domain.Membership) error {
	if f.ByKey == nil {
		f.ByKey = map[string]domain.Membership{}
	}
	m.EndedAt = nil
	f.ByKey[m.UserID+"|"+m.SalonID] = m
	return nil
}

func (f *FakeMemberships) Find(userID, salonID string) (domain.Membership, error) {
	m, ok := f.ByKey[userID+"|"+salonID]
	if !ok || !m.Active() {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// FindMember: la lectura que ve también la baja, como la query de pgstore.
func (f *FakeMemberships) FindMember(userID, salonID string) (domain.Membership, error) {
	m, ok := f.ByKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// ListByUser: puerto MembershipLister (rooms del websocket y marcar leídas).
func (f *FakeMemberships) ListByUser(userID string) ([]domain.Membership, error) {
	var out []domain.Membership
	for _, m := range f.ByKey {
		if m.UserID == userID && m.Active() {
			out = append(out, m)
		}
	}
	return out, nil
}

var _ ports.MembershipRepository = (*FakeMemberships)(nil)

// FakeServices es el catálogo de servicios en memoria (upsert por id, scoping
// por salón, List con o sin inactivos).
type FakeServices struct{ All []domain.Service }

func (f *FakeServices) Save(s domain.Service) error {
	for i := range f.All {
		if f.All[i].ID == s.ID {
			f.All[i] = s
			return nil
		}
	}
	f.All = append(f.All, s)
	return nil
}

func (f *FakeServices) FindByID(salonID, id string) (domain.Service, error) {
	for _, s := range f.All {
		if s.SalonID == salonID && s.ID == id {
			return s, nil
		}
	}
	return domain.Service{}, domain.ErrNotFound
}

func (f *FakeServices) List(salonID string, includeInactive bool) ([]domain.Service, error) {
	var out []domain.Service
	for _, s := range f.All {
		if s.SalonID != salonID {
			continue
		}
		if !includeInactive && !s.Active {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

var _ ports.ServiceRepository = (*FakeServices)(nil)

// FakeClients imita a pgstore.Clients: todo scopeado por salón y cliente.
type FakeClients struct {
	Clients []domain.Client
	Photos  []domain.ClientPhoto
	// FailSavePhoto simula una DB caída justo después de escribir el archivo.
	FailSavePhoto bool
	// FailSave simula una DB caída al guardar la ficha.
	FailSave bool
}

func (f *FakeClients) Save(c domain.Client) error {
	if f.FailSave {
		return errors.New("db caída")
	}
	for i := range f.Clients {
		if f.Clients[i].ID == c.ID {
			f.Clients[i] = c
			return nil
		}
	}
	f.Clients = append(f.Clients, c)
	return nil
}

func (f *FakeClients) FindByID(salonID, id string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && c.ID == id {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

// FindByPhone compara por teléfono normalizado, igual que el índice de pgstore.
func (f *FakeClients) FindByPhone(salonID, phone string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && domain.NormalizePhone(c.Phone) == phone {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

// FindByName es el respaldo sin teléfono: nombre exacto dentro del salón.
func (f *FakeClients) FindByName(salonID, name string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && c.Name == name {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *FakeClients) List(salonID string) ([]domain.Client, error) {
	var out []domain.Client
	for _, c := range f.Clients {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *FakeClients) SavePhoto(p domain.ClientPhoto) error {
	if f.FailSavePhoto {
		return errors.New("db caída")
	}
	f.Photos = append(f.Photos, p)
	return nil
}

func (f *FakeClients) ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error) {
	var out []domain.ClientPhoto
	for _, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *FakeClients) DeletePhoto(salonID, clientID, id string) error {
	for i, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			f.Photos = append(f.Photos[:i], f.Photos[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *FakeClients) FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error) {
	for _, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

func (f *FakeClients) PhotoByID(id string) (domain.ClientPhoto, error) {
	for _, p := range f.Photos {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

var _ ports.ClientRepository = (*FakeClients)(nil)

// FakeSettings es la configuración del salón en memoria: sin fila, ErrNotFound
// (los casos de uso aplican los valores de fábrica).
type FakeSettings struct {
	BySalon map[string]domain.SalonSettings
	// Err simula una DB caída al leer la configuración.
	Err error
}

func (f *FakeSettings) Settings(salonID string) (domain.SalonSettings, error) {
	if f.Err != nil {
		return domain.SalonSettings{}, f.Err
	}
	if s, ok := f.BySalon[salonID]; ok {
		return s, nil
	}
	return domain.SalonSettings{}, domain.ErrNotFound
}

func (f *FakeSettings) SaveSettings(s domain.SalonSettings) error {
	if f.BySalon == nil {
		f.BySalon = map[string]domain.SalonSettings{}
	}
	f.BySalon[s.SalonID] = s
	return nil
}

// SaveSettingsGuarded es el camino de la API: sin liquidaciones en el fake se
// comporta igual que SaveSettings (la zona congelada se prueba contra Postgres).
func (f *FakeSettings) SaveSettingsGuarded(s domain.SalonSettings) error {
	return f.SaveSettings(s)
}

var _ ports.SalonSettingsRepository = (*FakeSettings)(nil)

// Miembro siembra y devuelve la membresía del actor (lo que el handler ya cargó).
func Miembro(m *FakeMemberships, userID, salonID string, role domain.Role) domain.Membership {
	_ = m.Save(domain.Membership{UserID: userID, SalonID: salonID, Role: role})
	got, err := m.Find(userID, salonID)
	if err != nil {
		panic(err)
	}
	return got
}

// WantMsg exige el error de contrato: la familia (errors.Is) Y el texto exacto.
func WantMsg(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("se esperaba el error %q", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error %q (%v), quería %q de la familia %v", err.Error(), err, text, kind)
	}
}

// FakeUsers es el repositorio de cuentas en memoria: índice por email en
// minúsculas (la misma normalización que el registro) y búsqueda por id.
type FakeUsers struct{ ByEmail map[string]domain.User }

func (f *FakeUsers) Save(u domain.User) error {
	if f.ByEmail == nil {
		f.ByEmail = map[string]domain.User{}
	}
	f.ByEmail[strings.ToLower(u.Email)] = u
	return nil
}

func (f *FakeUsers) FindByEmail(email string) (domain.User, error) {
	u, ok := f.ByEmail[strings.ToLower(email)]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

// FindByID: lo usa GET /me (el token trae el userID, no el email).
func (f *FakeUsers) FindByID(id string) (domain.User, error) {
	for _, u := range f.ByEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

var _ ports.UserRepository = (*FakeUsers)(nil)

// FakeAudit es el registro de auditoría en memoria: scope por salón, orden
// (created_at, id) DESC, cursor estricto y el filtro de acción de
// domain.AuditActionMatches.
type FakeAudit struct {
	Mu        sync.Mutex
	Rows      []domain.AuditEvent
	AppendErr error
	// ListCalls cuenta las lecturas (el permiso va ANTES de tocar la base).
	ListCalls   int
	AppendCalls int
	LastQuery   domain.AuditQuery
}

func (f *FakeAudit) Append(e domain.AuditEvent) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.AppendCalls++
	if f.AppendErr != nil {
		return f.AppendErr
	}
	f.Rows = append(f.Rows, e)
	return nil
}

func (f *FakeAudit) List(salonID string, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.ListCalls++
	f.LastQuery = q
	var out []domain.AuditEvent
	for _, e := range f.Rows {
		if e.SalonID != salonID || !domain.AuditActionMatches(q.Action, e.Action) {
			continue
		}
		if q.Before != nil {
			b := q.Before
			if e.CreatedAt.After(b.CreatedAt) || (e.CreatedAt.Equal(b.CreatedAt) && e.ID >= b.ID) {
				continue
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

var _ ports.AuditRepository = (*FakeAudit)(nil)

// ErrAuditDown simula el registro de auditoría caído (un error que no debe
// tumbar la operación de negocio).
var ErrAuditDown = errors.New("audit down")

// FakeEvents es el bus de eventos en memoria: guarda todo lo publicado para que
// el test pueda contar y mirar el último de un tipo.
type FakeEvents struct{ All []ports.Event }

func (f *FakeEvents) Publish(ev ports.Event) error { f.All = append(f.All, ev); return nil }

// Typed cuenta los eventos de un tipo y devuelve el último.
func (f *FakeEvents) Typed(t string) (ports.Event, int) {
	var last ports.Event
	n := 0
	for _, ev := range f.All {
		if ev.Type == t {
			last, n = ev, n+1
		}
	}
	return last, n
}

var _ ports.EventPublisher = (*FakeEvents)(nil)
