package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// TimeOffHandler: ausencias (vacaciones y días libres puntuales) de cada
// profesional (F2d2).
//
//	GET    /salons/:id/time-off?from&to
//	POST   /salons/:id/members/:uid/time-off
//	DELETE /salons/:id/time-off/:tid
//
// La autorización vive en los casos de uso: cualquier miembro lee (con el motivo
// recortado), solo dueño/administración carga y borra.
type TimeOffHandler struct {
	TimeOff     app.TimeOffService
	Memberships ports.MembershipRepository
}

// Mensajes de la consulta, en español y listos para mostrar: las fechas viajan
// en RFC3339 (igual que los turnos) y el período es obligatorio.
const (
	msgTimeOffDateFormat = "Fecha inválida: usá RFC3339"
	msgTimeOffMissing    = "Indicá el inicio y el fin de la ausencia"
)

func (h TimeOffHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// timeOffJSON es la forma única de una ausencia en la API. Las horas van en UTC
// RFC3339 (la UI las pasa a la zona del salón); el motivo puede venir vacío por
// privacidad.
func timeOffJSON(off domain.TimeOff) gin.H {
	return gin.H{
		"id":        off.ID,
		"user_id":   off.UserID,
		"starts_at": off.StartsAt.UTC().Format(time.RFC3339),
		"ends_at":   off.EndsAt.UTC().Format(time.RFC3339),
		"reason":    off.Reason,
	}
}

// conflictJSON es un turno que se pisa con la ausencia recién creada: el salón
// lo reasigna (F2d3) o lo cancela. No se cancela solo (S-F2d2-4).
func conflictJSON(b domain.Booking) gin.H {
	return gin.H{
		"id":          b.ID,
		"client_name": b.ClientName,
		"starts_at":   b.StartsAt.UTC().Format(time.RFC3339),
		"ends_at":     b.EndsAt.UTC().Format(time.RFC3339),
	}
}

// parseRFC3339 lee una fecha del contrato. Vacía o ilegible = error de forma.
func parseRFC3339(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// List: GET /api/v1/salons/:id/time-off?from=RFC3339&to=RFC3339
// -> {"time_off":[{id,user_id,starts_at,ends_at,reason}]}
func (h TimeOffHandler) List(c *gin.Context) {
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
	list, err := h.TimeOff.List(actor, salonID, from, to)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, off := range list {
		out = append(out, timeOffJSON(off))
	}
	c.JSON(http.StatusOK, gin.H{"time_off": out})
}

// Create: POST /api/v1/salons/:id/members/:uid/time-off
// {"starts_at","ends_at","reason?"} -> 201 {"time_off":{...},"conflicts":[...]}
func (h TimeOffHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if strings.TrimSpace(in.StartsAt) == "" || strings.TrimSpace(in.EndsAt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msgTimeOffMissing})
		return
	}
	startsAt, okFrom := parseRFC3339(in.StartsAt)
	endsAt, okTo := parseRFC3339(in.EndsAt)
	if !okFrom || !okTo {
		c.JSON(http.StatusBadRequest, gin.H{"error": msgTimeOffDateFormat})
		return
	}
	result, err := h.TimeOff.Create(actor, salonID, c.Param("uid"), startsAt, endsAt, in.Reason)
	if err != nil {
		respondErr(c, err)
		return
	}
	conflicts := make([]gin.H, 0, len(result.Conflicts))
	for _, b := range result.Conflicts {
		conflicts = append(conflicts, conflictJSON(b))
	}
	c.JSON(http.StatusCreated, gin.H{
		"time_off":  timeOffJSON(result.TimeOff),
		"conflicts": conflicts,
	})
}

// Delete: DELETE /api/v1/salons/:id/time-off/:tid -> 204.
func (h TimeOffHandler) Delete(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.TimeOff.Delete(actor, salonID, c.Param("tid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
