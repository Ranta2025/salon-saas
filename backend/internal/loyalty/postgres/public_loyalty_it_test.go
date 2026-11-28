// Integración con Postgres real del programa de fidelidad en la vidriera pública
// (F9, D8). Se saltea sin SALON_IT=1.
package postgres

import (
	"testing"

	pgstore "salon-saas/internal/adapters/postgres"
	schedulespostgres "salon-saas/internal/schedules/postgres"
	"salon-saas/internal/settings"
	staffingpostgres "salon-saas/internal/staffing/postgres"
)

// D8: `loyalty` sale SOLO si el programa está activado Y su servicio sigue
// activo; el resto del payload no cambia. Aislamiento: el programa de otro
// salón nunca aparece.
func TestPublicSalon_loyaltyOnlyWhenEnabledAndServiceActive(t *testing.T) {
	f := loyFixtures(t)
	pub := settings.PublicSalonService{
		Salons: pgstore.Salons{Pool: f.pool}, Services: staffingpostgres.Services{Pool: f.pool}, Settings: pgstore.Salons{Pool: f.pool},
		Hours: schedulespostgres.WeeklyHours{Pool: f.pool}, Loyalty: f.programs,
	}
	loyalty := func(slug string) *settings.PublicLoyalty {
		t.Helper()
		got, err := pub.Public(slug)
		if err != nil {
			t.Fatalf("vidriera %s: %v", slug, err)
		}
		return got.Loyalty
	}

	// Nunca guardado: null.
	if l := loyalty(loySalon); l != nil {
		t.Fatalf("sin programa: %+v", l)
	}
	// Apagado: null.
	f.saveProgram(t, false, 5, nil)
	if l := loyalty(loySalon); l != nil {
		t.Fatalf("apagado: %+v", l)
	}
	// Encendido con servicio activo: se muestra.
	f.saveProgram(t, true, 5, nil)
	l := loyalty(loySalon)
	if l == nil || l.VisitsRequired != 5 || l.RewardServiceName != "Corte A" {
		t.Fatalf("encendido: %+v", l)
	}
	// El otro salón no ve el programa de este.
	if l := loyalty(loySalon2); l != nil {
		t.Fatalf("el otro salón ve el programa ajeno: %+v", l)
	}
	// Servicio de regalo desactivado: null (aunque el programa siga encendido).
	if _, err := f.pool.Exec(f.ctx, `UPDATE services SET active=false WHERE id=$1`, f.svcA.ID); err != nil {
		t.Fatal(err)
	}
	if l := loyalty(loySalon); l != nil {
		t.Fatalf("servicio inactivo: %+v", l)
	}
	// Reactivado: vuelve.
	if _, err := f.pool.Exec(f.ctx, `UPDATE services SET active=true WHERE id=$1`, f.svcA.ID); err != nil {
		t.Fatal(err)
	}
	if l := loyalty(loySalon); l == nil {
		t.Fatal("con el servicio activo otra vez debe mostrarse")
	}
	// Lo demás de la vidriera sigue igual (aditivo).
	got, err := pub.Public(loySalon)
	if err != nil || got.Salon.ID != loySalon || len(got.Services) != 2 || got.Hours == nil {
		t.Fatalf("vidriera: %+v %v", got.Salon, err)
	}
}
