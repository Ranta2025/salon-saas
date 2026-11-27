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

// Packages implementa ports.PackageRepository: el catálogo de paquetes (F8, D3).
type Packages struct {
	Pool *pgxpool.Pool
	// TxTimeout se reserva para operaciones de varios pasos (0 = default).
	TxTimeout time.Duration
}

const packageColumns = `id,salon_id,name,service_id,sessions,price_cents,validity_days,active,created_at`

func scanServicePackage(scan func(dest ...any) error) (domain.ServicePackage, error) {
	var p domain.ServicePackage
	if err := scan(&p.ID, &p.SalonID, &p.Name, &p.ServiceID, &p.Sessions, &p.PriceCents,
		&p.ValidityDays, &p.Active, &p.CreatedAt); err != nil {
		return domain.ServicePackage{}, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

// Create inserta el paquete. El índice único de nombres activos traduce 23505
// a ErrAlreadyExists.
func (r Packages) Create(p domain.ServicePackage) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO service_packages(id,salon_id,name,service_id,sessions,price_cents,validity_days,active,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.SalonID, p.Name, p.ServiceID, p.Sessions, p.PriceCents, p.ValidityDays, p.Active, p.CreatedAt)
	return pgError(err)
}

// Save REEMPLAZA nombre, precio, vigencia (NULL incluida) y estado. El WHERE con
// salon_id es la autorización: un id de otro tenant no afecta filas.
func (r Packages) Save(p domain.ServicePackage) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE service_packages SET name=$3, price_cents=$4, validity_days=$5, active=$6
		 WHERE salon_id=$1 AND id=$2`,
		p.SalonID, p.ID, p.Name, p.PriceCents, p.ValidityDays, p.Active)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r Packages) FindByID(salonID, id string) (domain.ServicePackage, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+packageColumns+` FROM service_packages WHERE salon_id=$1 AND id=$2`, salonID, id)
	p, err := scanServicePackage(row.Scan)
	if err != nil {
		return domain.ServicePackage{}, notFound(err)
	}
	return p, nil
}

// List trae activos e inactivos ordenados por nombre, con el nombre del servicio.
func (r Packages) List(salonID string) ([]domain.ServicePackage, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT p.id,p.salon_id,p.name,p.service_id,p.sessions,p.price_cents,p.validity_days,p.active,p.created_at,sv.name
		 FROM service_packages p
		 JOIN services sv ON sv.id = p.service_id AND sv.salon_id = p.salon_id
		 WHERE p.salon_id=$1
		 ORDER BY lower(p.name), p.id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ServicePackage, 0, 8)
	for rows.Next() {
		var p domain.ServicePackage
		if err := rows.Scan(&p.ID, &p.SalonID, &p.Name, &p.ServiceID, &p.Sessions, &p.PriceCents,
			&p.ValidityDays, &p.Active, &p.CreatedAt, &p.ServiceName); err != nil {
			return nil, err
		}
		p.CreatedAt = p.CreatedAt.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClientPackages implementa ports.ClientPackageRepository: los paquetes comprados
// por una clienta y su libro de saldo (F8, D3/D4/D5/D7/D9).
//
// Toda query va scopeada por salon_id y client_id. Cada cambio de saldo escribe
// su movimiento en la MISMA transacción con tope de tiempo: sessions_left = Σ
// delta en todo momento. Un 40P01/40001 se traduce a 409 (la transacción ya se
// deshizo: reintentar es seguro).
type ClientPackages struct {
	Pool *pgxpool.Pool
	// TxTimeout acota cada transacción (0 = default).
	TxTimeout time.Duration
}

const clientPackageColumns = `cp.id,cp.salon_id,cp.client_id,cp.package_id,cp.service_id,sv.name,cp.name,
	cp.sessions_total,cp.sessions_left,cp.price_cents,cp.purchased_at,cp.expires_at,cp.sale_id`

const clientPackageFrom = `FROM client_packages cp JOIN services sv ON sv.id = cp.service_id`

func scanClientPackage(scan func(dest ...any) error) (domain.ClientPackage, error) {
	var c domain.ClientPackage
	if err := scan(&c.ID, &c.SalonID, &c.ClientID, &c.PackageID, &c.ServiceID, &c.ServiceName, &c.Name,
		&c.SessionsTotal, &c.SessionsLeft, &c.PriceCents, &c.PurchasedAt, &c.ExpiresAt, &c.SaleID); err != nil {
		return domain.ClientPackage{}, err
	}
	c.PurchasedAt = c.PurchasedAt.UTC()
	if c.ExpiresAt != nil {
		u := c.ExpiresAt.UTC()
		c.ExpiresAt = &u
	}
	return c, nil
}

// packageWriteError traduce el error de escribir. Un interbloqueo (40P01) o una
// serialización perdida (40001) son una carrera perdida, no un 500: la
// transacción ya se deshizo y reintentar es seguro.
func packageWriteError(err error) error { return BusyError(err, domain.MsgPackageBusy) }

// Purchase escribe la compra en UNA transacción: la venta (sin líneas), el
// paquete de la clienta y el movimiento `purchase`. El INSERT del paquete es un
// INSERT ... SELECT que EXIGE que la clienta, el paquete y el servicio sean del
// salón: un id de otro tenant no inserta nada y deshace también la venta. Un
// client_op_id repetido lo frena el índice único de `sales` (ErrAlreadyExists) y
// no deja nada escrito.
func (r ClientPackages) Purchase(o domain.PackagePurchaseOrder) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)

	s, cp, m := o.Sale, o.ClientPackage, o.Movement
	if _, err := tx.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,client_op_id,created_at,client_id,packages_cents)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		s.ID, s.SalonID, s.ProfessionalID, s.ClientName, s.TotalCents, string(s.Method),
		s.ClientOpID, s.CreatedAt, cp.ClientID, cp.PriceCents); err != nil {
		return packageWriteError(err)
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO client_packages(id,salon_id,client_id,package_id,service_id,name,sessions_total,price_cents,sessions_left,purchased_at,expires_at,sale_id)
		 SELECT $1,$2,c.id,p.id,sv.id,$5,$6,$7,$8,$9,$10,$11
		 FROM clients c, service_packages p, services sv
		 WHERE c.id=$3 AND c.salon_id=$2
		   AND p.id=$4 AND p.salon_id=$2
		   AND sv.id=p.service_id AND sv.salon_id=$2`,
		cp.ID, s.SalonID, cp.ClientID, cp.PackageID, cp.Name, cp.SessionsTotal, cp.PriceCents,
		cp.SessionsLeft, cp.PurchasedAt, cp.ExpiresAt, s.ID)
	if err != nil {
		return packageWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
	}
	if err := insertPackageMovement(ctx, tx, m); err != nil {
		return err
	}
	return packageWriteError(tx.Commit(ctx))
}

// insertPackageMovement escribe una fila del libro (append-only). Las columnas
// opcionales viajan como NULL cuando no hay valor ("" en Go).
func insertPackageMovement(ctx context.Context, q querier, m domain.PackageMovement) error {
	_, err := q.Exec(ctx,
		`INSERT INTO package_movements(id,salon_id,client_package_id,delta,reason,note,professional_id,client_op_id,actor,created_at)
		 VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),$10)`,
		m.ID, m.SalonID, m.ClientPackageID, m.Delta, string(m.Reason), m.Note, m.ProfessionalID,
		m.ClientOpID, m.Actor, m.CreatedAt)
	return packageWriteError(err)
}

// FindPurchaseByOp resuelve la compra original de un client_op_id (el índice
// único parcial de sales es la garantía de que hay una sola). Sin venta con ese
// id: ErrNotFound. Con una venta que NO es una compra de paquete: ErrAlreadyExists
// (el id ya se usó para otra cosa).
func (r ClientPackages) FindPurchaseByOp(salonID, clientOpID string) (domain.ClientPackage, error) {
	ctx := context.Background()
	var saleID string
	err := r.Pool.QueryRow(ctx,
		`SELECT id FROM sales WHERE salon_id=$1 AND client_op_id=$2 AND client_op_id <> ''`,
		salonID, clientOpID).Scan(&saleID)
	if err != nil {
		return domain.ClientPackage{}, notFound(err)
	}
	row := r.Pool.QueryRow(ctx,
		`SELECT `+clientPackageColumns+` `+clientPackageFrom+`
		 WHERE cp.salon_id=$1 AND cp.sale_id=$2`, salonID, saleID)
	cp, err := scanClientPackage(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
		}
		return domain.ClientPackage{}, err
	}
	return cp, nil
}

// ListByClient trae los paquetes de la clienta: los vigentes con saldo primero y
// después el resto, cada grupo del más nuevo al más viejo; tope de 200.
func (r ClientPackages) ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+clientPackageColumns+` `+clientPackageFrom+`
		 WHERE cp.salon_id=$1 AND cp.client_id=$2
		 ORDER BY (CASE WHEN cp.sessions_left > 0 AND (cp.expires_at IS NULL OR cp.expires_at > $3) THEN 0 ELSE 1 END),
		          cp.purchased_at DESC, cp.id DESC
		 LIMIT $4`,
		salonID, clientID, now.UTC(), domain.MaxPackageListed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ClientPackage, 0, 8)
	for rows.Next() {
		cp, err := scanClientPackage(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}

// ListMovements trae el libro de UN paquete de la clienta, más nuevo primero
// (la compra es siempre la más vieja: desempata las fechas iguales), tope 200.
func (r ClientPackages) ListMovements(salonID, clientID, clientPackageID string) ([]domain.PackageMovement, error) {
	ctx := context.Background()
	var one int
	if err := r.Pool.QueryRow(ctx,
		`SELECT 1 FROM client_packages WHERE salon_id=$1 AND client_id=$2 AND id=$3`,
		salonID, clientID, clientPackageID).Scan(&one); err != nil {
		return nil, notFound(err)
	}
	rows, err := r.Pool.Query(ctx,
		`SELECT id, delta, reason, COALESCE(note,''), COALESCE(professional_id,''), created_at
		 FROM package_movements
		 WHERE salon_id=$1 AND client_package_id=$2
		 ORDER BY created_at DESC, (reason = 'purchase') ASC, id DESC
		 LIMIT $3`,
		salonID, clientPackageID, domain.MaxPackageListed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PackageMovement, 0, 8)
	for rows.Next() {
		var m domain.PackageMovement
		var reason string
		if err := rows.Scan(&m.ID, &m.Delta, &reason, &m.Note, &m.ProfessionalID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Reason = domain.PackageMovementReason(reason)
		m.SalonID, m.ClientPackageID = salonID, clientPackageID
		m.CreatedAt = m.CreatedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// Redeem descuenta UNA sesión con UN UPDATE condicional (saldo >= 1 y no
// vencido en q.Now) más el movimiento `redeem`, en UNA transacción: dos consumos
// simultáneos de la última sesión => UNO gana (el lock de fila serializa y el
// segundo reevalúa el WHERE). Sin filas afectadas, un SELECT de la MISMA
// transacción distingue 404 / sin saldo / vencido.
//
// Idempotencia por client_op_id (índice único parcial de package_movements): el
// repetido devuelve el estado actual SIN descontar. Se detecta en tres puntos y
// los tres terminan igual, sin descuento: (1) una consulta previa en la
// transacción; (2) el UPDATE sin filas, porque el pedido gemelo pudo consumir
// justo la última sesión; (3) el 23505 del INSERT del movimiento, que deshace
// también el descuento.
func (r ClientPackages) Redeem(q domain.PackageRedeemRequest) (domain.ClientPackage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	defer rollbackTx(tx)

	if q.ClientOpID != "" {
		if cp, done, err := r.replayRedeem(ctx, tx, q); err != nil || done {
			return cp, err
		}
	}
	const decrement = `UPDATE client_packages SET sessions_left = sessions_left - 1
		 WHERE id=$1 AND salon_id=$2 AND client_id=$3
		   AND sessions_left >= 1 AND (expires_at IS NULL OR expires_at > $4)`
	tag, err := tx.Exec(ctx, decrement, q.ClientPackageID, q.SalonID, q.ClientID, q.Now.UTC())
	if err != nil {
		return domain.ClientPackage{}, packageWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		if q.ClientOpID != "" {
			if cp, done, err := r.replayRedeem(ctx, tx, q); err != nil || done {
				return cp, err
			}
		}
		// El motivo se lee CON LOCK de la fila: el estado ya no puede cambiar
		// hasta el final de la transacción. Si entre el UPDATE y esta lectura otro
		// pedido le devolvió saldo (un ajuste), el paquete SÍ se puede consumir y
		// el segundo UPDATE no puede fallar: no es un 409 inventado.
		retry, err := explainFailedRedeem(ctx, tx, q)
		if err != nil {
			return domain.ClientPackage{}, err
		}
		if !retry {
			return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageBusy)
		}
		tag, err = tx.Exec(ctx, decrement, q.ClientPackageID, q.SalonID, q.ClientID, q.Now.UTC())
		if err != nil {
			return domain.ClientPackage{}, packageWriteError(err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageBusy)
		}
	}
	if err := insertPackageMovement(ctx, tx, domain.PackageMovement{
		ID: domain.NewID(), SalonID: q.SalonID, ClientPackageID: q.ClientPackageID, Delta: -1,
		Reason: domain.PackageRedeem, Note: q.Note, ProfessionalID: q.ProfessionalID,
		ClientOpID: q.ClientOpID, Actor: q.Actor, CreatedAt: q.Now.UTC(),
	}); err != nil {
		if q.ClientOpID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			// Un pedido gemelo ganó la carrera: la transacción se deshace (con
			// su descuento) y se devuelve el estado que dejó el ganador.
			rollbackTx(tx)
			return r.replayAfterRace(q)
		}
		return domain.ClientPackage{}, err
	}
	cp, err := readClientPackage(ctx, tx, q.SalonID, q.ClientID, q.ClientPackageID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ClientPackage{}, packageWriteError(err)
	}
	return cp, nil
}

// replayRedeem mira si el client_op_id ya se usó. done=true es «ya está
// resuelto»: devuelve el estado actual de ESTE paquete (sin descontar) o el 409
// de un id usado para otra cosa.
func (r ClientPackages) replayRedeem(ctx context.Context, q querier, req domain.PackageRedeemRequest) (domain.ClientPackage, bool, error) {
	var usedBy string
	err := q.QueryRow(ctx,
		`SELECT client_package_id FROM package_movements WHERE salon_id=$1 AND client_op_id=$2`,
		req.SalonID, req.ClientOpID).Scan(&usedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ClientPackage{}, false, nil
		}
		return domain.ClientPackage{}, false, pgError(err)
	}
	if usedBy != req.ClientPackageID {
		return domain.ClientPackage{}, true, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
	}
	cp, err := readClientPackage(ctx, q, req.SalonID, req.ClientID, req.ClientPackageID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// El op id es de ese paquete pero de OTRA clienta.
			return domain.ClientPackage{}, true, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
		}
		return domain.ClientPackage{}, true, err
	}
	return cp, true, nil
}

// replayAfterRace resuelve el repetido FUERA de la transacción ya deshecha.
func (r ClientPackages) replayAfterRace(req domain.PackageRedeemRequest) (domain.ClientPackage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	cp, done, err := r.replayRedeem(ctx, r.Pool, req)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if !done {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageBusy)
	}
	return cp, nil
}

// readClientPackage lee UN paquete con el scope completo (salón y clienta).
func readClientPackage(ctx context.Context, q querier, salonID, clientID, id string) (domain.ClientPackage, error) {
	row := q.QueryRow(ctx,
		`SELECT `+clientPackageColumns+` `+clientPackageFrom+`
		 WHERE cp.salon_id=$1 AND cp.client_id=$2 AND cp.id=$3`, salonID, clientID, id)
	cp, err := scanClientPackage(row.Scan)
	if err != nil {
		return domain.ClientPackage{}, notFound(err)
	}
	return cp, nil
}

// explainFailedRedeem distingue POR QUÉ el UPDATE condicional no afectó filas:
// inexistente o de otra clienta/salón (404), sin saldo o vencido (409). Sin saldo
// gana sobre vencido: no hay nada que consumir. Corre en la MISMA transacción y
// bloquea la fila (FOR UPDATE) para que el motivo no cambie bajo sus pies;
// retry=true dice que el paquete SÍ se puede consumir (un ajuste concurrente
// devolvió saldo) y quien llama repite el UPDATE, que ya no puede fallar.
func explainFailedRedeem(ctx context.Context, q querier, req domain.PackageRedeemRequest) (retry bool, err error) {
	var left int
	var expires *time.Time
	err = q.QueryRow(ctx,
		`SELECT sessions_left, expires_at FROM client_packages
		 WHERE id=$1 AND salon_id=$2 AND client_id=$3 FOR UPDATE`,
		req.ClientPackageID, req.SalonID, req.ClientID).Scan(&left, &expires)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrNotFound
		}
		return false, packageWriteError(err)
	}
	switch {
	case left < 1:
		return false, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageNoSessions)
	case expires != nil && !expires.After(req.Now):
		return false, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageExpired)
	default:
		return true, nil
	}
}

// Adjust mueve el saldo con un UPDATE condicional (el resultado queda en
// 0..sessions_total) y escribe el movimiento `adjust` en la misma transacción.
func (r ClientPackages) Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	defer rollbackTx(tx)

	tag, err := tx.Exec(ctx,
		`UPDATE client_packages SET sessions_left = sessions_left + $4
		 WHERE id=$1 AND salon_id=$2 AND client_id=$3
		   AND sessions_left + $4 >= 0 AND sessions_left + $4 <= sessions_total`,
		a.ClientPackageID, a.SalonID, a.ClientID, a.Delta)
	if err != nil {
		return domain.ClientPackage{}, packageWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		var one int
		err := tx.QueryRow(ctx,
			`SELECT 1 FROM client_packages WHERE id=$1 AND salon_id=$2 AND client_id=$3`,
			a.ClientPackageID, a.SalonID, a.ClientID).Scan(&one)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ClientPackage{}, domain.ErrNotFound
			}
			return domain.ClientPackage{}, pgError(err)
		}
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageAdjustRange)
	}
	if err := insertPackageMovement(ctx, tx, domain.PackageMovement{
		ID: domain.NewID(), SalonID: a.SalonID, ClientPackageID: a.ClientPackageID, Delta: a.Delta,
		Reason: domain.PackageAdjust, Note: a.Note, Actor: a.Actor, CreatedAt: a.Now.UTC(),
	}); err != nil {
		return domain.ClientPackage{}, err
	}
	cp, err := readClientPackage(ctx, tx, a.SalonID, a.ClientID, a.ClientPackageID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ClientPackage{}, packageWriteError(err)
	}
	return cp, nil
}

var (
	_ ports.PackageRepository       = Packages{}
	_ ports.ClientPackageRepository = ClientPackages{}
)
