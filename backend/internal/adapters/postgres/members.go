package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// MemberAdminRepository: la pantalla de equipo del salón. Todo va scopeado por
// salon_id: un membership de otro tenant no existe para estas queries.
//
// Bajas (F2g2): la fila NO se borra, se marca ended_at. Las lecturas
// COMPARTIDAS (Find, ListByUser, CountRole, ListStaff, ListProfessionalsFor,
// CountProfessionals, MembershipExists) filtran ended_at IS NULL: quien está
// dado de baja pierde el acceso en su próximo pedido y no aparece en ningún
// selector (S-F2g2-3). Las que son de esta pantalla (ListMembers, FindMember)
// sí ven la baja.

// ListMembers devuelve el equipo con nombre y email (join con users), ordenado
// por nombre como lo espera la pantalla. Incluye la marca de quién atiende
// (F2a): la pantalla la muestra y la cambia con SetAttends. Devuelve activos Y
// dados de baja con active/ended_at (S-F2g2-4): el frontend decide cómo los
// muestra.
func (r Memberships) ListMembers(salonID string) ([]domain.Member, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT u.id, u.name, u.email, m.role, m.attends, m.trade, m.ended_at
		 FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 WHERE m.salon_id=$1
		 ORDER BY u.name, u.id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Member, 0, 8)
	for rows.Next() {
		var role string
		var m domain.Member
		var endedAt *time.Time
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &role, &m.Attends, &m.Trade, &endedAt); err != nil {
			return nil, err
		}
		m.Role = domain.Role(role)
		m.EndedAt = utcOrNil(endedAt)
		m.Active = m.EndedAt == nil
		out = append(out, m)
	}
	return out, rows.Err()
}

// utcOrNil normaliza la fecha de la baja a UTC en el borde (el instante es el
// mismo, la forma que ve el cliente es estable) y deja nil para quien está
// activo.
func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// SetAttends enciende o apaga "atiende clientes" de un miembro. El WHERE lleva
// el salon_id: la marca de un miembro de otro tenant no se toca (ErrNotFound).
func (r Memberships) SetAttends(userID, salonID string, attends bool) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE memberships SET attends=$3 WHERE user_id=$1 AND salon_id=$2`,
		userID, salonID, attends)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetTrade guarda el oficio de un miembro. Mismo contrato que SetAttends: el
// WHERE lleva el salon_id y una fila de otro tenant es ErrNotFound.
func (r Memberships) SetTrade(userID, salonID, trade string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE memberships SET trade=$3 WHERE user_id=$1 AND salon_id=$2`,
		userID, salonID, trade)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteMember quita a alguien del salón. Si no era miembro de ESTE salón no
// afecta filas: ErrNotFound (no distingue "no existe" de "es de otro salón").
func (r Memberships) DeleteMember(userID, salonID string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM memberships WHERE user_id=$1 AND salon_id=$2`, userID, salonID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountRole cuenta miembros ACTIVOS con un rol dentro del salón (protección del
// último owner). Una baja no cuenta: el salón no puede quedarse sin dueño activo.
func (r Memberships) CountRole(salonID string, role domain.Role) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM memberships WHERE salon_id=$1 AND role=$2 AND ended_at IS NULL`,
		salonID, string(role)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// FindMember resuelve la membresía de la pantalla de equipo INCLUYENDO a quien
// fue dado de baja: es la única lectura que necesita ver el estado anterior
// (reactivar y la autorización por rol). `Memberships.Find` sigue devolviendo
// solo activas (S-F2g2-3).
func (r Memberships) FindMember(userID, salonID string) (domain.Membership, error) {
	var role, trade string
	var attends bool
	var endedAt *time.Time
	err := r.Pool.QueryRow(context.Background(),
		`SELECT role, attends, trade, ended_at FROM memberships WHERE user_id=$1 AND salon_id=$2`,
		userID, salonID).Scan(&role, &attends, &trade, &endedAt)
	if err != nil {
		return domain.Membership{}, notFound(err)
	}
	m, err := domain.NewMembership(userID, salonID, domain.Role(role))
	if err != nil {
		return domain.Membership{}, err
	}
	m.Attends = attends
	m.Trade = trade
	m.EndedAt = utcOrNil(endedAt)
	return m, nil
}

// DeactivateMember da de baja una membresía ACTIVA: el UPDATE condicional por
// ended_at es el row lock que serializa dos bajas simultáneas (una sola gana,
// la otra ve 0 filas -> ErrNotFound) y nunca mueve la fecha de una baja previa.
func (r Memberships) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	return deactivateMembership(r.Pool, userID, salonID, endedAt)
}

// deactivateMembership es la ÚNICA forma de dar de baja: la usan el adaptador y
// la transacción de la baja (con el mismo UPDATE condicional).
func deactivateMembership(q querier, userID, salonID string, endedAt time.Time) error {
	tag, err := q.Exec(context.Background(),
		`UPDATE memberships SET ended_at=$3 WHERE user_id=$1 AND salon_id=$2 AND ended_at IS NULL`,
		userID, salonID, endedAt.UTC())
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ReactivateMember vuelve a activar una membresía dada de baja (UPDATE
// condicional ended_at IS NOT NULL). Sin filas es ErrNotFound: ya estaba activa
// o es de otro salón. No cambia rol, attends ni trade.
func (r Memberships) ReactivateMember(userID, salonID string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE memberships SET ended_at=NULL WHERE user_id=$1 AND salon_id=$2 AND ended_at IS NOT NULL`,
		userID, salonID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountFutureBookings cuenta los turnos FUTUROS no cancelados de la persona en
// el salón: con al menos uno no se la puede dar de baja (409 del contrato). El
// status se compara por != 'cancelled' porque una cancelada libera el hueco.
func (r Memberships) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	return countFutureBookings(r.Pool, salonID, userID, now)
}

// countFutureBookings es la ÚNICA query del conteo: la comparten la lectura del
// adaptador y la transacción de la baja, donde corre DESPUÉS del UPDATE
// condicional y dentro de la misma transacción.
func countFutureBookings(q querier, salonID, userID string, now time.Time) (int, error) {
	var n int
	err := q.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM bookings
		 WHERE salon_id=$1 AND professional_id=$2 AND status <> 'cancelled' AND starts_at > $3`,
		salonID, userID, now.UTC()).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// InMemberTx abre la transacción de la baja: desactivar, contar los turnos
// futuros (el 409 revierte la baja) y revocar las invitaciones pendientes que
// emitió la persona commitean juntos o no pasan (S-F2g2-4).
//
// No toma lock por salón: la serialización la da el row lock del UPDATE
// condicional por ended_at, que es más fino. TxTimeout acota cuánto puede
// retener una conexión del pool una transacción trabada.
func (r Memberships) InMemberTx(fn func(tx ports.MemberTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx) // no-op si ya commiteó
	if err := fn(memberTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// memberTx es la vista transaccional de la baja: las mismas operaciones sobre la
// transacción abierta, nunca sobre el pool (si no, no serían atómicas).
type memberTx struct{ tx pgx.Tx }

func (t memberTx) DeactivateMember(userID, salonID string, endedAt time.Time) error {
	return deactivateMembership(t.tx, userID, salonID, endedAt)
}

func (t memberTx) CountFutureBookings(salonID, userID string, now time.Time) (int, error) {
	return countFutureBookings(t.tx, salonID, userID, now)
}

// RevokeInvitationsBy revoca las invitaciones PENDIENTES que emitió esa persona
// en ese salón (hallazgo de F2g1: hoy seguían vivas hasta 7 días aunque quien
// las emitió ya no estuviera). Mismo UPDATE condicional que la revocación
// individual: una aceptada o ya revocada no se toca, y el token viejo deja de
// servir. Sin filas no es error: quien no emitió nada no tiene nada que revocar.
func (t memberTx) RevokeInvitationsBy(salonID, createdBy string, now time.Time) error {
	_, err := t.tx.Exec(context.Background(),
		`UPDATE invitations SET revoked_at=$3
		 WHERE salon_id=$1 AND created_by=$2 AND accepted_at IS NULL AND revoked_at IS NULL`,
		salonID, createdBy, now.UTC())
	return pgError(err)
}

var _ ports.MemberAdminRepository = Memberships{}
var _ ports.MemberTxRunner = Memberships{}
var _ ports.MemberTx = memberTx{}
var _ ports.SalonSettingsRepository = Salons{}
