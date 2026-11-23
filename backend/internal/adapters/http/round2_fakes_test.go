package httpapi

import (
	"sort"
	"time"

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
	mems *mm
	// bookings cuenta los turnos futuros de la baja y inv revoca las
	// invitaciones pendientes que emitió quien se va (S-F2g2-4).
	bookings *mb
	inv      *minvitations
	users    *mu
}

// ListMembers incluye a quien fue dado de baja con su estado (S-F2g2-4): la
// pantalla de equipo decide cómo mostrarlo.
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
			Active: m.Active(), EndedAt: m.EndedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FindMember resuelve la membresía INCLUYENDO a quien fue dado de baja, como la
// query de pgstore: es la única lectura que ve el estado anterior.
func (f *mmembers) FindMember(userID, salonID string) (domain.Membership, error) {
	m, ok := f.mems.byKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// DeactivateMember es el UPDATE condicional por ended_at: una membresía ya dada
// de baja (o de otro salón) no afecta filas.
func (f *mmembers) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	key := userID + "|" + salonID
	m, ok := f.mems.byKey[key]
	if !ok || !m.Active() {
		return domain.ErrNotFound
	}
	at := endedAt
	m.EndedAt = &at
	f.mems.byKey[key] = m
	return nil
}

// ReactivateMember es el UPDATE condicional al revés: sin filas (ya activa o de
// otro salón) es ErrNotFound.
func (f *mmembers) ReactivateMember(userID, salonID string) error {
	key := userID + "|" + salonID
	m, ok := f.mems.byKey[key]
	if !ok || m.Active() {
		return domain.ErrNotFound
	}
	m.EndedAt = nil
	f.mems.byKey[key] = m
	return nil
}

// CountFutureBookings cuenta los turnos `booked` que empiezan después de now de
// esa persona (la misma consulta que pgstore).
func (f *mmembers) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	if f.bookings == nil {
		return 0, nil
	}
	n := 0
	for _, b := range f.bookings.all {
		if b.SalonID != salonID || b.ProfessionalID != userID {
			continue
		}
		if b.Status != domain.BookingBooked || !b.StartsAt.After(now) {
			continue
		}
		n++
	}
	return n, nil
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

// CountRole cuenta solo ACTIVOS, como pgstore: una baja no protege al último
// owner (si contara las filas dadas de baja, el dueño que queda solo nunca
// podría irse).
func (f *mmembers) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.mems.byKey {
		if m.SalonID == salonID && m.Role == role && m.Active() {
			n++
		}
	}
	return n, nil
}

// InMemberTx: snapshot + rollback, como la transacción real. Si la baja falla
// (turnos pendientes) no queda la membresía dada de baja ni una invitación
// revocada.
func (f *mmembers) InMemberTx(fn func(tx ports.MemberTx) error) error {
	mems := map[string]domain.Membership{}
	for k, v := range f.mems.byKey {
		mems[k] = v
	}
	var invs map[string]domain.Invitation
	if f.inv != nil {
		invs = map[string]domain.Invitation{}
		for k, v := range f.inv.rows {
			invs[k] = v
		}
	}
	err := fn(mmemberTx{f})
	if err != nil {
		f.mems.byKey = mems
		if f.inv != nil {
			f.inv.rows = invs
		}
	}
	return err
}

type mmemberTx struct{ f *mmembers }

func (t mmemberTx) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	return t.f.DeactivateMember(userID, salonID, endedAt)
}

func (t mmemberTx) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	return t.f.CountFutureBookings(salonID, userID, now)
}

// CountRole: solo activos, leído después del UPDATE de la baja (como la query
// dentro de la transacción real).
func (t mmemberTx) CountRole(salonID string, role domain.Role) (int, error) {
	return t.f.CountRole(salonID, role)
}

// RevokeInvitationsBy: las pendientes que emitió esa persona dejan de servir en
// el MISMO commit que la baja (hallazgo de F2g1).
func (t mmemberTx) RevokeInvitationsBy(salonID, createdBy string, now time.Time) error {
	if t.f.inv == nil {
		return nil
	}
	for id, row := range t.f.inv.rows {
		if row.SalonID != salonID || row.CreatedBy != createdBy {
			continue
		}
		if row.AcceptedAt != nil || row.RevokedAt != nil {
			continue
		}
		at := now
		row.RevokedAt = &at
		t.f.inv.rows[id] = row
	}
	return nil
}

var (
	_ ports.MemberAdminRepository = (*mmembers)(nil)
	_ ports.MemberTxRunner        = (*mmembers)(nil)
	_ ports.MemberTx              = mmemberTx{}
)

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
