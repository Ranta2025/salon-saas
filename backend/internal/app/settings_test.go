package app

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

// T6: GET/PUT /salons/:id/settings. Cualquier miembro lee; owner/admin edita.
func TestSettingsService(t *testing.T) {
	mems := &fakeMemberships{}
	svc := SettingsService{Settings: &fakeSettings{}, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)

	// Sin fila en la DB: la default de fábrica (un salón viejo no se rompe).
	got, err := svc.Get(owner, "s1")
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got.SalonID != "s1" || got.Timezone != domain.DefaultTimezone ||
		got.OpenTime != domain.DefaultOpenTime || got.CloseTime != domain.DefaultCloseTime ||
		got.SlotStepMin != domain.DefaultSlotStepMin {
		t.Fatalf("defaults: %+v", got)
	}

	// El profesional lee, pero no edita.
	if _, err := svc.Get(profe, "s1"); err != nil {
		t.Fatalf("profesional debe leer: %v", err)
	}
	if _, err := svc.Update(profe, "s1", got); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional editando debe dar Forbidden, dio %v", err)
	}

	// Owner edita (y se persiste).
	newSettings, err := svc.Update(owner, "s1", domain.SalonSettings{
		SalonID: "s1", Timezone: "America/Bogota", OpenTime: "08:00", CloseTime: "17:00", SlotStepMin: 20,
	})
	if err != nil {
		t.Fatalf("owner edita: %v", err)
	}
	if newSettings.Timezone != "America/Bogota" || newSettings.SlotStepMin != 20 {
		t.Fatalf("no aplicó: %+v", newSettings)
	}
	got, _ = svc.Get(owner, "s1")
	if got.Timezone != "America/Bogota" || got.OpenTime != "08:00" || got.CloseTime != "17:00" {
		t.Fatalf("no persistió: %+v", got)
	}

	// Validación: no pasa al repo si es inválida.
	if _, err := svc.Update(owner, "s1", domain.SalonSettings{SalonID: "s1", Timezone: "no/existe", OpenTime: "08:00", CloseTime: "17:00", SlotStepMin: 20}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("zona inválida: %v", err)
	}
	// Admin también edita.
	admin := miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	if _, err := svc.Update(admin, "s1", got); err != nil {
		t.Fatalf("admin edita: %v", err)
	}
	// Ajeno al salón: 403 en lectura y escritura (no filtra configuración).
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := svc.Get(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro leyendo: %v", err)
	}
	if _, err := svc.Update(ajeno, "s1", got); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro escribiendo: %v", err)
	}
	// Rol viejo en el actor: manda la DB (membership real), no el actor.
	viejo := domain.Membership{UserID: "u-ex", SalonID: "s1", Role: domain.RoleOwner}
	if _, err := svc.Update(viejo, "s1", got); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("actor sin membership real: %v", err)
	}
}
