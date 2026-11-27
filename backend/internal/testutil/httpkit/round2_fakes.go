package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"time"
)

// Fakes de la ronda 2 (misma semántica que pgstore: scope por salón,
// ErrNotFound, upsert).

type Settings struct {
	BySalon map[string]domain.SalonSettings
	// frozen simula un salón con liquidaciones de nómina: el PUT que cambia la
	// zona horaria no escribe nada y devuelve ErrAlreadyExists (F3.3).
	Frozen map[string]bool
}

func (f *Settings) Settings(salonID string) (domain.SalonSettings, error) {
	if s, ok := f.BySalon[salonID]; ok {
		return s, nil
	}
	return domain.SalonSettings{}, domain.ErrNotFound
}

func (f *Settings) SaveSettings(s domain.SalonSettings) error {
	if f.BySalon == nil {
		f.BySalon = map[string]domain.SalonSettings{}
	}
	f.BySalon[s.SalonID] = s
	return nil
}

// SaveSettingsGuarded modela la zona congelada: con liquidaciones (frozen) el
// PUT rechaza el cambio con ErrAlreadyExists y no escribe nada.
func (f *Settings) SaveSettingsGuarded(s domain.SalonSettings) error {
	if f.Frozen[s.SalonID] {
		return domain.ErrAlreadyExists
	}
	return f.SaveSettings(s)
}

var _ ports.SalonSettingsRepository = (*Settings)(nil)

type MemberDir struct {
	Mems *Memberships
	// bookings cuenta los turnos futuros de la baja y inv revoca las
	// invitaciones pendientes que emitió quien se va (S-F2g2-4).
	Bookings    *Bookings
	Invitations *Invitations
	Users       *Users
}

// ListMembers incluye a quien fue dado de baja con su estado (S-F2g2-4): la
// pantalla de equipo decide cómo mostrarlo.
func (f *MemberDir) ListMembers(salonID string) ([]domain.Member, error) {
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

// FindMember resuelve la membresía INCLUYENDO a quien fue dado de baja, como la
// query de pgstore: es la única lectura que ve el estado anterior.
func (f *MemberDir) FindMember(userID, salonID string) (domain.Membership, error) {
	m, ok := f.Mems.ByKey[userID+"|"+salonID]
	if !ok {
		return domain.Membership{}, domain.ErrNotFound
	}
	return m, nil
}

// DeactivateMember es el UPDATE condicional por ended_at: una membresía ya dada
// de baja (o de otro salón) no afecta filas.
func (f *MemberDir) DeactivateMember(userID, salonID string, endedAt time.Time) error {
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

// ReactivateMember es el UPDATE condicional al revés: sin filas (ya activa o de
// otro salón) es ErrNotFound.
func (f *MemberDir) ReactivateMember(userID, salonID string) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok || m.Active() {
		return domain.ErrNotFound
	}
	m.EndedAt = nil
	f.Mems.ByKey[key] = m
	return nil
}

// CountFutureBookings cuenta los turnos `booked` que empiezan después de now de
// esa persona (la misma consulta que pgstore).
func (f *MemberDir) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	if f.Bookings == nil {
		return 0, nil
	}
	n := 0
	for _, b := range f.Bookings.All {
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

func (f *MemberDir) DeleteMember(userID, salonID string) error {
	key := userID + "|" + salonID
	if _, ok := f.Mems.ByKey[key]; !ok {
		return domain.ErrNotFound
	}
	delete(f.Mems.ByKey, key)
	return nil
}

// SetAttends: mismo scope que pgstore (una fila por salón); sin fila, ErrNotFound.
func (f *MemberDir) SetAttends(userID, salonID string, attends bool) error {
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
func (f *MemberDir) SetTrade(userID, salonID, trade string) error {
	key := userID + "|" + salonID
	m, ok := f.Mems.ByKey[key]
	if !ok {
		return domain.ErrNotFound
	}
	m.Trade = trade
	f.Mems.ByKey[key] = m
	return nil
}

// CountRole cuenta solo ACTIVOS, como pgstore: una baja no protege al último
// owner (si contara las filas dadas de baja, el dueño que queda solo nunca
// podría irse).
func (f *MemberDir) CountRole(salonID string, role domain.Role) (int, error) {
	n := 0
	for _, m := range f.Mems.ByKey {
		if m.SalonID == salonID && m.Role == role && m.Active() {
			n++
		}
	}
	return n, nil
}

// InMemberTx: snapshot + rollback, como la transacción real. Si la baja falla
// (turnos pendientes) no queda la membresía dada de baja ni una invitación
// revocada.
func (f *MemberDir) InMemberTx(fn func(tx ports.MemberTx) error) error {
	mems := map[string]domain.Membership{}
	for k, v := range f.Mems.ByKey {
		mems[k] = v
	}
	var invs map[string]domain.Invitation
	if f.Invitations != nil {
		invs = map[string]domain.Invitation{}
		for k, v := range f.Invitations.Rows {
			invs[k] = v
		}
	}
	err := fn(MemberTx{f})
	if err != nil {
		f.Mems.ByKey = mems
		if f.Invitations != nil {
			f.Invitations.Rows = invs
		}
	}
	return err
}

type MemberTx struct{ F *MemberDir }

func (t MemberTx) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	return t.F.DeactivateMember(userID, salonID, endedAt)
}

func (t MemberTx) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	return t.F.CountFutureBookings(salonID, userID, now)
}

// CountRole: solo activos, leído después del UPDATE de la baja (como la query
// dentro de la transacción real).
func (t MemberTx) CountRole(salonID string, role domain.Role) (int, error) {
	return t.F.CountRole(salonID, role)
}

// RevokeInvitationsBy: las pendientes que emitió esa persona dejan de servir en
// el MISMO commit que la baja (hallazgo de F2g1).
func (t MemberTx) RevokeInvitationsBy(salonID, createdBy string, now time.Time) error {
	if t.F.Invitations == nil {
		return nil
	}
	for id, row := range t.F.Invitations.Rows {
		if row.SalonID != salonID || row.CreatedBy != createdBy {
			continue
		}
		if row.AcceptedAt != nil || row.RevokedAt != nil {
			continue
		}
		at := now
		row.RevokedAt = &at
		t.F.Invitations.Rows[id] = row
	}
	return nil
}

var (
	_ ports.MemberAdminRepository = (*MemberDir)(nil)
	_ ports.MemberTxRunner        = (*MemberDir)(nil)
	_ ports.MemberTx              = MemberTx{}
)

// FailUnread simula la query del badge caída (R4-003): listar no puede fallar
// por el contador.
type FailUnread struct{ Inner *Notifications }

func (f FailUnread) Save(n domain.Notification) error { return f.Inner.Save(n) }
func (f FailUnread) List(salonID, userID string, limit int) ([]domain.Notification, error) {
	return f.Inner.List(salonID, userID, limit)
}
func (f FailUnread) MarkRead(salonID, userID, id string) error {
	return f.Inner.MarkRead(salonID, userID, id)
}
func (f FailUnread) UnreadCount(string, string) (int, error) { return 0, ErrFakeInfra }

var _ ports.NotificationRepository = FailUnread{}

// typed busca un evento por tipo (mismo helper que en app, para los tests HTTP).
func (f *Events) Typed(t string) (ports.Event, int) {
	n := 0
	var found ports.Event
	for _, ev := range f.All {
		if ev.Type == t {
			n++
			found = ev
		}
	}
	return found, n
}

// Professionals: qué servicios hace cada profesional (F2c), con la misma semántica que
// pgstore. Un servicio sin filas grabadas lo hace todo el equipo reservable:
// es lo que deja la migración 016 en una base con datos previos, y así los
// tests anteriores (los que asumen "todos hacen todo") siguen siendo válidos.
type Professionals struct {
	Mems      *Memberships
	ByService map[string][]string // "salonID|serviceID" -> userIDs
	ByMember  map[string][]string // "salonID|userID"    -> serviceIDs
}

func (f *Professionals) ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error) {
	key := salonID + "|" + serviceID
	ids, recorded := f.ByService[key]
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []domain.StaffMember
	for _, m := range f.Mems.ByKey {
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

func (f *Professionals) ListServiceIDs(salonID, userID string) ([]string, error) {
	return append([]string{}, f.ByMember[salonID+"|"+userID]...), nil
}

func (f *Professionals) SetServices(salonID, userID string, serviceIDs []string) error {
	if _, ok := f.Mems.ByKey[userID+"|"+salonID]; !ok {
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
	if f.ByMember == nil {
		f.ByMember = map[string][]string{}
	}
	if f.ByService == nil {
		f.ByService = map[string][]string{}
	}
	f.ByMember[salonID+"|"+userID] = out
	for _, id := range out {
		key := salonID + "|" + id
		found := false
		for _, who := range f.ByService[key] {
			if who == userID {
				found = true
			}
		}
		if !found {
			f.ByService[key] = append(f.ByService[key], userID)
		}
	}
	return nil
}

// assign graba quién hace el servicio (lista vacía = nadie).
func (f *Professionals) Assign(salonID, serviceID string, userIDs ...string) {
	if f.ByService == nil {
		f.ByService = map[string][]string{}
	}
	f.ByService[salonID+"|"+serviceID] = userIDs
}

var _ ports.ProfessionalServiceRepository = (*Professionals)(nil)
