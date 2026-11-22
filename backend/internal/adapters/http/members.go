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
// `attends` (F2a) dice si esa persona atiende clientes; es lo que muestra la
// pantalla de equipo y lo que decide la agenda.
func memberJSON(m domain.Member) gin.H {
	return gin.H{"id": m.ID, "name": m.Name, "email": m.Email, "role": string(m.Role), "attends": m.Attends}
}

// List: GET /api/v1/salons/:id/members -> {"members":[{id,name,email,role,attends}]}
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

// Add: POST /api/v1/salons/:id/members {"email","role","attends?"} -> 201 miembro.
// `attends` es opcional: sin él manda el default del rol (S-F2a-4).
func (h MemberHandler) Add(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Email   string `json:"email"`
		Role    string `json:"role"`
		Attends *bool  `json:"attends"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	m, err := h.Members.Add(actor, salonID, in.Email, domain.Role(in.Role), in.Attends)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, memberJSON(m))
}

// UpdateAttends: PATCH /api/v1/salons/:id/members/:uid {"attends":true|false}
// -> 200 con el miembro ya actualizado (F2a). El body es obligatorio: la marca
// no se adivina.
func (h MemberHandler) UpdateAttends(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Attends *bool `json:"attends"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Attends == nil {
		badBody(c)
		return
	}
	m, err := h.Members.SetAttends(actor, salonID, c.Param("uid"), *in.Attends)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, memberJSON(m))
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
