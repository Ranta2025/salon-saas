package app

import (
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Dobles compartidos por los casos de uso que todavía viven en este paquete:
// el bus de eventos y el notificador. Salieron de notifications_test.go cuando
// los avisos pasaron a internal/notifications (los _test.go no se importan
// entre paquetes).
// fakeEvents guarda lo publicado: los casos de uso no saben que hay websockets.
type fakeEvents struct{ all []ports.Event }

func (f *fakeEvents) Publish(ev ports.Event) error { f.all = append(f.all, ev); return nil }

// typed cuenta los eventos de un tipo y devuelve el último.
func (f *fakeEvents) typed(t string) (ports.Event, int) {
	var last ports.Event
	n := 0
	for _, ev := range f.all {
		if ev.Type == t {
			last, n = ev, n+1
		}
	}
	return last, n
}

var _ ports.EventPublisher = (*fakeEvents)(nil)

// fakeNotifier registra los avisos que los casos de uso piden crear.
type fakeNotifier struct {
	calls  int
	bodies []string
}

func (f *fakeNotifier) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	f.calls++
	f.bodies = append(f.bodies, body)
	return domain.NewNotification(salonID, userID, title, body, kind)
}

// lastBody es el texto del último aviso: los tests de la hora local miran el
// texto que ve la persona, no el evento.
func (f *fakeNotifier) lastBody() string {
	if len(f.bodies) == 0 {
		return ""
	}
	return f.bodies[len(f.bodies)-1]
}

var _ Notifier = (*fakeNotifier)(nil)

// fakeResources: el equipamiento con capacidad (copia para los casos de uso que
// todavía viven acá; internal/staffing tiene la suya).
type fakeResources struct {
	types    []domain.ResourceType
	units    []domain.ResourceUnit
	bookings *fb
}

func (f *fakeResources) CreateType(t domain.ResourceType, units []domain.ResourceUnit) error {
	if f.nameTaken(t.SalonID, t.Name, "") {
		return domain.ErrAlreadyExists
	}
	f.types = append(f.types, t)
	f.units = append(f.units, units...)
	return nil
}

func (f *fakeResources) SaveType(t domain.ResourceType) error {
	for i := range f.types {
		if f.types[i].ID != t.ID || f.types[i].SalonID != t.SalonID {
			continue
		}
		if f.nameTaken(t.SalonID, t.Name, t.ID) {
			return domain.ErrAlreadyExists
		}
		f.types[i] = t
		return nil
	}
	return domain.ErrNotFound
}

// DeleteType borra el tipo y sus unidades en una transacción, con la misma
// semántica que la FK ON DELETE RESTRICT: una unidad con turnos frena el borrado.
func (f *fakeResources) DeleteType(salonID, id string) error {
	for i := range f.types {
		if f.types[i].ID != id || f.types[i].SalonID != salonID {
			continue
		}
		kept := make([]domain.ResourceUnit, 0, len(f.units))
		for _, u := range f.units {
			if u.TypeID != id || u.SalonID != salonID {
				kept = append(kept, u)
				continue
			}
			if f.unitHasBookings(salonID, u.ID, false) {
				return domain.ErrAlreadyExists // FK de bookings -> resource_units
			}
		}
		f.types = append(f.types[:i], f.types[i+1:]...)
		f.units = kept
		return nil
	}
	return domain.ErrNotFound
}

func (f *fakeResources) FindType(salonID, id string) (domain.ResourceType, error) {
	for _, t := range f.types {
		if t.ID == id && t.SalonID == salonID {
			return t, nil
		}
	}
	return domain.ResourceType{}, domain.ErrNotFound
}

func (f *fakeResources) ListTypes(salonID string) ([]domain.ResourceType, error) {
	out := make([]domain.ResourceType, 0, len(f.types))
	for _, t := range f.types {
		if t.SalonID == salonID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeResources) SaveUnit(u domain.ResourceUnit) error {
	if f.unitNameTaken(u.SalonID, u.TypeID, u.Name, u.ID) {
		return domain.ErrAlreadyExists
	}
	for i := range f.units {
		if f.units[i].ID == u.ID && f.units[i].SalonID == u.SalonID {
			f.units[i] = u
			return nil
		}
	}
	f.units = append(f.units, u)
	return nil
}

func (f *fakeResources) FindUnit(salonID, id string) (domain.ResourceUnit, error) {
	for _, u := range f.units {
		if u.ID == id && u.SalonID == salonID {
			return u, nil
		}
	}
	return domain.ResourceUnit{}, domain.ErrNotFound
}

func (f *fakeResources) ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	out := make([]domain.ResourceUnit, 0, len(f.units))
	for _, u := range f.units {
		if u.SalonID == salonID && u.TypeID == typeID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeResources) ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	all, err := f.ListUnits(salonID, typeID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ResourceUnit, 0, len(all))
	for _, u := range all {
		if u.Active {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeResources) UnitsWithBookings(salonID, typeID string) (int, error) {
	units, err := f.ListUnits(salonID, typeID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range units {
		if f.unitHasBookings(salonID, u.ID, false) {
			n++
		}
	}
	return n, nil
}

func (f *fakeResources) UnitFutureBookings(salonID, unitID string, now time.Time) (int, error) {
	n := 0
	for _, b := range f.bookingList() {
		if b.SalonID != salonID || b.ResourceUnitID != unitID || b.Status != domain.BookingBooked {
			continue
		}
		if b.StartsAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (f *fakeResources) OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error) {
	var out []domain.UnitOccupancy
	for _, b := range f.bookingList() {
		if b.SalonID != salonID || b.ResourceUnitID == "" || b.Status != domain.BookingBooked || b.ID == excludeBookingID {
			continue
		}
		if b.StartsAt.Before(to) && from.Before(b.EndsAt) {
			out = append(out, domain.UnitOccupancy{UnitID: b.ResourceUnitID, StartsAt: b.StartsAt, EndsAt: b.EndsAt})
		}
	}
	return out, nil
}

func (f *fakeResources) bookingList() []domain.Booking {
	if f.bookings == nil {
		return nil
	}
	return f.bookings.all
}

func (f *fakeResources) unitHasBookings(salonID, unitID string, futureOnly bool) bool {
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked {
			return true
		}
	}
	return false
}

func (f *fakeResources) nameTaken(salonID, name, ignoreID string) bool {
	for _, t := range f.types {
		if t.SalonID != salonID || t.ID == ignoreID {
			continue
		}
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

func (f *fakeResources) unitNameTaken(salonID, typeID, name, ignoreID string) bool {
	for _, u := range f.units {
		if u.SalonID != salonID || u.TypeID != typeID || u.ID == ignoreID {
			continue
		}
		if strings.EqualFold(u.Name, name) {
			return true
		}
	}
	return false
}

var _ ports.ResourceRepository = (*fakeResources)(nil)
