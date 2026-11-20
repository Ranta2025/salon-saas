package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// MemberHandler: pantalla de equipo del salón (GET/POST/DELETE
// /salons/:id/members). Solo owner/admin; la regla de qué rol puede sumar cada
// uno vive en app/domain.
type MemberHandler struct {
	Members     app.MemberService
	Memberships ports.MembershipRepository
}

func (h MemberHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// memberJSON es la forma única de serializar un miembro del equipo.
func memberJSON(m domain.Member) gin.H {
	return gin.H{"id": m.ID, "name": m.Name, "email": m.Email, "role": string(m.Role)}
}

// List: GET /api/v1/salons/:id/members -> {"members":[{id,name,email,role}]}
func (h MemberHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Members.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, m := range list {
		out = append(out, memberJSON(m))
	}
	c.JSON(http.StatusOK, gin.H{"members": out})
}

// Add: POST /api/v1/salons/:id/members {"email","role"} -> 201 miembro.
func (h MemberHandler) Add(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	m, err := h.Members.Add(actor, salonID, in.Email, domain.Role(in.Role))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, memberJSON(m))
}

// Remove: DELETE /api/v1/salons/:id/members/:uid -> 204.
func (h MemberHandler) Remove(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Members.Remove(actor, salonID, c.Param("uid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
