// Nómina y comisiones (F3.2 del contrato F3) en Postgres: el modelo de pago de
// cada miembro (member_pay, migración 024) y los agregados de ventas por
// profesional de una ventana.
//
// Todas las queries van scopeadas por salon_id: el modelo de pago y las ventas
// de otro tenant no existen acá.
package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PayModels implementa ports.MemberPayRepository (D3).
type PayModels struct{ Pool *pgxpool.Pool }

// payModelColumns es el SELECT único: la persona (nombre desde users, rol y baja
// desde memberships) con su modelo de pago o el default de D3 cuando no hay
// fila. Se usa en el listado y en la lectura de una persona para que las dos
// devuelvan exactamente lo mismo.
const payModelColumns = `
	 m.salon_id, m.user_id, u.name, m.role, (m.ended_at IS NULL) AS active,
	 COALESCE(p.fixed_cents, 0), COALESCE(p.fixed_period, 'month'),
	 COALESCE(p.commission_enabled, true)`

func (r PayModels) ListPayModels(salonID string) ([]domain.MemberPayView, error) {
	return listPayModels(context.Background(), r.Pool, salonID)
}

// listPayModels es la ÚNICA query del listado: la usan la pantalla de Equipo y
// la vista previa (fuera y dentro de la transacción de una liquidación).
func listPayModels(ctx context.Context, q querier, salonID string) ([]domain.MemberPayView, error) {
	rows, err := q.Query(ctx,
		`SELECT`+payModelColumns+`
		 FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 LEFT JOIN member_pay p ON p.salon_id = m.salon_id AND p.user_id = m.user_id
		 WHERE m.salon_id=$1
		 ORDER BY u.name, m.user_id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.MemberPayView, 0, 8)
	for rows.Next() {
		v, err := scanPayModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// FindPayModel resuelve UNA persona del salón. Sin membresía es ErrNotFound (una
// persona de otro tenant no existe para este salón); sin fila en member_pay
// devuelve el default de D3.
func (r PayModels) FindPayModel(salonID, userID string) (domain.MemberPayView, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT`+payModelColumns+`
		 FROM memberships m
		 JOIN users u ON u.id = m.user_id
		 LEFT JOIN member_pay p ON p.salon_id = m.salon_id AND p.user_id = m.user_id
		 WHERE m.salon_id=$1 AND m.user_id=$2`, salonID, userID)
	v, err := scanPayModel(row)
	if err != nil {
		return domain.MemberPayView{}, notFound(err)
	}
	return v, nil
}

// SavePayModel inserta o actualiza el modelo. La FK compuesta de la migración es
// la autorización dura (una persona de otro salón no tiene dónde guardarse: 400
// por pgError); el 404 de «no es del salón» lo da la lectura previa del caso de
// uso, que tiene el mensaje correcto para la persona.
func (r PayModels) SavePayModel(p domain.MemberPay) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO member_pay(salon_id,user_id,fixed_cents,fixed_period,commission_enabled)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (salon_id, user_id) DO UPDATE
		 SET fixed_cents=EXCLUDED.fixed_cents,
		     fixed_period=EXCLUDED.fixed_period,
		     commission_enabled=EXCLUDED.commission_enabled`,
		p.SalonID, p.UserID, p.FixedCents, string(p.FixedPeriod), p.CommissionEnabled)
	return pgError(err)
}

// scanPayModel lee una fila de payModelColumns.
func scanPayModel(row interface {
	Scan(dest ...any) error
}) (domain.MemberPayView, error) {
	var v domain.MemberPayView
	var role, period string
	if err := row.Scan(&v.SalonID, &v.UserID, &v.Name, &role, &v.Active,
		&v.FixedCents, &period, &v.CommissionEnabled); err != nil {
		return domain.MemberPayView{}, err
	}
	v.Role = domain.Role(role)
	v.FixedPeriod = domain.PayPeriodKind(period)
	return v, nil
}

// PayrollSales implementa ports.PayrollSalesRepository (D2).
type PayrollSales struct{ Pool *pgxpool.Pool }

// commissionTotals es la ÚNICA query del agregado, con el filtro opcional de una
// persona («Mis comisiones»).
//
// La comisión se suma en ENTEROS: qty × precio × pct por línea y bigint, sin
// división ni float. La división (floor /100) la hace domain sobre la suma, así
// el redondeo no se acumula ítem por ítem.
//
// La tarifa sale de la FOTO del ítem (sale_items.commission_pct) y, cuando la
// línea es anterior a F3.1 (NULL), de la tarifa ACTUAL del servicio; en ese caso
// la fila queda marcada con estimated. El total vendido se suma por VENTA (no
// por ítem): una venta con dos líneas cuenta una vez.
func commissionTotals(ctx context.Context, q querier, salonID string, from, to time.Time, userID string) ([]domain.PayrollSalesTotal, error) {
	rows, err := q.Query(ctx,
		`WITH ventas AS (
		   SELECT s.id, s.professional_id, s.total_cents
		   FROM sales s
		   WHERE s.salon_id=$1 AND s.created_at >= $2 AND s.created_at < $3
		     AND ($4 = '' OR s.professional_id = $4)
		 ),
		 lineas AS (
		   SELECT v.professional_id,
		          SUM(si.qty::bigint * si.unit_price_cents
		              * COALESCE(si.commission_pct, sv.commission_pct))::bigint AS base,
		          BOOL_OR(si.commission_pct IS NULL) AS estimated
		   FROM ventas v
		   JOIN sale_items si ON si.sale_id = v.id
		   JOIN services sv ON sv.id = si.service_id AND sv.salon_id = $1
		   GROUP BY v.professional_id
		 )
		 SELECT t.professional_id, u.name, t.sales_count, t.sales_total,
		        COALESCE(l.base, 0), COALESCE(l.estimated, false)
		 FROM (SELECT professional_id, COUNT(*)::int AS sales_count,
		              SUM(total_cents)::bigint AS sales_total
		       FROM ventas GROUP BY professional_id) t
		 JOIN users u ON u.id = t.professional_id
		 LEFT JOIN lineas l ON l.professional_id = t.professional_id
		 ORDER BY u.name, t.professional_id`,
		salonID, from.UTC(), to.UTC(), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PayrollSalesTotal, 0, 8)
	for rows.Next() {
		var t domain.PayrollSalesTotal
		var total int64
		if err := rows.Scan(&t.UserID, &t.Name, &t.SalesCount, &total, &t.CommissionBase, &t.Estimated); err != nil {
			return nil, err
		}
		var err error
		if t.SalesTotalCents, err = bigintToInt(total, "el total vendido del período"); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r PayrollSales) CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error) {
	return commissionTotals(context.Background(), r.Pool, salonID, from, to, "")
}

// CommissionTotalFor es el agregado de UNA persona. Sin ventas devuelve el total
// en cero (no ErrNotFound): «no vendió» es una respuesta válida.
func (r PayrollSales) CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error) {
	totals, err := commissionTotals(context.Background(), r.Pool, salonID, from, to, userID)
	if err != nil {
		return domain.PayrollSalesTotal{}, err
	}
	if len(totals) == 0 {
		return domain.PayrollSalesTotal{UserID: userID}, nil
	}
	return totals[0], nil
}

var (
	_ ports.MemberPayRepository    = PayModels{}
	_ ports.PayrollSalesRepository = PayrollSales{}
)
