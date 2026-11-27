// DepositPolicyService: la política de señas del salón (F11, D3).
//
// Reglas del contrato que viven acá:
//   - leer: cualquier miembro ACTIVO del salón;
//   - guardar: SOLO owner/admin (CanManage). El permiso se chequea ANTES de mirar
//     el cuerpo: quien no puede guardar recibe 403 aunque el cuerpo esté vacío o
//     mal formado (el handler llama a RequireManager primero);
//   - las CUATRO claves son obligatorias (enabled, percent, hold_minutes,
//     refund_hours) y cada rango tiene su texto exacto;
//   - activar la sena sin banco configurado es 409 (D12);
//   - sin fila guardada la respuesta es `enabled:false` y el resto null: nunca se
//     inventan valores (D3).
package deposits

import (
	"errors"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// DepositPolicyInput es el cuerpo de PUT /salons/:id/deposit-policy con los
// punteros que dejó el JSON: nil = la clave NO vino (y es un error, D3).
type DepositPolicyInput struct {
	Enabled     *bool
	Percent     *int
	HoldMinutes *int
	RefundHours *int
}

type DepositPolicyService struct {
	Policies ports.DepositPolicyRepository
	// Memberships valida al actor contra la base (no confía en el rol del token).
	Memberships ports.MembershipRepository
	// Gateway dice si hay banco: con nil o Enabled()=false no se puede activar la
	// política (D12).
	Gateway ports.PaymentGateway
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s DepositPolicyService) NowUTC() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Get lee la política del salón. Cualquier miembro ACTIVO. Una política nunca
// guardada vuelve con Saved=false (el handler muestra enabled:false y el resto
// null).
func (s DepositPolicyService) Get(actor domain.Membership, salonID string) (domain.DepositPolicy, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return domain.DepositPolicy{}, err
	}
	return s.policy(salonID)
}

// policy lee la política sin permisos (ya validados por quien llama). Nunca
// guardada no es un error: Saved=false y el handler muestra los nulls de D3.
func (s DepositPolicyService) policy(salonID string) (domain.DepositPolicy, error) {
	p, err := s.Policies.Find(salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.DepositPolicy{SalonID: salonID}, nil
		}
		return domain.DepositPolicy{}, err
	}
	p.Saved = true
	return p, nil
}

// Put REEMPLAZA la política con las cuatro claves obligatorias. Solo owner/admin.
// Orden: permiso, claves, rangos, banco, guardado.
func (s DepositPolicyService) Put(actor domain.Membership, salonID string, in DepositPolicyInput) (domain.DepositPolicy, error) {
	// 1) Permiso ANTES de mirar el cuerpo.
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.DepositPolicy{}, err
	}
	// 2) Las cuatro claves son obligatorias (D3).
	if in.Enabled == nil || in.Percent == nil || in.HoldMinutes == nil || in.RefundHours == nil {
		return domain.DepositPolicy{}, domain.NewMessage(domain.ErrValidation, domain.MsgDepositPolicyMissing)
	}
	// 3) Rangos, en el orden del contrato.
	p, err := domain.NewDepositPolicy(salonID, *in.Enabled, *in.Percent, *in.HoldMinutes, *in.RefundHours, s.NowUTC())
	if err != nil {
		return domain.DepositPolicy{}, err
	}
	// 4) Activar sin banco es 409 (D12).
	if p.Enabled && (s.Gateway == nil || !s.Gateway.Enabled()) {
		return domain.DepositPolicy{}, errDepositGatewayUnavailable()
	}
	// 5) Guardar.
	saved, err := s.Policies.Save(p)
	if err != nil {
		return domain.DepositPolicy{}, err
	}
	saved.Saved = true
	return saved, nil
}

// RequireManager es el permiso SOLO (sin tocar datos): el handler lo llama ANTES
// de leer el cuerpo, así quien no puede guardar recibe 403 aunque el cuerpo esté
// mal formado.
func (s DepositPolicyService) RequireManager(actor domain.Membership, salonID string) error {
	_, err := s.manager(actor, salonID)
	return err
}

// RequireMember es el permiso de LECTURA: cualquier miembro activo del salón.
func (s DepositPolicyService) RequireMember(actor domain.Membership, salonID string) error {
	_, err := s.member(actor, salonID)
	return err
}

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s DepositPolicyService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
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
func (s DepositPolicyService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// errDepositGatewayUnavailable es el 409 del contrato (D3/D12).
func errDepositGatewayUnavailable() error {
	return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgDepositGatewayUnavailable)
}
