// ClientHistoryService: el historial de una clienta (F5, D6/D7). Reglas:
// cualquier miembro activo ve las visitas y los turnos; el DINERO (gasto y
// ventas) solo quien puede ver la plata (owner/admin/recepción). El resumen con
// el día local lo arma el dominio; acá solo se decide qué se muestra y qué no.
package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ClientHistoryLimit es el tope de filas de CADA lista del historial (D6): los
// turnos recientes y las ventas recientes, más reciente primero.
const ClientHistoryLimit = 20

type ClientHistoryService struct {
	Clients ports.ClientRepository
	// Source es la lectura del historial (una sola transacción, D6).
	Source      ports.ClientHistoryRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón (last_visit es el día LOCAL, D7).
	Settings ports.SalonSettingsRepository
	// Now es el reloj de «próximo turno» (nil = time.Now).
	Now func() time.Time
}

// ClientHistoryView es lo que el handler serializa. Spend y Sales van en nil
// cuando el actor no puede ver la plata: el JSON del contrato manda null, no 0.
type ClientHistoryView struct {
	ClientID string
	Summary  domain.ClientHistorySummary
	Bookings []domain.ClientHistoryBooking
	Spend    *domain.ClientSpend
	Sales    []domain.ClientHistorySale
}

// History arma la vista del historial de UNA ficha del salón.
//
// Orden de las reglas: (1) membership activa del salón, (2) la ficha existe EN
// ESTE salón (404 «Ficha no encontrada»), (3) permiso de dinero según el rol,
// (4) la lectura en una sola transacción y el resumen con la zona del salón.
func (s ClientHistoryService) History(actor domain.Membership, salonID, clientID string) (ClientHistoryView, error) {
	if actor.SalonID != salonID {
		return ClientHistoryView{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return ClientHistoryView{}, domain.ErrForbidden
	}
	if _, err := s.Clients.FindByID(salonID, clientID); err != nil {
		return ClientHistoryView{}, ClientError(err)
	}
	loc, err := SalonLocation(s.Settings, salonID)
	if err != nil {
		return ClientHistoryView{}, err
	}
	data, err := s.Source.History(salonID, clientID, s.now(), ClientHistoryLimit)
	if err != nil {
		return ClientHistoryView{}, err
	}
	view := ClientHistoryView{
		ClientID: clientID,
		Summary:  domain.NewClientHistorySummary(data, loc),
		Bookings: data.Bookings,
		Spend:    &data.Spend,
		Sales:    data.Sales,
	}
	// El profesional ve visitas y turnos, nunca la facturación de la clienta.
	if !puedeVerPlata(m) {
		view.Spend = nil
		view.Sales = nil
	}
	return view, nil
}

// now es el reloj del caso de uso.
func (s ClientHistoryService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}
