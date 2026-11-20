package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
)

// MeHandler: GET /api/v1/me. Devuelve quién es el dueño del token y en qué
// salones está. Es lo que permite que el frontend elija el salón activo sin
// pedirle un ID a la persona.
type MeHandler struct {
	Me app.MeService
}

func (h MeHandler) Get(c *gin.Context) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	me, err := h.Me.Me(userID)
	if err != nil {
		respondErr(c, err)
		return
	}
	salons := make([]gin.H, 0, len(me.Salons))
	for _, s := range me.Salons {
		salons = append(salons, gin.H{"id": s.ID, "slug": s.Slug, "name": s.Name, "role": string(s.Role)})
	}
	c.JSON(http.StatusOK, gin.H{
		"user":   gin.H{"id": me.User.ID, "name": me.User.Name, "email": me.User.Email},
		"salons": salons,
	})
}
