package settings_test

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/settings"
	"salon-saas/internal/testutil"
)

type degradeSalons struct{ ports.SalonRepository }

func (degradeSalons) FindBySlug(string) (domain.Salon, error) {
	return domain.Salon{ID: "s1", Name: "Aurelia", Slug: "aurelia"}, nil
}

type degradeServices struct{ ports.ServiceRepository }

func (degradeServices) List(string, bool) ([]domain.Service, error) { return nil, nil }

type brokenLoyalty struct{ ports.LoyaltyProgramRepository }

func (brokenLoyalty) Find(string) (domain.LoyaltyProgram, error) {
	return domain.LoyaltyProgram{}, errors.New("relation loyalty_programs does not exist")
}

// F9 (R4-001): the loyalty section is purely additive. If its query fails (for
// example a rolling deploy before migration 033), the public page must still
// answer with loyalty:null instead of a 500.
func TestPublicSalon_loyaltyFailureDegradesToNull(t *testing.T) {
	svc := settings.PublicSalonService{Salons: degradeSalons{}, Services: degradeServices{}, Loyalty: brokenLoyalty{}}
	got, err := svc.Public("aurelia")
	if err != nil {
		t.Fatalf("a failing loyalty query must not fail the public page: %v", err)
	}
	if got.Loyalty != nil {
		t.Fatalf("loyalty must be nil, got %+v", got.Loyalty)
	}
}

func TestPublicSalonService(t *testing.T) {
	salons := &testutil.FakeSalons{}
	services := &testutil.FakeServices{}
	svc := settings.PublicSalonService{Salons: salons, Services: services}
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "navajas", Name: "Navajas de Oro"})
	activo, _ := domain.NewService("s1", "Corte", 45, 150000, 20)
	_ = services.Save(activo)
	inactivo, _ := domain.NewService("s1", "Viejo", 30, 1000, 0)
	inactivo, _ = inactivo.WithDetails(inactivo.Name, inactivo.DurationMin, inactivo.PriceCents, inactivo.CommissionPct, false)
	_ = services.Save(inactivo)

	got, err := svc.Public("navajas")
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if got.Salon.Name != "Navajas de Oro" || len(got.Services) != 1 || got.Services[0].Name != "Corte" {
		t.Fatalf("landing: %+v", got)
	}
	if _, err := svc.Public("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente debe dar ErrNotFound, dio %v", err)
	}
}
