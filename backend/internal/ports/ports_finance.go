package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// --- Gastos y ganancia neta (F3b del contrato Fase 6) ---

// ExpenseRepository: los gastos operativos del salón (migración 026). El gasto
// guarda un DÍA LOCAL (`spent_on DATE`) y un monto entero en centavos.
//
// Todas las queries van scopeadas por salon_id: el gasto de otro tenant no se
// lista, no se encuentra, no se edita y no se borra (ErrNotFound -> 404).
type ExpenseRepository interface {
	// Save inserta el gasto. Los CHECK y las FK de la migración son la red de
	// seguridad de los límites que ya validó el dominio.
	Save(e domain.Expense) error
	// ListRange trae hasta `limit` gastos del salón con spent_on en
	// [from, to] (inclusivo), del más nuevo al más viejo (spent_on y created_at
	// descendentes). El segundo valor dice si había MÁS filas que el tope.
	ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error)
	// FindByID resuelve un gasto del salón (el id de otro tenant es ErrNotFound).
	FindByID(salonID, id string) (domain.Expense, error)
	// Update reemplaza los cuatro campos del gasto SOLO si es de ese salón. Sin
	// filas afectadas es ErrNotFound (nunca un upsert).
	Update(e domain.Expense) error
	// Delete borra el gasto del salón (DELETE físico: es dinero operativo).
	// Sin filas es ErrNotFound.
	Delete(salonID, id string) error
}

// ProfitRepository: la foto de la ganancia neta del salón (D1, D2 y D6). El
// adaptador lee ingresos, gastos y nómina PAGADA en UNA sola transacción
// REPEATABLE READ de solo lectura, así los tres componentes son la misma foto.
//
// El día es el LOCAL del salón (zona `timezone`): las ventas y los pagos se
// agrupan por `AT TIME ZONE` y la ventana es [from, to), las medianoches locales
// que derivó el dominio. La nómina suma `payroll_lines.total_cents` de las
// corridas con status='paid' en el día local de `paid_at`; las abiertas no
// cuentan (D1).
type ProfitRepository interface {
	// DailyProfit devuelve los montos por día local (clave YYYY-MM-DD) y el
	// total por categoría de los gastos del período. Un día sin movimientos no
	// aparece en los mapas: el reporte lo rellena con ceros.
	DailyProfit(salonID, timezone string, from, to time.Time) (domain.ProfitData, error)
}
