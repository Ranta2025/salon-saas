// Concurrencia REAL contra Postgres de la sena (F11, D7/D8): 30 callbacks
// `approved` simultáneos del mismo evento dejan UN `paid`, UN evento en el libro
// mayor y UN correo de confirmación; dos eventos distintos del mismo cargo dejan
// UNA transición; 20 callbacks aprobados contra una sena vencida dejan UN
// reembolso; el mismo client_op_id 16 veces crea UN cargo.
package postgres

import (
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// emailFor deja la dirección de la clienta del turno (lo que lee el correo de la
// confirmación de una sena pagada, D7).
func (f *depFix) emailFor(t *testing.T, bookingID, email string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO booking_emails(booking_id,salon_id,email) VALUES($1,$2,$3)
		 ON CONFLICT (booking_id) DO UPDATE SET email=EXCLUDED.email`,
		bookingID, depSalon, email); err != nil {
		t.Fatalf("email del turno: %v", err)
	}
}

// paidEvents cuenta los eventos `paid` de una sena.
func (f *depFix) paidEvents(t *testing.T, depositID string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM deposit_events WHERE deposit_id=$1 AND kind='paid'`, depositID).Scan(&n); err != nil {
		t.Fatalf("eventos paid: %v", err)
	}
	return n
}

// waitMail espera el correo y comprueba que llegue UNO solo (con una espera
// extra para que un segundo envío tardío tenga tiempo de aparecer).
func waitMail(t *testing.T, m *itMailer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && m.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := m.count(); got != 1 {
		t.Fatalf("correos de confirmación: %d (tiene que ser exactamente 1)", got)
	}
}

// D7: 30 callbacks aprobados SIMULTÁNEOS del mismo evento -> un solo `paid`, un
// solo evento del banco y un solo correo.
func TestDepositsConcurrencyIT_thirtyIdenticalCallbacks(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	mailer := &itMailer{}
	svc := f.sweepEnv(t, gateway, mailer)
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))
	f.emailFor(t, b, "lia@test.com")
	h, body := signedCallback(t, gateway.secret, d.ChargeID, "ev-30", domain.CallbackApproved, 4500)

	var wg sync.WaitGroup
	errs := make([]error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.ApplyCallback(h, body)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("callback %d: %v", i, err)
		}
	}
	got, err := f.deposits.FindByCharge(d.ChargeID)
	if err != nil || got.Status != domain.DepositPaid {
		t.Fatalf("estado: %+v (%v)", got, err)
	}
	if n := f.paidEvents(t, d.ID); n != 1 {
		t.Fatalf("eventos paid: %d", n)
	}
	if n := len(f.eventKinds(t, d.ID)); n != 2 {
		t.Fatalf("libro mayor: %v", f.eventKinds(t, d.ID))
	}
	waitMail(t, mailer)
}

// D7: dos eventos DISTINTOS del mismo cargo no pueden hacer dos transiciones.
func TestDepositsConcurrencyIT_twoDifferentEvents(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))

	var wg sync.WaitGroup
	for _, eventID := range []string{"ev-dos-1", "ev-dos-2"} {
		wg.Add(1)
		go func(eventID string) {
			defer wg.Done()
			h, body := signedCallback(t, gateway.secret, d.ChargeID, eventID, domain.CallbackApproved, 4500)
			if err := svc.ApplyCallback(h, body); err != nil {
				t.Errorf("callback %s: %v", eventID, err)
			}
		}(eventID)
	}
	wg.Wait()
	if n := f.paidEvents(t, d.ID); n != 1 {
		t.Fatalf("eventos paid: %d", n)
	}
	got, _ := f.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositPaid {
		t.Fatalf("estado: %+v", got)
	}
}

// D7: contra una sena vencida, 20 callbacks aprobados dejan UN reembolso y UN
// cierre (el banco es idempotente por cargo y el cierre es un UPDATE condicional).
func TestDepositsConcurrencyIT_expiredRefundsOnce(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(-time.Minute))
	if _, err := f.pool.Exec(f.ctx, `UPDATE booking_deposits SET status='expired' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	h, body := signedCallback(t, gateway.secret, d.ChargeID, "ev-vencida", domain.CallbackApproved, 4500)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.ApplyCallback(h, body); err != nil {
				t.Errorf("callback: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := gateway.refundCount(d.ChargeID); n != 1 {
		t.Fatalf("reembolsos: %d", n)
	}
	got, _ := f.deposits.FindByCharge(d.ChargeID)
	if got.Status != domain.DepositRefunded {
		t.Fatalf("estado: %+v", got)
	}
	var late int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM deposit_events WHERE deposit_id=$1 AND kind='late_payment_refunded'`, d.ID).Scan(&late); err != nil || late != 1 {
		t.Fatalf("cierres: %d (%v)", late, err)
	}
}

// D8: el mismo client_op_id 16 veces crea UN solo cargo y UNA sola sena (el
// reintento del navegador no cobra de nuevo).
func TestDepositsConcurrencyIT_sixteenReplaysOneCharge(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	if _, err := f.policies.Save(domain.DepositPolicy{SalonID: depSalon, Saved: true, Enabled: true,
		Percent: 30, HoldMinutes: 30, RefundHours: 24, UpdatedAt: f.t0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))

	var firstID string
	for i := 0; i < 16; i++ {
		d, err := svc.CreateForBooking(domain.DepositOrder{
			SalonID: depSalon, BookingID: b, PriceCents: 15000,
			ClientOpID: "op-repetida-16", Description: "Corte",
		})
		if err != nil {
			t.Fatalf("alta %d: %v", i, err)
		}
		if d == nil {
			t.Fatalf("alta %d: sin seña", i)
		}
		if firstID == "" {
			firstID = d.ID
		} else if d.ID != firstID {
			t.Fatalf("el reintento cambió la seña: %s vs %s", d.ID, firstID)
		}
	}
	if n := gateway.chargeCount(); n != 1 {
		t.Fatalf("cargos creados: %d", n)
	}
	var rows int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM booking_deposits WHERE salon_id=$1`, depSalon).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("señas: %d (%v)", rows, err)
	}
	if kinds := f.eventKinds(t, firstID); len(kinds) != 1 || kinds[0] != domain.DepositEventCreated {
		t.Fatalf("libro mayor: %v", kinds)
	}
}

// Guarda del UPDATE condicional `status='pending'`: el barrido y el callback
// corren a la vez sobre la MISMA sena y NUNCA pueden dejar un `expired` y un
// `paid` en el libro mayor. Gana quien haga primero el UPDATE; el otro camino se
// entera al releer (vencida = pago tardío). Sin la condición, el callback
// pisaría la sena vencida con un `paid`.
func TestDepositsConcurrencyIT_expireVersusCallbackInvariant(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	for i := 0; i < 60; i++ {
		now := time.Now().UTC()
		b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
		// Vence en el instante de la carrera: el callback puede leerla pagable y
		// el barrido vencerla entre la lectura y el UPDATE.
		d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(time.Millisecond))
		h, body := signedCallback(t, gateway.secret, d.ChargeID, "ev-carrera-"+d.ID, domain.CallbackApproved, 4500)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := svc.ApplyCallback(h, body); err != nil {
				t.Errorf("callback: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			if _, err := svc.Sweep(50); err != nil {
				t.Errorf("barrido: %v", err)
			}
		}()
		wg.Wait()

		kinds := f.eventKinds(t, d.ID)
		hasExpired := containsKind(kinds, domain.DepositEventExpired)
		hasPaid := containsKind(kinds, domain.DepositEventPaid)
		hasLate := containsKind(kinds, domain.DepositEventLatePaymentRefunded)
		// Lo INACEPTABLE es que la sena quede vencida Y pagada a la vez.
		if hasExpired && hasPaid {
			t.Fatalf("iteración %d: la sena tiene `expired` y `paid` a la vez: %v", i, kinds)
		}
		if !hasPaid && !hasLate {
			t.Fatalf("iteración %d: la carrera no la resolvió: %v", i, kinds)
		}
		got, err := f.deposits.FindByCharge(d.ChargeID)
		if err != nil {
			t.Fatalf("iteración %d: %v", i, err)
		}
		want := domain.DepositPaid
		switch {
		case hasLate:
			want = domain.DepositRefunded
		case hasExpired:
			want = domain.DepositExpired
		}
		if hasExpired {
			// El barrido cancela el turno de la vencida.
			var st string
			if err := f.pool.QueryRow(f.ctx, `SELECT status FROM bookings WHERE id=$1`, b).Scan(&st); err != nil || st != string(domain.BookingCancelled) {
				t.Fatalf("iteración %d: turno %q (%v)", i, st, err)
			}
		}
		if got.Status != want {
			t.Fatalf("iteración %d: estado %q, quería %q (%v)", i, got.Status, want, kinds)
		}
	}
}

// La guarda única del evento del banco: un evento RECHAZADO reenviado se registra
// UNA vez (el camino del rechazo no mueve el estado, así que el índice único es el
// que lo frena).
func TestDepositsConcurrencyIT_declinedReplayWritesOnce(t *testing.T) {
	f := depFixtures(t)
	gateway := newITGateway()
	svc := f.sweepEnv(t, gateway, &itMailer{})
	now := time.Now().UTC()
	b := f.booking(t, depSalon, domain.BookingBooked, now.Add(48*time.Hour))
	d := f.newDeposit(t, depSalon, b, 4500, 30, f.t0, now.Add(30*time.Minute))
	h, body := signedCallback(t, gateway.secret, d.ChargeID, "ev-rec-repetido", domain.CallbackDeclined, 4500)
	for i := 0; i < 5; i++ {
		if err := svc.ApplyCallback(h, body); err != nil {
			t.Fatalf("callback %d: %v", i, err)
		}
	}
	var declined int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM deposit_events WHERE deposit_id=$1 AND kind='attempt_declined'`, d.ID).Scan(&declined); err != nil {
		t.Fatal(err)
	}
	if declined != 1 {
		t.Fatalf("el evento rechazado se registra UNA vez: %d", declined)
	}
}
