// DepositPolicyHandler: la política de señas del salón (F11, D3). Patrón de
// `loyalty-program`: leer cualquier miembro activo; guardar solo owner/admin con
// el permiso ANTES de leer el cuerpo y las CUATRO claves obligatorias.
//
//	GET /api/v1/salons/:id/deposit-policy   cualquier miembro activo
//	PUT /api/v1/salons/:id/deposit-policy   owner/admin (auditada como deposit.policy)
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/deposits"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type DepositPolicyHandler struct {
	Policy      deposits.DepositPolicyService
	Memberships ports.MembershipRepository
}

// loadActor delega en el helper compartido de la API (members.go): el actor
// sale de la base, no del token.
func (h DepositPolicyHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return httpx.LoadActor(h.Memberships, c, salonID)
}

// depositPolicyJSON es la forma D3. Una política nunca guardada es
// `enabled:false` y el resto null: nunca se inventan valores.
func depositPolicyJSON(p domain.DepositPolicy) gin.H {
	if !p.Saved {
		return gin.H{"enabled": false, "percent": nil, "hold_minutes": nil, "refund_hours": nil}
	}
	return gin.H{
		"enabled":      p.Enabled,
		"percent":      p.Percent,
		"hold_minutes": p.HoldMinutes,
		"refund_hours": p.RefundHours,
		"updated_at":   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// Get: GET /salons/:id/deposit-policy -> 200 {enabled, percent, hold_minutes,
// refund_hours} (el resto null si nunca se guardó).
func (h DepositPolicyHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	p, err := h.Policy.Get(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, depositPolicyJSON(p))
}

// Put: PUT /salons/:id/deposit-policy {enabled, percent, hold_minutes,
// refund_hours} -> 200 con la política guardada. REEMPLAZA y exige las cuatro
// claves: un cuerpo parcial es 400 «Faltan datos de la sena...» y no escribe
// nada. Activar sin banco configurado es 409 (D12).
func (h DepositPolicyHandler) Put(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	// El permiso va ANTES de mirar el cuerpo: quien no puede guardar recibe 403
	// aunque el cuerpo esté vacío o mal formado (D3).
	if err := h.Policy.RequireManager(actor, salonID); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	var in struct {
		Enabled     *bool `json:"enabled"`
		Percent     *int  `json:"percent"`
		HoldMinutes *int  `json:"hold_minutes"`
		RefundHours *int  `json:"refund_hours"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	p, err := h.Policy.Put(actor, salonID, deposits.DepositPolicyInput{
		Enabled:     in.Enabled,
		Percent:     in.Percent,
		HoldMinutes: in.HoldMinutes,
		RefundHours: in.RefundHours,
	})
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, depositPolicyJSON(p))
}
