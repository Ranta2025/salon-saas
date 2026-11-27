package testutil

import (
	"sort"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type FakePackageCatalog struct {
	All      []domain.ServicePackage
	Services ServiceLookup
}

// ServiceLookup es lo que necesita el catálogo de paquetes para completar el
// nombre del servicio (lo cumplen FakeServices y los fakes de cada paquete).
type ServiceLookup interface {
	FindByID(salonID, id string) (domain.Service, error)
}

func (f *FakePackageCatalog) nameTaken(salonID, name, ignoreID string) bool {
	for _, p := range f.All {
		if p.SalonID == salonID && p.Active && p.ID != ignoreID && strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func (f *FakePackageCatalog) Create(p domain.ServicePackage) error {
	if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
		return domain.ErrAlreadyExists
	}
	f.All = append(f.All, p)
	return nil
}

func (f *FakePackageCatalog) Save(p domain.ServicePackage) error {
	for i := range f.All {
		if f.All[i].ID == p.ID && f.All[i].SalonID == p.SalonID {
			if p.Active && f.nameTaken(p.SalonID, p.Name, p.ID) {
				return domain.ErrAlreadyExists
			}
			f.All[i] = p
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *FakePackageCatalog) FindByID(salonID, id string) (domain.ServicePackage, error) {
	for _, p := range f.All {
		if p.SalonID == salonID && p.ID == id {
			return p, nil
		}
	}
	return domain.ServicePackage{}, domain.ErrNotFound
}

func (f *FakePackageCatalog) List(salonID string) ([]domain.ServicePackage, error) {
	out := make([]domain.ServicePackage, 0, len(f.All))
	for _, p := range f.All {
		if p.SalonID != salonID {
			continue
		}
		if svc, err := f.Services.FindByID(salonID, p.ServiceID); err == nil {
			p.ServiceName = svc.Name
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		li, lj := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if li != lj {
			return li < lj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

var _ ports.PackageRepository = (*FakePackageCatalog)(nil)
