package schedules

// PRESERVE: el listado de ausencias y sus conflictos siguen igual (F2d2), ahora
// que el filtro de turnos que se pisan vive en un helper compartido.

import (
	"testing"

	"salon-saas/internal/domain"
)

// PRESERVE: el listado de ausencias y sus conflictos siguen igual (F2d2), ahora
// que el filtro de turnos que se pisan vive en un helper compartido.
func TestCoverage_preserve_time_off_list_and_conflicts(t *testing.T) {
	env := newTimeOffEnv(t)
	conflict := domain.Booking{
		ID: "b-1", SalonID: "s1", ProfessionalID: env.pro1.UserID,
		StartsAt: at(3, 10, 0), EndsAt: at(3, 11, 0), Status: domain.BookingBooked,
	}
	env.bookings.All = append(env.bookings.All,
		conflict,
		domain.Booking{ID: "b-2", SalonID: "s1", ProfessionalID: env.pro2.UserID,
			StartsAt: at(3, 10, 0), EndsAt: at(3, 11, 0), Status: domain.BookingBooked},
	)
	res, err := env.svc.Create(env.owner, "s1", env.pro1.UserID, at(3, 9, 0), at(3, 12, 0), "Médico")
	if err != nil {
		t.Fatalf("alta de ausencia: %v", err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].ID != "b-1" {
		t.Fatalf("conflictos: %+v (solo los de ESA persona)", res.Conflicts)
	}
	list, err := env.svc.List(env.owner, "s1", at(3, 0, 0), at(4, 0, 0))
	if err != nil || len(list) != 1 || list[0].Reason != "Médico" {
		t.Fatalf("listado: %+v %v", list, err)
	}
}
