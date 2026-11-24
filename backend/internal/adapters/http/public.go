package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
)

// PublicHandler: las tres rutas de la landing, SIN auth.
//   - GET  /public/salons/:slug                (vidriera)
//   - GET  /public/salons/:slug/availability   (huecos libres)
//   - POST /public/salons/:slug/bookings       (reserva)
type PublicHandler struct {
	Public   app.PublicSalonService
	Avail    app.AvailabilityService
	Bookings app.PublicBookingService
}

// GetSalon: GET /api/v1/public/salons/:slug. La vidriera del salón (F16, D4):
// perfil, la carta de servicios activos que alguien hace (con precio, duración y
// descripción), el equipo que atiende (solo nombre y oficio), la galería, los
// paquetes activos y el horario de los días abiertos (F2d1). Todo sale de lo que
// el dueño edita en el panel; las secciones vacías son listas vacías.
func (h PublicHandler) GetSalon(c *gin.Context) {
	p, err := h.Public.Public(c.Param("slug"))
	if err != nil {
		respondErr(c, err)
		return
	}
	services := make([]gin.H, 0, len(p.Services))
	for _, s := range p.Services {
		services = append(services, gin.H{
			"id":           s.ID,
			"name":         s.Name,
			"duration_min": s.DurationMin,
			"price_cents":  s.PriceCents,
			// description (F16, D2): null si el servicio no tiene.
			"description": optionalID(s.Description),
		})
	}
	// Equipo: SOLO nombre y oficio. Ni ids, ni email, ni rol, ni pagos.
	team := make([]gin.H, 0, len(p.Team))
	for _, m := range p.Team {
		team = append(team, gin.H{"name": m.Name, "trade": m.Trade})
	}
	gallery := make([]gin.H, 0, len(p.Gallery))
	for _, ph := range p.Gallery {
		gallery = append(gallery, gin.H{"id": ph.ID, "url": ph.URL, "caption": optionalID(ph.Caption)})
	}
	packages := make([]gin.H, 0, len(p.Packages))
	for _, pk := range p.Packages {
		var validity any
		if pk.ValidityDays != nil {
			validity = *pk.ValidityDays
		}
		packages = append(packages, gin.H{
			"id":            pk.ID,
			"name":          pk.Name,
			"service_name":  pk.ServiceName,
			"sessions":      pk.Sessions,
			"price_cents":   pk.PriceCents,
			"validity_days": validity,
		})
	}
	// `hours` viene ya expandido: solo los días abiertos. La landing pone
	// "Cerrado" en los que no están y resalta el día de hoy.
	hours := make([]gin.H, 0, len(p.Hours))
	for _, d := range p.Hours {
		hours = append(hours, gin.H{"weekday": d.Weekday, "open": d.Open, "close": d.Close})
	}
	c.JSON(http.StatusOK, gin.H{
		"salon": gin.H{
			"id":          p.Salon.ID,
			"slug":        p.Salon.Slug,
			"name":        p.Profile.Name,
			"description": optionalID(p.Profile.Description),
			"address":     optionalID(p.Profile.Address),
			"phone":       optionalID(p.Profile.Phone),
			"email":       optionalID(p.Profile.Email),
			"instagram":   optionalID(p.Profile.Instagram),
			"timezone":    p.Timezone,
		},
		"services": services,
		"team":     team,
		"gallery":  gallery,
		"packages": packages,
		"hours":    hours,
	})
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
	b, err := h.Bookings.Book(c.Param("slug"), app.PublicBookingInput{
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
	c.JSON(http.StatusCreated, gin.H{"booking": publicBookingJSON(b)})
}
