package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sync"
	"time"
)

// Invitations es el repositorio de invitaciones en memoria con la MISMA
// semántica que el adaptador Postgres: el token se busca hasheado, el canje es
// un UPDATE condicional y la transacción revierte todo si algo falla.
//
// `clock` deja adelantar el reloj para probar el vencimiento sin esperar 7 días.
type Invitations struct {
	Mu    sync.Mutex
	InTx  bool
	Clock func() time.Time
	Rows  map[string]domain.Invitation
	Users *Users
	Mems  *Memberships
	// failMembership fuerza el fallo del último paso del canje (el "todo o nada"
	// del caso de uso; la transacción real la prueba el test de Postgres).
	FailMembership bool
	Claims         int
}

func (f *Invitations) now() time.Time {
	if f.Clock != nil {
		return f.Clock()
	}
	return time.Now().UTC()
}

func (f *Invitations) lock() func() {
	if f.InTx {
		return func() {}
	}
	f.Mu.Lock()
	return f.Mu.Unlock
}

func (f *Invitations) ListPending(salonID string, now time.Time) ([]domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	out := []domain.Invitation{}
	for _, row := range f.Rows {
		if row.SalonID == salonID && row.Pending(now) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *Invitations) FindByID(salonID, id string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.Rows[id]
	if !ok || row.SalonID != salonID {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return row, nil
}

func (f *Invitations) FindByTokenHash(hash string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.Rows {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.Invitation{}, domain.ErrNotFound
}

func (f *Invitations) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	rows := map[string]domain.Invitation{}
	for k, v := range f.Rows {
		rows[k] = v
	}
	users := map[string]domain.User{}
	for k, v := range f.Users.ByEmail {
		users[k] = v
	}
	mems := map[string]domain.Membership{}
	for k, v := range f.Mems.ByKey {
		mems[k] = v
	}
	f.InTx = true
	err := fn(f)
	f.InTx = false
	if err != nil {
		f.Rows, f.Users.ByEmail, f.Mems.ByKey = rows, users, mems
	}
	return err
}

func (f *Invitations) CountPending(salonID string, now time.Time) (int, error) {
	unlock := f.lock()
	defer unlock()
	n := 0
	for _, row := range f.Rows {
		if row.SalonID == salonID && row.Pending(now) {
			n++
		}
	}
	return n, nil
}

func (f *Invitations) RevokePendingByEmail(salonID, email string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	for id, row := range f.Rows {
		if row.SalonID == salonID && row.Email == email && row.AcceptedAt == nil && row.RevokedAt == nil {
			at := now
			row.RevokedAt = &at
			f.Rows[id] = row
		}
	}
	return nil
}

func (f *Invitations) Save(i domain.Invitation) error {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.Rows {
		if row.TokenHash == i.TokenHash {
			return domain.ErrAlreadyExists
		}
		if row.SalonID == i.SalonID && row.Email == i.Email && row.AcceptedAt == nil && row.RevokedAt == nil {
			return domain.ErrAlreadyExists
		}
	}
	f.Rows[i.ID] = i
	return nil
}

func (f *Invitations) Claim(id, userID string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.Rows[id]
	if !ok || !row.Pending(f.now()) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	f.Claims++
	at := f.now()
	row.AcceptedAt = &at
	row.AcceptedBy = userID
	f.Rows[id] = row
	return row, nil
}

func (f *Invitations) Revoke(salonID, id string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	row, ok := f.Rows[id]
	if !ok || row.SalonID != salonID || !row.Pending(f.now()) {
		return domain.ErrNotFound
	}
	at := now
	row.RevokedAt = &at
	f.Rows[id] = row
	return nil
}

func (f *Invitations) SaveUser(u domain.User) error { return f.Users.Save(u) }

// MembershipExists dice si la persona ya es parte del salón. Una membresía dada
// de baja NO lo es (S-F2g2-4): aceptar la invitación la reactiva con el rol de la
// invitación, igual que el alta por email.
func (f *Invitations) MembershipExists(salonID, userID string) (bool, error) {
	unlock := f.lock()
	defer unlock()
	m, ok := f.Mems.ByKey[userID+"|"+salonID]
	return ok && m.Active(), nil
}

func (f *Invitations) SaveMembership(m domain.Membership) error {
	unlock := f.lock()
	defer unlock()
	if f.FailMembership {
		return domain.NewMessage(domain.ErrValidation, "una referencia del pedido no existe en el salón")
	}
	return f.Mems.Save(m)
}

var (
	_ ports.InvitationRepository = (*Invitations)(nil)
	_ ports.InvitationTxRunner   = (*Invitations)(nil)
)
