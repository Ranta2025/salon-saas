package testutil

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeMembers es la vista de administración del equipo sobre las memberships:
// lista con nombre/email (join con users), da de baja, reactiva y cuenta por
// rol. Es la copia exportada del fake de los tests de app.
type FakeMembers struct {
	Mems  *FakeMemberships
	Users *FakeUsers
	// Future son los turnos futuros no cancelados por persona ("salon|user").
	Future map[string]int
	// Invitations son las pendientes de la base falsa: la baja revoca las que
	// emitió la persona, en la misma transacción.
	Invitations []domain.Invitation
	// ConcurrentBaja es la clave "user|salon" de quien YA fue dado de baja por
	// la otra transacción mientras esta corría (la carrera de dos owners, F1).
	ConcurrentBaja string
}

func (f *FakeMembers) ListMembers(salonID string) ([]domain.Member, error) {
	out := []domain.Member{}
	for _, m := range f.Mems.ByKey {
		if m.SalonID != salonID {
			continue
		}
		u, err := f.Users.FindByID(m.UserID)
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

func (f *FakeMembers) DeleteMember(userID, salonID string) error {
	key := userID + "|" + salonID
	if _, ok := f.Mems.ByKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(f.Mems.ByKey, key)
	return nil
}

// SetAttends: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *FakeMembers) SetAttends(userID, salonID string, attends bool) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Attends = attends
	f.Mems.ByKey[key] = m
	return nil
}

// SetTrade: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *FakeMembers) SetTrade(userID, salonID, trade string) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Trade = trade
	f.Mems.ByKey[key] = m
	return nil
}

func (f *FakeMembers) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.Mems.ByKey {
		if m.SalonID == salonID && m.Role == role && m.Active() {
			n++
		}
	}
	return n, nil
}

// FindMember incluye a quien fue dado de baja (la pantalla de equipo lo
// necesita para reactivar y para autorizar por rol).
func (f *FakeMembers) FindMember(userID, salonID string) (domain.Membership, error) {
	m, ok := f.Mems.ByKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// DeactivateMember: UPDATE condicional por ended_at, como pgstore.
func (f *FakeMembers) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok || !m.Active() {
		return domain.ErrNotFound
	}
	at := endedAt
	m.EndedAt = &at
	f.Mems.ByKey[key] = m
	return nil
}

// ReactivateMember: UPDATE condicional ended_at IS NOT NULL.
func (f *FakeMembers) ReactivateMember(userID, salonID string) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok || m.Active() {
		return domain.ErrNotFound
	}
	m.EndedAt = nil
	f.Mems.ByKey[key] = m
	return nil
}

func (f *FakeMembers) CountFutureBookings(salonID, userID string, _ time.Time) (int, error) {
	return f.Future[salonID+"|"+userID], nil
}

// InMemberTx: snapshot + rollback, como la transacción real.
func (f *FakeMembers) InMemberTx(fn func(tx ports.MemberTx) error) error {
	mems := map[string]domain.Membership{}
	for k, v := range f.Mems.ByKey {
		mems[k] = v
	}
	invs := append([]domain.Invitation{}, f.Invitations...)
	err := fn(fakeMemberTx{f})
	if err != nil {
		f.Mems.ByKey = mems
		f.Invitations = invs
	}
	return err
}

type fakeMemberTx struct{ f *FakeMembers }

func (t fakeMemberTx) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	if err := t.f.DeactivateMember(userID, salonID, endedAt); err != nil {
		return err
	}
	// La baja paralela ya commiteó: su owner dejó de estar activo antes del
	// conteo de adentro (así se ve la carrera sin goroutines en el test).
	if key := t.f.ConcurrentBaja; key != "" {
		other := t.f.Mems.ByKey[key]
		at := endedAt
		other.EndedAt = &at
		t.f.Mems.ByKey[key] = other
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
	for i, inv := range t.f.Invitations {
		if inv.SalonID != salonID || inv.CreatedBy != createdBy {
			continue
		}
		if inv.AcceptedAt != nil || inv.RevokedAt != nil {
			continue
		}
		at := now
		inv.RevokedAt = &at
		t.f.Invitations[i] = inv
	}
	return nil
}

var (
	_ ports.MemberAdminRepository = (*FakeMembers)(nil)
	_ ports.MemberTxRunner        = (*FakeMembers)(nil)
)
