// Invitaciones de equipo (F2g1): rutas autenticadas del salón y rutas públicas
// del canje.
//
// El token llega SIEMPRE en el cuerpo (nunca en la ruta ni en el query) y la
// única respuesta que lo contiene es el alta: el listado y el lookup no lo
// devuelven ni en claro ni hasheado.
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// InvitationHandler traduce HTTP <-> casos de uso de invitaciones.
type InvitationHandler struct {
	Invitations app.InvitationService
	Public      app.InvitationPublicService
	Memberships ports.MembershipRepository
}

// invitationJSON es la forma única de serializar una invitación para el salón.
// Lleva lo que la pantalla de Equipo necesita y NADA más: ni el token (no existe
// fuera del alta) ni su hash (serviría para el mismo fin).
func invitationJSON(inv domain.Invitation) gin.H {
	return gin.H{
		"id":         inv.ID,
		"email":      inv.Email,
		"role":       string(inv.Role),
		"attends":    inv.Attends,
		"expires_at": inv.ExpiresAt.UTC().Format(time.RFC3339),
		"created_at": inv.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Create: POST /api/v1/salons/:id/invitations {"email","role","attends?"} -> 201
// {"invitation":{...},"link":"<APP_URL>/invitacion#<token>","email_sent":bool}.
//
// El `link` se devuelve SOLO acá (se puede compartir a mano si el correo falla) y
// no se puede recuperar después.
func (h InvitationHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
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
	created, err := h.Invitations.Invite(actor, salonID, app.InvitationInput{
		Email: in.Email, Role: domain.Role(in.Role), Attends: in.Attends,
	})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"invitation": invitationJSON(created.Invitation),
		"link":       created.Link,
		"email_sent": created.EmailSent,
	})
}

// List: GET /api/v1/salons/:id/invitations -> {"invitations":[...]} (pendientes).
func (h InvitationHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
	if !ok {
		return
	}
	list, err := h.Invitations.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, inv := range list {
		out = append(out, invitationJSON(inv))
	}
	c.JSON(http.StatusOK, gin.H{"invitations": out})
}

// Revoke: DELETE /api/v1/salons/:id/invitations/:iid -> 204. El token revocado
// pasa a responder el 404 genérico.
func (h InvitationHandler) Revoke(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
	if !ok {
		return
	}
	if err := h.Invitations.Revoke(actor, salonID, c.Param("iid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Lookup: POST /api/v1/public/invitations/lookup {"token"} -> 200
// {salon_name, role, role_label, email_hint, expires_at, has_account}.
// Token inexistente, vencido, usado o revocado -> 404 {"Invitación inválida o
// vencida"} (idéntico en los cuatro casos: no se distingue).
func (h InvitationHandler) Lookup(c *gin.Context) {
	var in struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	got, err := h.Public.Lookup(in.Token)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"salon_name":  got.SalonName,
		"role":        string(got.Role),
		"role_label":  got.RoleLabel,
		"email_hint":  got.EmailHint,
		"expires_at":  got.ExpiresAt.UTC().Format(time.RFC3339),
		"has_account": got.HasAccount,
	})
}

// Accept: POST /api/v1/public/invitations/accept {"token","name","password"} ->
// 200 {"token"} (la sesión, con la MISMA forma que el login). La cuenta, la
// membresía y el estado de la invitación se guardan en una transacción.
func (h InvitationHandler) Accept(c *gin.Context) {
	var in struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	session, err := h.Public.AcceptNew(in.Token, in.Name, in.Password)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": session})
}

// AcceptAs: POST /api/v1/invitations/accept {"token"} con Bearer -> 200 con el
// salón y el rol. Es el camino de quien YA tiene cuenta: se une al salón solo si
// la invitación es para SU email (si no, 403).
func (h InvitationHandler) AcceptAs(c *gin.Context) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	var in struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	salon, inv, err := h.Public.AcceptExisting(userID, in.Token)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"salon":      gin.H{"id": salon.ID, "slug": salon.Slug, "name": salon.Name},
		"role":       string(inv.Role),
		"role_label": inv.Role.Label(),
	})
}
