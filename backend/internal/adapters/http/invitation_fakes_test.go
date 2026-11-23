package httpapi

import (
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// minvitations es el repositorio de invitaciones en memoria con la MISMA
// semántica que el adaptador Postgres: el token se busca hasheado, el canje es
// un UPDATE condicional y la transacción revierte todo si algo falla.
//
// `clock` deja adelantar el reloj para probar el vencimiento sin esperar 7 días.
type minvitations struct {
	mu    sync.Mutex
	inTx  bool
	clock func() time.Time
	rows  map[string]domain.Invitation
	users *mu
	mems  *mm
	// failMembership fuerza el fallo del último paso del canje (el "todo o nada"
	// del caso de uso; la transacción real la prueba el test de Postgres).
	failMembership bool
	claims         int
}

func (f *minvitations) now() time.Time {
	if f.clock != nil {
		return f.clock()
	}
	return time.Now().UTC()
}

func (f *minvitations) lock() func() {
	if f.inTx {
		return func() {}
	}
	f.mu.Lock()
	return f.mu.Unlock
}

func (f *minvitations) ListPending(salonID string, now time.Time) ([]domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	out := []domain.Invitation{}
	for _, row := range f.rows {
		if row.SalonID == salonID && row.Pending(now) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *minvitations) FindByID(salonID, id string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || row.SalonID != salonID {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return row, nil
}

func (f *minvitations) FindByTokenHash(hash string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.rows {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.Invitation{}, domain.ErrNotFound
}

func (f *minvitations) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := map[string]domain.Invitation{}
	for k, v := range f.rows {
		rows[k] = v
	}
	users := map[string]domain.User{}
	for k, v := range f.users.byEmail {
		users[k] = v
	}
	mems := map[string]domain.Membership{}
	for k, v := range f.mems.byKey {
		mems[k] = v
	}
	f.inTx = true
	err := fn(f)
	f.inTx = false
	if err != nil {
		f.rows, f.users.byEmail, f.mems.byKey = rows, users, mems
	}
	return err
}

func (f *minvitations) CountPending(salonID string, now time.Time) (int, error) {
	unlock := f.lock()
	defer unlock()
	n := 0
	for _, row := range f.rows {
		if row.SalonID == salonID && row.Pending(now) {
			n++
		}
	}
	return n, nil
}

func (f *minvitations) RevokePendingByEmail(salonID, email string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	for id, row := range f.rows {
		if row.SalonID == salonID && row.Email == email && row.AcceptedAt == nil && row.RevokedAt == nil {
			at := now
			row.RevokedAt = &at
			f.rows[id] = row
		}
	}
	return nil
}

func (f *minvitations) Save(i domain.Invitation) error {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.rows {
		if row.TokenHash == i.TokenHash {
			return domain.ErrAlreadyExists
		}
		if row.SalonID == i.SalonID && row.Email == i.Email && row.AcceptedAt == nil && row.RevokedAt == nil {
			return domain.ErrAlreadyExists
		}
	}
	f.rows[i.ID] = i
	return nil
}

func (f *minvitations) Claim(id, userID string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || !row.Pending(f.now()) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	f.claims++
	at := f.now()
	row.AcceptedAt = &at
	row.AcceptedBy = userID
	f.rows[id] = row
	return row, nil
}

func (f *minvitations) Revoke(salonID, id string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || row.SalonID != salonID || !row.Pending(f.now()) {
		return domain.ErrNotFound
	}
	at := now
	row.RevokedAt = &at
	f.rows[id] = row
	return nil
}

func (f *minvitations) SaveUser(u domain.User) error { return f.users.Save(u) }

// MembershipExists dice si la persona ya es parte del salón. Una membresía dada
// de baja NO lo es (S-F2g2-4): aceptar la invitación la reactiva con el rol de la
// invitación, igual que el alta por email.
func (f *minvitations) MembershipExists(salonID, userID string) (bool, error) {
	unlock := f.lock()
	defer unlock()
	m, ok := f.mems.byKey[userID+"|"+salonID]
	return ok && m.Active(), nil
}

func (f *minvitations) SaveMembership(m domain.Membership) error {
	unlock := f.lock()
	defer unlock()
	if f.failMembership {
		return domain.NewMessage(domain.ErrValidation, "una referencia del pedido no existe en el salón")
	}
	return f.mems.Save(m)
}

var (
	_ ports.InvitationRepository = (*minvitations)(nil)
	_ ports.InvitationTxRunner   = (*minvitations)(nil)
)
