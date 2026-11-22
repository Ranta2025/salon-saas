package httpapi

import (
	"errors"
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

// loadActor: la membresía de quien pide. "No es miembro" es 403; cualquier
// otra falla (DB caída) es 500 con el detalle en el log (H4). Antes TODO error
// de Find se veía como 403: la persona creía que no tenía permiso cuando el
// problema era del servidor, y nadie miraba el log.
func (h StaffHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err == nil {
		return m, true
	}
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	// respondErr deja el detalle en el log del servidor y responde genérico: no
	// agrega el id de la persona ni su email a la línea.
	respondErr(c, err)
	return domain.Membership{}, false
}

// List: GET /api/v1/salons/:id/staff -> {"staff":[{"id","name","role","attends"}]}
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
		// attends explica por qué está en la lista: el profesional atiende por
		// rol y el dueño/administración por la marca (F2a).
		out = append(out, gin.H{"id": m.ID, "name": m.Name, "role": string(m.Role), "attends": m.Attends})
	}
	c.JSON(http.StatusOK, gin.H{"staff": out})
}
