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

// Deposits implementa los tres puertos de la sena de F11 (migración 037):
// DepositRepository, BookingNoticeRepository y BookingCanceller.
//
// Garantías duras (las mismas que declara el bloque F11 de ports):
//   - todo cambio de estado corre en UNA transacción bajo
//     `pg_advisory_xact_lock(hashtext('deposit:' || id))`: dos callbacks del
//     mismo cargo no se intercalan ni con el barrido;
//   - cada transición es UN UPDATE condicional por el estado de origen y, en la
//     MISMA transacción, inserta su evento en deposit_events. Si el UPDATE no
//     afecta filas no se inserta el evento;
//   - el índice único parcial de deposit_events.bank_event_id es la guarda de
//     «el evento del banco se aplica UNA sola vez»: 30 callbacks simultáneos del
//     mismo evento dejan un `paid` y un correo;
//   - el pago tardío NO escribe nada en ApplyApproved: quien llama reembolsa con
//     el banco y recién después cierra con SettleLateRefund;
//   - deposit_events es append-only (trigger de 037): nunca hay UPDATE ni DELETE.
type Deposits struct {
	Pool *pgxpool.Pool
	// TxTimeout acota cada transacción (0 = el tope por defecto de txtimeout.go).
	TxTimeout time.Duration
}

// depositColumns es la lista única de columnas de una sena. Los campos de solo
// lectura del turno (estado, inicio y nombre de la clienta) vienen del join: el
// caso de uso decide con ellos sin volver a consultar.
const depositColumns = `d.id,d.salon_id,d.booking_id,d.amount_cents,d.percent,d.status,d.charge_id,
	d.created_at,d.expires_at,d.paid_at,d.settled_at,COALESCE(d.client_op_id,''),
	b.status,b.starts_at,b.client_name`

const depositFrom = ` FROM booking_deposits d JOIN bookings b ON b.id = d.booking_id`

func scanDeposit(scan func(dest ...any) error) (domain.BookingDeposit, error) {
	var d domain.BookingDeposit
	var status, bookingStatus string
	if err := scan(&d.ID, &d.SalonID, &d.BookingID, &d.AmountCents, &d.Percent, &status, &d.ChargeID,
		&d.CreatedAt, &d.ExpiresAt, &d.PaidAt, &d.SettledAt, &d.ClientOpID,
		&bookingStatus, &d.StartsAt, &d.ClientName); err != nil {
		return domain.BookingDeposit{}, err
	}
	d.Status = status
	d.BookingStatus = domain.BookingStatus(bookingStatus)
	d.CreatedAt = d.CreatedAt.UTC()
	d.ExpiresAt = d.ExpiresAt.UTC()
	d.StartsAt = d.StartsAt.UTC()
	for _, t := range []**time.Time{&d.PaidAt, &d.SettledAt} {
		if *t != nil {
			at := (*t).UTC()
			*t = &at
		}
	}
	return d, nil
}

// depositLockKey es la clave del advisory lock por sena (D7/D9): el callback y
// el barrido se serializan con el mismo texto.
func depositLockKey(depositID string) string { return "deposit:" + depositID }

// lockDeposit toma el xact lock de la sena dentro de la transacción.
func lockDeposit(ctx context.Context, q querier, depositID string) error {
	_, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, depositLockKey(depositID))
	return err
}

// depositIDForCharge resuelve el id de la sena de un cargo (el lock se toma por
// el id de la sena, no por el del cargo).
func depositIDForCharge(ctx context.Context, q querier, chargeID string) (string, error) {
	var id string
	if err := q.QueryRow(ctx, `SELECT id FROM booking_deposits WHERE charge_id=$1`, chargeID).Scan(&id); err != nil {
		return "", notFound(err)
	}
	return id, nil
}

// readDepositByID lee una sena con su turno, ya scopeada por el id.
func readDepositByID(ctx context.Context, q querier, depositID string) (domain.BookingDeposit, error) {
	row := q.QueryRow(ctx, `SELECT `+depositColumns+depositFrom+` WHERE d.id=$1`, depositID)
	d, err := scanDeposit(row.Scan)
	if err != nil {
		return domain.BookingDeposit{}, notFound(err)
	}
	return d, nil
}

// bankEventSeen dice si ese evento del banco ya está en el libro mayor.
func bankEventSeen(ctx context.Context, q querier, bankEventID string) (bool, error) {
	if bankEventID == "" {
		return false, nil
	}
	var one int
	err := q.QueryRow(ctx, `SELECT 1 FROM deposit_events WHERE bank_event_id=$1`, bankEventID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// insertDepositEvent escribe UNA fila del libro mayor (append-only).
func insertDepositEvent(ctx context.Context, q querier, depositID, kind string, amountCents int, detail, bankEventID string, now time.Time) error {
	_, err := q.Exec(ctx,
		`INSERT INTO deposit_events(id,deposit_id,kind,amount_cents,detail,bank_event_id,created_at)
		 VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7)`,
		domain.NewID(), depositID, kind, amountCents, detail, bankEventID, now.UTC())
	return err
}

// isUniqueViolation dice si el error es un 23505 (el evento del banco ya entró).
func isUniqueViolation(err error) bool {
	return constraintOf(err) != ""
}

// Create inserta la sena y su evento `created` en UNA transacción (D8).
//
// Idempotente por las dos claves del contrato: con el MISMO booking_id (UNIQUE)
// o el MISMO client_op_id (índice único parcial por salón) devuelve la sena
// original SIN escribir nada y SIN error. Dos altas simultáneas: una gana y la
// otra recibe ErrAlreadyExists (el caso de uso vuelve a leer y devuelve la
// original; el cargo huérfano del banco vence solo).
func (r Deposits) Create(d domain.BookingDeposit) (domain.BookingDeposit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.BookingDeposit{}, err
	}
	defer rollbackTx(tx)

	// La lectura de la idempotencia va dentro de la transacción y ANTES del
	// INSERT: el índice único es la guarda final, no la única.
	row := tx.QueryRow(ctx, `SELECT `+depositColumns+depositFrom+`
		WHERE d.booking_id=$1
		   OR ($2::text <> '' AND d.salon_id=$3 AND d.client_op_id=$2)`,
		d.BookingID, d.ClientOpID, d.SalonID)
	if existing, err := scanDeposit(row.Scan); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.BookingDeposit{}, pgError(err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO booking_deposits(id,salon_id,booking_id,amount_cents,percent,status,charge_id,
		   created_at,expires_at,client_op_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''))`,
		d.ID, d.SalonID, d.BookingID, d.AmountCents, d.Percent, d.Status, d.ChargeID,
		d.CreatedAt.UTC(), d.ExpiresAt.UTC(), d.ClientOpID); err != nil {
		return domain.BookingDeposit{}, pgError(err)
	}
	if err := insertDepositEvent(ctx, tx, d.ID, domain.DepositEventCreated, d.AmountCents, "", "", d.CreatedAt); err != nil {
		return domain.BookingDeposit{}, pgError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.BookingDeposit{}, pgError(err)
	}
	return d, nil
}

// FindByBooking resuelve la sena de un turno del salón (la idempotencia de D8).
func (r Deposits) FindByBooking(salonID, bookingID string) (domain.BookingDeposit, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+depositColumns+depositFrom+` WHERE d.salon_id=$1 AND d.booking_id=$2`, salonID, bookingID)
	d, err := scanDeposit(row.Scan)
	if err != nil {
		return domain.BookingDeposit{}, notFound(err)
	}
	return d, nil
}

// FindByCharge resuelve una sena por el id del cargo. NO va scopeada por salón a
// propósito: el id del cargo es la credencial pública (D8) y el callback del
// banco no sabe de qué salón es.
func (r Deposits) FindByCharge(chargeID string) (domain.BookingDeposit, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+depositColumns+depositFrom+` WHERE d.charge_id=$1`, chargeID)
	d, err := scanDeposit(row.Scan)
	if err != nil {
		return domain.BookingDeposit{}, notFound(err)
	}
	return d, nil
}

// ApplyApproved aplica un evento `approved` (D7) bajo el lock de la sena.
func (r Deposits) ApplyApproved(cb domain.DepositCallback) (domain.DepositApply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.DepositApply{}, err
	}
	defer rollbackTx(tx)

	id, err := depositIDForCharge(ctx, tx, cb.ChargeID)
	if err != nil {
		return domain.DepositApply{}, err
	}
	if err := lockDeposit(ctx, tx, id); err != nil {
		return domain.DepositApply{}, pgError(err)
	}
	d, err := readDepositByID(ctx, tx, id)
	if err != nil {
		return domain.DepositApply{}, err
	}
	// Un evento del banco se aplica UNA sola vez (índice único por banco).
	if seen, err := bankEventSeen(ctx, tx, cb.EventID); err != nil {
		return domain.DepositApply{}, pgError(err)
	} else if seen {
		return domain.DepositApply{Deposit: d}, tx.Commit(ctx)
	}
	now := cb.Now.UTC()
	switch {
	case d.Payable(now):
		// UN UPDATE condicional: si otro camino ya la movió, no afecta filas y
		// no se escribe el evento.
		tag, err := tx.Exec(ctx,
			`UPDATE booking_deposits SET status=$2, paid_at=$3 WHERE id=$1 AND status=$4`,
			d.ID, domain.DepositPaid, now, domain.DepositPending)
		if err != nil {
			return domain.DepositApply{}, pgError(err)
		}
		if tag.RowsAffected() == 1 {
			if err := insertDepositEvent(ctx, tx, d.ID, domain.DepositEventPaid, d.AmountCents, "", cb.EventID, now); err != nil {
				if isUniqueViolation(err) {
					return domain.DepositApply{Deposit: d}, tx.Rollback(ctx)
				}
				return domain.DepositApply{}, pgError(err)
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.DepositApply{}, pgError(err)
			}
			paid := now
			d.Status, d.PaidAt = domain.DepositPaid, &paid
			return domain.DepositApply{Applied: true, Deposit: d}, nil
		}
		// Otro camino ganó la transición: se vuelve a leer y se decide con el
		// estado nuevo (vencida = pago tardío; el resto = nada que hacer).
		if d, err = readDepositByID(ctx, tx, id); err != nil {
			return domain.DepositApply{}, err
		}
		if d.LatePayment(now) {
			return domain.DepositApply{LatePayment: true, Deposit: d}, tx.Rollback(ctx)
		}
		return domain.DepositApply{Deposit: d}, tx.Rollback(ctx)
	case d.LatePayment(now):
		// Pago tardío: NO se escribe nada acá. Quien llama reembolsa y después
		// llama a SettleLateRefund.
		return domain.DepositApply{LatePayment: true, Deposit: d}, tx.Rollback(ctx)
	default:
		// Ya resuelta (paid/refunded/forfeited): no se escribe nada.
		return domain.DepositApply{Deposit: d}, tx.Rollback(ctx)
	}
}

// ApplyDeclined registra un intento rechazado y deja la sena `pending` (D7). La
// MISMA guarda de idempotencia por bank_event_id: ese evento se registra UNA vez.
func (r Deposits) ApplyDeclined(cb domain.DepositCallback) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTx(tx)

	id, err := depositIDForCharge(ctx, tx, cb.ChargeID)
	if err != nil {
		return false, err
	}
	if err := lockDeposit(ctx, tx, id); err != nil {
		return false, pgError(err)
	}
	if seen, err := bankEventSeen(ctx, tx, cb.EventID); err != nil {
		return false, pgError(err)
	} else if seen {
		return false, nil
	}
	err = insertDepositEvent(ctx, tx, id, domain.DepositEventAttemptDeclined, cb.AmountCents, "", cb.EventID, cb.Now.UTC())
	if err != nil {
		if isUniqueViolation(err) {
			return false, tx.Rollback(ctx)
		}
		return false, pgError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, pgError(err)
	}
	return true, nil
}

// ExpirePending vence hasta `limit` señas `pending` con expires_at <= now en UNA
// transacción (D9(a)). Devuelve además las que YA estaban `expired` con el turno
// todavía `booked`: el tick anterior no alcanzó a cancelarlo y el cupo no puede
// quedar retenido para siempre.
func (r Deposits) ExpirePending(now time.Time, limit int) ([]domain.BookingDeposit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)

	// Los candidatos se leen SIN row lock a propósito: el lock de la sena es el
	// advisory (el MISMO que toma el callback) y tomarlo después de un FOR UPDATE
	// invertiría el orden de locks con el callback (deadlock). El UPDATE de abajo
	// es condicional por estado, así que dos barridos simultáneos no se pisan.
	rows, err := tx.Query(ctx,
		`SELECT id FROM booking_deposits
		 WHERE status=$1 AND expires_at <= $2
		 ORDER BY expires_at, id
		 LIMIT $3`,
		domain.DepositPending, now.UTC(), limit)
	if err != nil {
		return nil, pgError(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, pgError(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, pgError(err)
	}

	out := make([]domain.BookingDeposit, 0, len(ids))
	expiredNow := make(map[string]bool, len(ids))
	for _, id := range ids {
		// El MISMO lock de la sena que usan ApplyApproved/Settle*: el callback y el
		// barrido no pueden intercalar el UPDATE de estado.
		if err := lockDeposit(ctx, tx, id); err != nil {
			return nil, pgError(err)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE booking_deposits SET status=$2 WHERE id=$1 AND status=$3`,
			id, domain.DepositExpired, domain.DepositPending)
		if err != nil {
			return nil, pgError(err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		d, err := readDepositByID(ctx, tx, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if err := insertDepositEvent(ctx, tx, id, domain.DepositEventExpired, d.AmountCents, "", "", now); err != nil {
			return nil, pgError(err)
		}
		expiredNow[id] = true
		out = append(out, d)
	}

	// Las que ya estaban vencidas y el turno sigue retenido (tick anterior
	// incompleto): el caso de uso reintenta la cancelación.
	rows, err = tx.Query(ctx,
		`SELECT `+depositColumns+depositFrom+`
		 WHERE d.status=$1 AND b.status=$2
		 ORDER BY d.created_at, d.id
		 LIMIT $3`,
		domain.DepositExpired, domain.BookingBooked, limit)
	if err != nil {
		return nil, pgError(err)
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDeposit(rows.Scan)
		if err != nil {
			return nil, pgError(err)
		}
		if expiredNow[d.ID] {
			continue
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, pgError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, pgError(err)
	}
	return out, nil
}

// ListPaidCancelled trae las señas `paid` cuyo turno quedó `cancelled` (D9(b)),
// las más viejas primero. `done` y `no_show` no entran nunca.
func (r Deposits) ListPaidCancelled(limit int) ([]domain.BookingDeposit, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+depositColumns+depositFrom+`
		 WHERE d.status=$1 AND b.status=$2
		 ORDER BY d.created_at, d.id
		 LIMIT $3`,
		domain.DepositPaid, domain.BookingCancelled, limit)
	if err != nil {
		return nil, pgError(err)
	}
	defer rows.Close()
	out := []domain.BookingDeposit{}
	for rows.Next() {
		d, err := scanDeposit(rows.Scan)
		if err != nil {
			return nil, pgError(err)
		}
		out = append(out, d)
	}
	return out, pgError(rows.Err())
}

// SettleLateRefund cierra una sena `pending` o `expired` (pago tardío, D7): UN
// UPDATE condicional a `refunded` con settled_at y el evento
// late_payment_refunded. El reembolso al banco lo pidió quien llama ANTES y es
// idempotente por cargo.
func (r Deposits) SettleLateRefund(s domain.DepositSettlement) (bool, error) {
	return r.settle(s, domain.DepositEventLatePaymentRefunded, `status IN ('pending','expired')`)
}

// SettlePaidCancelled cierra una sena `paid` cuyo turno se canceló (D9(b)).
func (r Deposits) SettlePaidCancelled(s domain.DepositSettlement) (bool, error) {
	event := domain.DepositEventRefunded
	if s.Status == domain.DepositForfeited {
		event = domain.DepositEventForfeited
	}
	return r.settle(s, event, `status = 'paid'`)
}

// settle es el cierre común: lock por sena, UPDATE condicional por el estado de
// origen y evento en la misma transacción.
func (r Deposits) settle(s domain.DepositSettlement, eventKind, origin string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackTx(tx)

	if err := lockDeposit(ctx, tx, s.DepositID); err != nil {
		return false, pgError(err)
	}
	d, err := readDepositByID(ctx, tx, s.DepositID)
	if err != nil {
		return false, err
	}
	// El evento del banco que dispara el cierre entra UNA sola vez: el reintento
	// del callback no vuelve a escribir ni vuelve a reembolsar.
	if seen, err := bankEventSeen(ctx, tx, s.BankEventID); err != nil {
		return false, pgError(err)
	} else if seen {
		return false, nil
	}
	tag, err := tx.Exec(ctx,
		`UPDATE booking_deposits SET status=$2, settled_at=$3 WHERE id=$1 AND `+origin,
		d.ID, s.Status, s.Now.UTC())
	if err != nil {
		return false, pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := insertDepositEvent(ctx, tx, d.ID, eventKind, d.AmountCents, s.Detail, s.BankEventID, s.Now); err != nil {
		if isUniqueViolation(err) {
			return false, tx.Rollback(ctx)
		}
		return false, pgError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, pgError(err)
	}
	return true, nil
}

// List trae una página de la pantalla «Señas» (D10): del más nuevo al más viejo
// por (created_at DESC, id DESC), ESTRICTAMENTE anterior al cursor, opcionalmente
// filtrada por estado y SIEMPRE scopeada por salon_id.
func (r Deposits) List(salonID string, q domain.DepositQuery) ([]domain.BookingDeposit, error) {
	var beforeAt *time.Time
	var beforeID *string
	if q.Before != nil {
		at := q.Before.CreatedAt.UTC()
		beforeAt, beforeID = &at, &q.Before.ID
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+depositColumns+depositFrom+`
		 WHERE d.salon_id=$1
		   AND ($2::text = '' OR d.status = $2::text)
		   AND ($3::timestamptz IS NULL OR (d.created_at, d.id) < ($3::timestamptz, $4::text))
		 ORDER BY d.created_at DESC, d.id DESC
		 LIMIT $5`,
		salonID, q.Status, beforeAt, beforeID, q.Limit)
	if err != nil {
		return nil, pgError(err)
	}
	defer rows.Close()
	out := []domain.BookingDeposit{}
	for rows.Next() {
		d, err := scanDeposit(rows.Scan)
		if err != nil {
			return nil, pgError(err)
		}
		out = append(out, d)
	}
	return out, pgError(rows.Err())
}

// Notice implementa ports.BookingNoticeRepository: lo que necesita el correo de
// confirmación de una sena pagada (D7). ClientEmail vacío = reserva anónima.
func (r Deposits) Notice(salonID, bookingID string) (domain.BookingNotice, error) {
	var n domain.BookingNotice
	err := r.Pool.QueryRow(context.Background(),
		`SELECT s.id, b.id, s.name, b.client_name, COALESCE(be.email,''), b.starts_at
		 FROM bookings b
		 JOIN salons s ON s.id = b.salon_id
		 LEFT JOIN booking_emails be ON be.booking_id = b.id
		 WHERE b.salon_id=$1 AND b.id=$2`, salonID, bookingID,
	).Scan(&n.SalonID, &n.BookingID, &n.SalonName, &n.ClientName, &n.ClientEmail, &n.StartsAt)
	if err != nil {
		return domain.BookingNotice{}, notFound(err)
	}
	n.StartsAt = n.StartsAt.UTC()
	return n, nil
}

// BookingCanceller implementa ports.BookingCanceller reusando el camino
// EXISTENTE de cancelación de la agenda (D9): se lee el turno, se aplica
// domain.Booking.SetStatus(cancelled) (la misma regla del panel) y se persiste
// con Bookings.Update, que toma el lock `booking-write:<salón>`. No reimplementa
// lógica de turnos: no hay INSERT ni UPDATE propio.
type BookingCanceller struct {
	Bookings Bookings
}

// CancelBooking: un turno ya cerrado es nil; uno inexistente también (su sena se
// fue en cascada). Cualquier otro error vuelve al barrido.
func (c BookingCanceller) CancelBooking(salonID, bookingID string, _ time.Time) error {
	b, err := c.Bookings.FindByID(salonID, bookingID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	if b.Status != domain.BookingBooked {
		return nil
	}
	cancelled, err := b.SetStatus(domain.BookingCancelled)
	if err != nil {
		return err
	}
	if err := c.Bookings.Update(cancelled); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

var (
	_ ports.DepositRepository       = Deposits{}
	_ ports.BookingNoticeRepository = Deposits{}
	_ ports.BookingCanceller        = BookingCanceller{}
)
