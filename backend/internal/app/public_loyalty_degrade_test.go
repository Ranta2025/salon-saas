package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
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
	svc := PublicSalonService{Salons: degradeSalons{}, Services: degradeServices{}, Loyalty: brokenLoyalty{}}
	got, err := svc.Public("aurelia")
	if err != nil {
		t.Fatalf("a failing loyalty query must not fail the public page: %v", err)
	}
	if got.Loyalty != nil {
		t.Fatalf("loyalty must be nil, got %+v", got.Loyalty)
	}
}
