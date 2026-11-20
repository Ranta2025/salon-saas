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
		out = append(out, domain.Member{ID: u.ID, Name: u.Name, Email: u.Email, Role: m.Role})
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
