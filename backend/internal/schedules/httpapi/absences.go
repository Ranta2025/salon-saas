package httpapi

// AbsenceHandler: cubrir una ausencia (F2d3). Delgado como el resto: parsea,
// carga el actor y llama al caso de uso. La autorización (solo dueño y
// administración) vive en schedules.CoverageService.
//
//	GET  /salons/:id/absences?from&to
//	POST /salons/:id/absences/:tid/reassign   {"professional_id": "...", "booking_ids": [...]}
//	POST /salons/:id/bookings/:bid/reassign   {"professional_id": "..."} | {"cancel": true, "reason": "..."}
import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/schedules"
)

type AbsenceHandler struct {
	Coverage    schedules.CoverageService
	Memberships ports.MembershipRepository
}

func (h AbsenceHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// coverageCandidateJSON: el selector de la pantalla necesita id, nombre y por qué
// no sirve. `available` es reason == "" (lo mismo que devuelve el POST al
// rechazarla).
func coverageCandidateJSON(c schedules.CoverageCandidate) gin.H {
	return gin.H{
		"professional_id": c.ProfessionalID,
		"name":            c.Name,
		"available":       c.Reason == "",
		"reason":          c.Reason,
	}
}

// coverageBookingJSON es un turno de la ausencia con sus candidatas.
func coverageBookingJSON(b schedules.CoverageBooking) gin.H {
	candidates := make([]gin.H, 0, len(b.Candidates))
	for _, c := range b.Candidates {
		candidates = append(candidates, coverageCandidateJSON(c))
	}
	return gin.H{
		"id":           b.Booking.ID,
		"client_name":  b.Booking.ClientName,
		"starts_at":    b.Booking.StartsAt.UTC().Format(time.RFC3339),
		"ends_at":      b.Booking.EndsAt.UTC().Format(time.RFC3339),
		"service_id":   httpx.OptionalID(b.Booking.ServiceID),
		"service_name": b.ServiceName,
		"candidates":   candidates,
	}
}

// reassignedBookingsJSON es el detalle de la respuesta y del error: qué turnos
// se movieron (o cuáles no). Siempre un array.
func reassignedBookingsJSON(list []domain.Booking) []gin.H {
	out := make([]gin.H, 0, len(list))
	for _, b := range list {
		out = append(out, gin.H{
			"id":          b.ID,
			"client_name": b.ClientName,
			"starts_at":   b.StartsAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// respondCoverageErr es el camino de error de la cobertura: un rechazo con
// detalle (400 si la regla no se cumple, 409 si el turno ya no está reservado o
// el destino ya tiene ese horario) lleva SIEMPRE la lista de turnos afectados
// (S-F2d3-3).
func respondCoverageErr(c *gin.Context, err error) {
	var conflict *schedules.CoverageConflict
	if errors.As(err, &conflict) {
		c.JSON(httpx.StatusOf(conflict.Kind), gin.H{
			"error":    conflict.Text,
			"bookings": reassignedBookingsJSON(conflict.Bookings),
		})
		return
	}
	httpx.RespondErr(c, err)
}

// List: GET /salons/:id/absences?from=RFC3339&to=RFC3339
// -> {"absences":[{id,user_id,user_name,starts_at,ends_at,reason,bookings:[...]}]}
func (h AbsenceHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	from, okFrom := parseRFC3339(c.Query("from"))
	to, okTo := parseRFC3339(c.Query("to"))
	if !okFrom || !okTo {
		c.JSON(http.StatusBadRequest, gin.H{"error": msgTimeOffDateFormat})
		return
	}
	list, err := h.Coverage.Absences(actor, salonID, from, to)
	if err != nil {
		respondCoverageErr(c, err)
		return
	}
	absences := make([]gin.H, 0, len(list))
	for _, item := range list {
		bookings := make([]gin.H, 0, len(item.Bookings))
		for _, b := range item.Bookings {
			bookings = append(bookings, coverageBookingJSON(b))
		}
		absences = append(absences, gin.H{
			"id":        item.TimeOff.ID,
			"user_id":   item.TimeOff.UserID,
			"user_name": item.UserName,
			"starts_at": item.TimeOff.StartsAt.UTC().Format(time.RFC3339),
			"ends_at":   item.TimeOff.EndsAt.UTC().Format(time.RFC3339),
			"reason":    item.TimeOff.Reason,
			"bookings":  bookings,
		})
	}
	c.JSON(http.StatusOK, gin.H{"absences": absences})
}

// ReassignAbsence: POST /salons/:id/absences/:tid/reassign
// {"professional_id":"...","booking_ids":["..."]?} (sin lista = todos)
// -> 200 {"reassigned":N,"bookings":[{id,client_name,starts_at}]}
func (h AbsenceHandler) ReassignAbsence(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		ProfessionalID string   `json:"professional_id"`
		BookingIDs     []string `json:"booking_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	result, err := h.Coverage.ReassignAbsence(actor, salonID, c.Param("tid"), in.ProfessionalID, in.BookingIDs)
	if err != nil {
		respondCoverageErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reassigned": result.Reassigned,
		"bookings":   reassignedBookingsJSON(result.Bookings),
	})
}

// ReassignBooking: POST /salons/:id/bookings/:bid/reassign
// {"professional_id":"..."} -> 200 {"reassigned":1,"bookings":[...]}
// {"cancel":true,"reason":"..."} -> 200 {"cancelled":true,"booking":{...}}
func (h AbsenceHandler) ReassignBooking(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		ProfessionalID string `json:"professional_id"`
		Cancel         bool   `json:"cancel"`
		Reason         string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	bookingID := c.Param("bid")
	if in.Cancel {
		b, err := h.Coverage.CancelBooking(actor, salonID, bookingID, in.Reason)
		if err != nil {
			respondCoverageErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"cancelled": true, "booking": httpx.BookingJSON(b)})
		return
	}
	result, err := h.Coverage.ReassignBooking(actor, salonID, bookingID, in.ProfessionalID)
	if err != nil {
		respondCoverageErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reassigned": result.Reassigned,
		"bookings":   reassignedBookingsJSON(result.Bookings),
	})
}
