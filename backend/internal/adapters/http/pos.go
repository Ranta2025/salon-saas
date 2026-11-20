package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ServiceHandler: catálogo de servicios. Delgado: parsea, carga actor, llama app.
type ServiceHandler struct {
	Services    app.ServiceService
	Memberships ports.MembershipRepository
}

func (h ServiceHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// serviceJSON es la forma única de serializar un servicio: la usan Create,
// Update y List para que el frontend vea siempre el mismo objeto.
func serviceJSON(s domain.Service) gin.H {
	return gin.H{
		"id":             s.ID,
		"salon_id":       s.SalonID,
		"name":           s.Name,
		"duration_min":   s.DurationMin,
		"price_cents":    s.PriceCents,
		"commission_pct": s.CommissionPct,
		"active":         s.Active,
	}
}

// Create: POST /salons/:id/services
func (h ServiceHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	svc, err := h.Services.Create(actor, salonID, in.Name, in.DurationMin, in.PriceCents, in.CommissionPct)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, serviceJSON(svc))
}

// Update: PUT /salons/:id/services/:service_id
func (h ServiceHandler) Update(c *gin.Context) {
	salonID := c.Param("id")
	serviceID := c.Param("service_id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
		Active        *bool  `json:"active"` // ausente = sigue activo
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	svc, err := h.Services.Update(actor, salonID, serviceID, in.Name, in.DurationMin, in.PriceCents, in.CommissionPct, active)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, serviceJSON(svc))
}

// List: GET /salons/:id/services?include_inactive=true
func (h ServiceHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	includeInactive := c.Query("include_inactive") == "true"
	list, err := h.Services.List(actor, salonID, includeInactive)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, s := range list {
		out = append(out, serviceJSON(s))
	}
	c.JSON(http.StatusOK, gin.H{"services": out})
}

// --- Ventas ---

// SaleHandler: POS. El total que manda el cliente NO se lee: ni siquiera hay
// campo para él en el DTO de entrada.
type SaleHandler struct {
	Sales       app.SaleService
	Memberships ports.MembershipRepository
}

func (h SaleHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

func saleJSON(s domain.Sale) gin.H {
	items := make([]gin.H, 0, len(s.Items))
	for _, it := range s.Items {
		items = append(items, gin.H{
			"service_id":       it.ServiceID,
			"qty":              it.Qty,
			"unit_price_cents": it.UnitPriceCents,
		})
	}
	return gin.H{
		"id":              s.ID,
		"salon_id":        s.SalonID,
		"professional_id": s.ProfessionalID,
		"client_name":     s.ClientName,
		"items":           items,
		"total_cents":     s.TotalCents,
		"method":          string(s.Method),
		"created_at":      s.CreatedAt.Format(time.RFC3339),
	}
}

// Create: POST /salons/:id/sales -> 201 {sale_id, total} (centavos, calculado acá)
func (h SaleHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		ProfessionalID string `json:"professional_id"`
		ClientName     string `json:"client_name"`
		Method         string `json:"method"`
		Items          []struct {
			ServiceID      string `json:"service_id"`
			Qty            int    `json:"qty"`
			UnitPriceCents int    `json:"unit_price_cents"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	items := make([]domain.SaleItem, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, domain.SaleItem{
			ServiceID:      it.ServiceID,
			Qty:            it.Qty,
			UnitPriceCents: it.UnitPriceCents,
		})
	}
	sale, err := h.Sales.RegisterSale(actor, salonID, in.ProfessionalID, in.ClientName, items, domain.PaymentMethod(in.Method))
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"sale_id": sale.ID, "total": sale.TotalCents})
}

// List: GET /salons/:id/sales?from=&to= (por defecto, hoy UTC)
func (h SaleHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	from, to, ok := dayWindow(c)
	if !ok {
		return
	}
	list, err := h.Sales.ListSales(actor, salonID, from, to)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, s := range list {
		out = append(out, saleJSON(s))
	}
	c.JSON(http.StatusOK, gin.H{"sales": out})
}

// --- Cierre de caja ---

type CashCloseHandler struct {
	CashCloses  app.CashCloseService
	Memberships ports.MembershipRepository
}

func (h CashCloseHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

func cashCloseJSON(c domain.CashClose) gin.H {
	return gin.H{
		"id":                c.ID,
		"salon_id":          c.SalonID,
		"opened_at":         c.OpenedAt.Format(time.RFC3339),
		"closed_at":         c.ClosedAt.Format(time.RFC3339),
		"system_cash_cents": c.SystemCashCents,
		"counted_cents":     c.CountedCents,
		"difference_cents":  c.DifferenceCents,
		"note":              c.Note,
	}
}

// Create: POST /salons/:id/cash-closes -> 201 {close_id, expected_cash}
func (h CashCloseHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		CountedCents int    `json:"counted_cents"`
		Note         string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	cc, err := h.CashCloses.CloseCash(actor, salonID, in.CountedCents, in.Note)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"close_id": cc.ID, "expected_cash": cc.SystemCashCents})
}

// List: GET /salons/:id/cash-closes
func (h CashCloseHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.CashCloses.ListCloses(actor, salonID)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, cc := range list {
		out = append(out, cashCloseJSON(cc))
	}
	c.JSON(http.StatusOK, gin.H{"closes": out})
}

// --- Dashboard ---

type DashboardHandler struct {
	Dashboard   app.DashboardService
	Memberships ports.MembershipRepository
}

func (h DashboardHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// Get: GET /salons/:id/dashboard
func (h DashboardHandler) Get(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	d, err := h.Dashboard.Today(actor, salonID)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	top := make([]gin.H, 0, len(d.TopServices))
	for _, t := range d.TopServices {
		top = append(top, gin.H{
			"service_id":    t.ServiceID,
			"name":          t.Name,
			"qty":           t.Qty,
			"revenue_cents": t.RevenueCents,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"revenue_today":      d.RevenueTodayCents,
		"appointments_today": d.AppointmentsToday,
		"occupancy_rate":     d.OccupancyRate,
		"top_services":       top,
	})
}

// dayWindow parsea ?from=&to= (RFC3339). Sin parámetros: el día UTC de hoy.
// Responde 400 y devuelve ok=false si alguna fecha es inválida.
func dayWindow(c *gin.Context) (from, to time.Time, ok bool) {
	now := time.Now().UTC()
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	to = from.Add(24 * time.Hour)
	if q := c.Query("from"); q != "" {
		t, err := time.Parse(time.RFC3339, q)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from inválido"})
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if q := c.Query("to"); q != "" {
		t, err := time.Parse(time.RFC3339, q)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "to inválido"})
			return time.Time{}, time.Time{}, false
		}
		to = t
	}
	return from, to, true
}
