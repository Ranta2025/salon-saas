// Enlace firmado de un solo uso para cancelar o reprogramar el turno desde el
// email (F1). Cuatro rutas PÚBLICAS, sin sesión:
//
//	POST /api/v1/public/booking-links/lookup     {token}
//	POST /api/v1/public/booking-links/slots      {token, date}
//	POST /api/v1/public/booking-links/cancel     {token}
//	POST /api/v1/public/booking-links/reschedule {token, starts_at}
//
// El token viaja SIEMPRE en el cuerpo (nunca en la ruta ni en el query: no queda
// en ningún log). Un token que no sirve responde el MISMO 404 con el MISMO texto,
// y con un token vigente las respuestas no llevan teléfono, ni email, ni ids
// internos del cliente (D6).
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// BookingLinkHandler traduce HTTP <-> el caso de uso del enlace.
type BookingLinkHandler struct {
	Links app.BookingLinkService
}

// bookingLinkJSON es la forma única de un turno abierto por su enlace. Es la
// misma en `lookup` y en `reschedule` (contrato congelado para el frontend).
func bookingLinkJSON(v app.BookingLinkView) gin.H {
	return gin.H{
		"salon_name":        v.SalonName,
		"service_name":      v.ServiceName,
		"professional_name": v.ProfessionalName,
		"starts_at":         v.StartsAt.UTC().Format(time.RFC3339),
		"ends_at":           v.EndsAt.UTC().Format(time.RFC3339),
		"timezone":          v.Timezone,
		"can_change":        v.CanChange,
		"min_notice_hours":  domain.LinkMinNoticeHours,
	}
}

// linkToken lee el token del cuerpo. Un token que no es una cadena (número, objeto
// o null) es `400 body inválido`, nunca un 500 ni un 404 mentiroso.
func linkToken(c *gin.Context) (string, bool) {
	var in struct {
		Token *string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Token == nil {
		badBody(c)
		return "", false
	}
	return *in.Token, true
}

// Lookup: POST /public/booking-links/lookup {token} -> 200 {booking}.
func (h BookingLinkHandler) Lookup(c *gin.Context) {
	token, ok := linkToken(c)
	if !ok {
		return
	}
	view, err := h.Links.Lookup(token)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"booking": bookingLinkJSON(view)})
}

// Slots: POST /public/booking-links/slots {token, date} -> 200 {date, slots} con
// la MISMA forma que la disponibilidad pública. `date` es un día local del salón.
func (h BookingLinkHandler) Slots(c *gin.Context) {
	var in struct {
		Token *string `json:"token"`
		Date  *string `json:"date"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Token == nil || in.Date == nil {
		badBody(c)
		return
	}
	day, err := h.Links.Slots(*in.Token, *in.Date)
	if err != nil {
		respondErr(c, err)
		return
	}
	slots := make([]gin.H, 0, len(day.Slots))
	for _, s := range day.Slots {
		slots = append(slots, gin.H{
			"professional_id": s.ProfessionalID,
			"starts_at":       s.StartsAt.UTC().Format(time.RFC3339),
			"ends_at":         s.EndsAt.UTC().Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"date": day.Date, "slots": slots})
}

// Cancel: POST /public/booking-links/cancel {token} -> 200 {"status":"cancelled"}.
func (h BookingLinkHandler) Cancel(c *gin.Context) {
	token, ok := linkToken(c)
	if !ok {
		return
	}
	if err := h.Links.Cancel(token); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

// Reschedule: POST /public/booking-links/reschedule {token, starts_at} -> 200
// {"booking":{...},"new_link_sent":bool}. El token NUEVO del correo nunca viaja
// en la respuesta: solo se dice si el aviso quedó encolado.
func (h BookingLinkHandler) Reschedule(c *gin.Context) {
	var in struct {
		Token    *string `json:"token"`
		StartsAt *string `json:"starts_at"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Token == nil || in.StartsAt == nil {
		badBody(c)
		return
	}
	startsAt, err := time.Parse(time.RFC3339, *in.StartsAt)
	if err != nil {
		badBody(c)
		return
	}
	view, newLinkSent, err := h.Links.Reschedule(*in.Token, startsAt)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"booking": bookingLinkJSON(view), "new_link_sent": newLinkSent})
}
