package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// BookingHandler: agenda y calendario. Delgado: parsea, carga actor, llama app.
type BookingHandler struct {
	Bookings    app.BookingService
	Memberships ports.MembershipRepository
}

func (h BookingHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// Create: POST /salons/:id/bookings
func (h BookingHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		ProfessionalID string `json:"professional_id"`
		ClientName     string `json:"client_name"`
		ClientPhone    string `json:"client_phone"`
		StartsAt       string `json:"starts_at"`
		EndsAt         string `json:"ends_at"`
		// ClientOpID es opcional: lo manda la cola offline para idempotencia.
		ClientOpID string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	startsAt, err1 := time.Parse(time.RFC3339, in.StartsAt)
	endsAt, err2 := time.Parse(time.RFC3339, in.EndsAt)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fechas RFC3339 inválidas"})
		return
	}
	b, err := h.Bookings.CreateBookingWithOp(actor, salonID, in.ProfessionalID, in.ClientName, in.ClientPhone, startsAt, endsAt, in.ClientOpID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id": b.ID, "professional_id": b.ProfessionalID, "client_name": b.ClientName,
		"starts_at": b.StartsAt.Format(time.RFC3339), "ends_at": b.EndsAt.Format(time.RFC3339),
		"status": string(b.Status),
	})
}

// List: GET /salons/:id/bookings?from=...&to=... (por defecto, hoy UTC)
func (h BookingHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	if q := c.Query("from"); q != "" {
		t, err := time.Parse(time.RFC3339, q)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from inválido"})
			return
		}
		from = t
	}
	if q := c.Query("to"); q != "" {
		t, err := time.Parse(time.RFC3339, q)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "to inválido"})
			return
		}
		to = t
	}
	list, err := h.Bookings.List(actor, salonID, from, to)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, b := range list {
		out = append(out, gin.H{
			"id": b.ID, "professional_id": b.ProfessionalID, "client_name": b.ClientName,
			"starts_at": b.StartsAt.Format(time.RFC3339), "ends_at": b.EndsAt.Format(time.RFC3339),
			"status": string(b.Status),
		})
	}
	c.JSON(http.StatusOK, gin.H{"bookings": out})
}
