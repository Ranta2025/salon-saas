package httpapi

import (
	"net/http"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"

	"github.com/gin-gonic/gin"
)

// SalonProfileHandler: el perfil público del salón (F16, D1).
//
//	GET /salons/:id/profile   cualquier miembro activo
//	PUT /salons/:id/profile   solo owner/admin, REEMPLAZA (exige las 6 claves)
type SalonProfileHandler struct {
	Profile     app.SalonProfileService
	Memberships ports.MembershipRepository
}

func (h SalonProfileHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// salonProfileJSON es la forma única del perfil: los datos sin cargar salen como
// null (no como "").
func salonProfileJSON(p domain.SalonProfile) gin.H {
	return gin.H{
		"name":        p.Name,
		"slug":        p.Slug,
		"description": optionalID(p.Description),
		"address":     optionalID(p.Address),
		"phone":       optionalID(p.Phone),
		"email":       optionalID(p.Email),
		"instagram":   optionalID(p.Instagram),
	}
}

// Get: GET /salons/:id/profile -> {name, slug, description, address, phone,
// email, instagram}. Cualquier miembro activo.
func (h SalonProfileHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	p, err := h.Profile.Get(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, salonProfileJSON(p))
}

// Update: PUT /salons/:id/profile REEMPLAZA el perfil y exige las SEIS claves
// (name, description, address, phone, email, instagram): `null` explícito vale
// («sin dato»), una clave ausente es 400 y no se rellena en silencio. `slug` no
// se edita: si viene, se ignora. El permiso se mira ANTES de leer el cuerpo.
func (h SalonProfileHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Profile.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		Name        presentText `json:"name"`
		Description presentText `json:"description"`
		Address     presentText `json:"address"`
		Phone       presentText `json:"phone"`
		Email       presentText `json:"email"`
		Instagram   presentText `json:"instagram"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if !in.Name.Set || !in.Description.Set || !in.Address.Set || !in.Phone.Set || !in.Email.Set || !in.Instagram.Set {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgSalonProfileMissing))
		return
	}
	p, err := h.Profile.Update(actor, salonID, domain.SalonProfileInput{
		Name:        in.Name.String(),
		Description: in.Description.Value,
		Address:     in.Address.Value,
		Phone:       in.Phone.Value,
		Email:       in.Email.Value,
		Instagram:   in.Instagram.Value,
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, salonProfileJSON(p))
}

// presentText distingue una clave AUSENTE de una clave en `null` (vive en httpx).
type presentText = httpx.PresentText
