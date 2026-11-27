// DepositHandler: la pantalla «Señas» del panel (F11, D10) y la consulta pública
// del estado de un pago (D8). Delgado: carga el actor y delega en el caso de uso,
// que es donde viven los permisos y las validaciones.
//
//	GET  /api/v1/salons/:id/deposits?status=&limit=&before=   owner/admin/recepción
//	POST /api/v1/public/deposits/lookup {charge_id}           pública (id = credencial)
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type DepositHandler struct {
	Deposits    app.DepositService
	Memberships ports.MembershipRepository
}

// loadActor delega en el helper compartido de la API (members.go): el actor
// sale de la base, no del token.
func (h DepositHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return httpx.LoadActor(h.Memberships, c, salonID)
}

// depositRowJSON es la forma D10 de una fila de la pantalla.
func depositRowJSON(d domain.BookingDeposit) gin.H {
	var paidAt, settledAt any
	if d.PaidAt != nil {
		paidAt = d.PaidAt.UTC().Format(time.RFC3339)
	}
	if d.SettledAt != nil {
		settledAt = d.SettledAt.UTC().Format(time.RFC3339)
	}
	return gin.H{
		"id":           d.ID,
		"booking_id":   d.BookingID,
		"client_name":  d.ClientName,
		"starts_at":    d.StartsAt.UTC().Format(time.RFC3339),
		"amount_cents": d.AmountCents,
		"status":       d.Status,
		"paid_at":      paidAt,
		"settled_at":   settledAt,
	}
}

// List: GET /salons/:id/deposits -> 200
// {"deposits":[{id, booking_id, client_name, starts_at, amount_cents, status,
// paid_at, settled_at}], "next_cursor": string|null}, de la más nueva a la más
// vieja. El permiso (owner/admin/recepción) va ANTES de leer los parámetros: el
// caso de uso lo aplica antes de parsear estado, límite y cursor.
func (h DepositHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	status, limit, before := c.Query("status"), c.Query("limit"), c.Query("before")
	rows, err := h.Deposits.List(actor, salonID, status, limit, before)
	if err != nil {
		respondErr(c, err)
		return
	}
	deposits := make([]gin.H, 0, len(rows))
	for _, d := range rows {
		deposits = append(deposits, depositRowJSON(d))
	}
	// El cursor es la última fila devuelta. Solo se ofrece si la página vino
	// llena: una página corta es la última (mismo criterio que la auditoría).
	var next any
	if len(rows) > 0 {
		if q, qerr := domain.ParseDepositQuery(status, limit, before); qerr == nil && len(rows) == q.Limit {
			last := rows[len(rows)-1]
			next = domain.EncodeDepositCursor(domain.DepositCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		}
	}
	c.JSON(http.StatusOK, gin.H{"deposits": deposits, "next_cursor": next})
}

// Lookup: POST /api/v1/public/deposits/lookup {charge_id} -> 200
// {status, amount_cents, expires_at, starts_at}. Pública: el id del cargo ES la
// credencial (43 caracteres base64url) y un formato inválido o desconocido da el
// MISMO 404 «Pago no encontrado».
func (h DepositHandler) Lookup(c *gin.Context) {
	var in struct {
		ChargeID string `json:"charge_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	look, err := h.Deposits.Lookup(in.ChargeID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":       look.Status,
		"amount_cents": look.AmountCents,
		"expires_at":   look.ExpiresAt.UTC().Format(time.RFC3339),
		"starts_at":    look.StartsAt.UTC().Format(time.RFC3339),
	})
}
