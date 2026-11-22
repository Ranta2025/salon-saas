package httpapi

import (
	"sort"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la ronda 2 (misma semántica que pgstore: scope por salón,
// ErrNotFound, upsert).

type msettings struct {
	bySalon map[string]domain.SalonSettings
}

func (f *msettings) Settings(salonID string) (domain.SalonSettings, error) {
	if s, ok := f.bySalon[salonID]; ok {
		return s, nil
	}
	return domain.SalonSettings{}, domain.ErrNotFound
}

func (f *msettings) SaveSettings(s domain.SalonSettings) error {
	if f.bySalon == nil {
		f.bySalon = map[string]domain.SalonSettings{}
	}
	f.bySalon[s.SalonID] = s
	return nil
}

var _ ports.SalonSettingsRepository = (*msettings)(nil)

type mmembers struct {
	mems  *mm
	users *mu
}

func (f *mmembers) ListMembers(salonID string) ([]domain.Member, error) {
	out := []domain.Member{}
	for _, m := range f.mems.byKey {
		if m.SalonID != salonID {
			continue
		}
		u, err := f.users.FindByID(m.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.Member{
			ID: u.ID, Name: u.Name, Email: u.Email, Role: m.Role,
			Attends: m.Attends, Trade: m.Trade,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *mmembers) DeleteMember(userID, salonID string) error {
	key := userID + "|" + salonID
	if _, ok := f.mems.byKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(f.mems.byKey, key)
	return nil
}

// SetAttends: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *mmembers) SetAttends(userID, salonID string, attends bool) error {
	key := userID + "|" + salonID
	m, ok := f.mems.byKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Attends = attends
	f.mems.byKey[key] = m
	return nil
}

// SetTrade: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *mmembers) SetTrade(userID, salonID, trade string) error {
	key := userID + "|" + salonID
	m, ok := f.mems.byKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Trade = trade
	f.mems.byKey[key] = m
	return nil
}

func (f *mmembers) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.mems.byKey {
		if m.SalonID == salonID && m.Role == role {
			n++
		}
	}
	return n, nil
}

var _ ports.MemberAdminRepository = (*mmembers)(nil)

// failUnread simula la query del badge caída (R4-003): listar no puede fallar
// por el contador.
type failUnread struct{ inner *mnotif }

func (f failUnread) Save(n domain.Notification) error { return f.inner.Save(n) }
func (f failUnread) List(salonID, userID string, limit int) ([]domain.Notification, error) {
	return f.inner.List(salonID, userID, limit)
}
func (f failUnread) MarkRead(salonID, userID, id string) error {
	return f.inner.MarkRead(salonID, userID, id)
}
func (f failUnread) UnreadCount(string, string) (int, error) { return 0, errFakeInfra }

var _ ports.NotificationRepository = failUnread{}

// typed busca un evento por tipo (mismo helper que en app, para los tests HTTP).
func (f *mevents) typed(t string) (ports.Event, int) {
	n := 0
	var found ports.Event
	for _, ev := range f.all {
		if ev.Type == t {
			n++
			found = ev
		}
	}
	return found, n
}

// mprof: qué servicios hace cada profesional (F2c), con la misma semántica que
// pgstore. Un servicio sin filas grabadas lo hace todo el equipo reservable:
// es lo que deja la migración 016 en una base con datos previos, y así los
// tests anteriores (los que asumen "todos hacen todo") siguen siendo válidos.
type mprof struct {
	mems      *mm
	byService map[string][]string // "salonID|serviceID" -> userIDs
	byMember  map[string][]string // "salonID|userID"    -> serviceIDs
}

func (f *mprof) ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error) {
	key := salonID + "|" + serviceID
	ids, recorded := f.byService[key]
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []domain.StaffMember
	for _, m := range f.mems.byKey {
		if m.SalonID != salonID || !m.Bookable() {
			continue
		}
		if recorded && !want[m.UserID] {
			continue
		}
		out = append(out, domain.StaffMember{
			ID: m.UserID, Name: m.UserID, Role: m.Role, Attends: m.Attends, Trade: m.Trade,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *mprof) ListServiceIDs(salonID, userID string) ([]string, error) {
	return append([]string{}, f.byMember[salonID+"|"+userID]...), nil
}

func (f *mprof) SetServices(salonID, userID string, serviceIDs []string) error {
	if _, ok := f.mems.byKey[userID+"|"+salonID]; !ok {
		return domain.ErrNotFound
	}
	uniq := map[string]bool{}
	out := make([]string, 0, len(serviceIDs))
	for _, id := range serviceIDs {
		if id == "" || uniq[id] {
			continue
		}
		uniq[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	if f.byMember == nil {
		f.byMember = map[string][]string{}
	}
	if f.byService == nil {
		f.byService = map[string][]string{}
	}
	f.byMember[salonID+"|"+userID] = out
	for _, id := range out {
		key := salonID + "|" + id
		found := false
		for _, who := range f.byService[key] {
			if who == userID {
				found = true
			}
		}
		if !found {
			f.byService[key] = append(f.byService[key], userID)
		}
	}
	return nil
}

// assign graba quién hace el servicio (lista vacía = nadie).
func (f *mprof) assign(salonID, serviceID string, userIDs ...string) {
	if f.byService == nil {
		f.byService = map[string][]string{}
	}
	f.byService[salonID+"|"+serviceID] = userIDs
}

var _ ports.ProfessionalServiceRepository = (*mprof)(nil)
