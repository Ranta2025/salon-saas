package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// MemberHandler: pantalla de equipo del salón (GET/POST/PATCH/DELETE
// /salons/:id/members y la reactivación de quien fue dado de baja, F2g2). Solo
// owner/admin; la regla de qué rol puede sumar cada uno vive en app/domain.
type MemberHandler struct {
	Members     app.MemberService
	Memberships ports.MembershipRepository
}

func (h MemberHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return loadActor(h.Memberships, c, salonID)
}

// loadActor resuelve la membresía del usuario del token en el salón de la ruta.
// Vive una sola vez porque lo usan la pantalla de equipo y las invitaciones
// (F2g1): sin membresía no hay acceso, y el 403 no distingue por qué.
func loadActor(memberships ports.MembershipRepository, c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	m, err := memberships.Find(userID, salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// memberJSON es la forma única de serializar un miembro del equipo.
// `attends` (F2a) dice si esa persona atiende clientes, `trade` es su oficio y
// `service_ids` los servicios que hace (F2c). Las dos listas salen siempre como
// array: el frontend no tiene que distinguir null de [].
//
// `active` y `ended_at` (F2g2) son la baja del equipo: el listado devuelve
// activos Y dados de baja y el frontend decide cómo mostrarlos. Un miembro
// activo sale con `ended_at: null` (la clave está siempre).
func memberJSON(m domain.Member) gin.H {
	ids := make([]string, 0, len(m.ServiceIDs))
	ids = append(ids, m.ServiceIDs...)
	var endedAt any
	if m.EndedAt != nil {
		endedAt = m.EndedAt.UTC().Format(time.RFC3339)
	}
	return gin.H{
		"id": m.ID, "name": m.Name, "email": m.Email,
		"role": string(m.Role), "attends": m.Attends,
		"trade": m.Trade, "service_ids": ids,
		"active": m.Active, "ended_at": endedAt,
	}
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

// Update: PATCH /api/v1/salons/:id/members/:uid
// {"attends":true|false,"trade":"Barbero"} -> 200 con el miembro ya actualizado.
// Las dos cosas son opcionales pero una tiene que venir (S-F2c-3: si no, 400
// "Indicá qué cambiar"): la marca y el oficio no se adivinan.
func (h MemberHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Attends *bool   `json:"attends"`
		Trade   *string `json:"trade"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	m, err := h.Members.Update(actor, salonID, c.Param("uid"),
		app.MemberPatch{Attends: in.Attends, Trade: in.Trade})
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, memberJSON(m))
}

// SetServices: PUT /api/v1/salons/:id/members/:uid/services
// {"service_ids":["svc_..."]} -> 200 {"service_ids":[...]}. Reemplaza la lista
// entera: la vacía es válida (esa persona no hace ninguno más).
func (h MemberHandler) SetServices(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		ServiceIDs []string `json:"service_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	ids, err := h.Members.SetServices(actor, salonID, c.Param("uid"), in.ServiceIDs)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]string, 0, len(ids))
	out = append(out, ids...)
	c.JSON(http.StatusOK, gin.H{"service_ids": out})
}

// Remove: DELETE /api/v1/salons/:id/members/:uid -> 204.
//
// Desde F2g2 NO borra: da de baja (ended_at), así el historial de turnos y
// ventas, los horarios, las ausencias y los servicios se conservan. Con turnos
// futuros sin cancelar el caso de uso responde 409 con la oración del contrato y
// no cambia nada. Autorización: el dueño puede dar de baja a cualquiera (otro
// dueño incluido) y la administración solo a profesional, recepción y cliente;
// el último dueño activo no se puede dar de baja (400).
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

// Reactivate: POST /api/v1/salons/:id/members/:uid/reactivate -> 200 el miembro.
//
// Deshace una baja conservando el rol, `attends`, el oficio y los servicios: la
// fila nunca se borró. Si ya estaba activo es 409 ("Esa persona ya está en el
// equipo") y un miembro de otro salón o inexistente es 404.
func (h MemberHandler) Reactivate(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	m, err := h.Members.Reactivate(actor, salonID, c.Param("uid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, memberJSON(m))
}
