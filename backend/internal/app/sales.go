// SaleService: ventas del POS. Reglas: quién puede vender, y el total SIEMPRE
// se calcula acá (nunca se acepta del cliente). Cada ítem se valida contra el
// catálogo del propio salón: no se puede vender un servicio de otro tenant.
package app

import (
	"errors"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type SaleService struct {
	Sales       ports.SaleRepository
	Services    ports.ServiceRepository
	Memberships ports.MembershipRepository
	// Events es opcional (nil = no avisa): la venta no depende del realtime.
	Events ports.EventPublisher
}

// puedeVender: recepción y quien administra (el profesional cobra en caja...
// o no: el POS es del salón, no de la persona). Se mantiene explícito y en un
// solo lugar para que el día que cambie, cambie una función.
func puedeVender(m domain.Membership) bool {
	return m.CanManage() || m.Role == domain.RoleReception
}

// puedeVerPlata: el dinero (ventas, cierres de caja, revenue del panel) es
// información sensible del negocio. Solo la ve quien administra o recepción.
// Un profesional o un cliente del salón ven la agenda y el catálogo, no la
// facturación. Regla en un solo lugar: cambiarla es cambiar esta función.
func puedeVerPlata(m domain.Membership) bool {
	return m.CanManage() || m.Role == domain.RoleReception
}

// RegisterSale registra la venta y devuelve la entidad con el total calculado.
func (s SaleService) RegisterSale(actor domain.Membership, salonID, professionalID, clientName string, items []domain.SaleItem, method domain.PaymentMethod) (domain.Sale, error) {
	return s.RegisterSaleWithOp(actor, salonID, professionalID, clientName, items, method, "")
}

// RegisterSaleWithOp registra la venta con un operation id del cliente.
// Idempotente: si esa operación ya existe en este salón, devuelve la venta
// original (mismo id y mismo total) en vez de crear otra. Es lo que evita que
// la cola offline cobre dos veces una venta que ya había llegado al servidor.
func (s SaleService) RegisterSaleWithOp(actor domain.Membership, salonID, professionalID, clientName string, items []domain.SaleItem, method domain.PaymentMethod, clientOpID string) (domain.Sale, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Sale{}, err
	}
	if !puedeVender(m) {
		return domain.Sale{}, domain.ErrForbidden
	}
	sale, err := domain.NewSale(salonID, professionalID, clientName, items, method)
	if err != nil {
		return domain.Sale{}, err
	}
	sale.ClientOpID = strings.TrimSpace(clientOpID)
	if sale.ClientOpID != "" {
		if existing, err := s.Sales.FindByClientOpID(salonID, sale.ClientOpID); err == nil {
			return existing, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Sale{}, err
		}
	}
	// Validación de catálogo: cada ítem tiene que existir en ESTE salón.
	// Si el servicio fuera de otro tenant, la venta rompería el aislamiento.
	for _, it := range sale.Items {
		if _, err := s.Services.FindByID(salonID, it.ServiceID); err != nil {
			return domain.Sale{}, domain.ErrValidation
		}
	}
	if err := s.Sales.Save(sale); err != nil {
		// Carrera entre el check previo y el insert (dos reintentos a la vez):
		// el índice único dejó pasar uno; el otro se resuelve devolviendo el original.
		if sale.ClientOpID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if existing, rerr := s.Sales.FindByClientOpID(salonID, sale.ClientOpID); rerr == nil {
				return existing, nil
			}
		}
		return domain.Sale{}, err
	}
	// El total que viaja acá es el que calculó el dominio, no el del cliente.
	publish(s.Events, ports.Event{
		Type:    "sale.created",
		SalonID: salonID,
		Data: map[string]any{
			"id":              sale.ID,
			"total_cents":     sale.TotalCents,
			"method":          string(sale.Method),
			"client_name":     sale.ClientName,
			"professional_id": sale.ProfessionalID,
		},
		TS: sale.CreatedAt,
	})
	return sale, nil
}

// ListSales trae las ventas del salón en [from,to). Solo quien puede ver la plata.
func (s SaleService) ListSales(actor domain.Membership, salonID string, from, to time.Time) ([]domain.Sale, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !puedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	return s.Sales.ListRange(salonID, from, to)
}

func (s SaleService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
