// AuditHandler: registro de auditoría (F10, parte A del contrato de la fase 6).
// Dos piezas:
//
//   - `Audit(action)`: MIDDLEWARE que cuelga de las rutas sensibles de D2 en
//     routes.go. Corre la ruta (c.Next()) y DESPUÉS, solo si respondió 2xx,
//     escribe el evento. Nunca cambia el status, el cuerpo ni las cabeceras de la
//     ruta: solo observa c.Writer.Status(). Es de MEJOR ESFUERZO: un error o un
//     pánico al escribir se loguea y se traga, y la respuesta ya armada sale
//     igual (limitación conocida: no es transaccional y la escritura corre en el
//     camino de la petición con el tope de tiempo del adaptador).
//
//   - `GET /salons/:id/audit-log?limit=&before=&action=` (D3): solo owner/admin;
//     el permiso va ANTES de mirar los parámetros.
//
// Qué id queda como entidad: el parámetro de ruta PROPIO de la acción
// (AuditEntityParams). Las acciones sin id propio en la ruta (altas) guardan
// NULL; las de dos ids (`payroll_run.adjust` lleva :rid y :uid) guardan el de la
// corrida (:rid), la cosa sobre la que se actúa.
package httpapi

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/audit"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// AuditEntityParams dice qué parámetro de ruta es la entidad de cada acción de
// D2. Una acción que no está acá no tiene id propio: la entidad queda vacía.
var AuditEntityParams = map[string]string{
	"member.update":      "uid",
	"member.remove":      "uid",
	"member.reactivate":  "uid",
	"member.services":    "uid",
	"member.pay_model":   "uid",
	"invitation.revoke":  "iid",
	"payroll_run.adjust": "rid",
	"payroll_run.pay":    "rid",
	"payroll_run.delete": "rid",
	"expense.update":     "eid",
	"expense.delete":     "eid",
	"product.adjust":     "pid",
}

type AuditHandler struct {
	Service     audit.AuditService
	Memberships ports.MembershipRepository
	// Logf recibe los fallos de escritura (nil = log.Printf).
	Logf func(string, ...any)
}

func (h AuditHandler) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Audit devuelve el middleware de la acción `action` (nombre exacto de D2). Con
// el servicio sin repositorio no hace nada.
func (h AuditHandler) Audit(action string) gin.HandlerFunc {
	param := AuditEntityParams[action]
	return func(c *gin.Context) {
		c.Next()
		if !h.Service.Enabled() {
			return
		}
		if st := c.Writer.Status(); st < 200 || st > 299 {
			return
		}
		uid, _ := c.Get("userID")
		actor, _ := uid.(string)
		salonID := c.Param("id")
		if actor == "" || salonID == "" {
			return
		}
		entity := ""
		if param != "" {
			entity = c.Param(param)
		}
		h.record(salonID, actor, action, entity)
	}
}

// record escribe el evento sin dejar escapar nada: el error y el pánico se
// loguean (acción, salón y causa; nunca el cuerpo ni datos de la petición).
func (h AuditHandler) record(salonID, actor, action, entity string) {
	defer func() {
		if r := recover(); r != nil {
			h.logf("audit: pánico al registrar %s en el salón %s: %v", action, salonID, r)
		}
	}()
	if err := h.Service.Record(salonID, actor, action, entity); err != nil {
		h.logf("audit: no se pudo registrar %s en el salón %s: %v", action, salonID, err)
	}
}

func (h AuditHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// List: GET /salons/:id/audit-log?limit=&before=&action= -> 200
// {"events":[{id, action, entity_id, actor_user_id, actor_name, created_at}],
// "next_cursor": string|null}, del más nuevo al más viejo. Un parámetro vacío
// vale como ausente. `next_cursor` es null en la última página.
func (h AuditHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	// El caso de uso comprueba el permiso (owner/admin) ANTES de mirar los
	// parámetros y de tocar la base: quien no puede ver recibe 403 aunque el
	// límite o el cursor estén mal.
	page, err := h.Service.List(actor, salonID, c.Query("limit"), c.Query("before"), c.Query("action"))
	if err != nil {
		respondErr(c, err)
		return
	}
	events := make([]gin.H, 0, len(page.Events))
	for _, e := range page.Events {
		var entity any
		if e.EntityID != "" {
			entity = e.EntityID
		}
		events = append(events, gin.H{
			"id":            e.ID,
			"action":        e.Action,
			"entity_id":     entity,
			"actor_user_id": e.ActorUserID,
			"actor_name":    e.ActorName,
			"created_at":    e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	var next any
	if page.NextCursor != "" {
		next = page.NextCursor
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "next_cursor": next})
}
