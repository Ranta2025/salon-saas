// LoyaltyHandler: fidelidad «cada N visitas, un servicio de regalo» (F9 del
// contrato de la fase 6). Delgado: parsea, carga el actor y delega en el caso de
// uso, que es donde viven los permisos (D4..D7) y las validaciones.
//
//	GET  /salons/:id/loyalty-program                               programa (cualquier miembro)
//	PUT  /salons/:id/loyalty-program                               reemplaza (owner/admin)
//	GET  /salons/:id/clients/:cid/loyalty                          ficha de la clienta
//	POST /salons/:id/clients/:cid/loyalty/grant                    entregar un vale
//	POST /salons/:id/clients/:cid/loyalty/rewards/:rid/redeem      usar un vale
//
// En las escrituras el permiso va ANTES de leer el cuerpo: quien no puede hacer
// la acción recibe 403 aunque el cuerpo esté mal formado.
package httpapi

import (
	"errors"
	"io"
	"net/http"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/loyalty"
	"salon-saas/internal/ports"
	"time"

	"github.com/gin-gonic/gin"
)

type LoyaltyHandler struct {
	Loyalty     loyalty.LoyaltyService
	Memberships ports.MembershipRepository
}

func (h LoyaltyHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// loyaltyProgramJSON es la forma D4 del programa. Un programa nunca guardado es
// `enabled:false` y el resto null.
func loyaltyProgramJSON(p domain.LoyaltyProgram) gin.H {
	if !p.Saved {
		return gin.H{
			"enabled": false, "visits_required": nil, "reward_service_id": nil,
			"reward_service_name": nil, "reward_validity_days": nil, "counting_from": nil,
		}
	}
	var validity any
	if p.ValidityDays != nil {
		validity = *p.ValidityDays
	}
	return gin.H{
		"enabled":              p.Enabled,
		"visits_required":      p.VisitsRequired,
		"reward_service_id":    p.RewardServiceID,
		"reward_service_name":  p.RewardServiceName,
		"reward_validity_days": validity,
		"counting_from":        p.CountingFrom.UTC().Format(time.RFC3339),
	}
}

// loyaltyRewardJSON es la forma D5 de un vale.
func loyaltyRewardJSON(r domain.LoyaltyReward) gin.H {
	var expires, redeemed any
	if r.ExpiresAt != nil {
		expires = r.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if r.RedeemedAt != nil {
		redeemed = r.RedeemedAt.UTC().Format(time.RFC3339)
	}
	return gin.H{
		"id":            r.ID,
		"reward_number": r.RewardNumber,
		"service_id":    r.ServiceID,
		"service_name":  r.ServiceName,
		"granted_at":    r.GrantedAt.UTC().Format(time.RFC3339),
		"expires_at":    expires,
		"redeemed_at":   redeemed,
		"state":         r.State,
	}
}

// GetProgram: GET /salons/:id/loyalty-program -> 200 {enabled, visits_required,
// reward_service_id, reward_service_name, reward_validity_days, counting_from}.
func (h LoyaltyHandler) GetProgram(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	p, err := h.Loyalty.Program(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, loyaltyProgramJSON(p))
}

// PutProgram: PUT /salons/:id/loyalty-program {enabled, visits_required,
// reward_service_id, reward_validity_days}. REEMPLAZA y exige las CUATRO claves:
// un cuerpo parcial es 400 y no escribe nada. `reward_validity_days: null`
// explícito = sin vencimiento; la clave ausente NO se rellena en silencio.
func (h LoyaltyHandler) PutProgram(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Loyalty.RequireManager(actor, salonID); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	var in struct {
		Enabled        *bool            `json:"enabled"`
		VisitsRequired *int             `json:"visits_required"`
		RewardService  *string          `json:"reward_service_id"`
		ValidityDays   httpx.PresentInt `json:"reward_validity_days"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	if in.Enabled == nil || in.VisitsRequired == nil || in.RewardService == nil || !in.ValidityDays.Set {
		httpx.RespondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgLoyaltyProgramMissing))
		return
	}
	p, err := h.Loyalty.SaveProgram(actor, salonID, *in.Enabled, *in.VisitsRequired, *in.RewardService, in.ValidityDays.Value)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, loyaltyProgramJSON(p))
}

// GetClient: GET /salons/:id/clients/:cid/loyalty -> 200 {enabled, visits,
// visits_required, rewards_earned, rewards_granted, rewards_available, next_in,
// rewards:[...]}.
func (h LoyaltyHandler) GetClient(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	st, err := h.Loyalty.ClientStatus(actor, salonID, c.Param("cid"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	rewards := make([]gin.H, 0, len(st.Rewards))
	for _, r := range st.Rewards {
		rewards = append(rewards, loyaltyRewardJSON(r))
	}
	var required, nextIn any
	if st.Program.Saved {
		required, nextIn = st.Progress.VisitsRequired, st.Progress.NextIn
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":           st.Program.Saved && st.Program.Enabled,
		"visits":            st.Progress.Visits,
		"visits_required":   required,
		"rewards_earned":    st.Progress.Earned,
		"rewards_granted":   st.Progress.Granted,
		"rewards_available": st.Progress.Available,
		"next_in":           nextIn,
		"rewards":           rewards,
	})
}

// Grant: POST /salons/:id/clients/:cid/loyalty/grant {client_op_id?}
// -> 201 {"reward":{...}}. Idempotente por client_op_id. El cuerpo es opcional:
// vacío vale como {}.
func (h LoyaltyHandler) Grant(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Loyalty.RequireSeller(actor, salonID); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	var in struct {
		ClientOpID string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		httpx.BadBody(c)
		return
	}
	r, err := h.Loyalty.Grant(actor, salonID, c.Param("cid"), in.ClientOpID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"reward": loyaltyRewardJSON(r)})
}

// Redeem: POST /salons/:id/clients/:cid/loyalty/rewards/:rid/redeem
// {professional_id?, note?, client_op_id?} -> 200 {"reward":{...}}. Todos los
// campos son opcionales: un cuerpo vacío vale como {}.
func (h LoyaltyHandler) Redeem(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Loyalty.RequireSeller(actor, salonID); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	var in struct {
		ProfessionalID string `json:"professional_id"`
		Note           string `json:"note"`
		ClientOpID     string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		httpx.BadBody(c)
		return
	}
	r, err := h.Loyalty.Redeem(actor, salonID, c.Param("cid"), c.Param("rid"), in.ProfessionalID, in.Note, in.ClientOpID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reward": loyaltyRewardJSON(r)})
}
