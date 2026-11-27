package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/schedules"
)

// HoursHandler: horario semanal del salón y de cada profesional (F2d1).
//
//	GET/PUT /salons/:id/hours
//	GET/PUT /salons/:id/members/:uid/hours
//
// La autorización vive en los casos de uso (cualquier miembro lee el horario del
// salón, owner/admin lo edita; cada persona lee el suyo).
type HoursHandler struct {
	Salon        schedules.SalonHoursService
	Professional schedules.ProfessionalHoursService
	Memberships  ports.MembershipRepository
}

func (h HoursHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// daysJSON serializa el horario del salón: siempre un array (nunca null), porque
// el frontend pinta una fila por día.
func daysJSON(days []domain.WeekdayHours) []gin.H {
	out := make([]gin.H, 0, len(days))
	for _, d := range days {
		out = append(out, gin.H{"weekday": d.Weekday, "open": d.Open, "close": d.Close})
	}
	return out
}

// segmentsJSON serializa los tramos de una profesional: siempre un array.
func segmentsJSON(segments []domain.HoursSegment) []gin.H {
	out := make([]gin.H, 0, len(segments))
	for _, s := range segments {
		out = append(out, gin.H{"weekday": s.Weekday, "start": s.Start, "end": s.End})
	}
	return out
}

// GetSalon: GET /api/v1/salons/:id/hours -> {"days":[...],"custom":bool}
func (h HoursHandler) GetSalon(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	days, custom, err := h.Salon.Get(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": daysJSON(days), "custom": custom})
}

// UpdateSalon: PUT /api/v1/salons/:id/hours {"days":[...]} -> 200 con el horario
// guardado. Reemplazo total; la lista vacía vuelve al horario único.
func (h HoursHandler) UpdateSalon(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Days []struct {
			Weekday int    `json:"weekday"`
			Open    string `json:"open"`
			Close   string `json:"close"`
		} `json:"days"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	days := make([]domain.WeekdayHours, 0, len(in.Days))
	for _, d := range in.Days {
		days = append(days, domain.WeekdayHours{Weekday: d.Weekday, Open: d.Open, Close: d.Close})
	}
	saved, custom, err := h.Salon.Set(actor, salonID, days)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": daysJSON(saved), "custom": custom})
}

// GetMember: GET /api/v1/salons/:id/members/:uid/hours ->
// {"segments":[...],"custom":bool}
func (h HoursHandler) GetMember(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	segments, custom, err := h.Professional.Get(actor, salonID, c.Param("uid"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"segments": segmentsJSON(segments), "custom": custom})
}

// UpdateMember: PUT /api/v1/salons/:id/members/:uid/hours {"segments":[...]} ->
// 200 con los tramos guardados. Reemplazo total; la lista vacía vuelve a "igual
// que el salón".
func (h HoursHandler) UpdateMember(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Segments []struct {
			Weekday int    `json:"weekday"`
			Start   string `json:"start"`
			End     string `json:"end"`
		} `json:"segments"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	segments := make([]domain.HoursSegment, 0, len(in.Segments))
	for _, s := range in.Segments {
		segments = append(segments, domain.HoursSegment{Weekday: s.Weekday, Start: s.Start, End: s.End})
	}
	saved, custom, err := h.Professional.Set(actor, salonID, c.Param("uid"), segments)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"segments": segmentsJSON(saved), "custom": custom})
}
