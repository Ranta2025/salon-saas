package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// depNow es el instante fijo de los tests de la sena.
var depNow = time.Date(2026, 4, 20, 15, 0, 0, 0, time.UTC)

// --- D3: política ---------------------------------------------------------------

func TestDepositPolicy_guardar_y_rangos(t *testing.T) {
	p, err := NewDepositPolicy("s1", true, 30, 30, 24, depNow)
	if err != nil {
		t.Fatalf("política válida rechazada: %v", err)
	}
	if !p.Saved || !p.Enabled || p.Percent != 30 || p.HoldMinutes != 30 || p.RefundHours != 24 {
		t.Fatalf("política = %+v", p)
	}
	if !p.UpdatedAt.Equal(depNow) {
		t.Fatalf("UpdatedAt = %s, quiero %s", p.UpdatedAt, depNow)
	}

	cases := []struct {
		name     string
		percent  int
		hold     int
		refund   int
		wantText string
	}{
		{"porcentaje bajo", 9, 30, 24, MsgDepositPercentRange},
		{"porcentaje alto", 101, 30, 24, MsgDepositPercentRange},
		{"espera baja", 30, 4, 24, MsgDepositHoldRange},
		{"espera alta", 30, 121, 24, MsgDepositHoldRange},
		{"devolucion negativa", 30, 30, -1, MsgDepositRefundRange},
		{"devolucion alta", 30, 30, 169, MsgDepositRefundRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDepositPolicy("s1", true, tc.percent, tc.hold, tc.refund, depNow)
			if err == nil {
				t.Fatal("política inválida aceptada")
			}
			if !errors.Is(err, ErrValidation) || err.Error() != tc.wantText {
				t.Fatalf("error = %q, quiero validación %q", err.Error(), tc.wantText)
			}
		})
	}

	// Los bordes válidos de cada rango.
	for _, tc := range [][3]int{{10, 5, 0}, {100, 120, 168}} {
		if _, err := NewDepositPolicy("s1", false, tc[0], tc[1], tc[2], depNow); err != nil {
			t.Fatalf("borde válido %v rechazado: %v", tc, err)
		}
	}
}

// --- D4: monto ----------------------------------------------------------------

func TestDepositAmountCents_tabla_y_redondeo(t *testing.T) {
	cases := []struct {
		price   int
		percent int
		want    int
		why     string
	}{
		{0, 30, 0, "precio 0 = no hay sena"},
		{-5, 30, 0, "precio negativo = no hay sena"},
		{1, 10, 1, "mínimo 1 centavo"},
		{1, 100, 1, "1 centavo al 100% es 1 centavo"},
		{50, 10, 5, "50 centavos al 10% son 5"},
		{49, 10, 5, "4,9 redondea para arriba"},
		{44, 10, 4, "4,4 redondea para abajo"},
		{45, 10, 5, "4,5 redondea para arriba (mitad hacia arriba)"},
		{555, 10, 56, "55,5 redondea para arriba"},
		{99, 33, 33, "32,67 redondea para arriba"},
		{100, 10, 10, "exacto no redondea"},
		{15000, 30, 4500, "el caso normal del salón"},
		{2000000000, 100, 2000000000, "tope: el monto no pasa el máximo"},
		{2000000000, 10, 200000000, "tope al 10%"},
		{3000000000, 100, MaxDepositAmountCents, "por encima del techo se acota"},
	}
	for _, tc := range cases {
		if got := DepositAmountCents(tc.price, tc.percent); got != tc.want {
			t.Fatalf("DepositAmountCents(%d, %d) = %d, quiero %d (%s)", tc.price, tc.percent, got, tc.want, tc.why)
		}
	}
}

// Propiedad: para todo precio y todo porcentaje válido el monto es exactamente
// la fórmula de D4 con enteros, nunca negativo, nunca por encima del techo y
// monótono en el precio.
func TestDepositAmountCents_propiedad(t *testing.T) {
	percents := []int{10, 33, 50, 66, 99, 100}
	prev := make(map[int]int, len(percents))
	for price := 0; price <= 1000; price++ {
		for _, percent := range percents {
			got := DepositAmountCents(price, percent)
			want := (price*percent + 50) / 100
			if want < 1 {
				want = 1
			}
			if price == 0 {
				want = 0
			}
			if got != want {
				t.Fatalf("precio %d al %d%%: %d, quiero %d", price, percent, got, want)
			}
			if got < 0 || got > MaxDepositAmountCents {
				t.Fatalf("precio %d al %d%%: monto fuera de rango %d", price, percent, got)
			}
			if price > 0 && got < prev[percent] {
				t.Fatalf("precio %d al %d%%: el monto bajó (%d < %d)", price, percent, got, prev[percent])
			}
			prev[percent] = got
		}
	}
}

// --- D2/D7: máquina de estados -----------------------------------------------

func TestDeposit_status_transiciones(t *testing.T) {
	states := []string{DepositPending, DepositPaid, DepositExpired, DepositRefunded, DepositForfeited}
	want := map[string][]string{
		DepositPending:   {DepositPaid, DepositExpired, DepositRefunded},
		DepositPaid:      {DepositRefunded, DepositForfeited},
		DepositExpired:   {DepositRefunded},
		DepositRefunded:  {},
		DepositForfeited: {},
	}
	for _, from := range states {
		if !ValidDepositStatus(from) {
			t.Fatalf("estado %q debería ser válido", from)
		}
		for _, to := range states {
			got := DepositTransitionAllowed(from, to)
			allowed := false
			for _, x := range want[from] {
				if x == to {
					allowed = true
				}
			}
			if got != allowed {
				t.Fatalf("%s -> %s = %v, quiero %v", from, to, got, allowed)
			}
		}
	}
	if DepositTransitionAllowed("nope", DepositPaid) || DepositTransitionAllowed(DepositPending, "nope") {
		t.Fatal("estados desconocidos no pueden transicionar")
	}
	if ValidDepositStatus("") || ValidDepositStatus("PAID") {
		t.Fatal("estado inválido aceptado")
	}
	if !DepositSettled(DepositRefunded) || !DepositSettled(DepositForfeited) {
		t.Fatal("refunded y forfeited son finales")
	}
	if DepositSettled(DepositPaid) || DepositSettled(DepositPending) || DepositSettled(DepositExpired) {
		t.Fatal("paid/pending/expired no están cerrados")
	}
}

// --- D9(b): reembolsar o quedarse la sena ------------------------------------

func TestDepositCancelDecision_ventana_de_devolucion(t *testing.T) {
	starts := depNow.Add(48 * time.Hour)
	cases := []struct {
		name        string
		startsAt    time.Time
		refundHours int
		want        string
	}{
		{"sobran horas", starts, 24, DepositRefunded},
		{"justo en el borde (al menos)", depNow.Add(24 * time.Hour), 24, DepositRefunded},
		{"un segundo menos", depNow.Add(24*time.Hour - time.Second), 24, DepositForfeited},
		{"cero horas y turno futuro", starts, 0, DepositRefunded},
		{"cero horas y turno ahora", depNow, 0, DepositRefunded},
		{"cero horas y turno pasado", depNow.Add(-time.Minute), 0, DepositForfeited},
		{"ventana máxima", depNow.Add(168 * time.Hour), 168, DepositRefunded},
		{"ventana máxima, falta un minuto", depNow.Add(168*time.Hour - time.Minute), 168, DepositForfeited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DepositCancelDecision(tc.startsAt, depNow, tc.refundHours); got != tc.want {
				t.Fatalf("decisión = %q, quiero %q", got, tc.want)
			}
		})
	}
}

func TestDeposit_kinds(t *testing.T) {
	for _, kind := range []string{DepositEventCreated, DepositEventAttemptDeclined, DepositEventPaid,
		DepositEventExpired, DepositEventRefunded, DepositEventForfeited, DepositEventLatePaymentRefunded} {
		if !ValidDepositEventKind(kind) {
			t.Fatalf("evento %q debería ser válido", kind)
		}
	}
	for _, kind := range []string{"", "paid ", "createdx"} {
		if ValidDepositEventKind(kind) {
			t.Fatalf("evento %q no debería ser válido", kind)
		}
	}
	for _, kind := range []string{CallbackApproved, CallbackDeclined, CallbackInsufficient} {
		if !ValidDepositCallbackKind(kind) {
			t.Fatalf("callback %q debería ser válido", kind)
		}
	}
	for _, kind := range []string{"", "OK", "refunded"} {
		if ValidDepositCallbackKind(kind) {
			t.Fatalf("callback %q no debería ser válido", kind)
		}
	}
}

// --- La sena de un turno -----------------------------------------------------

func depositPolicyFixture(t *testing.T) DepositPolicy {
	t.Helper()
	p, err := NewDepositPolicy("s1", true, 30, 30, 24, depNow)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func chargeIDFixture(t *testing.T) string {
	t.Helper()
	id, err := NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNewBookingDeposit_arma_la_sena_pendiente(t *testing.T) {
	policy := depositPolicyFixture(t)
	chargeID := chargeIDFixture(t)
	d, err := NewBookingDeposit(DepositOrder{
		SalonID: "s1", BookingID: "b1", PriceCents: 15000, ClientOpID: " op-1 ", Description: "Seña Corte",
	}, policy, chargeID, depNow)
	if err != nil {
		t.Fatalf("sena válida rechazada: %v", err)
	}
	if d.ID == "" || d.SalonID != "s1" || d.BookingID != "b1" || d.ChargeID != chargeID {
		t.Fatalf("sena = %+v", d)
	}
	if d.AmountCents != 4500 || d.Percent != 30 {
		t.Fatalf("monto/porcentaje = %d/%d, quiero 4500/30", d.AmountCents, d.Percent)
	}
	if d.Status != DepositPending || d.PaidAt != nil || d.SettledAt != nil {
		t.Fatalf("estado inicial = %+v", d)
	}
	if !d.ExpiresAt.Equal(depNow.Add(30 * time.Minute)) {
		t.Fatalf("ExpiresAt = %s, quiero %s", d.ExpiresAt, depNow.Add(30*time.Minute))
	}
	if d.ClientOpID != "op-1" {
		t.Fatalf("ClientOpID = %q, quiero recortado", d.ClientOpID)
	}
}

func TestNewBookingDeposit_rechazos(t *testing.T) {
	policy := depositPolicyFixture(t)
	chargeID := chargeIDFixture(t)
	good := DepositOrder{SalonID: "s1", BookingID: "b1", PriceCents: 15000}
	cases := []struct {
		name   string
		order  DepositOrder
		policy DepositPolicy
		charge string
	}{
		{"sin salón", DepositOrder{BookingID: "b1", PriceCents: 100}, policy, chargeID},
		{"sin turno", DepositOrder{SalonID: "s1", PriceCents: 100}, policy, chargeID},
		{"id con NUL", DepositOrder{SalonID: "s1", BookingID: "b\x001", PriceCents: 100}, policy, chargeID},
		{"precio 0", DepositOrder{SalonID: "s1", BookingID: "b1", PriceCents: 0}, policy, chargeID},
		{"porcentaje fuera de rango (política corrupta)", good, DepositPolicy{SalonID: "s1", Saved: true, Enabled: true, Percent: 5, HoldMinutes: 30}, chargeID},
		{"espera fuera de rango (política corrupta)", good, DepositPolicy{SalonID: "s1", Saved: true, Enabled: true, Percent: 30, HoldMinutes: 1}, chargeID},
		{"cargo con forma inválida", good, policy, "no-es-un-cargo"},
		{"cargo vacío", good, policy, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewBookingDeposit(tc.order, tc.policy, tc.charge, depNow); err == nil {
				t.Fatal("sena inválida aceptada")
			}
		})
	}
	// El precio 0 no es una sena: el caso de uso devuelve «no hay seña», acá
	// también es un error de validación (nunca un monto de 0 en la DB).
	if _, err := NewBookingDeposit(good, policy, chargeID, depNow); err != nil {
		t.Fatalf("orden válida rechazada: %v", err)
	}
	zero := good
	zero.PriceCents = 0
	if _, err := NewBookingDeposit(zero, policy, chargeID, depNow); !errors.Is(err, ErrValidation) {
		t.Fatalf("precio 0 tiene que ser validación, es %v", err)
	}
}

func TestBookingDeposit_pagable_y_pago_tardio(t *testing.T) {
	chargeID := chargeIDFixture(t)
	policy := depositPolicyFixture(t)
	d, err := NewBookingDeposit(DepositOrder{SalonID: "s1", BookingID: "b1", PriceCents: 15000}, policy, chargeID, depNow)
	if err != nil {
		t.Fatal(err)
	}
	d.BookingStatus = BookingBooked
	d.StartsAt = depNow.Add(24 * time.Hour)
	if !d.Payable(depNow) || d.LatePayment(depNow) {
		t.Fatal("una sena pendiente y vigente es pagable")
	}
	// Un estado de turno vacío (adaptador sin join) se trata como vivo.
	noJoin := d
	noJoin.BookingStatus = ""
	if !noJoin.Payable(depNow) {
		t.Fatal("sin el estado del turno la sena sigue siendo pagable")
	}
	// Vencida: pagable no, tardía sí.
	late := d
	late.ExpiresAt = depNow
	if late.Payable(depNow) || !late.LatePayment(depNow) {
		t.Fatal("en el instante del vencimiento la sena ya no es pagable y el pago es tardío")
	}
	// Reserva cancelada: pagable no, tardía sí.
	cancelled := d
	cancelled.BookingStatus = BookingCancelled
	if cancelled.Payable(depNow) || !cancelled.LatePayment(depNow) {
		t.Fatal("con la reserva cancelada el pago es tardío")
	}
	// Ya pagada/cerrada: ni pagable ni tardía (no hay nada que hacer).
	for _, status := range []string{DepositPaid, DepositRefunded, DepositForfeited} {
		x := d
		x.Status = status
		if x.Payable(depNow) {
			t.Fatalf("%s no es pagable", status)
		}
		if x.LatePayment(depNow) {
			t.Fatalf("%s no es un pago tardío: ya está resuelta", status)
		}
	}
	// Expired: no es pagable, es tardío (D7: se reembolsa).
	exp := d
	exp.Status = DepositExpired
	if exp.Payable(depNow) || !exp.LatePayment(depNow) {
		t.Fatal("una sena expirada con pago aprobado es tardía")
	}
}

// --- D8: id del cargo --------------------------------------------------------

func TestDepositChargeID_forma(t *testing.T) {
	id, err := NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != PaymentChargeIDLen {
		t.Fatalf("largo = %d, quiero %d", len(id), PaymentChargeIDLen)
	}
	if !ValidDepositChargeID(id) {
		t.Fatalf("el id generado no pasa su propia validación: %q", id)
	}
	for _, bad := range []string{"", "corto", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("a", 42) + "=", strings.Repeat("a", 42) + "/", strings.Repeat("a", 42) + "\x00"} {
		if ValidDepositChargeID(bad) {
			t.Fatalf("forma inválida aceptada: %q", bad)
		}
	}
	// Dos ids no se repiten.
	other, err := NewDepositChargeID()
	if err != nil {
		t.Fatal(err)
	}
	if other == id {
		t.Fatal("dos cargos salieron con el mismo id")
	}
}

// --- D10: cursor y filtros de la pantalla -----------------------------------

func TestDepositCursor_ida_y_vuelta(t *testing.T) {
	c := DepositCursor{CreatedAt: depNow, ID: "dep-1"}
	encoded := EncodeDepositCursor(c)
	if encoded == "" {
		t.Fatal("cursor vacío")
	}
	got, err := DecodeDepositCursor(encoded)
	if err != nil {
		t.Fatalf("cursor propio rechazado: %v", err)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) || got.ID != c.ID {
		t.Fatalf("cursor = %+v, quiero %+v", got, c)
	}
	for _, bad := range []string{"", "no-base64!", encoded[:len(encoded)/2], strings.Repeat("a", maxDepositCursorLen+1)} {
		if _, err := DecodeDepositCursor(bad); err == nil {
			t.Fatalf("cursor inválido aceptado: %q", bad)
		} else if err.Error() != MsgDepositCursorInvalid {
			t.Fatalf("texto = %q, quiero %q", err.Error(), MsgDepositCursorInvalid)
		}
	}
}

func TestParseDepositQuery(t *testing.T) {
	q, err := ParseDepositQuery("", "", "")
	if err != nil {
		t.Fatalf("consulta vacía rechazada: %v", err)
	}
	if q.Limit != DefaultDepositLimit || q.Status != "" || q.Before != nil {
		t.Fatalf("consulta por defecto = %+v", q)
	}
	q, err = ParseDepositQuery("paid", "10", EncodeDepositCursor(DepositCursor{CreatedAt: depNow, ID: "d1"}))
	if err != nil {
		t.Fatalf("consulta válida rechazada: %v", err)
	}
	if q.Status != DepositPaid || q.Limit != 10 || q.Before == nil || q.Before.ID != "d1" {
		t.Fatalf("consulta = %+v", q)
	}
	for _, limit := range []string{"0", "101", "abc", "-1", "1.5", "99999999999999999999"} {
		if _, err := ParseDepositQuery("", limit, ""); err == nil || err.Error() != MsgDepositLimitRange {
			t.Fatalf("límite %q: %v", limit, err)
		}
	}
	if _, err := ParseDepositQuery("nope", "", ""); err == nil || err.Error() != MsgDepositStatusInvalid {
		t.Fatalf("estado inválido: %v", err)
	}
	if _, err := ParseDepositQuery("paid ", "10", ""); err != nil {
		t.Fatalf("los espacios del borde se recortan: %v", err)
	}
	if _, err := ParseDepositQuery("", "10", "basura"); err == nil || err.Error() != MsgDepositCursorInvalid {
		t.Fatalf("cursor inválido: %v", err)
	}
}
