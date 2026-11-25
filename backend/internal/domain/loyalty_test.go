package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func wantLoyaltyErr(t *testing.T, err error, kind error, text string) {
	t.Helper()
	if err == nil {
		t.Fatalf("se esperaba el error %q", text)
	}
	if !errors.Is(err, kind) || err.Error() != text {
		t.Fatalf("error %q, quería %q de la familia %v", err.Error(), text, kind)
	}
}

// D4: las visitas necesarias van de 2 a 50, con el texto EXACTO del contrato.
func TestLoyalty_visitsRange(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 51, 1000} {
		wantLoyaltyErr(t, ValidateLoyaltyVisits(n), ErrValidation, "Las visitas necesarias deben ser entre 2 y 50")
	}
	for _, n := range []int{2, 3, 49, 50} {
		if err := ValidateLoyaltyVisits(n); err != nil {
			t.Fatalf("%d visitas es válido: %v", n, err)
		}
	}
}

// D4: la vigencia es opcional (nil = no vence) y, si viene, de 1 a 3650 días.
func TestLoyalty_validityRange(t *testing.T) {
	if err := ValidateLoyaltyValidity(nil); err != nil {
		t.Fatalf("sin vigencia es válido: %v", err)
	}
	for _, n := range []int{-5, 0, 3651, 100000} {
		n := n
		wantLoyaltyErr(t, ValidateLoyaltyValidity(&n), ErrValidation, "La vigencia debe ser entre 1 y 3650 días")
	}
	for _, n := range []int{1, 30, 3650} {
		n := n
		if err := ValidateLoyaltyValidity(&n); err != nil {
			t.Fatalf("%d días es válido: %v", n, err)
		}
	}
}

func TestLoyalty_newProgram(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	days := 30
	p, err := NewLoyaltyProgram("s1", true, 5, " svc-1 ", &days, now)
	if err != nil {
		t.Fatalf("programa: %v", err)
	}
	if p.SalonID != "s1" || !p.Enabled || p.VisitsRequired != 5 || p.RewardServiceID != "svc-1" ||
		p.ValidityDays == nil || *p.ValidityDays != 30 || !p.CountingFrom.Equal(now) {
		t.Fatalf("programa armado mal: %+v", p)
	}
	// La vigencia se COPIA: cambiar la variable de quien llama no cambia el programa.
	days = 99
	if *p.ValidityDays != 30 {
		t.Fatalf("la vigencia no se copió")
	}
	bad := 0
	if _, err := NewLoyaltyProgram("s1", true, 1, "svc-1", nil, now); err == nil {
		t.Fatal("1 visita es inválido")
	}
	if _, err := NewLoyaltyProgram("s1", true, 5, "svc-1", &bad, now); err == nil {
		t.Fatal("vigencia 0 es inválida")
	}
	if _, err := NewLoyaltyProgram("s1", true, 5, "  ", nil, now); err == nil {
		t.Fatal("sin servicio es inválido")
	}
}

// D5: earned = floor(visitas / N), available = max(0, earned - granted),
// next_in = N - visitas % N.
func TestLoyalty_progress(t *testing.T) {
	cases := []struct {
		visits, req, granted      int
		earned, available, nextIn int
	}{
		{0, 5, 0, 0, 0, 5},
		{4, 5, 0, 0, 0, 1},
		{5, 5, 0, 1, 1, 5},
		{7, 5, 1, 1, 0, 3},
		{10, 5, 1, 2, 1, 5},
		{12, 5, 0, 2, 2, 3},
		// Entregados de más (se bajó N a mano): nunca queda negativo.
		{5, 5, 3, 1, 0, 5},
		{49, 50, 0, 0, 0, 1},
		{100, 50, 0, 2, 2, 50},
	}
	for _, c := range cases {
		got := ComputeLoyaltyProgress(c.visits, c.req, c.granted)
		if got.Visits != c.visits || got.VisitsRequired != c.req || got.Granted != c.granted ||
			got.Earned != c.earned || got.Available != c.available || got.NextIn != c.nextIn {
			t.Fatalf("visitas=%d N=%d entregados=%d: %+v, quería earned=%d available=%d next_in=%d",
				c.visits, c.req, c.granted, got, c.earned, c.available, c.nextIn)
		}
	}
	// Sin programa (N = 0) no se divide por cero.
	if got := ComputeLoyaltyProgress(9, 0, 0); got.Earned != 0 || got.Available != 0 || got.NextIn != 0 {
		t.Fatalf("N=0: %+v", got)
	}
}

func loyaltyProgram(enabled bool, validity *int) LoyaltyProgram {
	return LoyaltyProgram{
		SalonID: "s1", Enabled: enabled, VisitsRequired: 5, RewardServiceID: "svc-1",
		RewardServiceName: "Corte", ValidityDays: validity,
		CountingFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// D6: entregar exige programa activado y un premio disponible; el vale lleva el
// número granted+1, el servicio y su nombre de HOY y el fin del día LOCAL + vigencia.
func TestLoyalty_newGrant(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	// 10 de marzo 11:30 UTC = 11 de marzo 00:30 en Auckland (NZDT +13).
	now := time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC)
	days := 1

	r, err := NewLoyaltyGrant(loyaltyProgram(true, &days), "c1", 10, 1, " op-1 ", now, auckland)
	if err != nil {
		t.Fatalf("entregar: %v", err)
	}
	if r.RewardNumber != 2 || r.ClientID != "c1" || r.SalonID != "s1" || r.ServiceID != "svc-1" ||
		r.ServiceName != "Corte" || r.GrantOpID != "op-1" || !r.GrantedAt.Equal(now) || r.ID == "" ||
		r.RedeemedAt != nil || r.RedeemedBy != "" {
		t.Fatalf("vale: %+v", r)
	}
	// Hoy en Auckland es el 11; +1 día => fin del día 12 local = 12 de marzo 23:59:59.999999 NZDT.
	want := time.Date(2026, 3, 12, 23, 59, 59, 999_999_000, auckland).UTC()
	if r.ExpiresAt == nil || !r.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, quería %v", r.ExpiresAt, want)
	}

	// Sin vigencia no vence.
	r, err = NewLoyaltyGrant(loyaltyProgram(true, nil), "c1", 5, 0, "", now, time.UTC)
	if err != nil || r.ExpiresAt != nil || r.RewardNumber != 1 || r.GrantOpID != "" {
		t.Fatalf("sin vigencia: %+v %v", r, err)
	}

	// Apagado: nunca se entrega, aunque haya premios.
	_, err = NewLoyaltyGrant(loyaltyProgram(false, nil), "c1", 50, 0, "", now, time.UTC)
	wantLoyaltyErr(t, err, ErrAlreadyExists, "La fidelidad no está activada")
	// Nunca guardado (programa vacío) es lo mismo.
	_, err = NewLoyaltyGrant(LoyaltyProgram{}, "c1", 50, 0, "", now, time.UTC)
	wantLoyaltyErr(t, err, ErrAlreadyExists, "La fidelidad no está activada")
	// Sin premio disponible.
	_, err = NewLoyaltyGrant(loyaltyProgram(true, nil), "c1", 9, 1, "", now, time.UTC)
	wantLoyaltyErr(t, err, ErrAlreadyExists, "No hay premios disponibles")
}

// El estado del vale: usado gana sobre vencido; vence EXACTO en expires_at.
func TestLoyalty_rewardState(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	exact := now
	cases := []struct {
		name string
		r    LoyaltyReward
		want string
	}{
		{"disponible sin vencimiento", LoyaltyReward{}, "available"},
		{"disponible vigente", LoyaltyReward{ExpiresAt: &future}, "available"},
		{"vence justo ahora", LoyaltyReward{ExpiresAt: &exact}, "expired"},
		{"vencido", LoyaltyReward{ExpiresAt: &past}, "expired"},
		{"usado", LoyaltyReward{RedeemedAt: &past}, "redeemed"},
		{"usado y vencido: usado", LoyaltyReward{RedeemedAt: &past, ExpiresAt: &past}, "redeemed"},
	}
	for _, c := range cases {
		if got := c.r.StateAt(now); got != c.want {
			t.Fatalf("%s: %q, quería %q", c.name, got, c.want)
		}
	}
}

// La nota del uso y el client_op_id siguen las reglas de F8 (una línea, sin NUL).
func TestLoyalty_noteAndOpID(t *testing.T) {
	if n, err := ValidateLoyaltyNote("  Cumpleaños  "); err != nil || n != "Cumpleaños" {
		t.Fatalf("nota: %q %v", n, err)
	}
	for _, bad := range []string{"a\nb", "a\x00b", "a\u2028b", strings.Repeat("x", 201)} {
		if _, err := ValidateLoyaltyNote(bad); err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("la nota %q debe ser 400: %v", bad, err)
		}
	}
	if id, err := ValidateLoyaltyOpID(" op-9 "); err != nil || id != "op-9" {
		t.Fatalf("op id: %q %v", id, err)
	}
	for _, bad := range []string{"a\x00b", strings.Repeat("x", 201)} {
		if _, err := ValidateLoyaltyOpID(bad); err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("el op id %q debe ser 400: %v", bad, err)
		}
	}
}
