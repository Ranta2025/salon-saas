package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/domain"
)

// Servicios con descripción (F16, D2). Los handlers de pos.go (Create, Update,
// List) siguen siendo los de antes de F16; estos los reemplazan en las rutas y
// agregan UNA clave OPCIONAL, `description` (<=300, string o null), tanto en el
// body como en el JSON del servicio. Nada de lo anterior cambia: sin la clave el
// alta crea el servicio sin descripción y la edición CONSERVA la que tiene.

// serviceDescribedJSON es la forma única del servicio con su descripción (null
// si no tiene): serviceJSON más la clave nueva.
func serviceDescribedJSON(s domain.Service) gin.H {
	out := serviceJSON(s)
	out["description"] = optionalID(s.Description)
	return out
}

// CreateDescribed: POST /salons/:id/services
func (h ServiceHandler) CreateDescribed(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name            string   `json:"name"`
		DurationMin     int      `json:"duration_min"`
		PriceCents      int      `json:"price_cents"`
		CommissionPct   int      `json:"commission_pct"`
		ProfessionalIDs []string `json:"professional_ids"`
		ResourceTypeID  string   `json:"resource_type_id"`
		// Description es opcional: ausente, null o "" = sin descripción.
		Description presentText `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	svc, err := h.Services.CreateDescribed(actor, salonID, in.Name, in.DurationMin, in.PriceCents, in.CommissionPct,
		in.ProfessionalIDs, in.ResourceTypeID, in.Description.String())
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, serviceDescribedJSON(svc))
}

// UpdateDescribed: PUT /salons/:id/services/:service_id
func (h ServiceHandler) UpdateDescribed(c *gin.Context) {
	salonID := c.Param("id")
	serviceID := c.Param("service_id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name           string `json:"name"`
		DurationMin    int    `json:"duration_min"`
		PriceCents     int    `json:"price_cents"`
		CommissionPct  int    `json:"commission_pct"`
		Active         *bool  `json:"active"` // ausente = sigue activo
		ResourceTypeID string `json:"resource_type_id"`
		// Description: ausente = se CONSERVA la actual (un cliente anterior a F16
		// no la borra sin querer); null o "" = se quita; con texto, se reemplaza.
		Description presentText `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	var description *string
	if in.Description.Set {
		text := in.Description.String()
		description = &text
	}
	svc, err := h.Services.UpdateDescribed(actor, salonID, serviceID, in.Name, in.DurationMin, in.PriceCents,
		in.CommissionPct, active, in.ResourceTypeID, description)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, serviceDescribedJSON(svc))
}

// ListDescribed: GET /salons/:id/services?include_inactive=true
func (h ServiceHandler) ListDescribed(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Services.List(actor, salonID, c.Query("include_inactive") == "true")
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, s := range list {
		out = append(out, serviceDescribedJSON(s))
	}
	c.JSON(http.StatusOK, gin.H{"services": out})
}
