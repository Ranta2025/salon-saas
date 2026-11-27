package pgstore_test

import (
	"context"
	"testing"
	"time"

	pgstore "salon-saas/internal/adapters/postgres"
	bookingspostgres "salon-saas/internal/bookings/postgres"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil/itdb"
)

// H7 (revisión nativa de F2d3): InSalonTx esperaba el lock del salón con
// context.Background(), o sea sin límite. Si otra transacción tenía el lock
// trabado, el pedido se quedaba esperando para siempre con una conexión del
// pool tomada, y los pedidos siguientes del mismo salón hacían cola detrás.
// Ahora la espera tiene un tope y devuelve error liberando la conexión.
func TestBookings_InSalonTx_lock_wait_is_bounded(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()

	// Otra transacción tiene el lock del salón y no lo suelta.
	holder, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin del que retiene el lock: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1))`, "booking-cover:s-h7"); err != nil {
		t.Fatalf("tomar el lock: %v", err)
	}

	repo := bookingspostgres.Bookings{Pool: pool, TxTimeout: 300 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		done <- repo.InSalonTx("s-h7", func(ports.BookingTx) error { return nil })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("con el lock tomado por otro, InSalonTx no puede terminar sin error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("InSalonTx quedó esperando el lock sin límite (H7)")
	}

	// La conexión volvió al pool: solo queda tomada la del que retiene el lock.
	deadline := time.Now().Add(2 * time.Second)
	for pool.Stat().AcquiredConns() > 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := pool.Stat().AcquiredConns(); got != 1 {
		t.Fatalf("conexiones tomadas: %d (esperaba 1, la del que retiene el lock)", got)
	}
}

// H7: la caja tiene el mismo patrón (lock del salón) y el mismo riesgo.
func TestCashCloses_InSalonTx_lock_wait_is_bounded(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()

	holder, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin del que retiene el lock: %v", err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1))`, "cash-close:s-h7"); err != nil {
		t.Fatalf("tomar el lock: %v", err)
	}

	repo := pgstore.CashCloses{Pool: pool, TxTimeout: 300 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		done <- repo.InSalonTx("s-h7", func(ports.CashCloseTx) error { return nil })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("con el lock tomado por otro, InSalonTx no puede terminar sin error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("InSalonTx de la caja quedó esperando el lock sin límite (H7)")
	}
}

// El tope no puede cortar el trabajo normal: sin nadie reteniendo el lock, la
// transacción corre y confirma.
func TestInSalonTx_normal_work_still_commits(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	ran := false
	if err := (bookingspostgres.Bookings{Pool: pool}).InSalonTx("s-h7-ok", func(ports.BookingTx) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("reservas: ran=%v err=%v", ran, err)
	}
	ran = false
	if err := (pgstore.CashCloses{Pool: pool}).InSalonTx("s-h7-ok", func(ports.CashCloseTx) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("caja: ran=%v err=%v", ran, err)
	}
}
