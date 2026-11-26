package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Invitations es el adaptador de invitaciones de equipo (F2g1).
//
// Dos garantías duras viven acá y no en el caso de uso:
//   - el canje es un UPDATE CONDICIONAL: dos canjes simultáneos del mismo token
//     se serializan con el row lock del UPDATE y el segundo ve 0 filas
//     (ErrNotFound -> el 404 genérico del contrato);
//   - una sola pendiente por (salón, email): el índice único parcial rechaza la
//     segunda con ErrAlreadyExists.
//
// El token en claro no aparece en ninguna query: se busca y se guarda su SHA-256.
type Invitations struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción del canje y de la emisión (H7): sin tope,
	// una transacción trabada deja el pedido esperando para siempre con una
	// conexión del pool tomada.
	TxTimeout time.Duration
}

// invitationColumns es la lista única de columnas. accepted_at y revoked_at son
// NULL hasta que pasan (se escanean en punteros); el resto es NOT NULL con
// default ”, así el escaneo no necesita tipos anulables.
const invitationColumns = `id,salon_id,email,role,attends,token_hash,expires_at,accepted_at,accepted_by,revoked_at,created_by,created_at`

func scanInvitation(scan func(...any) error) (domain.Invitation, error) {
	var inv domain.Invitation
	var role string
	if err := scan(&inv.ID, &inv.SalonID, &inv.Email, &role, &inv.Attends, &inv.TokenHash,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.AcceptedBy, &inv.RevokedAt, &inv.CreatedBy, &inv.CreatedAt); err != nil {
		return domain.Invitation{}, err
	}
	inv.Role = domain.Role(role)
	// Normalizar a UTC en el borde (el instante es el mismo, la forma es estable).
	inv.ExpiresAt = inv.ExpiresAt.UTC()
	inv.CreatedAt = inv.CreatedAt.UTC()
	return inv, nil
}

// ListPending trae las pendientes del salón: ni aceptadas, ni revocadas, ni
// vencidas. El scope por salon_id es la autorización del listado.
func (r Invitations) ListPending(salonID string, now time.Time) ([]domain.Invitation, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+invitationColumns+` FROM invitations
		 WHERE salon_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		 ORDER BY created_at, id`, salonID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Invitation, 0, 8)
	for rows.Next() {
		inv, err := scanInvitation(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// FindByID resuelve una invitación del salón: el id de otro tenant no existe.
func (r Invitations) FindByID(salonID, id string) (domain.Invitation, error) {
	inv, err := scanInvitation(r.Pool.QueryRow(context.Background(),
		`SELECT `+invitationColumns+` FROM invitations WHERE salon_id=$1 AND id=$2`, salonID, id).Scan)
	if err != nil {
		return domain.Invitation{}, notFound(err)
	}
	return inv, nil
}

// FindByTokenHash es la ÚNICA query sin salon_id: el hash del token es la
// capability que resuelve la invitación antes de saber de qué salón es.
func (r Invitations) FindByTokenHash(tokenHash string) (domain.Invitation, error) {
	inv, err := scanInvitation(r.Pool.QueryRow(context.Background(),
		`SELECT `+invitationColumns+` FROM invitations WHERE token_hash=$1`, tokenHash).Scan)
	if err != nil {
		return domain.Invitation{}, notFound(err)
	}
	return inv, nil
}

// InInvitationTx abre la transacción de las invitaciones y commitea SOLO si fn
// termina bien. No toma advisory lock: la serialización del canje es el row lock
// del UPDATE condicional (Claim), que es más fino que un lock por salón.
func (r Invitations) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx) // no-op si ya commiteó
	if err := fn(invitationTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// invitationTx es la vista transaccional: las mismas operaciones sobre la
// transacción abierta.
type invitationTx struct{ tx pgx.Tx }

func (t invitationTx) CountPending(salonID string, now time.Time) (int, error) {
	var n int
	err := t.tx.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM invitations
		 WHERE salon_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2`,
		salonID, now).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// RevokePendingByEmail revoca las pendientes de ese email en el salón. Reinvidar
// pasa por acá primero: sin esto el índice único parcial rechazaría la nueva.
func (t invitationTx) RevokePendingByEmail(salonID, email string, now time.Time) error {
	_, err := t.tx.Exec(context.Background(),
		`UPDATE invitations SET revoked_at=$3
		 WHERE salon_id=$1 AND lower(email)=lower($2) AND accepted_at IS NULL AND revoked_at IS NULL`,
		salonID, email, now)
	return err
}

func (t invitationTx) Save(i domain.Invitation) error {
	return saveInvitation(t.tx, i)
}

func saveInvitation(q querier, i domain.Invitation) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO invitations(id,salon_id,email,role,attends,token_hash,expires_at,created_by,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		i.ID, i.SalonID, i.Email, string(i.Role), i.Attends, i.TokenHash, i.ExpiresAt, i.CreatedBy, i.CreatedAt)
	// 23505: token repetido o dos pendientes del mismo email (el índice parcial).
	return pgError(err)
}

// Claim es el canje atómico: se lleva la invitación SOLO si sigue pendiente y
// vigente, y la devuelve ya aceptada. Sin filas es ErrNotFound (el caso de uso
// lo traduce al 404 genérico, sin oráculo).
func (t invitationTx) Claim(id, userID string) (domain.Invitation, error) {
	inv, err := scanInvitation(t.tx.QueryRow(context.Background(),
		`UPDATE invitations SET accepted_at=now(), accepted_by=$2
		 WHERE id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		 RETURNING `+invitationColumns, id, userID).Scan)
	if err != nil {
		return domain.Invitation{}, notFound(err)
	}
	return inv, nil
}

// Revoke da de baja una pendiente del salón. Una ya aceptada, revocada o de otro
// salón no afecta filas: ErrNotFound.
func (t invitationTx) Revoke(salonID, id string, now time.Time) error {
	tag, err := t.tx.Exec(context.Background(),
		`UPDATE invitations SET revoked_at=$3
		 WHERE salon_id=$1 AND id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`,
		salonID, id, now)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SaveUser crea la cuenta de quien acepta la invitación (mismo INSERT que
// Users.Save: un solo lugar decide cómo nace una cuenta).
func (t invitationTx) SaveUser(u domain.User) error { return saveUser(t.tx, u) }

// TakeOverShell es la toma del cascarón de F15 (D8) DENTRO de la transacción del
// canje: la invitación llegó a ese correo, así que quien la abre demuestra que es
// el dueño de la dirección.
//
// Condiciones (las tres, o no se toma nada): la cuenta existe, NO está
// verificada y no tiene ninguna membresía ACTIVA (una membresía dada de baja no
// cuenta: es el caso de quien registró el correo ajeno y nunca entró a ningún
// salón). Se hace con `SELECT ... FOR UPDATE` sobre la fila del usuario para que
// dos canjes simultáneos del mismo correo no se pisen, y el UPDATE escribe el
// MISMO instante en `password_changed_at` y `email_verified_at`.
//
// false = ya no es un cascarón (nada cambió): el caso de uso responde el 409 de
// siempre.
func (t invitationTx) TakeOverShell(userID, name, passHash string) (bool, error) {
	ctx := context.Background()
	var id string
	err := t.tx.QueryRow(ctx,
		`SELECT id FROM users WHERE id=$1 AND email_verified_at IS NULL FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var active bool
	if err := t.tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id=$1 AND ended_at IS NULL)`, userID).Scan(&active); err != nil {
		return false, err
	}
	if active {
		return false, nil
	}
	var at time.Time
	if err := t.tx.QueryRow(ctx, `SELECT now()`).Scan(&at); err != nil {
		return false, err
	}
	tag, err := t.tx.Exec(ctx,
		`UPDATE users SET name=$2, pass_hash=$3, password_changed_at=$4, email_verified_at=$4 WHERE id=$1`,
		userID, name, passHash, at)
	if err != nil {
		return false, pgError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// MembershipExists dice si la persona ya es parte ACTIVA del salón. Una baja no
// cuenta: canjear una invitación a quien estaba dado de baja lo REACTIVA con el
// rol de la invitación (el upsert de SaveMembership limpia ended_at), que es lo
// que pide S-F2g2-4.
func (t invitationTx) MembershipExists(salonID, userID string) (bool, error) {
	var exists bool
	err := t.tx.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id=$1 AND salon_id=$2 AND ended_at IS NULL)`,
		userID, salonID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// SaveMembership crea la membresía con el rol y la marca de la invitación
// (mismo upsert que Memberships.Save: una sola forma de escribir una membresía).
func (t invitationTx) SaveMembership(m domain.Membership) error {
	return saveMembership(t.tx, m)
}

var (
	_ ports.InvitationRepository = Invitations{}
	_ ports.InvitationTxRunner   = Invitations{}
	_ ports.InvitationTx         = invitationTx{}
	// F15/D8: la toma del cascarón es OPCIONAL en la transacción (los fakes de
	// las pruebas existentes no la implementan y el caso de uso cae al 409).
	_ ports.ShellTakeoverTx = invitationTx{}
)
