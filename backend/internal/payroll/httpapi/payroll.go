// Nómina y comisiones (F3.2 del contrato F3): modelo de pago por miembro, vista
// previa del período y «Mis comisiones». El handler solo traduce HTTP: la
// derivación del período, la fórmula y los permisos viven en app/domain.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/payroll"
	"salon-saas/internal/ports"
)

// PayrollHandler agrupa las cuatro rutas de F3.2.
type PayrollHandler struct {
	PayModels   payroll.PayModelService
	Payroll     payroll.PayrollService
	Memberships ports.MembershipRepository
}

func (h PayrollHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return httpx.LoadActor(h.Memberships, c, salonID)
}

// payModelJSON es la forma única del modelo de pago (D3): la usan el listado y
// la respuesta del PUT para que el frontend vea siempre el mismo objeto.
func payModelJSON(v domain.MemberPayView) gin.H {
	return gin.H{
		"user_id":     v.UserID,
		"name":        v.Name,
		"role":        string(v.Role),
		"active":      v.Active,
		"fixed_cents": v.FixedCents,
		// week | fortnight | month: el valor crudo que espera el frontend.
		"fixed_period":       string(v.FixedPeriod),
		"commission_enabled": v.CommissionEnabled,
	}
}

// periodJSON es la forma única del período derivado: from y to son fechas
// locales INCLUSIVAS (YYYY-MM-DD) y closed dice si ya terminó.
func periodJSON(p domain.PayPeriod, closed bool) gin.H {
	return gin.H{
		"kind":   string(p.Kind),
		"from":   p.FromDate(),
		"to":     p.ToDate(),
		"closed": closed,
	}
}

// ListPayModels: GET /api/v1/salons/:id/pay-models -> {"pay_models":[...]}.
// Todos los miembros, activos y dados de baja; sin fila, el default de D3.
func (h PayrollHandler) ListPayModels(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.PayModels.List(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, v := range list {
		out = append(out, payModelJSON(v))
	}
	c.JSON(http.StatusOK, gin.H{"pay_models": out})
}

// UpdatePayModel: PUT /api/v1/salons/:id/members/:uid/pay-model -> 200 el modelo.
// El PUT REEMPLAZA el modelo entero, así que los tres campos son obligatorios:
// un cuerpo parcial ({"fixed_period":"week"}) guardaba fixed_cents=0 en silencio
// y borraba un sueldo (hallazgo de F3.2). Ausente o null es un 400 y no escribe.
func (h PayrollHandler) UpdatePayModel(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		FixedCents        *int    `json:"fixed_cents"`
		FixedPeriod       *string `json:"fixed_period"`
		CommissionEnabled *bool   `json:"commission_enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		// Incluye un monto con decimales (1500.5): no es un modelo válido.
		httpx.BadBody(c)
		return
	}
	if in.FixedCents == nil || in.FixedPeriod == nil || in.CommissionEnabled == nil {
		httpx.RespondErr(c, domain.NewMessage(domain.ErrValidation,
			"Faltan datos del modelo de pago: indicá el sueldo fijo, su período y si cobra comisión"))
		return
	}
	v, err := h.PayModels.Update(actor, salonID, c.Param("uid"), *in.FixedCents, *in.FixedPeriod, *in.CommissionEnabled)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, payModelJSON(v))
}

// Preview: POST /api/v1/salons/:id/payroll/preview {kind,date} -> 200 con el
// período, las líneas y los totales. 400 "Período inválido" si el tipo o la
// fecha no son válidos (el mensaje lo pone el dominio).
func (h PayrollHandler) Preview(c *gin.Context) {
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
		httpx.BadBody(c)
		return
	}
	out, err := h.Payroll.Preview(actor, salonID, in.Kind, in.Date)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	lines := make([]gin.H, 0, len(out.Lines))
	for _, l := range out.Lines {
		lines = append(lines, gin.H{
			"user_id":           l.UserID,
			"name":              l.Name,
			"active":            l.Active,
			"sales_count":       l.SalesCount,
			"sales_total_cents": l.SalesTotalCents,
			"commission_cents":  l.CommissionCents,
			"fixed_cents":       l.FixedCents,
			"total_cents":       l.TotalCents,
			"estimated":         l.Estimated,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"period": periodJSON(out.Period, out.Closed),
		"lines":  lines,
		"totals": gin.H{
			"commission_cents": out.CommissionCents,
			"fixed_cents":      out.FixedCents,
			"total_cents":      out.TotalCents,
		},
	})
}

// Me: GET /api/v1/salons/:id/payroll/me?kind=&date= -> 200 con las figuras del
// que llama. Ni los números ni el modelo de pago de otra persona viajan acá.
func (h PayrollHandler) Me(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	out, err := h.Payroll.Me(actor, salonID, c.Query("kind"), c.Query("date"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"period":            periodJSON(out.Period, out.Closed),
		"sales_count":       out.SalesCount,
		"sales_total_cents": out.SalesTotalCents,
		"commission_cents":  out.CommissionCents,
		"estimated":         out.Estimated,
	})
}
