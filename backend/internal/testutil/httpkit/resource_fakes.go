package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"strings"
	"time"
)

// Resources: tipos y unidades de equipo con la misma semántica que pgstore
// (scope por salón, ErrNotFound, unicidad case-insensitive). La ocupación sale
// del fake de turnos para no duplicar estado.
type Resources struct {
	Types    []domain.ResourceType
	Units    []domain.ResourceUnit
	Bookings *Bookings
}

func (f *Resources) CreateType(t domain.ResourceType, units []domain.ResourceUnit) error {
	if f.nameTaken(t.SalonID, t.Name, "") {
		return domain.ErrAlreadyExists
	}
	f.Types = append(f.Types, t)
	f.Units = append(f.Units, units...)
	return nil
}

func (f *Resources) SaveType(t domain.ResourceType) error {
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

func (f *Resources) DeleteType(salonID, id string) error {
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
			if f.unitHasBookings(salonID, u.ID) {
				return domain.ErrAlreadyExists
			}
		}
		f.Types = append(f.Types[:i], f.Types[i+1:]...)
		f.Units = kept
		return nil
	}
	return domain.ErrNotFound
}

func (f *Resources) FindType(salonID, id string) (domain.ResourceType, error) {
	for _, t := range f.Types {
		if t.ID == id && t.SalonID == salonID {
			return t, nil
		}
	}
	return domain.ResourceType{}, domain.ErrNotFound
}

func (f *Resources) ListTypes(salonID string) ([]domain.ResourceType, error) {
	out := make([]domain.ResourceType, 0, len(f.Types))
	for _, t := range f.Types {
		if t.SalonID == salonID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *Resources) SaveUnit(u domain.ResourceUnit) error {
	for _, other := range f.Units {
		if other.SalonID == u.SalonID && other.TypeID == u.TypeID && other.ID != u.ID &&
			strings.EqualFold(other.Name, u.Name) {
			return domain.ErrAlreadyExists
		}
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

func (f *Resources) FindUnit(salonID, id string) (domain.ResourceUnit, error) {
	for _, u := range f.Units {
		if u.ID == id && u.SalonID == salonID {
			return u, nil
		}
	}
	return domain.ResourceUnit{}, domain.ErrNotFound
}

func (f *Resources) ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	out := make([]domain.ResourceUnit, 0, len(f.Units))
	for _, u := range f.Units {
		if u.SalonID == salonID && u.TypeID == typeID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *Resources) ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
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

func (f *Resources) UnitsWithBookings(salonID, typeID string) (int, error) {
	units, err := f.ListUnits(salonID, typeID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range units {
		if f.unitHasBookings(salonID, u.ID) {
			n++
		}
	}
	return n, nil
}

func (f *Resources) UnitFutureBookings(salonID, unitID string, now time.Time) (int, error) {
	n := 0
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked && b.StartsAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (f *Resources) OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error) {
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

func (f *Resources) bookingList() []domain.Booking {
	if f.Bookings == nil {
		return nil
	}
	return f.Bookings.All
}

func (f *Resources) unitHasBookings(salonID, unitID string) bool {
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked {
			return true
		}
	}
	return false
}

func (f *Resources) nameTaken(salonID, name, ignoreID string) bool {
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

var _ ports.ResourceRepository = (*Resources)(nil)
