// Liquidaciones de nómina (F3.3 del contrato F3): crear, listar, detalle,
// ajuste por línea, pago, borrado y exportación CSV. El handler solo traduce
// HTTP; los permisos, las reglas de dinero y las frases viven en app/domain.
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PayrollRunHandler agrupa las rutas de F3.3.
type PayrollRunHandler struct {
	Runs        app.PayrollRunService
	Memberships ports.MembershipRepository
}

func (h PayrollRunHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return loadActor(h.Memberships, c, salonID)
}

// payrollPaidAt es null para una abierta y RFC3339 para una pagada: la clave
// está SIEMPRE en el JSON (el frontend no distingue ausente de null).
func payrollPaidAt(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// payrollTotalsJSON es la forma única de los totales (detalle y listado).
func payrollTotalsJSON(t domain.PayrollTotals) gin.H {
	return gin.H{
		"commission_cents": t.CommissionCents,
		"fixed_cents":      t.FixedCents,
		"adjustment_cents": t.AdjustmentCents,
		"total_cents":      t.TotalCents,
	}
}

// payrollLineJSON es la línea congelada de una liquidación.
func payrollLineJSON(l domain.PayrollLine) gin.H {
	return gin.H{
		"user_id":           l.UserID,
		"name":              l.Name,
		"active":            l.Active,
		"sales_count":       l.SalesCount,
		"sales_total_cents": l.SalesTotalCents,
		"commission_cents":  l.CommissionCents,
		"fixed_cents":       l.FixedCents,
		"adjustment_cents":  l.AdjustmentCents,
		"adjustment_note":   l.AdjustmentNote,
		"total_cents":       l.TotalCents,
		"estimated":         l.Estimated,
	}
}

// payrollRunJSON es el objeto `run` del detalle (contrato congelado para el
// frontend): fechas locales inclusivas, estado, pago, totales y líneas.
func payrollRunJSON(run domain.PayrollRun) gin.H {
	lines := make([]gin.H, 0, len(run.Lines))
	for _, l := range run.Lines {
		lines = append(lines, payrollLineJSON(l))
	}
	return gin.H{
		"id":         run.ID,
		"kind":       string(run.Kind),
		"from":       run.From,
		"to":         run.To,
		"status":     string(run.Status),
		"created_at": run.CreatedAt.UTC().Format(time.RFC3339),
		"paid_at":    payrollPaidAt(run.PaidAt),
		"totals":     payrollTotalsJSON(run.Totals()),
		"lines":      lines,
	}
}

// payrollSummaryJSON es la fila del listado: sin líneas, con lines_count.
func payrollSummaryJSON(s domain.PayrollRunSummary) gin.H {
	return gin.H{
		"id":          s.ID,
		"kind":        string(s.Kind),
		"from":        s.From,
		"to":          s.To,
		"status":      string(s.Status),
		"created_at":  s.CreatedAt.UTC().Format(time.RFC3339),
		"paid_at":     payrollPaidAt(s.PaidAt),
		"lines_count": s.LinesCount,
		"totals":      payrollTotalsJSON(s.Totals),
	}
}

// Create: POST /api/v1/salons/:id/payroll/runs {kind,date} -> 201 run.
func (h PayrollRunHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Kind string `json:"kind"`
		Date string `json:"date"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	run, err := h.Runs.Create(actor, salonID, in.Kind, in.Date)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, payrollRunJSON(run))
}

// List: GET /api/v1/salons/:id/payroll/runs -> {"runs":[...]}, la más nueva
// primero.
func (h PayrollRunHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Runs.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, s := range list {
		out = append(out, payrollSummaryJSON(s))
	}
	c.JSON(http.StatusOK, gin.H{"runs": out})
}

// Get: GET /api/v1/salons/:id/payroll/runs/:rid -> 200 run o 404.
func (h PayrollRunHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	run, err := h.Runs.Get(actor, salonID, c.Param("rid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, payrollRunJSON(run))
}

// AdjustLine: PATCH .../runs/:rid/lines/:uid {adjustment_cents, adjustment_note}
// -> 200 run. `adjustment_cents` es OBLIGATORIO: ausente o null es 400, nunca 0
// en silencio (lección de F3.2).
func (h PayrollRunHandler) AdjustLine(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		AdjustmentCents *int    `json:"adjustment_cents"`
		AdjustmentNote  *string `json:"adjustment_note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.AdjustmentCents == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgPayrollAdjustmentMissing))
		return
	}
	note := ""
	if in.AdjustmentNote != nil {
		note = *in.AdjustmentNote
	}
	run, err := h.Runs.AdjustLine(actor, salonID, c.Param("rid"), c.Param("uid"), *in.AdjustmentCents, note)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, payrollRunJSON(run))
}

// Pay: POST .../runs/:rid/pay -> 200 run pagada; la segunda vez, 409.
func (h PayrollRunHandler) Pay(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	run, err := h.Runs.Pay(actor, salonID, c.Param("rid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, payrollRunJSON(run))
}

// Delete: DELETE .../runs/:rid -> 204 solo si está abierta.
func (h PayrollRunHandler) Delete(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Runs.Delete(actor, salonID, c.Param("rid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ExportCSV: GET .../runs/:rid/export.csv -> 200 text/csv con BOM. El nombre del
// archivo ya viene saneado (solo el tipo y las dos fechas), así que no puede
// inyectar cabeceras en Content-Disposition.
func (h PayrollRunHandler) ExportCSV(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	name, data, err := h.Runs.ExportCSV(actor, salonID, c.Param("rid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}
