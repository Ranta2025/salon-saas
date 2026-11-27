package httpapi

import (
	"net/http"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/staffing"

	"github.com/gin-gonic/gin"
)

// ResourceHandler: puestos y equipamiento con capacidad (F2e).
//
//	GET    /salons/:id/resources
//	POST   /salons/:id/resources              {name, count}
//	PATCH  /salons/:id/resources/:tid         {name}
//	DELETE /salons/:id/resources/:tid
//	POST   /salons/:id/resources/:tid/units   {count}
//	PATCH  /salons/:id/resource-units/:uid    {name?, active?}
//
// Delgado como el resto: parsea, carga actor y llama al caso de uso. La
// autorización (leer cualquier miembro, escribir dueño/administración) vive en
// ResourceService; acá solo se traduce el error a su status.
type ResourceHandler struct {
	Resources   staffing.ResourceService
	Memberships ports.MembershipRepository
}

func (h ResourceHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// resourceUnitJSON es la forma única de una unidad en la API.
func resourceUnitJSON(u domain.ResourceUnit) gin.H {
	return gin.H{"id": u.ID, "name": u.Name, "active": u.Active}
}

// resourceTypeJSON es la forma única de un tipo con sus unidades.
func resourceTypeJSON(v staffing.ResourceTypeView) gin.H {
	units := make([]gin.H, 0, len(v.Units))
	for _, u := range v.Units {
		units = append(units, resourceUnitJSON(u))
	}
	return gin.H{
		"id":       v.Type.ID,
		"name":     v.Type.Name,
		"units":    units,
		"services": v.Services,
	}
}

// List: GET /salons/:id/resources -> {"types":[...]}
func (h ResourceHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	types, err := h.Resources.List(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(types))
	for _, t := range types {
		out = append(out, resourceTypeJSON(t))
	}
	c.JSON(http.StatusOK, gin.H{"types": out})
}

// Create: POST /salons/:id/resources {name, count} -> 201 con el tipo.
func (h ResourceHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	view, err := h.Resources.Create(actor, salonID, in.Name, in.Count)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, resourceTypeJSON(view))
}

// Rename: PATCH /salons/:id/resources/:tid {name} -> 200 con el tipo.
func (h ResourceHandler) Rename(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	rt, err := h.Resources.Rename(actor, salonID, c.Param("tid"), in.Name)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	units, err := h.Resources.List(actor, salonID)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	for _, v := range units {
		if v.Type.ID == rt.ID {
			c.JSON(http.StatusOK, resourceTypeJSON(v))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": rt.ID, "name": rt.Name, "units": []gin.H{}, "services": 0})
}

// AddUnits: POST /salons/:id/resources/:tid/units {count} -> 201 con las nuevas.
func (h ResourceHandler) AddUnits(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Count int `json:"count"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	units, err := h.Resources.AddUnits(actor, salonID, c.Param("tid"), in.Count)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(units))
	for _, u := range units {
		out = append(out, resourceUnitJSON(u))
	}
	c.JSON(http.StatusCreated, gin.H{"units": out})
}

// UpdateUnit: PATCH /salons/:id/resource-units/:uid {name?, active?}. Quitar
// equipo es DESACTIVAR: la respuesta dice cuántos turnos futuros lo usan.
func (h ResourceHandler) UpdateUnit(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name   string `json:"name"`
		Active *bool  `json:"active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.BadBody(c)
		return
	}
	got, err := h.Resources.UpdateUnit(actor, salonID, c.Param("uid"), in.Name, in.Active)
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	out := resourceUnitJSON(got.Unit)
	out["future_bookings"] = got.FutureBookings
	c.JSON(http.StatusOK, out)
}

// DeleteType: DELETE /salons/:id/resources/:tid -> 204, o 409 con el motivo.
func (h ResourceHandler) DeleteType(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Resources.DeleteType(actor, salonID, c.Param("tid")); err != nil {
		httpx.RespondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
