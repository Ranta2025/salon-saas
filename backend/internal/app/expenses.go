// ExpenseService: los gastos operativos del salón (F3b, D3/D4/D5 del contrato).
// Los permisos son de owner/administración (D5) y las fechas se miden en la zona
// del SALÓN, no en UTC. El adaptador es el que guarda; acá viven las reglas y las
// frases del contrato.
package app

import (
	"errors"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// expenseListLimit es el tope del listado que fija el contrato (500 filas); el
// adaptador pide una más para poder decir `truncated`.
const expenseListLimit = 500

// ExpenseService orquesta el alta, el listado, la edición y el borrado de gastos.
type ExpenseService struct {
	Expenses    ports.ExpenseRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón: «hoy» es el día local, nunca UTC.
	// nil = configuración de fábrica.
	Settings ports.SalonSettingsRepository
	// Now es el reloj de «hoy» (los tests lo fijan). nil = time.Now.
	Now func() time.Time
}

// Create registra un gasto. Solo owner/admin (403 para el resto, D5). Un 400 de
// validación no escribe nada.
func (s ExpenseService) Create(actor domain.Membership, salonID string, f domain.ExpenseFields) (domain.Expense, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.Expense{}, err
	}
	loc, err := salonLocation(s.Settings, salonID)
	if err != nil {
		return domain.Expense{}, err
	}
	fields, err := f.Validate(s.now(), loc)
	if err != nil {
		return domain.Expense{}, err
	}
	e := domain.NewExpense(salonID, fields, actor.UserID, s.now())
	if err := s.Expenses.Save(e); err != nil {
		return domain.Expense{}, err
	}
	return e, nil
}

// List devuelve los gastos del rango (from y to INCLUSIVOS) y si había más de
// 500. El rango es obligatorio, `to` >= `from` y de hasta 366 días.
func (s ExpenseService) List(actor domain.Membership, salonID, from, to string) ([]domain.Expense, bool, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return nil, false, err
	}
	fromDate, toDate, err := domain.ExpenseRange(from, to)
	if err != nil {
		return nil, false, err
	}
	list, truncated, err := s.Expenses.ListRange(salonID, fromDate, toDate, expenseListLimit)
	if err != nil {
		return nil, false, err
	}
	if list == nil {
		list = []domain.Expense{} // el frontend recibe [] y no null
	}
	return list, truncated, nil
}

// Update REEMPLAZA los cuatro campos de un gasto. Orden de las reglas (lección de
// F3.2): (1) permisos, (2) el gasto existe en ESTE salón (404), (3) validación
// (400, no escribe nada). Un gasto de otro salón es 404 «Gasto no encontrado».
func (s ExpenseService) Update(actor domain.Membership, salonID, expenseID string, f domain.ExpenseFields) (domain.Expense, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return domain.Expense{}, err
	}
	current, err := s.Expenses.FindByID(salonID, expenseID)
	if err != nil {
		return domain.Expense{}, expenseNotFound(err)
	}
	loc, err := salonLocation(s.Settings, salonID)
	if err != nil {
		return domain.Expense{}, err
	}
	fields, err := f.Validate(s.now(), loc)
	if err != nil {
		return domain.Expense{}, err
	}
	out := current.WithFields(fields)
	if err := s.Expenses.Update(out); err != nil {
		return domain.Expense{}, expenseNotFound(err)
	}
	return out, nil
}

// Delete borra un gasto del salón (D4: DELETE físico). Inexistente o de otro
// salón es 404 con la frase del contrato.
func (s ExpenseService) Delete(actor domain.Membership, salonID, expenseID string) error {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return err
	}
	return expenseNotFound(s.Expenses.Delete(salonID, expenseID))
}

// expenseNotFound traduce el ErrNotFound del adaptador a la frase del contrato.
// Cualquier otro error se propaga sin disfrazarse.
func expenseNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgExpenseNotFound)
	}
	return err
}

// salonLocation resuelve la zona horaria del salón con los defaults de fábrica
// cuando no hay fila de configuración. Es la MISMA lectura que usa la nómina y
// el reporte de ganancia: no hay una segunda regla de «hoy».
func salonLocation(repo ports.SalonSettingsRepository, salonID string) (*time.Location, error) {
	settings, err := loadSalonSettings(repo, salonID)
	if err != nil {
		return nil, err
	}
	return settings.Location()
}

// now es el reloj del caso de uso.
func (s ExpenseService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
