package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// SettingsHandler: configuración operativa del salón (zona horaria, horario y
// paso de turnos). Cualquier miembro lee; owner/admin escribe.
type SettingsHandler struct {
	Settings    app.SettingsService
	Memberships ports.MembershipRepository
}

func (h SettingsHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// settingsJSON es la forma única de serializar la configuración.
func settingsJSON(s domain.SalonSettings) gin.H {
	return gin.H{
		"timezone":      s.Timezone,
		"open_time":     s.OpenTime,
		"close_time":    s.CloseTime,
		"slot_step_min": s.SlotStepMin,
	}
}

// Get: GET /api/v1/salons/:id/settings
func (h SettingsHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	settings, err := h.Settings.Get(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, settingsJSON(settings))
}

// Update: PUT /api/v1/salons/:id/settings (body completo, cuatro campos).
func (h SettingsHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Timezone    string `json:"timezone"`
		OpenTime    string `json:"open_time"`
		CloseTime   string `json:"close_time"`
		SlotStepMin int    `json:"slot_step_min"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	settings, err := h.Settings.Update(actor, salonID, domain.SalonSettings{
		SalonID:     salonID,
		Timezone:    in.Timezone,
		OpenTime:    in.OpenTime,
		CloseTime:   in.CloseTime,
		SlotStepMin: in.SlotStepMin,
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, settingsJSON(settings))
}
