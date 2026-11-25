package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// LoyaltyPrograms implementa ports.LoyaltyProgramRepository: el programa de
// fidelidad del salón (F9, D3/D4). Toda query va scopeada por salon_id.
type LoyaltyPrograms struct {
	Pool *pgxpool.Pool
	// TxTimeout se reserva para operaciones de varios pasos (0 = default).
	TxTimeout time.Duration
}

const loyaltyProgramSelect = `SELECT lp.salon_id, lp.enabled, lp.visits_required, lp.reward_service_id, sv.name,
	lp.reward_validity_days, lp.counting_from, lp.updated_at
	FROM loyalty_programs lp JOIN services sv ON sv.id = lp.reward_service_id AND sv.salon_id = lp.salon_id`

func scanLoyaltyProgram(scan func(dest ...any) error) (domain.LoyaltyProgram, error) {
	var p domain.LoyaltyProgram
	if err := scan(&p.SalonID, &p.Enabled, &p.VisitsRequired, &p.RewardServiceID, &p.RewardServiceName,
		&p.ValidityDays, &p.CountingFrom, &p.UpdatedAt); err != nil {
		return domain.LoyaltyProgram{}, err
	}
	p.Saved = true
	p.CountingFrom, p.UpdatedAt = p.CountingFrom.UTC(), p.UpdatedAt.UTC()
	return p, nil
}

// Find lee el programa del salón con el nombre del servicio de regalo.
func (r LoyaltyPrograms) Find(salonID string) (domain.LoyaltyProgram, error) {
	row := r.Pool.QueryRow(context.Background(), loyaltyProgramSelect+` WHERE lp.salon_id=$1`, salonID)
	p, err := scanLoyaltyProgram(row.Scan)
	if err != nil {
		return domain.LoyaltyProgram{}, notFound(err)
	}
	return p, nil
}

// Save guarda el programa en UN upsert. El primer guardado fija counting_from;
// ON CONFLICT NO lo toca, así que los siguientes lo conservan (D2/D4). El
// INSERT ... SELECT exige que el servicio sea DEL SALÓN: la FK sola dejaría
// pasar el de otro tenant (0 filas => «Servicio inválido»).
func (r LoyaltyPrograms) Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error) {
	tag, err := r.Pool.Exec(context.Background(),
		`INSERT INTO loyalty_programs(salon_id,enabled,visits_required,reward_service_id,reward_validity_days,counting_from,updated_at)
		 SELECT $1,$2,$3,sv.id,$5,$6,$7 FROM services sv WHERE sv.id=$4 AND sv.salon_id=$1
		 ON CONFLICT (salon_id) DO UPDATE SET
		   enabled=EXCLUDED.enabled, visits_required=EXCLUDED.visits_required,
		   reward_service_id=EXCLUDED.reward_service_id,
		   reward_validity_days=EXCLUDED.reward_validity_days,
		   updated_at=EXCLUDED.updated_at`,
		p.SalonID, p.Enabled, p.VisitsRequired, p.RewardServiceID, p.ValidityDays, p.CountingFrom.UTC(), p.UpdatedAt.UTC())
	if err != nil {
		return domain.LoyaltyProgram{}, pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.LoyaltyProgram{}, domain.NewMessage(domain.ErrValidation, domain.MsgLoyaltyServiceInvalid)
	}
	return r.Find(p.SalonID)
}

// LoyaltyRewards implementa ports.LoyaltyRewardRepository: los vales entregados
// y las visitas de una clienta (F9, D2/D5/D6/D7).
//
// Toda query va scopeada por salon_id y client_id. Entregar corre en UNA
// transacción bajo un advisory lock por clienta y usar es UN UPDATE condicional;
// un 40P01/40001 se traduce a 409 (la transacción ya se deshizo: reintentar es
// seguro).
type LoyaltyRewards struct {
	Pool *pgxpool.Pool
	// TxTimeout acota cada transacción (0 = default).
	TxTimeout time.Duration
}

const loyaltyRewardColumns = `id,salon_id,client_id,reward_number,service_id,service_name,granted_at,expires_at,redeemed_at,
	COALESCE(redeemed_by,''),COALESCE(redeemed_professional_id,''),COALESCE(redeem_note,''),
	COALESCE(grant_op_id,''),COALESCE(redeem_op_id,'')`

func scanLoyaltyReward(scan func(dest ...any) error) (domain.LoyaltyReward, error) {
	var x domain.LoyaltyReward
	if err := scan(&x.ID, &x.SalonID, &x.ClientID, &x.RewardNumber, &x.ServiceID, &x.ServiceName, &x.GrantedAt,
		&x.ExpiresAt, &x.RedeemedAt, &x.RedeemedBy, &x.RedeemedProfessionalID, &x.RedeemNote,
		&x.GrantOpID, &x.RedeemOpID); err != nil {
		return domain.LoyaltyReward{}, err
	}
	x.GrantedAt = x.GrantedAt.UTC()
	for _, t := range []**time.Time{&x.ExpiresAt, &x.RedeemedAt} {
		if *t != nil {
			u := (*t).UTC()
			*t = &u
		}
	}
	return x, nil
}

// Visits cuenta las ventas de la clienta con created_at >= since y AL MENOS UNA
// línea de servicio en sale_items (D2). Es de SOLO LECTURA: no toca el flujo de
// ventas. Una venta solo de productos y la compra de un paquete no tienen
// líneas de servicio y no cuentan; dos líneas en una venta son UNA visita.
func (r LoyaltyRewards) Visits(salonID, clientID string, since time.Time) (int, error) {
	return countLoyaltyVisits(context.Background(), r.Pool, salonID, clientID, since)
}

func countLoyaltyVisits(ctx context.Context, q querier, salonID, clientID string, since time.Time) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM sales s
		 WHERE s.salon_id=$1 AND s.client_id=$2 AND s.created_at >= $3
		   AND EXISTS (SELECT 1 FROM sale_items si WHERE si.sale_id = s.id)`,
		salonID, clientID, since.UTC()).Scan(&n)
	return n, err
}

// CountGranted cuenta los vales ya entregados a la clienta (usados o no).
func (r LoyaltyRewards) CountGranted(salonID, clientID string) (int, error) {
	return countLoyaltyGranted(context.Background(), r.Pool, salonID, clientID)
}

func countLoyaltyGranted(ctx context.Context, q querier, salonID, clientID string) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM loyalty_rewards WHERE salon_id=$1 AND client_id=$2`, salonID, clientID).Scan(&n)
	return n, err
}

// ListByClient trae los vales de la clienta, los más nuevos primero; tope 100.
func (r LoyaltyRewards) ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+loyaltyRewardColumns+` FROM loyalty_rewards
		 WHERE salon_id=$1 AND client_id=$2
		 ORDER BY reward_number DESC LIMIT $3`, salonID, clientID, domain.MaxLoyaltyListed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.LoyaltyReward, 0, 8)
	for rows.Next() {
		x, err := scanLoyaltyReward(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// constraintOf devuelve el nombre de la restricción de un 23505, o "".
func constraintOf(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return pg.ConstraintName
	}
	return ""
}

func loyaltyOpReused() error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyOpReused)
}

// Grant entrega UN vale en UNA transacción (D6). Bajo
// pg_advisory_xact_lock(hashtext('loyalty:<salón>:<clienta>')) recalcula visitas
// y entregados y aplica domain.NewLoyaltyGrant: dos pedidos de la misma clienta
// no pueden leer el mismo «entregados». UNIQUE (client_id, reward_number) es la
// guarda FINAL: si algo se saltara el lock, el segundo INSERT falla con 23505 y
// responde «No hay premios disponibles», nunca dos vales por hito.
//
// Idempotencia por client_op_id (índice único parcial): el repetido devuelve el
// MISMO vale sin entregar otro; se detecta bajo el lock y, si dos pedidos
// gemelos igual llegaran al INSERT, por el 23505 de ese índice.
func (r LoyaltyRewards) Grant(q domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	defer rollbackTx(tx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		"loyalty:"+q.SalonID+":"+q.ClientID); err != nil {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	// La clienta tiene que ser DE ESTE salón: un id ajeno no entrega nada.
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM clients WHERE id=$1 AND salon_id=$2`, q.ClientID, q.SalonID).Scan(&one); err != nil {
		return domain.LoyaltyReward{}, notFound(err)
	}
	if q.ClientOpID != "" {
		if x, done, err := replayLoyaltyGrant(ctx, tx, q); err != nil || done {
			return x, err
		}
	}
	prog := domain.LoyaltyProgram{SalonID: q.SalonID}
	visits := 0
	row := tx.QueryRow(ctx, loyaltyProgramSelect+` WHERE lp.salon_id=$1`, q.SalonID)
	if p, err := scanLoyaltyProgram(row.Scan); err == nil {
		prog = p
		if visits, err = countLoyaltyVisits(ctx, tx, q.SalonID, q.ClientID, p.CountingFrom); err != nil {
			return domain.LoyaltyReward{}, packageWriteError(err)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	granted, err := countLoyaltyGranted(ctx, tx, q.SalonID, q.ClientID)
	if err != nil {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	reward, err := domain.NewLoyaltyGrant(prog, q.ClientID, visits, granted, q.ClientOpID, q.Now, q.Loc)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO loyalty_rewards(id,salon_id,client_id,reward_number,service_id,service_name,granted_at,expires_at,grant_op_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`,
		reward.ID, reward.SalonID, reward.ClientID, reward.RewardNumber, reward.ServiceID, reward.ServiceName,
		reward.GrantedAt, reward.ExpiresAt, reward.GrantOpID); err != nil {
		switch constraintOf(err) {
		case "loyalty_rewards_client_number_uniq":
			// Perdió la carrera por el hito: ya lo tiene otro vale.
			return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyNoReward)
		case "loyalty_rewards_grant_op_uniq":
			// Un pedido gemelo ganó: se devuelve SU vale, fuera de esta transacción.
			rollbackTx(tx)
			return r.replayGrantAfterRace(q)
		}
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	return reward, nil
}

// replayLoyaltyGrant mira si el client_op_id ya entregó un vale. done=true es
// «ya está resuelto»: devuelve el MISMO vale o el 409 de un id usado para otra
// clienta.
func replayLoyaltyGrant(ctx context.Context, q querier, req domain.LoyaltyGrantRequest) (domain.LoyaltyReward, bool, error) {
	row := q.QueryRow(ctx,
		`SELECT `+loyaltyRewardColumns+` FROM loyalty_rewards WHERE salon_id=$1 AND grant_op_id=$2`,
		req.SalonID, req.ClientOpID)
	x, err := scanLoyaltyReward(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.LoyaltyReward{}, false, nil
		}
		return domain.LoyaltyReward{}, true, packageWriteError(err)
	}
	if x.ClientID != req.ClientID {
		return domain.LoyaltyReward{}, true, loyaltyOpReused()
	}
	return x, true, nil
}

func (r LoyaltyRewards) replayGrantAfterRace(req domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	x, done, err := replayLoyaltyGrant(ctx, r.Pool, req)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	if !done {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyBusy)
	}
	return x, nil
}

// Redeem usa UN vale con UN UPDATE condicional (no usado y no vencido en q.Now),
// en UNA transacción (D7): dos usos simultáneos del mismo vale => UNO gana (el
// lock de fila serializa y el segundo reevalúa el WHERE). Sin filas afectadas, un
// SELECT ... FOR UPDATE de la MISMA transacción distingue 404 / ya usado /
// vencido.
//
// Idempotencia por client_op_id (índice único parcial de redeem_op_id): el
// repetido devuelve el estado actual SIN usar otra vez. Se detecta antes del
// UPDATE, cuando el UPDATE no afecta filas (el pedido gemelo acaba de usar el
// vale) y por el 23505 del índice.
func (r LoyaltyRewards) Redeem(q domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	defer rollbackTx(tx)

	if q.ClientOpID != "" {
		if x, done, err := replayLoyaltyRedeem(ctx, tx, q); err != nil || done {
			return x, err
		}
	}
	// Con el programa apagado (o nunca guardado) no se usan vales: los ya
	// entregados se conservan intactos.
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM loyalty_programs WHERE salon_id=$1`, q.SalonID).Scan(&enabled); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	if !enabled {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyDisabled)
	}
	const update = `UPDATE loyalty_rewards
		 SET redeemed_at=$4, redeemed_by=$5, redeemed_professional_id=NULLIF($6,''),
		     redeem_note=NULLIF($7,''), redeem_op_id=NULLIF($8,'')
		 WHERE id=$1 AND salon_id=$2 AND client_id=$3
		   AND redeemed_at IS NULL AND (expires_at IS NULL OR expires_at > $4)
		 RETURNING ` + loyaltyRewardColumns
	args := []any{q.RewardID, q.SalonID, q.ClientID, q.Now.UTC(), q.Actor, q.ProfessionalID, q.Note, q.ClientOpID}
	x, err := scanLoyaltyReward(tx.QueryRow(ctx, update, args...).Scan)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		if q.ClientOpID != "" {
			if x, done, err := replayLoyaltyRedeem(ctx, tx, q); err != nil || done {
				return x, err
			}
		}
		// El motivo se lee CON LOCK de la fila: no puede cambiar hasta el fin de
		// la transacción.
		if err := explainFailedLoyaltyRedeem(ctx, tx, q); err != nil {
			return domain.LoyaltyReward{}, err
		}
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyBusy)
	case constraintOf(err) == "loyalty_rewards_redeem_op_uniq":
		// Un pedido gemelo (u otro vale) ya tiene ese op id: la transacción se
		// deshace y se resuelve el repetido FUERA de ella.
		rollbackTx(tx)
		return r.replayRedeemAfterRace(q)
	default:
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LoyaltyReward{}, packageWriteError(err)
	}
	return x, nil
}

// replayLoyaltyRedeem mira si el client_op_id ya se usó. done=true es «ya está
// resuelto»: devuelve el estado actual de ESTE vale (sin usar otra vez) o el 409
// de un id usado para otro vale o para otra clienta.
func replayLoyaltyRedeem(ctx context.Context, q querier, req domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, bool, error) {
	row := q.QueryRow(ctx,
		`SELECT `+loyaltyRewardColumns+` FROM loyalty_rewards WHERE salon_id=$1 AND redeem_op_id=$2`,
		req.SalonID, req.ClientOpID)
	x, err := scanLoyaltyReward(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.LoyaltyReward{}, false, nil
		}
		return domain.LoyaltyReward{}, true, packageWriteError(err)
	}
	if x.ID != req.RewardID || x.ClientID != req.ClientID {
		return domain.LoyaltyReward{}, true, loyaltyOpReused()
	}
	return x, true, nil
}

func (r LoyaltyRewards) replayRedeemAfterRace(req domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	x, done, err := replayLoyaltyRedeem(ctx, r.Pool, req)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	if !done {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyBusy)
	}
	return x, nil
}

// explainFailedLoyaltyRedeem distingue POR QUÉ el UPDATE condicional no afectó
// filas: inexistente o de otra clienta/salón (404), ya usado o vencido (409).
// Usado gana sobre vencido: no hay nada que consumir. Corre en la MISMA
// transacción y bloquea la fila (FOR UPDATE). Un vale que ni está usado ni
// vencido (no debería pasar) es «chocó con otra operación».
func explainFailedLoyaltyRedeem(ctx context.Context, q querier, req domain.LoyaltyRedeemRequest) error {
	var redeemedAt, expiresAt *time.Time
	err := q.QueryRow(ctx,
		`SELECT redeemed_at, expires_at FROM loyalty_rewards
		 WHERE id=$1 AND salon_id=$2 AND client_id=$3 FOR UPDATE`,
		req.RewardID, req.SalonID, req.ClientID).Scan(&redeemedAt, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return packageWriteError(err)
	}
	switch {
	case redeemedAt != nil:
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyRewardUsed)
	case expiresAt != nil && !expiresAt.After(req.Now):
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyRewardExpired)
	default:
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyBusy)
	}
}

var (
	_ ports.LoyaltyProgramRepository = LoyaltyPrograms{}
	_ ports.LoyaltyRewardRepository  = LoyaltyRewards{}
)
