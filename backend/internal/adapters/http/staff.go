package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// StaffHandler: GET /api/v1/salons/:id/staff. El equipo que atiende, con id y
// nombre: es la fuente del select de profesional del frontend.
type StaffHandler struct {
	Staff       app.StaffService
	Memberships ports.MembershipRepository
}

func (h StaffHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// List: GET /api/v1/salons/:id/staff -> {"staff":[{"id","name","role"}]}
func (h StaffHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Staff.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, m := range list {
		out = append(out, gin.H{"id": m.ID, "name": m.Name, "role": string(m.Role)})
	}
	c.JSON(http.StatusOK, gin.H{"staff": out})
}
