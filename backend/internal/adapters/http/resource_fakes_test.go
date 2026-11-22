package httpapi

import (
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// mresources: tipos y unidades de equipo con la misma semántica que pgstore
// (scope por salón, ErrNotFound, unicidad case-insensitive). La ocupación sale
// del fake de turnos para no duplicar estado.
type mresources struct {
	types    []domain.ResourceType
	units    []domain.ResourceUnit
	bookings *mb
}

func (f *mresources) CreateType(t domain.ResourceType, units []domain.ResourceUnit) error {
	if f.nameTaken(t.SalonID, t.Name, "") {
		return domain.ErrAlreadyExists
	}
	f.types = append(f.types, t)
	f.units = append(f.units, units...)
	return nil
}

func (f *mresources) SaveType(t domain.ResourceType) error {
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

func (f *mresources) DeleteType(salonID, id string) error {
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
			if f.unitHasBookings(salonID, u.ID) {
				return domain.ErrAlreadyExists
			}
		}
		f.types = append(f.types[:i], f.types[i+1:]...)
		f.units = kept
		return nil
	}
	return domain.ErrNotFound
}

func (f *mresources) FindType(salonID, id string) (domain.ResourceType, error) {
	for _, t := range f.types {
		if t.ID == id && t.SalonID == salonID {
			return t, nil
		}
	}
	return domain.ResourceType{}, domain.ErrNotFound
}

func (f *mresources) ListTypes(salonID string) ([]domain.ResourceType, error) {
	out := make([]domain.ResourceType, 0, len(f.types))
	for _, t := range f.types {
		if t.SalonID == salonID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *mresources) SaveUnit(u domain.ResourceUnit) error {
	for _, other := range f.units {
		if other.SalonID == u.SalonID && other.TypeID == u.TypeID && other.ID != u.ID &&
			strings.EqualFold(other.Name, u.Name) {
			return domain.ErrAlreadyExists
		}
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

func (f *mresources) FindUnit(salonID, id string) (domain.ResourceUnit, error) {
	for _, u := range f.units {
		if u.ID == id && u.SalonID == salonID {
			return u, nil
		}
	}
	return domain.ResourceUnit{}, domain.ErrNotFound
}

func (f *mresources) ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	out := make([]domain.ResourceUnit, 0, len(f.units))
	for _, u := range f.units {
		if u.SalonID == salonID && u.TypeID == typeID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *mresources) ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
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

func (f *mresources) UnitsWithBookings(salonID, typeID string) (int, error) {
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

func (f *mresources) UnitFutureBookings(salonID, unitID string, now time.Time) (int, error) {
	n := 0
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked && b.StartsAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (f *mresources) OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error) {
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

func (f *mresources) bookingList() []domain.Booking {
	if f.bookings == nil {
		return nil
	}
	return f.bookings.all
}

func (f *mresources) unitHasBookings(salonID, unitID string) bool {
	for _, b := range f.bookingList() {
		if b.SalonID == salonID && b.ResourceUnitID == unitID && b.Status == domain.BookingBooked {
			return true
		}
	}
	return false
}

func (f *mresources) nameTaken(salonID, name, ignoreID string) bool {
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

var _ ports.ResourceRepository = (*mresources)(nil)
