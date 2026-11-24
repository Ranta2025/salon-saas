// Productos e inventario del POS en Postgres (F6, D1/D2).
//
// Toda query va scopeada por salon_id: el producto de otro tenant no existe acá
// (ErrNotFound). El stock es un contador y CADA cambio escribe su movimiento en
// la MISMA transacción; el UPDATE de stock lleva la condición `stock + $delta >=
// 0`, así la DB es la última barrera contra un stock negativo.
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

// Products implementa ports.ProductRepository.
type Products struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción de un alta o un ajuste (0 = default).
	TxTimeout time.Duration
}

const productColumns = `id,salon_id,name,price_cents,stock,low_stock_threshold,active,created_at`

func scanProduct(scan func(dest ...any) error) (domain.Product, error) {
	var p domain.Product
	if err := scan(&p.ID, &p.SalonID, &p.Name, &p.PriceCents, &p.Stock,
		&p.LowStockThreshold, &p.Active, &p.CreatedAt); err != nil {
		return domain.Product{}, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

// Create inserta el producto y, si initial != nil, su movimiento inicial en la
// MISMA transacción. El índice único de nombres activos traduce 23505.
func (r Products) Create(p domain.Product, initial *domain.StockMovement) error {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO products(id,salon_id,name,price_cents,stock,low_stock_threshold,active,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.ID, p.SalonID, p.Name, p.PriceCents, p.Stock, p.LowStockThreshold, p.Active, p.CreatedAt); err != nil {
		return pgError(err)
	}
	if initial != nil {
		if err := insertStockMovement(ctx, tx, *initial); err != nil {
			return err
		}
	}
	return pgError(tx.Commit(ctx))
}

// Save actualiza nombre, precio, umbral y estado. El WHERE con salon_id es la
// autorización: un id de otro tenant no afecta filas (ErrNotFound).
func (r Products) Save(p domain.Product) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE products SET name=$3, price_cents=$4, low_stock_threshold=$5, active=$6
		 WHERE salon_id=$1 AND id=$2`,
		p.SalonID, p.ID, p.Name, p.PriceCents, p.LowStockThreshold, p.Active)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r Products) FindByID(salonID, id string) (domain.Product, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+productColumns+` FROM products WHERE salon_id=$1 AND id=$2`, salonID, id)
	p, err := scanProduct(row.Scan)
	if err != nil {
		return domain.Product{}, notFound(err)
	}
	return p, nil
}

// List trae activos e inactivos ordenados por nombre (el POS filtra los activos).
func (r Products) List(salonID string) ([]domain.Product, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+productColumns+` FROM products WHERE salon_id=$1 ORDER BY lower(name), id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Product, 0, 8)
	for rows.Next() {
		p, err := scanProduct(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Adjust aplica el delta con el WHERE condicional de stock y escribe el
// movimiento en la misma transacción (D2). Un delta que dejaría el stock
// negativo es 409 con el texto del contrato y no escribe nada.
func (r Products) Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Product{}, err
	}
	defer rollbackTx(tx)

	row := tx.QueryRow(ctx,
		`UPDATE products SET stock = stock + $3
		 WHERE salon_id=$1 AND id=$2 AND stock + $3 >= 0 AND stock + $3 <= 1000000
		 RETURNING `+productColumns,
		salonID, productID, delta)
	updated, err := scanProduct(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Product{}, r.explainFailedAdjust(ctx, tx, salonID, productID, delta)
		}
		return domain.Product{}, pgError(err)
	}
	if err := insertStockMovement(ctx, tx, domain.StockMovement{
		ID:        domain.NewID(),
		SalonID:   salonID,
		ProductID: productID,
		Delta:     delta,
		Reason:    reason,
		Note:      note,
		CreatedBy: createdBy,
	}); err != nil {
		return domain.Product{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Product{}, pgError(err)
	}
	return updated, nil
}

// explainFailedAdjust distingue POR QUÉ el UPDATE condicional no afectó filas:
// producto inexistente/de otro salón (404), stock que quedaría negativo (409) o
// stock por encima del tope (400). Así el cliente nunca ve un 500.
func (r Products) explainFailedAdjust(ctx context.Context, q querier, salonID, productID string, delta int) error {
	var stock int
	err := q.QueryRow(ctx,
		`SELECT stock FROM products WHERE salon_id=$1 AND id=$2`, salonID, productID).Scan(&stock)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return pgError(err)
	}
	if stock+delta < 0 {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductNegativeStock)
	}
	return domain.NewMessage(domain.ErrValidation, domain.MsgProductStockRange)
}

// ListMovements trae el libro de un producto, más reciente primero.
func (r Products) ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.Pool.Query(context.Background(),
		`SELECT id, delta, reason, COALESCE(note,''), COALESCE(sale_id,''), created_at
		 FROM stock_movements
		 WHERE salon_id=$1 AND product_id=$2
		 ORDER BY created_at DESC, id DESC
		 LIMIT $3`,
		salonID, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.StockMovement, 0, limit)
	for rows.Next() {
		var m domain.StockMovement
		var reason string
		if err := rows.Scan(&m.ID, &m.Delta, &reason, &m.Note, &m.SaleID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Reason = domain.StockReason(reason)
		m.SalonID, m.ProductID = salonID, productID
		m.CreatedAt = m.CreatedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// insertStockMovement escribe una fila del libro (append-only). sale_id y
// created_by van como NULL cuando no hay valor ("" en Go).
func insertStockMovement(ctx context.Context, q querier, m domain.StockMovement) error {
	_, err := q.Exec(ctx,
		`INSERT INTO stock_movements(id,salon_id,product_id,delta,reason,note,sale_id,created_by)
		 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''))`,
		m.ID, m.SalonID, m.ProductID, m.Delta, string(m.Reason), m.Note, m.SaleID, m.CreatedBy)
	return pgError(err)
}

var _ ports.ProductRepository = Products{}
