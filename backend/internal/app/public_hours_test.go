package app

import (
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// S-F2d1-4: la vidriera pública devuelve el horario ya expandido, solo los días
// abiertos. Un salón sin horario propio responde los 7 días de la ventana única
// (lo que la landing mostraba como texto indicativo).
func TestPublicSalon_hours(t *testing.T) {
	salon := domain.Salon{ID: "s1", Slug: "navajas-oro", Name: "Navajas de Oro"}
	salons := &fakeSalons{bySlug: map[string]domain.Salon{"navajas-oro": salon}}
	settings := &fakeSettings{bySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	hours := &testutil.FakeWeeklyHours{}
	svc := PublicSalonService{Salons: salons, Services: &fakeServices{}, Settings: settings, Hours: hours}

	p, err := svc.Public("navajas-oro")
	if err != nil {
		t.Fatalf("vidriera: %v", err)
	}
	if len(p.Hours) != 7 {
		t.Fatalf("sin horario propio son 7 días: %+v", p.Hours)
	}
	for i, d := range p.Hours {
		if d.Weekday != i+1 || d.Open != "09:00" || d.Close != "19:00" {
			t.Fatalf("día %d: %+v", i+1, d)
		}
	}

	// Con horario propio, solo los días abiertos y en su ventana.
	if err := hours.SaveSalonHours("s1", []domain.WeekdayHours{
		{Weekday: 1, Open: "10:00", Close: "14:00"},
		{Weekday: 3, Open: "09:00", Close: "18:00"},
	}); err != nil {
		t.Fatalf("horario: %v", err)
	}
	p, err = svc.Public("navajas-oro")
	if err != nil {
		t.Fatalf("vidriera: %v", err)
	}
	if len(p.Hours) != 2 || p.Hours[0] != (domain.WeekdayHours{Weekday: 1, Open: "10:00", Close: "14:00"}) ||
		p.Hours[1] != (domain.WeekdayHours{Weekday: 3, Open: "09:00", Close: "18:00"}) {
		t.Fatalf("días abiertos: %+v", p.Hours)
	}
}
