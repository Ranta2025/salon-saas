package bookings_test

import (
	"errors"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

func TestBookingService_profesional_debe_ser_del_salon(t *testing.T) {
	mems := &testutil.FakeMemberships{}
	svc := bkg.BookingService{Bookings: &testutil.FakeBookings{}, Memberships: mems}
	rec := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	start := time.Now().UTC().Add(24 * time.Hour)

	if _, err := svc.CreateBooking(rec, "s1", "fantasma", "Lía", "", start, start.Add(time.Hour)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional inexistente debe dar ErrValidation, dio %v", err)
	}
	testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.CreateBooking(rec, "s1", "u-prof", "Lía", "", start, start.Add(time.Hour)); err != nil {
		t.Fatalf("profesional del salón debe poder agendarse: %v", err)
	}
}

// --- Endpoints nuevos: /me, /staff, /public/salons/:slug ---
