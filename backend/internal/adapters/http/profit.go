// Reporte de ganancia neta (F3b, D1/D2/D5 del contrato F3b). El handler solo
// traduce HTTP: el período lo deriva el caso de uso en la zona del salón y las
// tres componentes (ingresos, gastos y nómina pagada) salen de una sola foto.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ProfitHandler agrupa la ruta del panel de ganancias.
type ProfitHandler struct {
	Profit      app.ProfitService
	Memberships ports.MembershipRepository
}

func (h ProfitHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return loadActor(h.Memberships, c, salonID)
}

// profitDayJSON es la fila diaria del reporte.
func profitDayJSON(d domain.ProfitDayTotal) gin.H {
	return gin.H{
		"date":           d.Date,
		"revenue_cents":  d.RevenueCents,
		"expenses_cents": d.ExpensesCents,
		"payroll_cents":  d.PayrollCents,
		"net_cents":      d.NetCents,
	}
}

// Get: GET /api/v1/salons/:id/profit?kind=day|week|month&date=YYYY-MM-DD -> 200.
// `date` es opcional: sin él, el período es HOY en la zona del salón.
func (h ProfitHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	report, err := h.Profit.Report(actor, salonID, c.Query("kind"), c.Query("date"))
	if err != nil {
		respondErr(c, err)
		return
	}
	days := make([]gin.H, 0, len(report.Days))
	for _, d := range report.Days {
		days = append(days, profitDayJSON(d))
	}
	// `expenses_by_category` sale siempre como array (el frontend no tiene que
	// distinguir null de []).
	categories := make([]gin.H, 0, len(report.ExpensesByCategory))
	for _, c := range report.ExpensesByCategory {
		categories = append(categories, gin.H{
			"category":    string(c.Category),
			"total_cents": c.TotalCents,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"period": gin.H{
			"kind": string(report.Period.Kind),
			"from": report.Period.FromDate(),
			"to":   report.Period.ToDate(),
		},
		"totals": gin.H{
			"revenue_cents":  report.Totals.RevenueCents,
			"expenses_cents": report.Totals.ExpensesCents,
			"payroll_cents":  report.Totals.PayrollCents,
			"net_cents":      report.Totals.NetCents,
		},
		"days":                 days,
		"expenses_by_category": categories,
	})
}
