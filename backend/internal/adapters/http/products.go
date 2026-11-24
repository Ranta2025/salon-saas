// ProductHandler: catálogo de productos e inventario del POS (F6 del contrato
// de la fase 6). Delgado: parsea, carga el actor y delega en el caso de uso,
// que es donde viven los permisos (D7), las validaciones y el aviso de stock
// bajo (D8).
//
//	GET    /salons/:id/products
//	POST   /salons/:id/products
//	PUT    /salons/:id/products/:pid
//	POST   /salons/:id/products/:pid/adjust
//	GET    /salons/:id/products/:pid/movements?limit=
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type ProductHandler struct {
	Products    app.ProductService
	Memberships ports.MembershipRepository
}

func (h ProductHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// productJSON es la forma única del producto (la usan las cinco rutas).
func productJSON(p domain.Product) gin.H {
	return gin.H{
		"id":                  p.ID,
		"name":                p.Name,
		"price_cents":         p.PriceCents,
		"stock":               p.Stock,
		"low_stock_threshold": p.LowStockThreshold,
		"active":              p.Active,
		"low":                 p.Low(),
		"created_at":          p.CreatedAt.Format(time.RFC3339),
	}
}

// List: GET /salons/:id/products -> 200 {"products":[...]} por nombre.
func (h ProductHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Products.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		out = append(out, productJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{"products": out})
}

// Create: POST /salons/:id/products {name, price_cents, stock?, low_stock_threshold?}
func (h ProductHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name              string `json:"name"`
		PriceCents        int    `json:"price_cents"`
		Stock             *int   `json:"stock"`
		LowStockThreshold *int   `json:"low_stock_threshold"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	p, err := h.Products.Create(actor, salonID, in.Name, in.PriceCents, in.Stock, in.LowStockThreshold)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, productJSON(p))
}

// Update: PUT /salons/:id/products/:pid {name, price_cents, low_stock_threshold, active}
// Exige las CUATRO claves: un cuerpo parcial es 400 y no escribe nada.
func (h ProductHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	productID := c.Param("pid")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name              *string `json:"name"`
		PriceCents        *int    `json:"price_cents"`
		LowStockThreshold *int    `json:"low_stock_threshold"`
		Active            *bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Name == nil || in.PriceCents == nil || in.LowStockThreshold == nil || in.Active == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgProductMissingFields))
		return
	}
	p, err := h.Products.Update(actor, salonID, productID, *in.Name, *in.PriceCents, *in.LowStockThreshold, *in.Active)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, productJSON(p))
}

// Adjust: POST /salons/:id/products/:pid/adjust {delta, reason, note?}
func (h ProductHandler) Adjust(c *gin.Context) {
	salonID := c.Param("id")
	productID := c.Param("pid")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Delta  int    `json:"delta"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	p, err := h.Products.Adjust(actor, salonID, productID, in.Delta, in.Reason, in.Note)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, productJSON(p))
}

// Movements: GET /salons/:id/products/:pid/movements?limit= (1..100, default 50).
func (h ProductHandler) Movements(c *gin.Context) {
	salonID := c.Param("id")
	productID := c.Param("pid")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	limit := 0
	if q := c.Query("limit"); q != "" {
		n, err := strconv.Atoi(q)
		// Un 0 explícito NO es "sin parámetro": el rango válido es 1..100.
		if err != nil || n < 1 {
			respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgProductLimit))
			return
		}
		limit = n
	}
	list, err := h.Products.Movements(actor, salonID, productID, limit)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, m := range list {
		out = append(out, gin.H{
			"id":         m.ID,
			"delta":      m.Delta,
			"reason":     string(m.Reason),
			"note":       m.Note,
			"sale_id":    optionalID(m.SaleID),
			"created_at": m.CreatedAt.Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"movements": out})
}
