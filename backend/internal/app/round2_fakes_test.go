package app

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de la ronda 2: settings del salón y administración de miembros.
// Misma semántica que pgstore (scope por salón, ErrNotFound, upsert).

type fakeSettings struct {
	BySalon map[string]domain.SalonSettings
	// err simula una DB caída al leer la configuración (los recordatorios
	// tienen que salir igual, en UTC).
	err error
}

func (f *fakeSettings) Settings(salonID string) (domain.SalonSettings, error) {
	if f.err != nil {
		return domain.SalonSettings{}, f.err
	}
	if s, ok := f.BySalon[salonID]; ok {
		return s, nil
	}
	return domain.SalonSettings{}, domain.ErrNotFound
}

func (f *fakeSettings) SaveSettings(s domain.SalonSettings) error {
	if f.BySalon == nil {
		f.BySalon = map[string]domain.SalonSettings{}
	}
	f.BySalon[s.SalonID] = s
	return nil
}

// SaveSettingsGuarded es el camino de la API: sin liquidaciones en el fake, se
// comporta igual que SaveSettings (la garantía de la zona congelada se prueba
// contra Postgres real en los tests de integración).
func (f *fakeSettings) SaveSettingsGuarded(s domain.SalonSettings) error {
	return f.SaveSettings(s)
}

var _ ports.SalonSettingsRepository = (*fakeSettings)(nil)

// fakeMembers es la vista de administración del equipo sobre las memberships:
// lista con nombre/email (join con users), da de baja, reactiva y cuenta por rol.
type fakeMembers struct {
	mems  *fakeMemberships
	users *fakeUsers
	// future son los turnos futuros no cancelados por persona ("salon|user").
	future map[string]int
	// invitations son las pendientes de la base falsa: la baja revoca las que
	// emitió la persona, en la misma transacción.
	invitations []domain.Invitation
	// concurrentBaja es la clave "user|salon" de quien YA fue dado de baja por
	// la otra transacción mientras esta corría (la carrera de dos owners, F1).
	concurrentBaja string
}

func (f *fakeMembers) ListMembers(salonID string) ([]domain.Member, error) {
	out := []domain.Member{}
	for _, m := range f.mems.ByKey {
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
			Active: m.Active(), EndedAt: m.EndedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeMembers) DeleteMember(userID, salonID string) error {
	key := userID + "|" + salonID
	if _, ok := f.mems.ByKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(f.mems.ByKey, key)
	return nil
}

// SetAttends: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *fakeMembers) SetAttends(userID, salonID string, attends bool) error {
	key := userID + "|" + salonID
	m, ok := f.mems.ByKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Attends = attends
	f.mems.ByKey[key] = m
	return nil
}

// SetTrade: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *fakeMembers) SetTrade(userID, salonID, trade string) error {
	key := userID + "|" + salonID
	m, ok := f.mems.ByKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Trade = trade
	f.mems.ByKey[key] = m
	return nil
}

func (f *fakeMembers) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.mems.ByKey {
		if m.SalonID == salonID && m.Role == role && m.Active() {
			n++
		}
	}
	return n, nil
}

// FindMember incluye a quien fue dado de baja (la pantalla de equipo lo
// necesita para reactivar y para autorizar por rol).
func (f *fakeMembers) FindMember(userID, salonID string) (domain.Membership, error) {
	m, ok := f.mems.ByKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// DeactivateMember: UPDATE condicional por ended_at, como pgstore.
func (f *fakeMembers) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	key := userID + "|" + salonID
	m, ok := f.mems.ByKey[key]
	if !ok || !m.Active() {
		return domain.ErrNotFound
	}
	at := endedAt
	m.EndedAt = &at
	f.mems.ByKey[key] = m
	return nil
}

// ReactivateMember: UPDATE condicional ended_at IS NOT NULL.
func (f *fakeMembers) ReactivateMember(userID, salonID string) error {
	key := userID + "|" + salonID
	m, ok := f.mems.ByKey[key]
	if !ok || m.Active() {
		return domain.ErrNotFound
	}
	m.EndedAt = nil
	f.mems.ByKey[key] = m
	return nil
}

func (f *fakeMembers) CountFutureBookings(salonID, userID string, _ time.Time) (int, error) {
	return f.future[salonID+"|"+userID], nil
}

// InMemberTx: snapshot + rollback, como la transacción real.
func (f *fakeMembers) InMemberTx(fn func(tx ports.MemberTx) error) error {
	mems := map[string]domain.Membership{}
	for k, v := range f.mems.ByKey {
		mems[k] = v
	}
	invs := append([]domain.Invitation{}, f.invitations...)
	err := fn(fakeMemberTx{f})
	if err != nil {
		f.mems.ByKey = mems
		f.invitations = invs
	}
	return err
}

type fakeMemberTx struct{ f *fakeMembers }

func (t fakeMemberTx) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	if err := t.f.DeactivateMember(userID, salonID, endedAt); err != nil {
		return err
	}
	// La baja paralela ya commiteó: su owner dejó de estar activo antes del
	// conteo de adentro (así se ve la carrera sin goroutines en el test).
	if key := t.f.concurrentBaja; key != "" {
		other := t.f.mems.ByKey[key]
		at := endedAt
		other.EndedAt = &at
		t.f.mems.ByKey[key] = other
	}
	return nil
}

func (t fakeMemberTx) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	return t.f.CountFutureBookings(salonID, userID, now)
}

// CountRole: el mismo conteo que la lectura del adaptador (solo activos), pero
// leído DESPUÉS del UPDATE de la baja, como la query dentro de la transacción.
func (t fakeMemberTx) CountRole(salonID string, role domain.Role) (int, error) {
	return t.f.CountRole(salonID, role)
}

func (t fakeMemberTx) RevokeInvitationsBy(salonID, createdBy string, now time.Time) error {
	for i, inv := range t.f.invitations {
		if inv.SalonID != salonID || inv.CreatedBy != createdBy {
			continue
		}
		if inv.AcceptedAt != nil || inv.RevokedAt != nil {
			continue
		}
		at := now
		inv.RevokedAt = &at
		t.f.invitations[i] = inv
	}
	return nil
}

var (
	_ ports.MemberAdminRepository = (*fakeMembers)(nil)
	_ ports.MemberTxRunner        = (*fakeMembers)(nil)
)
