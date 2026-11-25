// LoyaltyService: fidelidad «cada N visitas, un servicio de regalo» (F9 del
// contrato de la fase 6). Decisiones D1/D2/D4..D7:
//
//   - El premio es un VALE, no un descuento: usarlo NO es una venta ni mueve
//     dinero. `POST /sales`, `domain.Sale`, `SaleService`, la nómina, la caja y
//     el panel no se tocan (D1).
//   - Las visitas se CALCULAN con una consulta de solo lectura sobre las ventas
//     de la clienta con una línea de servicio desde `counting_from` (D2).
//   - Leer el programa: cualquier miembro activo. Guardarlo: SOLO owner/admin
//     (CanManage). Ver a una clienta, entregar y usar un vale: owner/admin/
//     recepción (puedeVender); la profesional, la clienta y la baja reciben 403
//     (la ficha lleva datos de consumo) (D4..D7).
//   - Entregar y usar son atómicos en el adaptador (advisory lock por clienta +
//     UNIQUE (client_id, reward_number); UPDATE condicional) y un client_op_id
//     repetido devuelve el resultado original SIN entregar ni usar otra vez.
//
// Toda consulta va scopeada por salon_id: la clienta o el vale de otro salón es
// 404 y no escribe nada.
package app

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// LoyaltyStatus es la ficha de fidelidad de UNA clienta (D5).
type LoyaltyStatus struct {
	// Program es el programa del salón (Saved=false si nunca se guardó).
	Program domain.LoyaltyProgram
	// Progress son las cuentas: visitas, premios ganados, entregados, disponibles.
	Progress domain.LoyaltyProgress
	// Rewards son los vales (más nuevos primero, tope 100) con su State.
	Rewards []domain.LoyaltyReward
}

type LoyaltyService struct {
	Programs ports.LoyaltyProgramRepository
	Rewards  ports.LoyaltyRewardRepository
	Services ports.ServiceRepository
	Clients  ports.ClientRepository
	// Memberships valida al actor contra la base (no confía en el rol del token).
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón (el vencimiento es el fin del día
	// LOCAL). Sin configuración guardada se usan los valores de fábrica.
	Settings ports.SalonSettingsRepository
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s LoyaltyService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// --- Programa (D4) ------------------------------------------------------------

// Program lee el programa del salón. Cualquier miembro ACTIVO. Un programa
// nunca guardado vuelve con Saved=false (el handler lo muestra enabled:false y
// el resto null).
func (s LoyaltyService) Program(actor domain.Membership, salonID string) (domain.LoyaltyProgram, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return domain.LoyaltyProgram{}, err
	}
	return s.program(salonID)
}

// program lee el programa sin permisos: nunca guardado no es un error.
func (s LoyaltyService) program(salonID string) (domain.LoyaltyProgram, error) {
	p, err := s.Programs.Find(salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.LoyaltyProgram{SalonID: salonID}, nil
		}
		return domain.LoyaltyProgram{}, err
	}
	p.Saved = true
	return p, nil
}

// SaveProgram REEMPLAZA el programa (las CUATRO claves las exige el handler;
// `validityDays` nil = sin vencimiento). Solo owner/admin. Orden de las reglas
// del contrato: visitas, servicio y vigencia. El primer guardado fija
// counting_from = ahora; los siguientes lo conservan (lo decide el adaptador).
func (s LoyaltyService) SaveProgram(actor domain.Membership, salonID string, enabled bool, visits int, serviceID string, validityDays *int) (domain.LoyaltyProgram, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.LoyaltyProgram{}, err
	}
	if err := domain.ValidateLoyaltyVisits(visits); err != nil {
		return domain.LoyaltyProgram{}, err
	}
	svc, err := s.activeService(salonID, serviceID)
	if err != nil {
		return domain.LoyaltyProgram{}, err
	}
	p, err := domain.NewLoyaltyProgram(salonID, enabled, visits, svc.ID, validityDays, s.now())
	if err != nil {
		return domain.LoyaltyProgram{}, err
	}
	saved, err := s.Programs.Save(p)
	if err != nil {
		return domain.LoyaltyProgram{}, err
	}
	saved.Saved = true
	return saved, nil
}

// --- Ver a una clienta (D5) -----------------------------------------------------

// ClientStatus arma la ficha de fidelidad de una clienta. Solo
// owner/admin/recepción: lleva datos de consumo. Con el programa nunca guardado
// no hay counting_from desde el cual contar: todo en cero y sin vales.
func (s LoyaltyService) ClientStatus(actor domain.Membership, salonID, clientID string) (LoyaltyStatus, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	if !puedeVender(m) {
		return LoyaltyStatus{}, domain.ErrForbidden
	}
	client, err := s.client(salonID, clientID)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	prog, err := s.program(salonID)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	st := LoyaltyStatus{Program: prog, Rewards: []domain.LoyaltyReward{}}
	if !prog.Saved {
		return st, nil
	}
	visits, err := s.Rewards.Visits(salonID, client.ID, prog.CountingFrom)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	granted, err := s.Rewards.CountGranted(salonID, client.ID)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	list, err := s.Rewards.ListByClient(salonID, client.ID)
	if err != nil {
		return LoyaltyStatus{}, err
	}
	now := s.now()
	for i := range list {
		list[i].State = list[i].StateAt(now)
	}
	st.Progress = domain.ComputeLoyaltyProgress(visits, prog.VisitsRequired, granted)
	if list != nil {
		st.Rewards = list
	}
	return st, nil
}

// --- Entregar (D6) --------------------------------------------------------------

// Grant entrega UN vale a la clienta si le toca. Solo owner/admin/recepción. La
// regla y la atomicidad viven en el adaptador (lock por clienta, recalculo de
// visitas y entregados, UNIQUE de la guarda final). Un client_op_id repetido
// devuelve el MISMO vale sin entregar otro.
func (s LoyaltyService) Grant(actor domain.Membership, salonID, clientID, clientOpID string) (domain.LoyaltyReward, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	if !puedeVender(m) {
		return domain.LoyaltyReward{}, domain.ErrForbidden
	}
	client, err := s.client(salonID, clientID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	opID, err := domain.ValidateLoyaltyOpID(clientOpID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	loc, err := salonLocation(s.Settings, salonID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	now := s.now()
	reward, err := s.Rewards.Grant(domain.LoyaltyGrantRequest{
		SalonID: salonID, ClientID: client.ID, ClientOpID: opID, Now: now, Loc: loc,
	})
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	reward.State = reward.StateAt(now)
	return reward, nil
}

// --- Usar (D7) ------------------------------------------------------------------

// Redeem usa UN vale. Solo owner/admin/recepción. El adaptador hace UN UPDATE
// condicional (no usado y no vencido): dos usos simultáneos => UNO gana. Sin
// filas afectadas distingue 404 «Premio no encontrado», 409 «Ese premio ya se
// usó» y 409 «El premio venció». La profesional opcional debe atender en el
// salón (igual que F8).
func (s LoyaltyService) Redeem(actor domain.Membership, salonID, clientID, rewardID, professionalID, note, clientOpID string) (domain.LoyaltyReward, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	if !puedeVender(m) {
		return domain.LoyaltyReward{}, domain.ErrForbidden
	}
	client, err := s.client(salonID, clientID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	rewardID = strings.TrimSpace(rewardID)
	if rewardID == "" || !loyaltyCleanID(rewardID) {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrNotFound, domain.MsgLoyaltyRewardNotFound)
	}
	note, err = domain.ValidateLoyaltyNote(note)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	opID, err := domain.ValidateLoyaltyOpID(clientOpID)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	professionalID = strings.TrimSpace(professionalID)
	if professionalID != "" {
		if err := s.ensureStaff(salonID, professionalID); err != nil {
			return domain.LoyaltyReward{}, err
		}
	}
	now := s.now()
	reward, err := s.Rewards.Redeem(domain.LoyaltyRedeemRequest{
		SalonID: salonID, ClientID: client.ID, RewardID: rewardID,
		ProfessionalID: professionalID, Note: note, ClientOpID: opID,
		Actor: m.UserID, Now: now,
	})
	if err != nil {
		return domain.LoyaltyReward{}, loyaltyRewardNotFound(err)
	}
	reward.State = reward.StateAt(now)
	return reward, nil
}

// --- Auxiliares -----------------------------------------------------------------

// loyaltyCleanID dice si un id de la ruta o del cuerpo es usable: un NUL o un
// UTF-8 inválido llegaría a Postgres como un 500 (no cabe en un TEXT); es un
// recurso que no existe (la lección de F16).
func loyaltyCleanID(id string) bool {
	return utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

// loyaltyRewardNotFound traduce el ErrNotFound del adaptador a la frase del
// contrato. Cualquier otro error (incluidos los 409 con su texto) se propaga.
func loyaltyRewardNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgLoyaltyRewardNotFound)
	}
	return err
}

// client resuelve la ficha EN ESTE salón: otro salón o inexistente es 404 con
// el texto de las rutas de clientes.
func (s LoyaltyService) client(salonID, clientID string) (domain.Client, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || !loyaltyCleanID(clientID) {
		return domain.Client{}, domain.NewMessage(domain.ErrNotFound, domain.MsgClientNotFound)
	}
	c, err := s.Clients.FindByID(salonID, clientID)
	if err != nil {
		return domain.Client{}, clientError(err)
	}
	return c, nil
}

// activeService exige un servicio del salón y activo; si no, «Servicio inválido»
// (inexistente, inactivo, de otro salón o con un id sucio: el mismo texto).
func (s LoyaltyService) activeService(salonID, serviceID string) (domain.Service, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" || !loyaltyCleanID(serviceID) {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgLoyaltyServiceInvalid)
	}
	svc, err := s.Services.FindByID(salonID, serviceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgLoyaltyServiceInvalid)
		}
		return domain.Service{}, err
	}
	if !svc.Active {
		return domain.Service{}, domain.NewMessage(domain.ErrValidation, domain.MsgLoyaltyServiceInvalid)
	}
	return svc, nil
}

// ensureStaff valida a quien atiende igual que el POS y F8 (ensureProfessional)
// y rechaza un id con NUL o UTF-8 inválido sin tocar la base.
func (s LoyaltyService) ensureStaff(salonID, professionalID string) error {
	if !loyaltyCleanID(professionalID) {
		return domain.NewMessage(domain.ErrValidation, "el profesional no pertenece al salón")
	}
	return ensureProfessional(s.Memberships, salonID, professionalID)
}

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s LoyaltyService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// RequireManager y RequireSeller son el permiso SOLO (sin tocar datos): el handler
// los llama ANTES de leer el cuerpo, así quien no puede hacer la acción recibe
// 403 aunque el cuerpo esté mal formado.
func (s LoyaltyService) RequireManager(actor domain.Membership, salonID string) error {
	_, err := s.manager(actor, salonID)
	return err
}

func (s LoyaltyService) RequireSeller(actor domain.Membership, salonID string) error {
	m, err := s.member(actor, salonID)
	if err != nil {
		return err
	}
	if !puedeVender(m) {
		return domain.ErrForbidden
	}
	return nil
}

// manager exige membership activa y permiso de configuración del salón.
func (s LoyaltyService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
