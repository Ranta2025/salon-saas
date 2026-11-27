package app

import (
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// T6: la ventana del cierre de caja es el DÍA DEL SALÓN, no el día UTC. Un
// salón de CDMX (UTC-6) que cierra a las 03:00 UTC todavía está en el día
// anterior local: la caja abrió a medianoche local, no a medianoche UTC.
func TestCashCloseService_ventana_en_zona_del_salon(t *testing.T) {
	sales := &fakeSales{}
	closes := &fakeCashCloses{}
	mems := &fakeMemberships{}
	settings := &fakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	svc := CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems, Settings: settings}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)

	now := time.Now().UTC()
	sales.all = append(sales.all, domain.Sale{ID: "v-cash", SalonID: "s1", TotalCents: 120000, Method: domain.MethodCash, CreatedAt: now})

	c, err := svc.CloseCash(owner, "s1", 120000, "")
	if err != nil {
		t.Fatalf("cerrar: %v", err)
	}
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	local := now.In(loc)
	want := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
	if !c.OpenedAt.Equal(want) {
		t.Fatalf("la apertura debe ser la medianoche local (%v), dio %v", want, c.OpenedAt)
	}
	if c.SystemCashCents != 120000 {
		t.Fatalf("efectivo de la ventana: %d", c.SystemCashCents)
	}

	// Un cierre previo manda sobre el día local (sigue contabilizando una vez).
	// Fixture aparte para no mezclar con el cierre que acaba de guardar el
	// primer tramo (que es el más reciente).
	closes2 := &fakeCashCloses{}
	sales2 := &fakeSales{}
	svc2 := CashCloseService{CashCloses: closes2, Sales: sales2, Memberships: mems, Settings: settings}
	anterior := now.Add(-90 * time.Minute)
	closes2.all = append(closes2.all, domain.CashClose{
		ID: "c0", SalonID: "s1", OpenedAt: anterior.Add(-time.Hour), ClosedAt: anterior,
		SystemCashCents: 50000, CountedCents: 50000,
	})
	c, err = svc2.CloseCash(owner, "s1", 0, "")
	if err != nil {
		t.Fatalf("segundo cierre: %v", err)
	}
	if !c.OpenedAt.Equal(anterior) {
		t.Fatalf("la apertura debe ser el cierre anterior (%v), dio %v", anterior, c.OpenedAt)
	}

	// Settings ilegibles que no son "no hay fila": el cierre no inventa una
	// ventana, devuelve el error (no se cuenta plata con datos dudosos).
	settings.BySalon = nil
	roto := CashCloseService{CashCloses: &fakeCashCloses{}, Sales: &fakeSales{}, Memberships: mems, Settings: failSettings{}}
	if _, err := roto.CloseCash(owner, "s1", 0, ""); err == nil {
		t.Fatal("un error de settings debe propagarse")
	}
}

// failSettings simula una DB caída leyendo la configuración.
type failSettings struct{}

func (failSettings) Settings(string) (domain.SalonSettings, error) {
	return domain.SalonSettings{}, errFakeSettings
}
func (failSettings) SaveSettings(domain.SalonSettings) error { return errFakeSettings }

func (failSettings) SaveSettingsGuarded(domain.SalonSettings) error { return errFakeSettings }

var errFakeSettings = domain.ErrValidation
