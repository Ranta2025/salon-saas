// PayModelService: el modelo de pago de cada miembro (D3 del contrato F3): sueldo
// fijo por período, comisión o mixto. Solo owner/admin lo ven y lo cambian; el
// profesional no ve el de nadie (D7).
package payroll

import (
	"errors"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

type PayModelService struct {
	Pay         ports.MemberPayRepository
	Memberships ports.MembershipRepository
}

// List devuelve el modelo de todos los miembros del salón (activos y dados de
// baja; sin fila, el default de D3: solo comisión). Solo owner/admin: el sueldo
// de cada quien es información sensible del negocio (D7).
func (s PayModelService) List(actor domain.Membership, salonID string) ([]domain.MemberPayView, error) {
	if _, err := kernel.ManageActor(s.Memberships, actor, salonID); err != nil {
		return nil, err
	}
	list, err := s.Pay.ListPayModels(salonID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.MemberPayView{} // el frontend recibe [] y no null
	}
	return list, nil
}

// Update valida y guarda el modelo de UNA persona del salón.
//
// Orden de las reglas: (1) permisos, (2) existencia de la persona en ESTE salón
// (404, sin distinguir "no existe" de "es de otro salón"), (3) validación del
// modelo (400 en español). La validación va después de la existencia para que un
// pedido inválido sobre alguien ajeno no confirme que el salón lo conoce.
func (s PayModelService) Update(actor domain.Membership, salonID, userID string, fixedCents int, period string, commissionEnabled bool) (domain.MemberPayView, error) {
	if _, err := kernel.ManageActor(s.Memberships, actor, salonID); err != nil {
		return domain.MemberPayView{}, err
	}
	view, err := s.Pay.FindPayModel(salonID, userID)
	if err != nil {
		// Un miembro de otro tenant no existe para este salón: 404. Cualquier
		// otra falla se propaga (una caída de la DB no se disfraza de "no está").
		if errors.Is(err, domain.ErrNotFound) {
			return domain.MemberPayView{}, domain.ErrNotFound
		}
		return domain.MemberPayView{}, err
	}
	model, err := domain.NewMemberPay(salonID, userID, fixedCents, period, commissionEnabled)
	if err != nil {
		return domain.MemberPayView{}, err
	}
	if err := s.Pay.SavePayModel(model); err != nil {
		return domain.MemberPayView{}, err
	}
	// La respuesta es el mismo objeto del listado, con los valores recién
	// guardados: el frontend no tiene que releer para pintar la tarjeta.
	view.MemberPay = model
	return view, nil
}
