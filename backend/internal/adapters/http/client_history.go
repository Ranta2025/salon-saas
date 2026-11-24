// Rutas de la ficha y su historial (F5, D5/D6): GET y PUT de una clienta y
// GET del historial. El handler solo parsea, delega y serializa; los permisos y
// el dinero (spend/sales null para un profesional) los decide el caso de uso.
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// Get: GET /salons/:id/clients/:cid -> 200 ficha; 404 «Ficha no encontrada»
// (también la de otro salón).
func (h ClientHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	client, err := h.Clients.Get(actor, salonID, c.Param("cid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, clientJSON(client))
}

// Update: PUT /salons/:id/clients/:cid -> 200 ficha.
//
// El body tiene que traer las CINCO claves (email, notas y alergias pueden ser
// ""): por eso se leen con punteros, así «faltó la clave» se distingue de
// «vino vacía». Un 400 no escribe nada.
func (h ClientHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name      *string `json:"name"`
		Phone     *string `json:"phone"`
		Email     *string `json:"email"`
		Notes     *string `json:"notes"`
		Allergies *string `json:"allergies"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Name == nil || in.Phone == nil || in.Email == nil || in.Notes == nil || in.Allergies == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": domain.MsgClientFieldsRequired})
		return
	}
	client, err := h.Clients.Update(actor, salonID, c.Param("cid"),
		*in.Name, *in.Phone, *in.Email, *in.Notes, *in.Allergies)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, clientJSON(client))
}

// ClientHistory: GET /salons/:id/clients/:cid/history -> 200 {client_id, summary,
// bookings, spend, sales}. Para quien no puede ver la plata, spend y sales van
// en null.
func (h ClientHandler) ClientHistory(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	view, err := h.History.History(actor, salonID, c.Param("cid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, clientHistoryJSON(view))
}

// clientHistoryJSON es la forma única del historial.
func clientHistoryJSON(v app.ClientHistoryView) gin.H {
	bookings := make([]gin.H, 0, len(v.Bookings))
	for _, b := range v.Bookings {
		bookings = append(bookings, gin.H{
			"id":                b.ID,
			"starts_at":         b.StartsAt.Format(time.RFC3339),
			"status":            string(b.Status),
			"service_name":      b.ServiceName,
			"professional_name": b.ProfessionalName,
		})
	}
	// last_visit es un DÍA local; sin turnos hechos el contrato pide null.
	var lastVisit any
	if v.Summary.LastVisit != "" {
		lastVisit = v.Summary.LastVisit
	}
	var nextVisit any
	if v.Summary.NextVisit != nil {
		nextVisit = v.Summary.NextVisit.Format(time.RFC3339)
	}
	out := gin.H{
		"client_id": v.ClientID,
		"summary": gin.H{
			"visits":     v.Summary.Visits,
			"no_shows":   v.Summary.NoShows,
			"cancelled":  v.Summary.Cancelled,
			"upcoming":   v.Summary.Upcoming,
			"last_visit": lastVisit,
			"next_visit": nextVisit,
			"timezone":   v.Summary.Timezone,
		},
		"bookings": bookings,
		// El dinero es null para un profesional: no se serializa un cero que
		// parezca «no gastó».
		"spend": nil,
		"sales": nil,
	}
	if v.Spend != nil {
		sales := make([]gin.H, 0, len(v.Sales))
		for _, s := range v.Sales {
			sales = append(sales, gin.H{
				"id":          s.ID,
				"created_at":  s.CreatedAt.Format(time.RFC3339),
				"total_cents": s.TotalCents,
				"method":      string(s.Method),
			})
		}
		out["spend"] = gin.H{"sales_count": v.Spend.SalesCount, "total_cents": v.Spend.TotalCents}
		out["sales"] = sales
	}
	return out
}
