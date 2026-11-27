package httpapi

import (
	"net/http"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"time"

	"github.com/gin-gonic/gin"
)

// BookingHandler: agenda y calendario. Delgado: parsea, carga actor, llama app.
type BookingHandler struct {
	Bookings    bookings.BookingService
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
	c.JSON(http.StatusCreated, bookingJSON(b))
}

// publicBookingJSON es la forma de la reserva pública: el frontend de la
// landing solo necesita esto.
func publicBookingJSON(b domain.Booking) gin.H { return httpx.PublicBookingJSON(b) }

// bookingJSON es la reserva completa (calendario del salón y PATCH).
// `service_id` (F2c) es el servicio reservado y puede ser null: el turno que se
// agenda a mano desde el calendario no exige servicio. `covered_from`
// (S-F2d3-6) es quién TENÍA el turno antes de que lo cubriera otra persona, o
// null si nadie lo cubre: solo viaja en el JSON del personal ("Mi agenda" lo
// usa para avisar "lo cubre otra profesional"), nunca en el público ni en el
// payload del websocket.
func bookingJSON(b domain.Booking) gin.H { return httpx.BookingJSON(b) }

// optionalID serializa un id opcional como texto o null (nunca ""): el
// frontend distingue "sin servicio" de "servicio vacío".
func optionalID(id string) any { return httpx.OptionalID(id) }

// Update: PATCH /salons/:id/bookings/:bid
// Body: {"status":"cancelled|no_show|done"} y/o {"starts_at","ends_at",
// "professional_id"} para reprogramar. Respuesta 200 {booking}. Publica
// booking.updated (lo hace el caso de uso).
func (h BookingHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Status         *string `json:"status"`
		StartsAt       *string `json:"starts_at"`
		EndsAt         *string `json:"ends_at"`
		ProfessionalID *string `json:"professional_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	patch := bookings.BookingPatch{ProfessionalID: in.ProfessionalID}
	if in.Status != nil {
		status := domain.BookingStatus(*in.Status)
		patch.Status = &status
	}
	for _, item := range []struct {
		src   *string
		dst   **time.Time
		field string
	}{{in.StartsAt, &patch.StartsAt, "starts_at"}, {in.EndsAt, &patch.EndsAt, "ends_at"}} {
		if item.src == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, *item.src)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": item.field + " inválido (RFC3339)"})
			return
		}
		*item.dst = &t
	}
	b, err := h.Bookings.Update(actor, salonID, c.Param("bid"), patch)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"booking": bookingJSON(b)})
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
		out = append(out, bookingJSON(b))
	}
	c.JSON(http.StatusOK, gin.H{"bookings": out})
}
