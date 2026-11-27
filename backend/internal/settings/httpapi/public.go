// PublicSalonHandler: la vidriera pública del salón (GET /api/v1/public/salons/:slug),
// SIN auth. Vive en settings porque lee lo que el dueño edita en el panel:
// perfil, carta, equipo, galería, paquetes, horario y fidelidad.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	httpx "salon-saas/internal/adapters/http/httpx"
	"salon-saas/internal/domain"
	"salon-saas/internal/settings"
)

// PublicSalonHandler ...
type PublicSalonHandler struct {
	Public settings.PublicSalonService
}

// GetSalon: GET /api/v1/public/salons/:slug. La vidriera del salón (F16, D4):
// perfil, la carta de servicios activos que alguien hace (con precio, duración y
// descripción), el equipo que atiende (solo nombre y oficio), la galería, los
// paquetes activos, el horario de los días abiertos (F2d1) y el programa de
// fidelidad si está activado (F9, D8: `loyalty`, null si no). Todo sale de lo que
// el dueño edita en el panel; las secciones vacías son listas vacías.
func (h PublicSalonHandler) GetSalon(c *gin.Context) {
	p, err := h.Public.Public(c.Param("slug"))
	if err != nil {
		httpx.RespondErr(c, err)
		return
	}
	services := make([]gin.H, 0, len(p.Services))
	for _, s := range p.Services {
		svc := gin.H{
			"id":           s.ID,
			"name":         s.Name,
			"duration_min": s.DurationMin,
			"price_cents":  s.PriceCents,
			// description (F16, D2): null si el servicio no tiene.
			"description": httpx.OptionalID(s.Description),
		}
		// deposit_cents (F11, D11): el monto de la seña de ESTE servicio, con la
		// misma fórmula de D4 que el servidor usa al reservar. La clave es ADITIVA
		// y solo aparece con la seña activa: sin seña el payload es el de hoy,
		// byte a byte (la landing no dibuja la línea).
		if p.Deposit != nil {
			if amount := domain.DepositAmountCents(s.PriceCents, p.Deposit.Percent); amount > 0 {
				svc["deposit_cents"] = amount
			}
		}
		services = append(services, svc)
	}
	// Equipo: SOLO nombre y oficio. Ni ids, ni email, ni rol, ni pagos.
	team := make([]gin.H, 0, len(p.Team))
	for _, m := range p.Team {
		team = append(team, gin.H{"name": m.Name, "trade": m.Trade})
	}
	gallery := make([]gin.H, 0, len(p.Gallery))
	for _, ph := range p.Gallery {
		gallery = append(gallery, gin.H{"id": ph.ID, "url": ph.URL, "caption": httpx.OptionalID(ph.Caption)})
	}
	packages := make([]gin.H, 0, len(p.Packages))
	for _, pk := range p.Packages {
		var validity any
		if pk.ValidityDays != nil {
			validity = *pk.ValidityDays
		}
		packages = append(packages, gin.H{
			"id":            pk.ID,
			"name":          pk.Name,
			"service_name":  pk.ServiceName,
			"sessions":      pk.Sessions,
			"price_cents":   pk.PriceCents,
			"validity_days": validity,
		})
	}
	// `hours` viene ya expandido: solo los días abiertos. La landing pone
	// "Cerrado" en los que no están y resalta el día de hoy.
	hours := make([]gin.H, 0, len(p.Hours))
	for _, d := range p.Hours {
		hours = append(hours, gin.H{"weekday": d.Weekday, "open": d.Open, "close": d.Close})
	}
	// loyalty (F9, D8): null salvo programa activado con su servicio activo.
	// Solo cuántas visitas y qué servicio: ni progreso ni clientas.
	var loyalty any
	if p.Loyalty != nil {
		loyalty = gin.H{"visits_required": p.Loyalty.VisitsRequired, "reward_service_name": p.Loyalty.RewardServiceName}
	}
	// deposit (F11, D11): ADITIVA y solo con la seña activada. `simulated` es
	// verdadero porque el adaptador de hoy es el banco simulado: la landing
	// muestra el aviso «Pago simulado».
	//
	// Con la seña apagada la clave NO viaja y el payload queda idéntico al de hoy
	// (byte a byte): el test de forma del payload público (`public_test.go`) fija
	// el conjunto exacto de claves y no se toca. El frontend trata la clave
	// ausente como `null` (publicSalon.ts), así que «si no, null» de D11 se cumple
	// sin agregar una clave nueva al payload de un salón sin seña.
	var deposit any
	if p.Deposit != nil {
		deposit = gin.H{
			"percent":      p.Deposit.Percent,
			"hold_minutes": p.Deposit.HoldMinutes,
			"refund_hours": p.Deposit.RefundHours,
			"simulated":    true,
		}
	}
	body := gin.H{
		"salon": gin.H{
			"id":          p.Salon.ID,
			"slug":        p.Salon.Slug,
			"name":        p.Profile.Name,
			"description": httpx.OptionalID(p.Profile.Description),
			"address":     httpx.OptionalID(p.Profile.Address),
			"phone":       httpx.OptionalID(p.Profile.Phone),
			"email":       httpx.OptionalID(p.Profile.Email),
			"instagram":   httpx.OptionalID(p.Profile.Instagram),
			"timezone":    p.Timezone,
		},
		"services": services,
		"team":     team,
		"gallery":  gallery,
		"packages": packages,
		"hours":    hours,
		"loyalty":  loyalty,
	}
	if deposit != nil {
		body["deposit"] = deposit
	}
	c.JSON(http.StatusOK, body)
}
