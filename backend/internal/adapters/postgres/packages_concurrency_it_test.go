// Concurrencia de los paquetes prepagos contra Postgres real (F8, D4/D5/D7).
// Se saltea sin SALON_IT=1. El corazón: el consumo es UN UPDATE condicional
// (saldo >= 1 y vigente) más su movimiento en la misma transacción, así que bajo
// cualquier cantidad de pedidos simultáneos `sessions_left = Σ delta`, nunca
// queda negativo y cada sesión se consume UNA sola vez.
package pgstore

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// runParallel lanza n goroutines que arrancan JUNTAS (barrera) y junta sus errores.
func runParallel(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func isMsg(err error, kind error, text string) bool {
	return err != nil && errors.Is(err, kind) && err.Error() == text
}

// D4/D7: 30 consumos simultáneos de un paquete de 10 sesiones: EXACTAMENTE 10
// ganan y los otros 20 reciben 409 «No quedan sesiones en este paquete» (ni un
// 500, ni un 400 de CHECK, ni otro texto); el saldo final es 0 = Σ delta.
func TestPackagesConcurrencyIT_thirty_redeems_of_ten(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 10, 1000, nil)

	errs := runParallel(30, func(int) error { _, err := f.redeem(cp.ID, ""); return err })

	ok, noSessions := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case isMsg(err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete"):
			noSessions++
		default:
			t.Errorf("resultado inesperado: %v", err)
		}
	}
	if ok != 10 || noSessions != 20 {
		t.Fatalf("ganaron %d y %d recibieron 409; esperaba 10 y 20", ok, noSessions)
	}
	if got := f.left(t, cp.ID); got != 0 {
		t.Fatalf("saldo final = %d, esperaba 0", got)
	}
	if got := f.sum(t, cp.ID); got != 0 {
		t.Fatalf("Σ delta = %d, esperaba 0", got)
	}
	if n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1 AND reason='redeem'`, cp.ID); n != 10 {
		t.Fatalf("movimientos de consumo = %d, esperaba 10", n)
	}
	f.invariant(t, cp.ID)
}

// Dos consumos simultáneos de la ÚLTIMA sesión: UNO gana, el otro 409. Repetido
// en varias rondas para dar chance a la carrera.
func TestPackagesConcurrencyIT_last_session_one_wins(t *testing.T) {
	f := pkgFixtures(t)
	for round := 0; round < 15; round++ {
		cp := f.buy(t, 1, 100, nil)
		errs := runParallel(2, func(int) error { _, err := f.redeem(cp.ID, ""); return err })
		wins := 0
		for _, err := range errs {
			if err == nil {
				wins++
			} else if !isMsg(err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete") {
				t.Fatalf("ronda %d: resultado inesperado %v", round, err)
			}
		}
		if wins != 1 || f.left(t, cp.ID) != 0 {
			t.Fatalf("ronda %d: ganaron %d, saldo %d", round, wins, f.left(t, cp.ID))
		}
		f.invariant(t, cp.ID)
	}
}

// 16 pedidos con el MISMO client_op_id (cola offline reintentando): UN solo
// descuento y TODOS responden bien (el repetido devuelve el estado, no un 409).
func TestPackagesConcurrencyIT_same_op_redeems_once(t *testing.T) {
	f := pkgFixtures(t)
	for _, sessions := range []int{5, 1} { // con saldo de sobra y con UNA sola sesión
		cp := f.buy(t, sessions, 1000, nil)
		op := "op-same-" + domain.NewID()
		errs := runParallel(16, func(int) error { _, err := f.redeem(cp.ID, op); return err })
		for i, err := range errs {
			if err != nil {
				t.Fatalf("sesiones=%d: el pedido %d falló: %v (el repetido debe devolver el estado)", sessions, i, err)
			}
		}
		if got := f.left(t, cp.ID); got != sessions-1 {
			t.Fatalf("sesiones=%d: saldo %d, esperaba %d (un solo descuento)", sessions, got, sessions-1)
		}
		if n := f.count(t, `SELECT count(*) FROM package_movements WHERE client_package_id=$1 AND reason='redeem'`, cp.ID); n != 1 {
			t.Fatalf("sesiones=%d: movimientos de consumo = %d, esperaba 1", sessions, n)
		}
		f.invariant(t, cp.ID)
	}
}

// 16 compras simultáneas con el mismo client_op_id: UNA sola venta, UN paquete y
// UN movimiento; las otras 15 son ErrAlreadyExists y no dejan nada.
func TestPackagesConcurrencyIT_same_op_purchases_once(t *testing.T) {
	f := pkgFixtures(t)
	p := f.newPackage(t, "Pack carrera", 5, 1000, nil)
	now := time.Now().UTC()
	errs := runParallel(16, func(int) error {
		return f.owned.Purchase(f.order(t, p, f.lia, "op-compra-carrera", now))
	})
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrAlreadyExists):
		default:
			t.Errorf("resultado inesperado: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("compras exitosas = %d, esperaba 1", ok)
	}
	for _, q := range []string{
		`SELECT count(*) FROM sales WHERE salon_id='s-it-pkg'`,
		`SELECT count(*) FROM client_packages WHERE salon_id='s-it-pkg'`,
		`SELECT count(*) FROM package_movements WHERE salon_id='s-it-pkg'`,
	} {
		if n := f.count(t, q); n != 1 {
			t.Fatalf("%s = %d, esperaba 1 (las perdedoras dejaron filas)", q, n)
		}
	}
	got, err := f.owned.FindPurchaseByOp("s-it-pkg", "op-compra-carrera")
	if err != nil || got.SessionsLeft != 5 {
		t.Fatalf("compra original: %+v %v", got, err)
	}
}

// Consumos y ajustes (+1/-1) simultáneos sobre el mismo paquete: el invariante
// se mantiene, el saldo queda en 0..total y TODO error es uno de los dos 409 con
// su texto (nunca un 500, un deadlock ni un CHECK crudo).
func TestPackagesConcurrencyIT_redeems_and_adjusts_keep_invariant(t *testing.T) {
	f := pkgFixtures(t)
	cp := f.buy(t, 10, 1000, nil)
	now := time.Now().UTC()
	errs := runParallel(60, func(i int) error {
		switch i % 3 {
		case 0, 1:
			_, err := f.redeem(cp.ID, "")
			return err
		default:
			delta := 1
			if i%2 == 0 {
				delta = -1
			}
			_, err := f.owned.Adjust(domain.PackageAdjustRequest{
				SalonID: "s-it-pkg", ClientID: f.lia.ID, ClientPackageID: cp.ID,
				Delta: delta, Note: "ajuste concurrente", Actor: "x", Now: now,
			})
			return err
		}
	})
	for _, err := range errs {
		if err != nil &&
			!isMsg(err, domain.ErrAlreadyExists, "No quedan sesiones en este paquete") &&
			!isMsg(err, domain.ErrAlreadyExists, "El ajuste deja el saldo fuera de rango") {
			t.Errorf("resultado inesperado: %v", err)
		}
	}
	f.invariant(t, cp.ID)
}

// Compras simultáneas de OTRAS clientas (op ids distintos) no se pisan entre sí.
func TestPackagesConcurrencyIT_independent_purchases(t *testing.T) {
	f := pkgFixtures(t)
	p := f.newPackage(t, "Pack paralelo", 3, 500, nil)
	var done atomic.Int32
	errs := runParallel(20, func(i int) error {
		c := f.lia
		if i%2 == 1 {
			c = f.liaTwo
		}
		err := f.owned.Purchase(f.order(t, p, c, "", time.Now().UTC()))
		if err == nil {
			done.Add(1)
		}
		return err
	})
	for _, err := range errs {
		if err != nil {
			t.Fatalf("compra independiente falló: %v", err)
		}
	}
	if done.Load() != 20 || f.count(t, `SELECT count(*) FROM client_packages WHERE salon_id='s-it-pkg'`) != 20 ||
		f.count(t, `SELECT count(*) FROM sales WHERE salon_id='s-it-pkg' AND packages_cents=500`) != 20 {
		t.Fatalf("compras: %d", done.Load())
	}
	rows, err := f.pool.Query(f.ctx, `SELECT id FROM client_packages WHERE salon_id='s-it-pkg'`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		f.invariant(t, id)
	}
}
