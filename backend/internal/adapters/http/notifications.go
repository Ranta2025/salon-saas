package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// NotificationHandler: bandeja de avisos del salón. Delgado: carga actor,
// llama app, serializa con la MISMA forma que el evento `notify` del websocket
// (app.NotificationPayload), así el frontend parsea lo mismo por donde venga.
type NotificationHandler struct {
	Notifications app.NotificationService
	Memberships   ports.MembershipRepository
}

func (h NotificationHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// List: GET /api/v1/salons/:id/notifications?limit=50 -> {notifications, unread}
func (h NotificationHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	limit := 0 // app aplica el default
	if q := c.Query("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			limit = n
		}
	}
	list, err := h.Notifications.List(actor, salonID, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, n := range list {
		out = append(out, app.NotificationPayload(n))
	}
	// El contador va en la misma respuesta: el badge del frontend no necesita
	// otra vuelta a la API (campo extra, aditivo al contrato).
	unread, err := h.Notifications.UnreadCount(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifications": out, "unread": unread})
}

// MarkRead: POST /api/v1/notifications/:nid/read -> 204.
// La ruta no trae :salon_id; el alcance sale de las memberships del usuario
// (el repo solo acepta el aviso si es de un salón donde está esa persona).
func (h NotificationHandler) MarkRead(c *gin.Context) {
	uid, _ := c.Get("userID")
	actor := domain.Membership{UserID: uid.(string)}
	if err := h.Notifications.MarkRead(actor, c.Param("nid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
