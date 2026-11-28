package testutil

import (
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeResources es el equipamiento con capacidad: la copia exportada del fake
// de los tests de app (internal/staffing tiene la suya).
type FakeResources struct {
	Types    []domain.ResourceType
	Units    []domain.ResourceUnit
	Bookings *FakeBookings
}

func (f *FakeResources) CreateType(t domain.ResourceType, units []domain.ResourceUnit) error {
	if f.nameTaken(t.SalonID, t.Name, "") {
		return domain.ErrAlreadyExists
	}
	f.Types = append(f.Types, t)
	f.Units = append(f.Units, units...)
	return nil
}

func (f *FakeResources) SaveType(t domain.ResourceType) error {
	for i := range f.Types {
		if f.Types[i].ID != t.ID || f.Types[i].SalonID != t.SalonID {
			continue
		}
		if f.nameTaken(t.SalonID, t.Name, t.ID) {
			return domain.ErrAlreadyExists
		}
		f.Types[i] = t
		return nil
	}
	return domain.ErrNotFound
}

// DeleteType borra el tipo y sus unidades en una transacción, con la misma
// semántica que la FK ON DELETE RESTRICT: una unidad con turnos frena el borrado.
func (f *FakeResources) DeleteType(salonID, id string) error {
	for i := range f.Types {
		if f.Types[i].ID != id || f.Types[i].SalonID != salonID {
			continue
		}
		kept := make([]domain.ResourceUnit, 0, len(f.Units))
		for _, u := range f.Units {
			if u.TypeID != id || u.SalonID != salonID {
				kept = append(kept, u)
				continue
			}
			if f.unitHasBookings(salonID, u.ID, false) {
				return domain.ErrAlreadyExists // FK de bookings -> resource_units
			}
		}
		f.Types = append(f.Types[:i], f.Types[i+1:]...)
		f.Units = kept
		return nil
	}
	return domain.ErrNotFound
}

func (f *FakeResources) FindType(salonID, id string) (domain.ResourceType, error) {
	for _, t := range f.Types {
		if t.ID == id && t.SalonID == salonID {
			return t, nil
		}
	}
	return domain.ResourceType{}, domain.ErrNotFound
}

func (f *FakeResources) ListTypes(salonID string) ([]domain.ResourceType, error) {
	out := make([]domain.ResourceType, 0, len(f.Types))
	for _, t := range f.Types {
		if t.SalonID == salonID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *FakeResources) SaveUnit(u domain.ResourceUnit) error {
	if f.unitNameTaken(u.SalonID, u.TypeID, u.Name, u.ID) {
		return domain.ErrAlreadyExists
	}
	for i := range f.Units {
		if f.Units[i].ID == u.ID && f.Units[i].SalonID == u.SalonID {
			f.Units[i] = u
			return nil
		}
	}
	f.Units = append(f.Units, u)
	return nil
}

func (f *FakeResources) FindUnit(salonID, id string) (domain.ResourceUnit, error) {
	for _, u := range f.Units {
		if u.ID == id && u.SalonID == salonID {
			return u, nil
		}
	}
	return domain.ResourceUnit{}, domain.ErrNotFound
}

func (f *FakeResources) ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	out := make([]domain.ResourceUnit, 0, len(f.Units))
	for _, u := range f.Units {
		if u.SalonID == salonID && u.TypeID == typeID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *FakeResources) ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
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

func (f *FakeResources) UnitsWithBookings(salonID, typeID string) (int, error) {
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

func (f *FakeResources) UnitFutureBookings(salonID, unitID string, now time.Time) (int, error) {
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

func (f *FakeResources) OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error) {
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

func (f *FakeResources) bookingList() []domain.Booking {
	if f.Bookings == nil {
		return nil
	}
	return f.Bookings.All
}

func (f *FakeResources) unitHasBookings(salonID, unitID string, futureOnly bool) bool {
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked {
			return true
		}
	}
	return false
}

func (f *FakeResources) nameTaken(salonID, name, ignoreID string) bool {
	for _, t := range f.Types {
		if t.SalonID != salonID || t.ID == ignoreID {
			continue
		}
		if strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

func (f *FakeResources) unitNameTaken(salonID, typeID, name, ignoreID string) bool {
	for _, u := range f.Units {
		if u.SalonID != salonID || u.TypeID != typeID || u.ID == ignoreID {
			continue
		}
		if strings.EqualFold(u.Name, name) {
			return true
		}
	}
	return false
}

var _ ports.ResourceRepository = (*FakeResources)(nil)
