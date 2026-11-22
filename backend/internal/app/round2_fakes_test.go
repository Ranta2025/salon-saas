package app

import (
	"sort"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la ronda 2: settings del salón y administración de miembros.
// Misma semántica que pgstore (scope por salón, ErrNotFound, upsert).

type fakeSettings struct {
	bySalon map[string]domain.SalonSettings
	// err simula una DB caída al leer la configuración (los recordatorios
	// tienen que salir igual, en UTC).
	err error
}

func (f *fakeSettings) Settings(salonID string) (domain.SalonSettings, error) {
	if f.err != nil {
		return domain.SalonSettings{}, f.err
	}
	if s, ok := f.bySalon[salonID]; ok {
		return s, nil
	}
	return domain.SalonSettings{}, domain.ErrNotFound
}

func (f *fakeSettings) SaveSettings(s domain.SalonSettings) error {
	if f.bySalon == nil {
		f.bySalon = map[string]domain.SalonSettings{}
	}
	f.bySalon[s.SalonID] = s
	return nil
}

var _ ports.SalonSettingsRepository = (*fakeSettings)(nil)

// fakeMembers es la vista de administración del equipo sobre las memberships:
// lista con nombre/email (join con users), borra y cuenta por rol.
type fakeMembers struct {
	mems  *fakeMemberships
	users *fakeUsers
}

func (f *fakeMembers) ListMembers(salonID string) ([]domain.Member, error) {
	out := []domain.Member{}
	for _, m := range f.mems.byKey {
		if m.SalonID != salonID {
			continue
		}
		u, err := f.users.FindByID(m.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.Member{ID: u.ID, Name: u.Name, Email: u.Email, Role: m.Role, Attends: m.Attends})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeMembers) DeleteMember(userID, salonID string) error {
	key := userID + "|" + salonID
	if _, ok := f.mems.byKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(f.mems.byKey, key)
	return nil
}

// SetAttends: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *fakeMembers) SetAttends(userID, salonID string, attends bool) error {
	key := userID + "|" + salonID
	m, ok := f.mems.byKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Attends = attends
	f.mems.byKey[key] = m
	return nil
}

func (f *fakeMembers) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.mems.byKey {
		if m.SalonID == salonID && m.Role == role {
			n++
		}
	}
	return n, nil
}

var _ ports.MemberAdminRepository = (*fakeMembers)(nil)
