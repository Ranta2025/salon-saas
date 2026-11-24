// Venta CON productos en Postgres (F6, D3/D4/D8).
//
// SaveWithProducts hace TODO en UNA transacción con tope de tiempo:
//
//  1. inserta la venta (con `products_cents`) y sus líneas de servicio, igual
//     que Sales.Save (que queda intacto para las ventas sin productos);
//  2. inserta las líneas de producto con el precio CONGELADO;
//  3. por cada producto, ORDENADO por id (para que dos ventas con productos en
//     orden distinto no formen un interbloqueo), hace el descuento
//     `WHERE stock >= qty` (el lock de fila serializa las ventas simultáneas del
//     mismo producto) y escribe su movimiento `sale`;
//  4. si cualquier producto no alcanza o no está activo, el rollback deshace
//     TODO: la venta, los servicios y los descuentos ya hechos.
//
// Un 40P01/40001 se traduce a 409 (la transacción ya se deshizo: reintentar es
// seguro), como en F2e. El índice único de client_op_id es la garantía de la
// idempotencia: el pedido que pierde también deshace su descuento (D4).
package pgstore

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// SaleProducts implementa ports.SaleProductsRepository.
type SaleProducts struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción completa (venta + descuentos). 0 = default.
	TxTimeout time.Duration
}

func (r SaleProducts) SaveWithProducts(s domain.SaleWithProducts) ([]domain.Product, error) {
	ctx, cancel := context.WithTimeout(context.Background(), txTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,client_op_id,created_at,products_cents)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.ID, s.SalonID, s.ProfessionalID, s.ClientName, s.TotalCents, string(s.Method),
		s.ClientOpID, s.CreatedAt, s.ProductsCents); err != nil {
		return nil, saleWriteError(err)
	}
	for _, it := range s.Items {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents,commission_pct)
			 VALUES($1,$2,$3,$4,$5,$6)`,
			domain.NewID(), s.ID, it.ServiceID, it.Qty, it.UnitPriceCents, it.CommissionPct); err != nil {
			return nil, saleWriteError(err)
		}
	}

	// Orden por id: todas las transacciones toman los locks en el MISMO orden.
	lines := append([]domain.SaleProduct(nil), s.Products...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].ProductID < lines[j].ProductID })

	crossed := make([]domain.Product, 0, len(lines))
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sale_products(id,sale_id,product_id,qty,unit_price_cents)
			 VALUES($1,$2,$3,$4,$5)`,
			domain.NewID(), s.ID, l.ProductID, l.Qty, l.UnitPriceCents); err != nil {
			return nil, saleWriteError(err)
		}
		row := tx.QueryRow(ctx,
			`UPDATE products SET stock = stock - $3
			 WHERE id=$1 AND salon_id=$2 AND active AND stock >= $3
			 RETURNING `+productColumns,
			l.ProductID, s.SalonID, l.Qty)
		after, err := scanProduct(row.Scan)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, failedProductSale(ctx, tx, s.SalonID, l.ProductID, l.Qty)
			}
			return nil, saleWriteError(err)
		}
		if err := insertStockMovement(ctx, tx, domain.StockMovement{
			ID:        domain.NewID(),
			SalonID:   s.SalonID,
			ProductID: l.ProductID,
			Delta:     -l.Qty,
			Reason:    domain.StockSale,
			SaleID:    s.ID,
		}); err != nil {
			return nil, saleWriteError(err)
		}
		// El stock anterior se deriva del que quedó y la cantidad: bajo el lock
		// de la fila es el valor real (D8).
		before := after
		before.Stock = after.Stock + l.Qty
		if domain.CrossedLowStock(before, after) {
			crossed = append(crossed, after)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, saleWriteError(err)
	}
	return crossed, nil
}

// failedProductSale explica por qué el descuento no afectó filas: producto
// inexistente o inactivo (400 «Producto inválido») o stock insuficiente (409
// con el nombre). El rollback lo hace quien llama.
func failedProductSale(ctx context.Context, q querier, salonID, productID string, qty int) error {
	var name string
	var active bool
	err := q.QueryRow(ctx,
		`SELECT name, active FROM products WHERE salon_id=$1 AND id=$2`,
		salonID, productID).Scan(&name, &active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
		}
		return pgError(err)
	}
	if !active {
		return domain.NewMessage(domain.ErrValidation, domain.MsgProductInvalid)
	}
	return domain.NewMessage(domain.ErrAlreadyExists, domain.NoStockMessage(name))
}

// saleWriteError traduce el error de escribir una venta con productos. Un
// interbloqueo (40P01) o una serialización perdida (40001) son una carrera
// perdida, no un 500: la transacción ya se deshizo y reintentar es seguro.
func saleWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "40P01" || pg.Code == "40001") {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgProductBusy)
	}
	return pgError(err)
}

var _ ports.SaleProductsRepository = SaleProducts{}
