// TenantsHandler: alta de salón (y el equipo que la acompaña). Delgado: parsea,
// delega en el caso de uso y mapea el error al status del contrato.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/ports"
	"salon-saas/internal/team"
)

type TenantsHandler struct {
	Tenants     team.TenantService
	Memberships ports.MembershipRepository
}

func (h TenantsHandler) CreateSalon(c *gin.Context) {
	uid, _ := c.Get("userID")
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	s, err := h.Tenants.CreateSalon(uid.(string), in.Name, in.Slug)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": s.ID, "name": s.Name, "slug": s.Slug})
}
