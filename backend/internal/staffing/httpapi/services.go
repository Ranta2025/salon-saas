package httpapi

import (
	"net/http"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	ports "salon-saas/internal/ports"
	staffing "salon-saas/internal/staffing"

	"github.com/gin-gonic/gin"
)

// Servicios con descripción (F16, D2). Los handlers de pos.go (Create, Update,
// List) siguen siendo los de antes de F16; estos los reemplazan en las rutas y
// agregan UNA clave OPCIONAL, `description` (<=300, string o null), tanto en el
// body como en el JSON del servicio. Nada de lo anterior cambia: sin la clave el
// alta crea el servicio sin descripción y la edición CONSERVA la que tiene.

// serviceDescribedJSON es la forma única del servicio con su descripción (null
// si no tiene): serviceJSON más la clave nueva.
func serviceDescribedJSON(s domain.Service) gin.H {
	out := ServiceJSON(s)
	out["description"] = httpx.OptionalID(s.Description)
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
		Description httpx.PresentText `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	svc, err := h.Services.CreateDescribed(actor, salonID, in.Name, in.DurationMin, in.PriceCents, in.CommissionPct,
		in.ProfessionalIDs, in.ResourceTypeID, in.Description.String())
	if err != nil {
		httpx.RespondErr(c, err)
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
		Description httpx.PresentText `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
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
		httpx.RespondErr(c, err)
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
		httpx.RespondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, s := range list {
		out = append(out, serviceDescribedJSON(s))
	}
	c.JSON(http.StatusOK, gin.H{"services": out})
}

// ServiceHandler: catálogo de servicios. Delgado: parsea, carga actor, llama app.
type ServiceHandler struct {
	Services    staffing.ServiceService
	Memberships ports.MembershipRepository
}

func (h ServiceHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return httpx.LoadActor(h.Memberships, c, salonID)
}

// ServiceJSON es la forma única de serializar un servicio: la usan Create,
// Update y List para que el frontend vea siempre el mismo objeto.
func ServiceJSON(s domain.Service) gin.H {
	return gin.H{
		"id":             s.ID,
		"salon_id":       s.SalonID,
		"name":           s.Name,
		"duration_min":   s.DurationMin,
		"price_cents":    s.PriceCents,
		"commission_pct": s.CommissionPct,
		"active":         s.Active,
		// resource_type_id (F2e) puede ser null: el servicio no usa equipo.
		"resource_type_id": httpx.OptionalID(s.ResourceTypeID),
	}
}
