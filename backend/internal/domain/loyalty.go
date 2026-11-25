// Fidelidad: «cada N visitas, un servicio de regalo» (F9 del contrato de la fase 6).
//
// Cuando una clienta junta N visitas gana UN vale de «un servicio de regalo»
// (D1). Usar el vale NO es una venta ni mueve dinero: POST /sales, Sale, la
// nómina, la caja y el panel quedan intactos. Las visitas se CALCULAN (D2) de
// las ventas de la clienta con al menos una línea de servicio desde
// `counting_from`; nada las cuenta aparte.
//
// El dominio no sabe de HTTP ni de SQL: valida, calcula el progreso y arma el
// vale con los textos EXACTOS que ve la persona (los mensajes del contrato).
package domain

import (
	"strings"
	"time"
)

const (
	// MinLoyaltyVisits y MaxLoyaltyVisits acotan «cada N visitas».
	MinLoyaltyVisits = 2
	MaxLoyaltyVisits = 50
	// MaxLoyaltyValidityDays es el techo de la vigencia de un vale (10 años).
	MaxLoyaltyValidityDays = MaxPackageValidityDays
	// MaxLoyaltyListed es el tope de vales que lista la ficha de una clienta (D5).
	MaxLoyaltyListed = 100
)

// Textos exactos del contrato F9 (los que ve la persona).
const (
	MsgLoyaltyProgramMissing = "Faltan datos del programa: activado, visitas, servicio y vigencia"
	MsgLoyaltyVisitsRange    = "Las visitas necesarias deben ser entre 2 y 50"
	MsgLoyaltyServiceInvalid = "Servicio inválido"
	MsgLoyaltyValidityRange  = "La vigencia debe ser entre 1 y 3650 días"
	MsgLoyaltyNoReward       = "No hay premios disponibles"
	MsgLoyaltyDisabled       = "La fidelidad no está activada"
	MsgLoyaltyRewardNotFound = "Premio no encontrado"
	MsgLoyaltyRewardUsed     = "Ese premio ya se usó"
	MsgLoyaltyRewardExpired  = "El premio venció"

	// No están literales en el contrato: siguen el estilo de F8.
	// MsgLoyaltyOpReused es el 409 de un client_op_id que ya se usó para OTRO
	// pedido (otra clienta, otro vale u otra acción): devolver ese resultado como
	// si fuera el de este pedido mentiría.
	MsgLoyaltyOpReused = MsgPackageOpReused
	// MsgLoyaltyBusy es el 409 de un pedido que chocó con otra transacción
	// (40P01/40001): la transacción ya se deshizo, reintentar es seguro.
	MsgLoyaltyBusy = MsgPackageBusy
)

// Estados de un vale (D5).
const (
	LoyaltyAvailable = "available"
	LoyaltyRedeemed  = "redeemed"
	LoyaltyExpired   = "expired"
)

// LoyaltyProgram es el programa del salón (a lo sumo uno, D3).
type LoyaltyProgram struct {
	SalonID string
	// Saved es solo de lectura: false = el salón nunca lo guardó (D4: enabled
	// false y el resto null). Lo completa el caso de uso.
	Saved           bool
	Enabled         bool
	VisitsRequired  int
	RewardServiceID string
	// RewardServiceName es solo de lectura: lo completa el adaptador.
	RewardServiceName string
	// ValidityDays es la vigencia del vale en días; nil = no vence.
	ValidityDays *int
	// CountingFrom es el momento del PRIMER guardado: solo cuentan las ventas
	// desde ahí (D2). Los guardados siguientes lo conservan.
	CountingFrom time.Time
	UpdatedAt    time.Time
}

// ValidateLoyaltyVisits valida «cada N visitas»: de 2 a 50.
func ValidateLoyaltyVisits(n int) error {
	if n < MinLoyaltyVisits || n > MaxLoyaltyVisits {
		return NewMessage(ErrValidation, MsgLoyaltyVisitsRange)
	}
	return nil
}

// ValidateLoyaltyValidity valida la vigencia OPCIONAL del vale: nil = no vence;
// si viene, de 1 a 3650 días.
func ValidateLoyaltyValidity(days *int) error {
	if days != nil && (*days < 1 || *days > MaxLoyaltyValidityDays) {
		return NewMessage(ErrValidation, MsgLoyaltyValidityRange)
	}
	return nil
}

// NewLoyaltyProgram valida y arma el programa. El servicio lo resuelve el caso
// de uso (existe, es del salón y está activo). `now` es el CountingFrom del
// primer guardado: el adaptador lo ignora cuando el programa ya existía.
func NewLoyaltyProgram(salonID string, enabled bool, visits int, serviceID string, validityDays *int, now time.Time) (LoyaltyProgram, error) {
	serviceID = strings.TrimSpace(serviceID)
	if strings.TrimSpace(salonID) == "" || serviceID == "" {
		return LoyaltyProgram{}, NewMessage(ErrValidation, MsgLoyaltyServiceInvalid)
	}
	if err := ValidateLoyaltyVisits(visits); err != nil {
		return LoyaltyProgram{}, err
	}
	if err := ValidateLoyaltyValidity(validityDays); err != nil {
		return LoyaltyProgram{}, err
	}
	return LoyaltyProgram{
		SalonID:         salonID,
		Saved:           true,
		Enabled:         enabled,
		VisitsRequired:  visits,
		RewardServiceID: serviceID,
		ValidityDays:    copyIntPtr(validityDays),
		CountingFrom:    now.UTC().Truncate(time.Microsecond),
		UpdatedAt:       now.UTC().Truncate(time.Microsecond),
	}, nil
}

// LoyaltyProgress es el avance de una clienta (D5).
type LoyaltyProgress struct {
	Visits         int
	VisitsRequired int
	// Earned = floor(visitas / N): los premios que ya ganó en total.
	Earned int
	// Granted son los vales ya entregados (usados o no).
	Granted int
	// Available = max(0, Earned - Granted): los que puede pedir entregar ahora.
	Available int
	// NextIn = N - visitas % N: las visitas que faltan para el próximo premio.
	NextIn int
}

// ComputeLoyaltyProgress aplica las cuentas de D5 con enteros. Sin programa
// (N <= 0) no hay nada que dividir: todo cero.
func ComputeLoyaltyProgress(visits, required, granted int) LoyaltyProgress {
	p := LoyaltyProgress{Visits: visits, VisitsRequired: required, Granted: granted}
	if required <= 0 {
		return p
	}
	if visits < 0 {
		visits = 0
	}
	p.Earned = visits / required
	if p.Earned > granted {
		p.Available = p.Earned - granted
	}
	p.NextIn = required - visits%required
	return p
}

// LoyaltyReward es un vale entregado a una clienta: «un servicio de regalo».
type LoyaltyReward struct {
	ID       string
	SalonID  string
	ClientID string
	// RewardNumber es el hito: el vale 1, 2, 3... de esa clienta (UNIQUE).
	RewardNumber int
	// ServiceID y ServiceName son una FOTO del programa al entregar.
	ServiceID   string
	ServiceName string
	GrantedAt   time.Time
	// ExpiresAt es el FIN del día local de hoy + vigencia; nil = no vence.
	ExpiresAt  *time.Time
	RedeemedAt *time.Time
	RedeemedBy string
	// RedeemedProfessionalID y RedeemNote son opcionales ("" = no se dijo).
	RedeemedProfessionalID string
	RedeemNote             string
	GrantOpID              string
	RedeemOpID             string
	// State es solo de lectura: lo completa el caso de uso con su reloj (la
	// misma regla que StateAt).
	State string
}

// StateAt dice el estado del vale en `now`: usado gana sobre vencido; vence
// EXACTO en expires_at (la misma regla del UPDATE de uso: vigente mientras
// expires_at > now).
func (r LoyaltyReward) StateAt(now time.Time) string {
	switch {
	case r.RedeemedAt != nil:
		return LoyaltyRedeemed
	case r.ExpiresAt != nil && !r.ExpiresAt.After(now):
		return LoyaltyExpired
	default:
		return LoyaltyAvailable
	}
}

// NewLoyaltyGrant decide y arma la entrega (D6). Es la ÚNICA regla de «¿le toca
// un vale?», y la corren el adaptador (bajo el lock por clienta) y los fakes de
// los tests: programa apagado o nunca guardado => 409 «La fidelidad no está
// activada»; sin premio disponible => 409 «No hay premios disponibles». El vale
// lleva el número granted+1, el servicio y su nombre del programa de HOY y el
// vencimiento del fin del día LOCAL (zona del salón) + vigencia. NO persiste
// nada.
func NewLoyaltyGrant(p LoyaltyProgram, clientID string, visits, granted int, opID string, now time.Time, loc *time.Location) (LoyaltyReward, error) {
	if !p.Enabled {
		return LoyaltyReward{}, NewMessage(ErrAlreadyExists, MsgLoyaltyDisabled)
	}
	if ComputeLoyaltyProgress(visits, p.VisitsRequired, granted).Available < 1 {
		return LoyaltyReward{}, NewMessage(ErrAlreadyExists, MsgLoyaltyNoReward)
	}
	// A microsegundos, la precisión que guarda Postgres: el vale que devuelve la
	// entrega es idéntico al que devuelve su repetido.
	now = now.UTC().Truncate(time.Microsecond)
	return LoyaltyReward{
		ID:           NewID(),
		SalonID:      p.SalonID,
		ClientID:     clientID,
		RewardNumber: granted + 1,
		ServiceID:    p.RewardServiceID,
		ServiceName:  p.RewardServiceName,
		GrantedAt:    now,
		ExpiresAt:    ExpiryFor(now, p.ValidityDays, loc),
		GrantOpID:    strings.TrimSpace(opID),
	}, nil
}

// LoyaltyGrantRequest es una entrega pedida por el personal.
type LoyaltyGrantRequest struct {
	SalonID    string
	ClientID   string
	ClientOpID string // opcional (cola offline)
	Now        time.Time
	// Loc es la zona del salón: el vencimiento es el fin del día LOCAL.
	Loc *time.Location
}

// LoyaltyRedeemRequest es el uso de UN vale.
type LoyaltyRedeemRequest struct {
	SalonID        string
	ClientID       string
	RewardID       string
	ProfessionalID string // opcional
	Note           string // opcional, ya validada
	ClientOpID     string // opcional (cola offline)
	Actor          string
	Now            time.Time
}

// ValidateLoyaltyNote valida la nota OPCIONAL del uso: la misma regla que la de
// un consumo de paquete (sin NUL ni saltos de línea, hasta 200 caracteres).
func ValidateLoyaltyNote(note string) (string, error) { return ValidatePackageNote(note) }

// ValidateLoyaltyOpID valida el client_op_id OPCIONAL: la misma regla que F8.
func ValidateLoyaltyOpID(id string) (string, error) { return ValidatePackageOpID(id) }
