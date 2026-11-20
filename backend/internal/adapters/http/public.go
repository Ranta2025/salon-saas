package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
)

// PublicHandler: GET /api/v1/public/salons/:slug. La vidriera de la landing,
// SIN auth: solo nombre, slug y la carta de servicios activos.
type PublicHandler struct {
	Public app.PublicSalonService
}

func (h PublicHandler) GetSalon(c *gin.Context) {
	p, err := h.Public.Public(c.Param("slug"))
	if err != nil {
		respondErr(c, err)
		return
	}
	services := make([]gin.H, 0, len(p.Services))
	for _, s := range p.Services {
		services = append(services, gin.H{
			"id":           s.ID,
			"name":         s.Name,
			"duration_min": s.DurationMin,
			"price_cents":  s.PriceCents,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"salon":    gin.H{"id": p.Salon.ID, "slug": p.Salon.Slug, "name": p.Salon.Name},
		"services": services,
	})
}
