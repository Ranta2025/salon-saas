// PackageHandler: paquetes y bonos prepagos con saldo por clienta (F8 del
// contrato de la fase 6). Delgado: parsea, carga el actor y delega en el caso de
// uso, que es donde viven los permisos (D5/D7/D8/D9/D10) y las validaciones.
//
//	GET  /salons/:id/packages                                  catálogo
//	POST /salons/:id/packages                                  alta (owner/admin)
//	PUT  /salons/:id/packages/:pid                             reemplaza (owner/admin)
//	GET  /salons/:id/clients/:cid/packages                     paquetes de la clienta
//	POST /salons/:id/clients/:cid/packages                     comprar (venta + paquete)
//	GET  /salons/:id/clients/:cid/packages/:cpid/movements     libro del paquete
//	POST /salons/:id/clients/:cid/packages/:cpid/redeem        descontar UNA sesión
//	POST /salons/:id/clients/:cid/packages/:cpid/adjust        ajustar el saldo (owner/admin)
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/domain"
	"salon-saas/internal/packages"
	"salon-saas/internal/ports"
)

type PackageHandler struct {
	Packages    packages.PackageService
	Memberships ports.MembershipRepository
}

func (h PackageHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// presentInt distingue una clave AUSENTE de una clave en `null` (y de un
// número): el PUT del catálogo exige `validity_days` presente, y `null`
// explícito significa «sin vencimiento». Un campo ausente no llama a
// UnmarshalJSON, así que Set queda en false.
type presentInt struct {
	Set   bool
	Value *int
}

func (p *presentInt) UnmarshalJSON(b []byte) error {
	p.Set = true
	if strings.TrimSpace(string(b)) == "null" {
		p.Value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Value = &v
	return nil
}

// servicePackageJSON es la forma única del paquete del catálogo.
func servicePackageJSON(p domain.ServicePackage) gin.H {
	var validity any
	if p.ValidityDays != nil {
		validity = *p.ValidityDays
	}
	return gin.H{
		"id":            p.ID,
		"name":          p.Name,
		"service_id":    p.ServiceID,
		"service_name":  p.ServiceName,
		"sessions":      p.Sessions,
		"price_cents":   p.PriceCents,
		"validity_days": validity,
		"active":        p.Active,
		"created_at":    p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// clientPackageJSON es la forma D8 del paquete de una clienta.
func clientPackageJSON(cp domain.ClientPackage) gin.H {
	var expires any
	if cp.ExpiresAt != nil {
		expires = cp.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return gin.H{
		"id":             cp.ID,
		"name":           cp.Name,
		"service_id":     cp.ServiceID,
		"service_name":   cp.ServiceName,
		"sessions_total": cp.SessionsTotal,
		"sessions_left":  cp.SessionsLeft,
		"price_cents":    cp.PriceCents,
		"purchased_at":   cp.PurchasedAt.UTC().Format(time.RFC3339),
		"expires_at":     expires,
		"expired":        cp.Expired,
	}
}

// ListCatalog: GET /salons/:id/packages -> 200 {"packages":[...]} por nombre.
func (h PackageHandler) ListCatalog(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Packages.ListCatalog(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		out = append(out, servicePackageJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{"packages": out})
}

// CreateCatalog: POST /salons/:id/packages {name, service_id, sessions, price_cents, validity_days?}
func (h PackageHandler) CreateCatalog(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Packages.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		Name         *string `json:"name"`
		ServiceID    *string `json:"service_id"`
		Sessions     *int    `json:"sessions"`
		PriceCents   *int    `json:"price_cents"`
		ValidityDays *int    `json:"validity_days"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	// Nombre, servicio, sesiones y precio son obligatorios: ausente, null o en
	// blanco es 400 y no se escribe nada (no se rellenan con valores por defecto).
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || in.ServiceID == nil || strings.TrimSpace(*in.ServiceID) == "" ||
		in.Sessions == nil || in.PriceCents == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgPackageCreateMissing))
		return
	}
	p, err := h.Packages.CreateCatalog(actor, salonID, *in.Name, *in.ServiceID, *in.Sessions, *in.PriceCents, in.ValidityDays)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, servicePackageJSON(p))
}

// UpdateCatalog: PUT /salons/:id/packages/:pid {name, price_cents, validity_days, active}
// REEMPLAZA y exige las CUATRO claves: un cuerpo parcial es 400 y no escribe
// nada. `validity_days: null` explícito = sin vencimiento; la clave ausente NO
// se rellena en silencio.
func (h PackageHandler) UpdateCatalog(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Packages.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		Name         *string    `json:"name"`
		PriceCents   *int       `json:"price_cents"`
		ValidityDays presentInt `json:"validity_days"`
		Active       *bool      `json:"active"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Name == nil || in.PriceCents == nil || !in.ValidityDays.Set || in.Active == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgPackageUpdateMissing))
		return
	}
	p, err := h.Packages.UpdateCatalog(actor, salonID, c.Param("pid"), *in.Name, *in.PriceCents, in.ValidityDays.Value, *in.Active)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, servicePackageJSON(p))
}

// ListClient: GET /salons/:id/clients/:cid/packages -> 200 {"packages":[...]}
func (h PackageHandler) ListClient(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Packages.ListClientPackages(actor, salonID, c.Param("cid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, cp := range list {
		out = append(out, clientPackageJSON(cp))
	}
	c.JSON(http.StatusOK, gin.H{"packages": out})
}

// Purchase: POST /salons/:id/clients/:cid/packages {package_id, professional_id, method, client_op_id?}
// -> 201 {"sale_id", "client_package":{...}}. Idempotente por client_op_id.
func (h PackageHandler) Purchase(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Packages.RequireSeller(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		PackageID      string `json:"package_id"`
		ProfessionalID string `json:"professional_id"`
		Method         string `json:"method"`
		ClientOpID     string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	cp, err := h.Packages.Purchase(actor, salonID, c.Param("cid"), in.PackageID, in.ProfessionalID,
		domain.PaymentMethod(in.Method), in.ClientOpID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"sale_id": cp.SaleID, "client_package": clientPackageJSON(cp)})
}

// Movements: GET /salons/:id/clients/:cid/packages/:cpid/movements
func (h PackageHandler) Movements(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Packages.Movements(actor, salonID, c.Param("cid"), c.Param("cpid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, m := range list {
		out = append(out, gin.H{
			"id":              m.ID,
			"delta":           m.Delta,
			"reason":          string(m.Reason),
			"note":            optionalID(m.Note),
			"professional_id": optionalID(m.ProfessionalID),
			"created_at":      m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	c.JSON(http.StatusOK, gin.H{"movements": out})
}

// Redeem: POST /salons/:id/clients/:cid/packages/:cpid/redeem {professional_id?, note?, client_op_id?}
// -> 200 {"client_package":{...}}. Todos los campos son opcionales: un cuerpo
// vacío vale como {}.
func (h PackageHandler) Redeem(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Packages.RequireSeller(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		ProfessionalID string `json:"professional_id"`
		Note           string `json:"note"`
		ClientOpID     string `json:"client_op_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		badBody(c)
		return
	}
	cp, err := h.Packages.Redeem(actor, salonID, c.Param("cid"), c.Param("cpid"), in.ProfessionalID, in.Note, in.ClientOpID)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"client_package": clientPackageJSON(cp)})
}

// Adjust: POST /salons/:id/clients/:cid/packages/:cpid/adjust {delta, note}
// -> 200 {"client_package":{...}}. `delta` es obligatorio: ausente, null o de otro
// tipo es 400 (un cero explícito da «El ajuste no puede ser cero»).
func (h PackageHandler) Adjust(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Packages.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		Delta *int   `json:"delta"`
		Note  string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if in.Delta == nil {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgPackageAdjustZero))
		return
	}
	cp, err := h.Packages.Adjust(actor, salonID, c.Param("cid"), c.Param("cpid"), *in.Delta, in.Note)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"client_package": clientPackageJSON(cp)})
}
