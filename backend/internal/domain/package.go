// Paquetes y bonos prepagos con saldo por clienta (F8 del contrato de la fase 6).
//
// Un paquete es N sesiones de UN servicio a un precio fijo, con vigencia
// opcional (D1). COMPRAR es una VENTA (el dinero entra hoy); CONSUMIR una
// sesión NO es una venta: es un movimiento de saldo sin dinero (D2). El saldo
// es un CONTADOR y package_movements su libro: en todo momento
// sessions_left = Σ delta (D4).
//
// El dominio no sabe de HTTP ni de SQL: valida y arma las entidades con los
// textos EXACTOS que ve la persona (los mensajes del contrato).
package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxPackageSessions es el techo de sesiones de un paquete.
	MaxPackageSessions = 100
	// MaxPackagePriceCents es el techo del precio (el mismo rango que un producto).
	MaxPackagePriceCents = MaxProductPriceCents
	// MaxPackageValidityDays es el techo de la vigencia (10 años).
	MaxPackageValidityDays = 3650
	// MaxPackageNoteRunes es el largo máximo de la nota de un movimiento.
	MaxPackageNoteRunes = 200
	// MaxPackageListed es el tope de filas de las lecturas por clienta (D8).
	MaxPackageListed = 200
)

// Textos exactos del contrato F8 (los que ve la persona).
const (
	MsgPackageCreateMissing = "Faltan datos del paquete: nombre, servicio, sesiones y precio"
	MsgPackageUpdateMissing = "Faltan datos del paquete: nombre, precio, vigencia y estado"
	MsgPackageNameTaken     = "Ya hay un paquete activo con ese nombre"
	MsgPackageInvalid       = "Paquete inválido"
	MsgPackageNotFound      = "Paquete no encontrado"
	MsgPackageNoSessions    = "No quedan sesiones en este paquete"
	MsgPackageExpired       = "El paquete venció"
	MsgPackageAdjustNote    = "Escribí el motivo del ajuste"
	MsgPackageAdjustRange   = "El ajuste deja el saldo fuera de rango"

	// Los siguientes textos no están literales en el contrato (D3/D9 solo fijan
	// los rangos): siguen el estilo de los de F6.
	MsgPackageNameRequired = "El nombre del paquete es obligatorio"
	MsgPackageNameTooLong  = "El nombre del paquete no puede superar los 120 caracteres"
	MsgPackagePriceRange   = "El precio debe estar entre 0 y 10000000 centavos"
	MsgPackageSessions     = "Las sesiones deben estar entre 1 y 100"
	MsgPackageValidity     = "La vigencia debe estar entre 1 y 3650 días"
	MsgPackageInvalidChars = "El texto no puede tener saltos de línea ni caracteres no válidos"
	MsgPackageNoteTooLong  = "La nota no puede superar los 200 caracteres"
	MsgPackageBadOpID      = "El identificador de la operación no es válido"
	MsgPackageAdjustZero   = "El ajuste no puede ser cero"
	// MsgPackageOpReused es el 409 de un client_op_id que ya se usó para OTRA
	// cosa (otra clienta, otro paquete o una venta que no es una compra de
	// paquete): devolver ese resultado como si fuera el de este pedido mentiría.
	MsgPackageOpReused = "Esa operación ya se usó en otro pedido"
	// MsgPackageBusy es el 409 de un pedido que chocó con otra transacción
	// (40P01/40001): la transacción ya se deshizo, reintentar es seguro.
	MsgPackageBusy = "El pedido chocó con otra operación: reintentá"
)

// ServicePackage es un paquete del catálogo del salón.
type ServicePackage struct {
	ID         string
	SalonID    string
	Name       string
	ServiceID  string
	Sessions   int
	PriceCents int
	// ValidityDays es la vigencia en días desde la compra; nil = no vence.
	ValidityDays *int
	Active       bool
	CreatedAt    time.Time
	// ServiceName es solo de lectura: lo completa el adaptador al listar.
	ServiceName string
}

// NewServicePackage valida y crea un paquete del catálogo. El servicio lo
// resuelve el caso de uso (existe, es del salón y está activo).
func NewServicePackage(salonID, name, serviceID string, sessions, priceCents int, validityDays *int) (ServicePackage, error) {
	if strings.TrimSpace(salonID) == "" || strings.TrimSpace(serviceID) == "" {
		return ServicePackage{}, NewMessage(ErrValidation, MsgPackageInvalid)
	}
	name, err := validatePackageName(name)
	if err != nil {
		return ServicePackage{}, err
	}
	if err := validatePackageNumbers(sessions, priceCents, validityDays); err != nil {
		return ServicePackage{}, err
	}
	return ServicePackage{
		ID:           NewID(),
		SalonID:      salonID,
		Name:         name,
		ServiceID:    strings.TrimSpace(serviceID),
		Sessions:     sessions,
		PriceCents:   priceCents,
		ValidityDays: copyIntPtr(validityDays),
		Active:       true,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// WithDetails REEMPLAZA nombre, precio, vigencia y estado (el PUT del contrato
// exige las cuatro claves). El servicio y las sesiones NO se editan.
func (p ServicePackage) WithDetails(name string, priceCents int, validityDays *int, active bool) (ServicePackage, error) {
	name, err := validatePackageName(name)
	if err != nil {
		return ServicePackage{}, err
	}
	if err := validatePackageNumbers(p.Sessions, priceCents, validityDays); err != nil {
		return ServicePackage{}, err
	}
	p.Name = name
	p.PriceCents = priceCents
	p.ValidityDays = copyIntPtr(validityDays)
	p.Active = active
	return p, nil
}

func copyIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// hasLineBreak detecta un salto de línea (también los de Unicode): el nombre y
// la nota son de UNA línea.
func hasLineBreak(s string) bool {
	return strings.ContainsAny(s, "\n\r\u0085\u2028\u2029")
}

// validatePackageText rechaza un NUL o un salto de línea ANTES de recortar:
// «Pack\n» no se «arregla» en silencio.
func validatePackageText(s string) error {
	if hasNUL(s) || hasLineBreak(s) {
		return NewMessage(ErrValidation, MsgPackageInvalidChars)
	}
	return nil
}

func validatePackageName(name string) (string, error) {
	if err := validatePackageText(name); err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", NewMessage(ErrValidation, MsgPackageNameRequired)
	}
	if utf8.RuneCountInString(name) > MaxTextName {
		return "", NewMessage(ErrValidation, MsgPackageNameTooLong)
	}
	return name, nil
}

func validatePackageNumbers(sessions, priceCents int, validityDays *int) error {
	if sessions < 1 || sessions > MaxPackageSessions {
		return NewMessage(ErrValidation, MsgPackageSessions)
	}
	if priceCents < 0 || priceCents > MaxPackagePriceCents {
		return NewMessage(ErrValidation, MsgPackagePriceRange)
	}
	if validityDays != nil && (*validityDays < 1 || *validityDays > MaxPackageValidityDays) {
		return NewMessage(ErrValidation, MsgPackageValidity)
	}
	return nil
}

// ValidatePackageNote valida la nota OPCIONAL de un movimiento (consumo): sin
// NUL ni saltos de línea y hasta 200 caracteres. Devuelve la nota recortada.
func ValidatePackageNote(note string) (string, error) {
	if err := validatePackageText(note); err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxPackageNoteRunes {
		return "", NewMessage(ErrValidation, MsgPackageNoteTooLong)
	}
	return note, nil
}

// MaxPackageOpIDRunes es el largo máximo de un client_op_id (un UUID mide 36).
const MaxPackageOpIDRunes = 200

// ValidatePackageOpID valida el client_op_id OPCIONAL de la cola offline: sin
// NUL (Postgres lo rechaza en un TEXT: sería un 500) y de largo acotado.
// Devuelve el id recortado.
func ValidatePackageOpID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if hasNUL(id) || utf8.RuneCountInString(id) > MaxPackageOpIDRunes {
		return "", NewMessage(ErrValidation, MsgPackageBadOpID)
	}
	return id, nil
}

// ClientPackage es un paquete COMPRADO por una clienta: con el precio y el
// nombre congelados al comprar y el saldo de sesiones.
type ClientPackage struct {
	ID            string
	SalonID       string
	ClientID      string
	PackageID     string
	ServiceID     string
	Name          string
	SessionsTotal int
	SessionsLeft  int
	PriceCents    int
	PurchasedAt   time.Time
	// ExpiresAt es el FIN del día local de compra + vigencia; nil = no vence.
	ExpiresAt *time.Time
	SaleID    string
	// ServiceName es solo de lectura: lo completa el adaptador.
	ServiceName string
	// Expired es solo de lectura: lo completa el caso de uso con su reloj (la
	// misma regla que ExpiredAt) para la forma D8 del contrato.
	Expired bool
}

// ExpiredAt dice si el paquete ya venció en `now`. Es la MISMA regla del
// consumo: vigente mientras expires_at > now.
func (c ClientPackage) ExpiredAt(now time.Time) bool {
	return c.ExpiresAt != nil && !c.ExpiresAt.After(now)
}

// PackageMovementReason cataloga por qué cambió el saldo. Es cerrado: la DB lo
// repite con un CHECK.
type PackageMovementReason string

const (
	PackagePurchase PackageMovementReason = "purchase" // compra (+sesiones)
	PackageRedeem   PackageMovementReason = "redeem"   // consumo (-1)
	PackageAdjust   PackageMovementReason = "adjust"   // ajuste manual (owner/admin)
)

// PackageMovement es una fila del libro de saldo (append-only).
type PackageMovement struct {
	ID              string
	SalonID         string
	ClientPackageID string
	Delta           int
	Reason          PackageMovementReason
	Note            string
	ProfessionalID  string
	ClientOpID      string
	Actor           string
	CreatedAt       time.Time
}

// PackagePurchaseOrder es la compra completa, lista para escribir en UNA
// transacción: la venta (sin líneas), el paquete de la clienta y su movimiento
// `purchase`.
type PackagePurchaseOrder struct {
	Sale          Sale
	ClientPackage ClientPackage
	Movement      PackageMovement
}

// ExpiryFor calcula el vencimiento: el FIN del día LOCAL (zona del salón) del
// día de compra + validityDays, a su último microsegundo (el que guarda
// Postgres): una clienta que compra hoy un paquete de 1 día lo usa hoy y mañana.
// nil = no vence. time.Date normaliza el desborde de día y respeta el cambio de
// hora de la zona.
func ExpiryFor(purchasedAt time.Time, validityDays *int, loc *time.Location) *time.Time {
	if validityDays == nil {
		return nil
	}
	local := purchasedAt.In(loc)
	end := time.Date(local.Year(), local.Month(), local.Day()+*validityDays,
		23, 59, 59, 999_999_000, loc).UTC()
	return &end
}

// NewPackagePurchase valida y arma la compra. NO persiste nada. La venta
// guarda total = precio, packages_cents = precio (en la columna), SIN líneas, el
// nombre de la ficha y el método; el paquete lleva el precio y el nombre de
// FOTO (D5). `p` y `client` los resuelve el caso de uso (del salón, activos).
func NewPackagePurchase(p ServicePackage, client Client, professionalID string, method PaymentMethod, clientOpID, actor string, now time.Time, loc *time.Location) (PackagePurchaseOrder, error) {
	professionalID = strings.TrimSpace(professionalID)
	if professionalID == "" {
		return PackagePurchaseOrder{}, fmt.Errorf("%w: el profesional es obligatorio", ErrValidation)
	}
	if !method.Valid() {
		return PackagePurchaseOrder{}, fmt.Errorf("%w: método de pago desconocido %q", ErrValidation, method)
	}
	clientName := strings.TrimSpace(client.Name)
	if clientName == "" {
		return PackagePurchaseOrder{}, fmt.Errorf("%w: el nombre del cliente es obligatorio", ErrValidation)
	}
	if err := validateText("el nombre del cliente", clientName, MaxTextName); err != nil {
		return PackagePurchaseOrder{}, err
	}
	now = now.UTC()
	sale := Sale{
		ID:             NewID(),
		SalonID:        p.SalonID,
		ProfessionalID: professionalID,
		ClientName:     clientName,
		Items:          []SaleItem{},
		TotalCents:     p.PriceCents,
		Method:         method,
		ClientOpID:     strings.TrimSpace(clientOpID),
		CreatedAt:      now,
	}
	cp := ClientPackage{
		ID:            NewID(),
		SalonID:       p.SalonID,
		ClientID:      client.ID,
		PackageID:     p.ID,
		ServiceID:     p.ServiceID,
		ServiceName:   p.ServiceName,
		Name:          p.Name,
		SessionsTotal: p.Sessions,
		SessionsLeft:  p.Sessions,
		PriceCents:    p.PriceCents,
		PurchasedAt:   now,
		ExpiresAt:     ExpiryFor(now, p.ValidityDays, loc),
		SaleID:        sale.ID,
	}
	return PackagePurchaseOrder{
		Sale:          sale,
		ClientPackage: cp,
		Movement: PackageMovement{
			ID:              NewID(),
			SalonID:         p.SalonID,
			ClientPackageID: cp.ID,
			Delta:           p.Sessions,
			Reason:          PackagePurchase,
			ProfessionalID:  professionalID,
			Actor:           actor,
			CreatedAt:       now,
		},
	}, nil
}

// PackageRedeemRequest es un consumo de UNA sesión.
type PackageRedeemRequest struct {
	SalonID         string
	ClientID        string
	ClientPackageID string
	ProfessionalID  string // opcional
	Note            string // opcional, ya validada
	ClientOpID      string // opcional (cola offline)
	Actor           string
	Now             time.Time
}

// PackageAdjustRequest es un ajuste manual del saldo (D9).
type PackageAdjustRequest struct {
	SalonID         string
	ClientID        string
	ClientPackageID string
	Delta           int
	Note            string // obligatoria, ya validada
	Actor           string
	Now             time.Time
}

// ValidatePackageAdjust valida un ajuste: delta entero distinto de cero, nota
// obligatoria de 1..200 y delta dentro de ±sesiones máximas (un delta mayor deja
// el saldo fuera de rango SIEMPRE: 409, nunca un desborde de la columna).
// Devuelve la nota recortada. Que el saldo resultante quede en 0..sessions_total
// lo decide el UPDATE condicional de la base (la fuente de la verdad).
func ValidatePackageAdjust(delta int, note string) (string, error) {
	if delta == 0 {
		return "", NewMessage(ErrValidation, MsgPackageAdjustZero)
	}
	if err := validatePackageText(note); err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return "", NewMessage(ErrValidation, MsgPackageAdjustNote)
	}
	if utf8.RuneCountInString(note) > MaxPackageNoteRunes {
		return "", NewMessage(ErrValidation, MsgPackageNoteTooLong)
	}
	if delta > MaxPackageSessions || delta < -MaxPackageSessions {
		return "", NewMessage(ErrAlreadyExists, MsgPackageAdjustRange)
	}
	return note, nil
}
