// PackageService: paquetes y bonos prepagos con saldo por clienta (F8 del
// contrato de la fase 6). Decisiones D2/D5/D6/D7/D8/D9/D10:
//
//   - COMPRAR un paquete es una VENTA (el dinero entra hoy y lo ven la caja, el
//     dashboard, la ganancia, los exportes y el historial sin cambios); CONSUMIR
//     una sesión NO es una venta: es un movimiento de saldo sin dinero (D2).
//     `POST /sales`, `domain.Sale` y `SaleService` no se tocan.
//   - Vender, consumir y leer paquetes/saldos: owner/admin/recepción
//     (puedeVender). Ajustar el saldo y crear/editar el catálogo: SOLO
//     owner/admin (CanManage). Profesional y cliente no ven precios ni saldos.
//   - El saldo es un contador y package_movements su libro: en todo momento
//     sessions_left = Σ delta, en la MISMA transacción del adaptador (D4).
//   - Un client_op_id repetido devuelve el resultado original SIN crear ni
//     descontar otra vez (la compra por el índice único de `sales`, el consumo
//     por el de package_movements).
//
// Toda consulta va scopeada por salon_id: el paquete o la clienta de otro salón
// es 404 («Paquete inválido» al comprar un paquete que no existe) y no escribe
// nada.
package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type PackageService struct {
	// Packages es el catálogo; ClientPackages son los paquetes comprados y su
	// libro de saldo.
	Packages       ports.PackageRepository
	ClientPackages ports.ClientPackageRepository
	Services       ports.ServiceRepository
	Clients        ports.ClientRepository
	Memberships    ports.MembershipRepository
	// Settings da la zona horaria del salón (el vencimiento es el fin del día
	// LOCAL). Sin configuración guardada se usan los valores de fábrica.
	Settings ports.SalonSettingsRepository
	// Events es opcional (nil = no avisa): la compra publica `sale.created`
	// igual que el POS, después de escribir.
	Events ports.EventPublisher
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s PackageService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// --- Catálogo (D10) --------------------------------------------------------

// ListCatalog trae los paquetes del salón (activos e inactivos) con precio. La
// recepción los necesita para vender: owner/admin/recepción.
func (s PackageService) ListCatalog(actor domain.Membership, salonID string) ([]domain.ServicePackage, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !puedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	return s.Packages.List(salonID)
}

// CreateCatalog da de alta un paquete. Solo owner/admin. El servicio tiene que
// existir EN ESTE salón y estar activo («Paquete inválido» si no).
func (s PackageService) CreateCatalog(actor domain.Membership, salonID, name, serviceID string, sessions, priceCents int, validityDays *int) (domain.ServicePackage, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.ServicePackage{}, err
	}
	p, err := domain.NewServicePackage(salonID, name, serviceID, sessions, priceCents, validityDays)
	if err != nil {
		return domain.ServicePackage{}, err
	}
	svc, err := s.activeService(salonID, p.ServiceID)
	if err != nil {
		return domain.ServicePackage{}, err
	}
	p.ServiceName = svc.Name
	if err := s.ensureFreeName(salonID, p.Name, ""); err != nil {
		return domain.ServicePackage{}, err
	}
	if err := s.Packages.Create(p); err != nil {
		return domain.ServicePackage{}, packageNameTaken(err)
	}
	return p, nil
}

// UpdateCatalog REEMPLAZA nombre, precio, vigencia y estado (las CUATRO claves
// las exige el handler). El servicio y las sesiones no se editan.
func (s PackageService) UpdateCatalog(actor domain.Membership, salonID, packageID, name string, priceCents int, validityDays *int, active bool) (domain.ServicePackage, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.ServicePackage{}, err
	}
	current, err := s.findPackage(salonID, packageID)
	if err != nil {
		return domain.ServicePackage{}, packageNotFound(err)
	}
	updated, err := current.WithDetails(name, priceCents, validityDays, active)
	if err != nil {
		return domain.ServicePackage{}, err
	}
	if updated.Active {
		if err := s.ensureFreeName(salonID, updated.Name, updated.ID); err != nil {
			return domain.ServicePackage{}, err
		}
	}
	if err := s.Packages.Save(updated); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ServicePackage{}, packageNotFound(err)
		}
		return domain.ServicePackage{}, packageNameTaken(err)
	}
	// Solo de lectura (la respuesta lo muestra): un fallo aquí no cambia lo escrito.
	if svc, err := s.Services.FindByID(salonID, updated.ServiceID); err == nil {
		updated.ServiceName = svc.Name
	}
	return updated, nil
}

// --- Comprar (D5) ----------------------------------------------------------

// Purchase vende un paquete a una clienta: UNA transacción con la venta, el
// paquete (precio y nombre de FOTO) y el movimiento `purchase`. Solo
// owner/admin/recepción.
//
// Orden de las reglas (el mismo espíritu que el POS):
//  1. permisos del actor;
//  2. la clienta existe EN ESTE salón (404 con el texto de las rutas de clientes);
//  3. replay idempotente por client_op_id: devuelve la compra original;
//  4. el profesional atiende en el salón (igual que el POS);
//  5. el paquete existe, es del salón, está activo y su servicio también
//     (400 «Paquete inválido»);
//  6. forma de la venta (método de pago) y escritura.
func (s PackageService) Purchase(actor domain.Membership, salonID, clientID, packageID, professionalID string, method domain.PaymentMethod, clientOpID string) (domain.ClientPackage, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if !puedeVender(m) {
		return domain.ClientPackage{}, domain.ErrForbidden
	}
	client, err := s.client(salonID, clientID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	opID, err := domain.ValidatePackageOpID(clientOpID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if opID != "" {
		if cp, err := s.replay(salonID, client.ID, opID); err == nil {
			return cp, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.ClientPackage{}, err
		}
	}
	if err := s.ensureStaff(salonID, professionalID); err != nil {
		return domain.ClientPackage{}, err
	}
	pkg, err := s.activePackage(salonID, packageID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	loc, err := salonLocation(s.Settings, salonID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	order, err := domain.NewPackagePurchase(pkg, client, professionalID, method, opID, m.UserID, s.now(), loc)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if err := s.ClientPackages.Purchase(order); err != nil {
		// Carrera entre el replay y el insert (dos reintentos a la vez): el índice
		// único dejó pasar uno; el otro devuelve la compra que ganó.
		if opID != "" && errors.Is(err, domain.ErrAlreadyExists) {
			if cp, rerr := s.replay(salonID, client.ID, opID); rerr == nil {
				return cp, nil
			}
		}
		return domain.ClientPackage{}, err
	}
	sale := order.Sale
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
	cp := order.ClientPackage
	cp.Expired = cp.ExpiredAt(s.now())
	return cp, nil
}

// replay resuelve la compra original de un client_op_id y comprueba que fue de
// ESTA clienta: devolver la compra de otra como si fuera la de este pedido
// mentiría (409).
func (s PackageService) replay(salonID, clientID, opID string) (domain.ClientPackage, error) {
	cp, err := s.ClientPackages.FindPurchaseByOp(salonID, opID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if cp.ClientID != clientID {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageOpReused)
	}
	cp.Expired = cp.ExpiredAt(s.now())
	return cp, nil
}

// --- Leer (D8) ---------------------------------------------------------------

// ListClientPackages trae los paquetes de una clienta (vigentes con saldo
// primero, tope 200). Solo owner/admin/recepción: llevan precios y saldos.
func (s PackageService) ListClientPackages(actor domain.Membership, salonID, clientID string) ([]domain.ClientPackage, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !puedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	client, err := s.client(salonID, clientID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	list, err := s.ClientPackages.ListByClient(salonID, client.ID, now)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Expired = list[i].ExpiredAt(now)
	}
	return list, nil
}

// Movements trae el libro de UN paquete de la clienta (más nuevos primero,
// tope 200). Solo owner/admin/recepción.
func (s PackageService) Movements(actor domain.Membership, salonID, clientID, clientPackageID string) ([]domain.PackageMovement, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return nil, err
	}
	if !puedeVerPlata(m) {
		return nil, domain.ErrForbidden
	}
	if !cleanIDs(clientID, clientPackageID) {
		return nil, domain.NewMessage(domain.ErrNotFound, domain.MsgPackageNotFound)
	}
	list, err := s.ClientPackages.ListMovements(salonID, strings.TrimSpace(clientID), strings.TrimSpace(clientPackageID))
	if err != nil {
		return nil, packageNotFound(err)
	}
	return list, nil
}

// --- Consumir (D7) y ajustar (D9) -------------------------------------------

// Redeem descuenta UNA sesión. Solo owner/admin/recepción. El adaptador hace UN
// UPDATE condicional (saldo >= 1 y no vencido) más el movimiento: dos consumos
// simultáneos de la última sesión => UNO gana. Sin filas afectadas distingue
// 404 «Paquete no encontrado», 409 «No quedan sesiones en este paquete» y 409
// «El paquete venció». Un client_op_id repetido devuelve el estado actual SIN
// descontar otra vez.
func (s PackageService) Redeem(actor domain.Membership, salonID, clientID, clientPackageID, professionalID, note, clientOpID string) (domain.ClientPackage, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if !puedeVender(m) {
		return domain.ClientPackage{}, domain.ErrForbidden
	}
	if !cleanIDs(clientID, clientPackageID) {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrNotFound, domain.MsgPackageNotFound)
	}
	note, err = domain.ValidatePackageNote(note)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	opID, err := domain.ValidatePackageOpID(clientOpID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	professionalID = strings.TrimSpace(professionalID)
	if professionalID != "" {
		if err := s.ensureStaff(salonID, professionalID); err != nil {
			return domain.ClientPackage{}, err
		}
	}
	now := s.now()
	cp, err := s.ClientPackages.Redeem(domain.PackageRedeemRequest{
		SalonID:         salonID,
		ClientID:        strings.TrimSpace(clientID),
		ClientPackageID: strings.TrimSpace(clientPackageID),
		ProfessionalID:  professionalID,
		Note:            note,
		ClientOpID:      opID,
		Actor:           m.UserID,
		Now:             now,
	})
	if err != nil {
		return domain.ClientPackage{}, packageNotFound(err)
	}
	cp.Expired = cp.ExpiredAt(now)
	return cp, nil
}

// Adjust corrige el saldo (un consumo equivocado: no hay anulación de ventas).
// Solo owner/admin. La nota es obligatoria y el saldo no puede salir de
// 0..sessions_total.
func (s PackageService) Adjust(actor domain.Membership, salonID, clientID, clientPackageID string, delta int, note string) (domain.ClientPackage, error) {
	m, err := s.manager(actor, salonID)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	if !cleanIDs(clientID, clientPackageID) {
		return domain.ClientPackage{}, domain.NewMessage(domain.ErrNotFound, domain.MsgPackageNotFound)
	}
	note, err = domain.ValidatePackageAdjust(delta, note)
	if err != nil {
		return domain.ClientPackage{}, err
	}
	now := s.now()
	cp, err := s.ClientPackages.Adjust(domain.PackageAdjustRequest{
		SalonID:         salonID,
		ClientID:        strings.TrimSpace(clientID),
		ClientPackageID: strings.TrimSpace(clientPackageID),
		Delta:           delta,
		Note:            note,
		Actor:           m.UserID,
		Now:             now,
	})
	if err != nil {
		return domain.ClientPackage{}, packageNotFound(err)
	}
	cp.Expired = cp.ExpiredAt(now)
	return cp, nil
}

// --- Auxiliares ---------------------------------------------------------------

// cleanIDs dice si los ids de la ruta son usables: un NUL en un id llegaría a
// Postgres como un 500 (no cabe en un TEXT); es un recurso que no existe.
func cleanIDs(ids ...string) bool {
	for _, id := range ids {
		if strings.ContainsRune(id, 0) {
			return false
		}
	}
	return true
}

// packageNotFound traduce el ErrNotFound del adaptador a la frase del contrato.
// Cualquier otro error (incluidos los 409 con su texto) se propaga igual.
func packageNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgPackageNotFound)
	}
	return err
}

// packageNameTaken traduce el 23505 del índice de nombres activos.
func packageNameTaken(err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageNameTaken)
	}
	return err
}

// client resuelve la ficha EN ESTE salón: otro salón o inexistente es 404 con
// el texto de las rutas de clientes.
func (s PackageService) client(salonID, clientID string) (domain.Client, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || !cleanIDs(clientID) {
		return domain.Client{}, domain.NewMessage(domain.ErrNotFound, domain.MsgClientNotFound)
	}
	c, err := s.Clients.FindByID(salonID, clientID)
	if err != nil {
		return domain.Client{}, clientError(err)
	}
	return c, nil
}

// findPackage resuelve un paquete del catálogo EN ESTE salón.
func (s PackageService) findPackage(salonID, packageID string) (domain.ServicePackage, error) {
	packageID = strings.TrimSpace(packageID)
	if packageID == "" || !cleanIDs(packageID) {
		return domain.ServicePackage{}, domain.ErrNotFound
	}
	return s.Packages.FindByID(salonID, packageID)
}

// activeService exige un servicio del salón y activo; si no, «Paquete inválido».
func (s PackageService) activeService(salonID, serviceID string) (domain.Service, error) {
	if !cleanIDs(serviceID) {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
	}
	svc, err := s.Services.FindByID(salonID, serviceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
		}
		return domain.Service{}, err
	}
	if !svc.Active {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
	}
	return svc, nil
}

// activePackage exige un paquete del salón, activo y con el servicio activo; si
// no, «Paquete inválido» (inexistente, inactivo, de otro salón o servicio
// inactivo: el mismo texto, sin distinguir). Devuelve el paquete con el nombre
// del servicio.
func (s PackageService) activePackage(salonID, packageID string) (domain.ServicePackage, error) {
	p, err := s.findPackage(salonID, packageID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ServicePackage{}, domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
		}
		return domain.ServicePackage{}, err
	}
	if !p.Active {
		return domain.ServicePackage{}, domain.NewMessage(domain.ErrValidation, domain.MsgPackageInvalid)
	}
	svc, err := s.activeService(salonID, p.ServiceID)
	if err != nil {
		return domain.ServicePackage{}, err
	}
	p.ServiceName = svc.Name
	return p, nil
}

// ensureStaff valida a quien atiende/vende igual que el POS (ensureProfessional)
// y rechaza un id con NUL sin tocar la base.
func (s PackageService) ensureStaff(salonID, professionalID string) error {
	professionalID = strings.TrimSpace(professionalID)
	if !cleanIDs(professionalID) {
		return fmt.Errorf("%w: el profesional no pertenece al salón", domain.ErrValidation)
	}
	return ensureProfessional(s.Memberships, salonID, professionalID)
}

// ensureFreeName rechaza un nombre que ya usa un paquete ACTIVO del salón, sin
// importar mayúsculas (el índice de la DB es sobre lower(name) WHERE active).
func (s PackageService) ensureFreeName(salonID, name, ignoreID string) error {
	list, err := s.Packages.List(salonID)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.ID == ignoreID || !p.Active {
			continue
		}
		if strings.EqualFold(p.Name, name) {
			return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgPackageNameTaken)
		}
	}
	return nil
}

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s PackageService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// manager exige membership activa y permiso de configuración del salón.
func (s PackageService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
