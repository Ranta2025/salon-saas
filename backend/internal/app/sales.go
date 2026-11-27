// SaleService: ventas del POS. Reglas: quién puede vender, y el total SIEMPRE
// se calcula acá (nunca se acepta del cliente). Cada ítem se valida contra el
// catálogo del propio salón: no se puede vender un servicio de otro tenant.
package app

import (
	"errors"
	"fmt"
	"log"
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
	// Clients resuelve la ficha elegida en el POS (F5, D3-c): tiene que ser de
	// ESTE salón. ClientLinks escribe el vínculo después de guardar la venta,
	// best-effort (un fallo no tumba la venta).
	Clients     ports.ClientRepository
	ClientLinks ports.ClientLinkRepository
	// Products y ProductSales son la venta CON productos (F6, D3/D5).
	// Opcionales: nil = este proceso no vende productos, y una venta sin
	// productos no los toca nunca (sigue por Sales.Save, byte por byte).
	Products     ports.ProductRepository
	ProductSales ports.SaleProductsRepository
	// Notifier es opcional (nil = no avisa): el aviso de stock bajo de una
	// venta se deja DESPUÉS del commit y un fallo al avisar no cambia la
	// respuesta de la venta (F6, D8).
	Notifier Notifier
}

// puedeVender: recepción y quien administra (el profesional cobra en caja...
// o no: el POS es del salón, no de la persona). Se mantiene explícito y en un
// solo lugar para que el día que cambie, cambie una función.
func PuedeVender(m domain.Membership) bool {
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

// RegisterSaleForClient es la venta del POS con la ficha elegida a mano (F5,
// D3-c). clientID vacío = la venta de siempre, sin vínculo (no se infiere por
// nombre). Una ficha de otro salón es 400 «Ficha de clienta inválida» y la venta
// NO se guarda; el nombre de la venta se completa con el de la ficha si vino
// vacío.
func (s SaleService) RegisterSaleForClient(actor domain.Membership, salonID, professionalID, clientName, clientID string, items []domain.SaleItem, method domain.PaymentMethod, clientOpID string) (domain.Sale, error) {
	return s.register(actor, salonID, professionalID, clientName, clientID, items, method, clientOpID)
}

// RegisterSaleWithOp registra la venta con un operation id del cliente.
// Idempotente: si esa operación ya existe en este salón, devuelve la venta
// original (mismo id y mismo total) en vez de crear otra. Es lo que evita que
// la cola offline cobre dos veces una venta que ya había llegado al servidor.
//
// Orden de las reglas (importa):
//  1. permisos del actor;
//  2. forma del pedido TAL COMO LLEGÓ (B-03: qty/precio/total dentro de rango);
//  3. replay idempotente (no se rompe porque el catálogo cambió después);
//  4. el profesional atiende en el salón (B-12);
//  5. precio desde el catálogo y servicio activo (B-04).
func (s SaleService) RegisterSaleWithOp(actor domain.Membership, salonID, professionalID, clientName string, items []domain.SaleItem, method domain.PaymentMethod, clientOpID string) (domain.Sale, error) {
	return s.register(actor, salonID, professionalID, clientName, "", items, method, clientOpID)
}

// register es el cuerpo común de las ventas. El replay devuelve la venta
// original ANTES de guardar, así que nunca vuelve a tocar su vínculo (D3-d).
func (s SaleService) register(actor domain.Membership, salonID, professionalID, clientName, clientID string, items []domain.SaleItem, method domain.PaymentMethod, clientOpID string) (domain.Sale, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Sale{}, err
	}
	if !PuedeVender(m) {
		return domain.Sale{}, domain.ErrForbidden
	}
	// (1.b) La ficha del POS (F5): tiene que existir EN ESTE salón. Se lee antes
	// de validar la venta porque, si client_name vino vacío, el nombre de la
	// ficha es parte del pedido. Un id de otro tenant no se puede vincular.
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		client, err := s.lookupClient(salonID, clientID)
		if err != nil {
			return domain.Sale{}, err
		}
		if strings.TrimSpace(clientName) == "" {
			clientName = client.Name
		}
	}
	// (2) El pedido se valida tal como llegó: un precio manipulado fuera de
	// rango se rechaza con 400 en vez de "corregirse" en silencio.
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
	if err := EnsureProfessional(s.Memberships, salonID, professionalID); err != nil {
		return domain.Sale{}, err
	}
	// (5) El precio lo pone el catálogo del salón, nunca el cliente, y un
	// servicio inactivo ya no se vende. Se reconstruye la venta con los precios
	// del catálogo: el total sale de ahí, no del body. La comisión se congela en
	// la misma relectura (F3.1, D1 del contrato F3).
	priced := make([]domain.SaleItem, len(items))
	for i, it := range items {
		svc, err := s.Services.FindByID(salonID, it.ServiceID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.Sale{}, fmt.Errorf("%w: el servicio no existe en el salón", domain.ErrValidation)
			}
			return domain.Sale{}, err
		}
		if !svc.Active {
			return domain.Sale{}, fmt.Errorf("%w: el servicio %q ya no está activo", domain.ErrValidation, svc.Name)
		}
		// F3.1 (S-F3.1-2): la línea se lleva la FOTO del porcentaje vigente AHORA.
		// El servicio se acaba de releer para fijar el precio, así que la tarifa
		// sale del mismo punto de lectura y no de una segunda consulta: editar el
		// catálogo después no toca esta venta. Se copia a una variable propia por
		// ítem para no compartir el puntero entre líneas.
		pct := svc.CommissionPct
		priced[i] = domain.SaleItem{ServiceID: svc.ID, Qty: it.Qty, UnitPriceCents: svc.PriceCents, CommissionPct: &pct}
	}
	sale, err = domain.NewSale(salonID, professionalID, clientName, priced, method)
	if err != nil {
		return domain.Sale{}, err
	}
	sale.ClientOpID = strings.TrimSpace(clientOpID)
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
	// D3-c: la venta se vincula con la ficha ELEGIDA (solo si vino client_id).
	// Best-effort, igual que el turno: un fallo del UPDATE no deshace la venta.
	if clientID != "" && s.ClientLinks != nil {
		if err := s.ClientLinks.LinkSaleByID(salonID, sale.ID, clientID); err != nil {
			log.Printf("venta %s: vincular la ficha en el salón %s: %v", sale.ID, salonID, err)
		}
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

// lookupClient resuelve la ficha del POS con el scope del salón: otro tenant es
// el 400 del contrato (no un 404, porque lo que se está creando es la venta).
func (s SaleService) lookupClient(salonID, clientID string) (domain.Client, error) {
	if s.Clients == nil {
		return domain.Client{}, domain.NewMessage(domain.ErrValidation, domain.MsgSaleClientInvalid)
	}
	c, err := s.Clients.FindByID(salonID, clientID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Client{}, domain.NewMessage(domain.ErrValidation, domain.MsgSaleClientInvalid)
		}
		return domain.Client{}, err
	}
	return c, nil
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

// LinkedClient devuelve la ficha a la que la venta quedó vinculada de verdad ("" si
// no tiene o si no se pudo leer). El handler la usa para responder lo que se
// guardó y no lo que se pidió.
func (s SaleService) LinkedClient(salonID, saleID string) string {
	if s.ClientLinks == nil {
		return ""
	}
	id, err := s.ClientLinks.SaleClientID(salonID, saleID)
	if err != nil {
		log.Printf("leer el vínculo de la venta %s: %v", saleID, err)
		return ""
	}
	return id
}
