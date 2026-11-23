// Gastos del salón (F3b, D3/D4/D5 del contrato F3b). El handler solo traduce
// HTTP; los permisos, las reglas y las frases viven en app/domain.
package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ExpenseHandler agrupa las rutas de gastos.
type ExpenseHandler struct {
	Expenses    app.ExpenseService
	Memberships ports.MembershipRepository
}

func (h ExpenseHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	return loadActor(h.Memberships, c, salonID)
}

// expenseJSON es la forma única de serializar un gasto (contrato congelado para
// el frontend). `spent_on` es una fecha local YYYY-MM-DD y `created_at` RFC3339.
func expenseJSON(e domain.Expense) gin.H {
	return gin.H{
		"id":           e.ID,
		"category":     string(e.Category),
		"amount_cents": e.AmountCents,
		"spent_on":     e.SpentOn,
		"description":  e.Description,
		"created_at":   e.CreatedAt.UTC().Format(time.RFC3339),
		"created_by":   e.CreatedBy,
	}
}

// expenseBody es el cuerpo de POST/PUT. Las claves llegan como PUNTEROS para
// distinguir «ausente/null» de un valor válido: el PUT exige las CUATRO y un 400
// no puede escribir nada (lección de F3.2: nunca un valor por defecto en
// silencio).
type expenseBody struct {
	Category    *string `json:"category"`
	AmountCents *int    `json:"amount_cents"`
	SpentOn     *string `json:"spent_on"`
	Description *string `json:"description"`
}

// fields arma los campos del dominio con las claves presentes. description vacía
// es un valor válido; ausente en el PUT es un 400.
func (b expenseBody) fields(description string) domain.ExpenseFields {
	f := domain.ExpenseFields{Description: description}
	if b.Category != nil {
		f.Category = *b.Category
	}
	if b.AmountCents != nil {
		f.AmountCents = *b.AmountCents
	}
	if b.SpentOn != nil {
		f.SpentOn = *b.SpentOn
	}
	return f
}

// Create: POST /api/v1/salons/:id/expenses -> 201 expense. La descripción es
// opcional (por defecto ""); categoría, monto y fecha son obligatorios.
func (h ExpenseHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in expenseBody
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Category == nil || in.AmountCents == nil || in.SpentOn == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgExpenseMissingCreate))
		return
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	e, err := h.Expenses.Create(actor, salonID, in.fields(description))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, expenseJSON(e))
}

// List: GET /api/v1/salons/:id/expenses?from=&to= -> {"expenses":[...],"truncated":bool}.
func (h ExpenseHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, truncated, err := h.Expenses.List(actor, salonID, c.Query("from"), c.Query("to"))
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, e := range list {
		out = append(out, expenseJSON(e))
	}
	c.JSON(http.StatusOK, gin.H{"expenses": out, "truncated": truncated})
}

// Update: PUT /api/v1/salons/:id/expenses/:eid -> 200 expense. REEMPLAZA: las
// cuatro claves son obligatorias (la descripción puede ser "").
func (h ExpenseHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in expenseBody
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Category == nil || in.AmountCents == nil || in.SpentOn == nil || in.Description == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgExpenseMissingUpdate))
		return
	}
	e, err := h.Expenses.Update(actor, salonID, c.Param("eid"), in.fields(*in.Description))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, expenseJSON(e))
}

// Delete: DELETE /api/v1/salons/:id/expenses/:eid -> 204 (404 si no es del salón).
func (h ExpenseHandler) Delete(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Expenses.Delete(actor, salonID, c.Param("eid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
