// ReviewHandler: las rutas de las reseñas posvisita (F13, D4..D7). Es delgado:
// parsea, carga el actor y delega en el caso de uso, que es donde viven los
// permisos, las validaciones y la paginación.
//
//	GET  /public/reviews/:token                       enlace del correo (público)
//	POST /public/reviews/:token                       enviar la reseña (público)
//	GET  /salons/:id/reviews?limit=&before=           lista del panel
//	GET  /salons/:id/review-settings                  configuración
//	PUT  /salons/:id/review-settings                  reemplaza (owner/admin)
//	POST /salons/:id/reviews/:rid/hide                ocultar (owner/admin)
//	POST /salons/:id/reviews/:rid/unhide               mostrar (owner/admin)
//
// El permiso va ANTES de leer el cuerpo o los parámetros: quien no puede hacer la
// acción recibe 403 aunque el cuerpo o el límite estén mal. Un token con forma
// inválida, un comentario con caracteres de control o un rating fuera de rango
// son 400/404, NUNCA un 500.
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/reviews"
)

type ReviewHandler struct {
	Service     reviews.Service
	Memberships ports.MembershipRepository
}

func (h ReviewHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	m, err := h.Memberships.Find(userID, salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// reviewJSON es la forma D6 de un renglón del panel. El comentario viaja tal
// cual: el panel lo muestra como TEXTO PLANO (D5).
func reviewJSON(r ports.Review) gin.H {
	return gin.H{
		"id":           r.ID,
		"rating":       r.Rating,
		"comment":      r.Comment,
		"author_name":  r.AuthorName,
		"hidden":       r.Hidden,
		"created_at":   r.CreatedAt.UTC().Format(time.RFC3339),
		"service_name": r.ServiceName,
	}
}

// GetLink: GET /public/reviews/:token -> 200 {salon_name, service_name,
// starts_at, timezone}. Público: el token del fragmento ES la credencial. Un
// token desconocido, vencido o usado responde el MISMO 404 (D4).
func (h ReviewHandler) GetLink(c *gin.Context) {
	link, err := h.Service.Link(c.Param("token"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"salon_name":   link.SalonName,
		"service_name": link.ServiceName,
		"starts_at":    link.StartsAt.UTC().Format(time.RFC3339),
		"timezone":     link.Timezone,
	})
}

// Submit: POST /public/reviews/:token {rating, comment} -> 201 {"ok":true}.
// `rating` es obligatorio (entero 1..5) y `comment` opcional (hasta 500
// caracteres). El cuerpo se valida ANTES de canjear: un 400 no consume el enlace.
func (h ReviewHandler) Submit(c *gin.Context) {
	var in struct {
		Rating  *int    `json:"rating"`
		Comment *string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	if in.Rating == nil {
		httpx.RespondErr(c, domain.NewMessage(domain.ErrValidation, reviews.MsgRatingInvalid))
		return
	}
	comment := ""
	if in.Comment != nil {
		comment = *in.Comment
	}
	if _, err := h.Service.Submit(c.Param("token"), *in.Rating, comment); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

// List: GET /salons/:id/reviews?limit=&before= -> 200 {items:[...], next_cursor}.
// Owner/admin/recepción; el permiso va ANTES de mirar los parámetros (D6).
func (h ReviewHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	page, err := h.Service.List(actor, salonID, c.Query("limit"), c.Query("before"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, r := range page.Items {
		items = append(items, reviewJSON(r))
	}
	var next any
	if page.NextCursor != "" {
		next = page.NextCursor
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

// settingsJSON es la forma D6 de la configuración: las DOS claves siempre.
func settingsJSON(st ports.ReviewSettings) gin.H {
	return gin.H{"enabled": st.Enabled, "delay_hours": st.DelayHours}
}

// GetSettings: GET /salons/:id/review-settings -> 200 {enabled, delay_hours}. Sin
// configurar responde {enabled:false, delay_hours:24} (D6).
func (h ReviewHandler) GetSettings(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	st, err := h.Service.Settings(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, settingsJSON(st))
}

// PutSettings: PUT /salons/:id/review-settings {enabled, delay_hours}. REEMPLAZA
// y exige las DOS claves (un cuerpo parcial es 400 y no escribe nada); la demora
// es un entero de 1 a 72. El permiso (owner/admin) va ANTES del cuerpo.
func (h ReviewHandler) PutSettings(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Service.RequireManager(actor, salonID); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	var body struct {
		Enabled    *bool            `json:"enabled"`
		DelayHours httpx.PresentInt `json:"delay_hours"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.BadBody(c)
		return
	}
	if body.Enabled == nil || !body.DelayHours.Set || body.DelayHours.Value == nil {
		httpx.RespondErr(c, domain.NewMessage(domain.ErrValidation, reviews.MsgSettingsMissing))
		return
	}
	st, err := h.Service.SaveSettings(actor, salonID, *body.Enabled, *body.DelayHours.Value)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, settingsJSON(st))
}

// Hide: POST /salons/:id/reviews/:rid/hide -> 200 {"ok":true}. Owner/admin,
// idempotente; la reseña de otro salón es 404.
func (h ReviewHandler) Hide(c *gin.Context) { h.setHidden(c, true) }

// Unhide: POST /salons/:id/reviews/:rid/unhide -> 200 {"ok":true}.
func (h ReviewHandler) Unhide(c *gin.Context) { h.setHidden(c, false) }

func (h ReviewHandler) setHidden(c *gin.Context, hidden bool) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Service.Hide(actor, salonID, c.Param("rid"), hidden); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
