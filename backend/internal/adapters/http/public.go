package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/bookings"
)

// PublicHandler: las rutas del nudo en la landing, SIN auth.
//   - GET  /public/salons/:slug/availability   (huecos libres)
//   - POST /public/salons/:slug/bookings       (reserva)
//
// La vidriera (GET /public/salons/:slug) vive en internal/settings/httpapi.
type PublicHandler struct {
	Avail    bookings.AvailabilityService
	Bookings bookings.PublicBookingService
}

// Availability: GET /api/v1/public/salons/:slug/availability?date=&service_id=
// [&professional_id=] -> {"date","slots":[{"professional_id","starts_at","ends_at"}]}
func (h PublicHandler) Availability(c *gin.Context) {
	day, err := h.Avail.Day(c.Param("slug"), c.Query("date"), c.Query("service_id"), c.Query("professional_id"))
	if err != nil {
		respondErr(c, err)
		return
	}
	slots := make([]gin.H, 0, len(day.Slots))
	for _, s := range day.Slots {
		slots = append(slots, gin.H{
			"professional_id": s.ProfessionalID,
			"starts_at":       s.StartsAt.Format(time.RFC3339),
			"ends_at":         s.EndsAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"date": day.Date, "slots": slots})
}

// CreateBooking: POST /api/v1/public/salons/:slug/bookings -> 201 {booking}.
// La duración la pone el servicio y el hueco lo arbitra la DB (409).
func (h PublicHandler) CreateBooking(c *gin.Context) {
	var in struct {
		ServiceID      string `json:"service_id"`
		ProfessionalID string `json:"professional_id"`
		ClientName     string `json:"client_name"`
		ClientPhone    string `json:"client_phone"`
		// ClientEmail es opcional: con email, la persona recibe la confirmación
		// y el recordatorio del turno. Vacío = sin correos.
		ClientEmail string `json:"client_email"`
		StartsAt    string `json:"starts_at"`
		ClientOpID  string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.StartsAt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "falta el horario del turno (starts_at)"})
		return
	}
	startsAt, err := time.Parse(time.RFC3339, in.StartsAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "starts_at inválido (RFC3339)"})
		return
	}
	b, payment, err := h.Bookings.BookWithDeposit(c.Param("slug"), bookings.PublicBookingInput{
		ServiceID:      in.ServiceID,
		ProfessionalID: in.ProfessionalID,
		ClientName:     in.ClientName,
		ClientPhone:    in.ClientPhone,
		ClientEmail:    in.ClientEmail,
		StartsAt:       startsAt,
		ClientOpID:     in.ClientOpID,
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	body := gin.H{"booking": publicBookingJSON(b)}
	// payment (F11, D8): clave ADITIVA que solo aparece cuando la política está
	// activa y el monto es > 0. El id del cargo viaja en el fragmento de la URL
	// de la página del banco, nunca en un log del servidor.
	if payment != nil {
		body["payment"] = gin.H{
			"deposit_id":   payment.DepositID,
			"amount_cents": payment.AmountCents,
			"expires_at":   payment.ExpiresAt.UTC().Format(time.RFC3339),
			"pay_url":      payment.PayURL,
			"simulated":    true,
		}
	}
	c.JSON(http.StatusCreated, body)
}
