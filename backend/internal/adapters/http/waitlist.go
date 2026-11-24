// WaitlistHandler: la lista de espera del salón (F7 del contrato de la fase 6).
// Delgado a propósito: parsea, carga el actor y delega en el caso de uso, que es
// donde viven los permisos (D8), las validaciones, los topes (D4) y el 404
// genérico de la baja (D5).
//
//	POST   /api/v1/public/salons/:slug/waitlist   (sin sesión, rate limit)
//	POST   /api/v1/public/waitlist/leave          (sin sesión, cubo propio)
//	GET    /salons/:id/waitlist
//	DELETE /salons/:id/waitlist/:eid
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type WaitlistHandler struct {
	Service     app.WaitlistService
	Memberships ports.MembershipRepository
}

// loadActor autoriza el pedido igual que las pantallas del mostrador: sin
// membresía activa del salón es 403.
func (h WaitlistHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// Join: POST /api/v1/public/salons/:slug/waitlist
// {service_id, professional_id?, date, name, email, phone?} -> 201/200
// {"status","date","service_id"}. NUNCA devuelve el token ni el id (D4).
func (h WaitlistHandler) Join(c *gin.Context) {
	var in struct {
		ServiceID      string  `json:"service_id"`
		ProfessionalID *string `json:"professional_id"`
		Date           string  `json:"date"`
		Name           string  `json:"name"`
		Email          string  `json:"email"`
		Phone          *string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	// Los cuatro datos obligatorios se exigen EXPLÍCITAMENTE: un campo que falta
	// no se rellena por defecto (un aviso sin email no le sirve a nadie).
	if strings.TrimSpace(in.ServiceID) == "" || strings.TrimSpace(in.Date) == "" ||
		strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Email) == "" {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgWaitlistJoinMissing))
		return
	}
	input := app.WaitlistJoinInput{
		ServiceID: in.ServiceID,
		Date:      in.Date,
		Name:      in.Name,
		Email:     in.Email,
	}
	if in.ProfessionalID != nil {
		input.ProfessionalID = *in.ProfessionalID
	}
	if in.Phone != nil {
		input.Phone = *in.Phone
	}
	res, err := h.Service.Join(c.Param("slug"), input)
	if err != nil {
		respondErr(c, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"status": res.Status, "date": res.Date, "service_id": res.ServiceID})
}

// Leave: POST /api/v1/public/waitlist/leave {token} -> 200 {"status":"left"}.
// El token viaja en el CUERPO (nunca en la ruta ni en el query) y repetir la
// baja es 200 igual; un token que no sirve es 404 {"error":"Aviso no encontrado"}.
func (h WaitlistHandler) Leave(c *gin.Context) {
	var in struct {
		Token *string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	token := ""
	if in.Token != nil {
		token = *in.Token
	}
	if err := h.Service.Leave(token); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": domain.WaitlistStatusLeft})
}

// List: GET /salons/:id/waitlist -> 200 {"entries":[...]} con las entradas
// vigentes (día local >= hoy, sin baja), ordenadas por día y alta (D8).
func (h WaitlistHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Service.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	entries := make([]gin.H, 0, len(list))
	for _, e := range list {
		entries = append(entries, gin.H{
			"id":                e.ID,
			"service_id":        e.ServiceID,
			"service_name":      e.ServiceName,
			"professional_id":   optionalID(e.ProfessionalID),
			"professional_name": optionalID(e.ProfessionalName),
			"date":              e.Day,
			"name":              e.Name,
			"email":             e.Email,
			"phone":             optionalID(e.Phone),
			"status":            e.Status(),
			"created_at":        e.CreatedAt.Format(time.RFC3339),
			"notified_at":       optionalTime(e.NotifiedAt),
		})
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// Remove: DELETE /salons/:id/waitlist/:eid -> 204 (setea left_at). De otro salón
// o ya dada de baja: 404 {"error":"Aviso no encontrado"}.
func (h WaitlistHandler) Remove(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Service.Remove(actor, salonID, c.Param("eid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// optionalTime deja un instante vacío como null (notified_at|null).
func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
