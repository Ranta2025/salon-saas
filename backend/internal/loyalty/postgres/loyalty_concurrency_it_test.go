// Concurrencia con Postgres real de la fidelidad (F9, D6/D7). Se saltea sin
// SALON_IT=1. Son las pruebas que un mutante tiene que romper: sin el lock por
// clienta Y el UNIQUE (client_id, reward_number), o sin el `redeemed_at IS NULL`
// del UPDATE, dos pedidos simultáneos se llevarían el mismo premio.
package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

// bigLoyaltyRewards devuelve el adaptador sobre un pool de 40 conexiones: con el
// pool por defecto (pocas conexiones) los pedidos se encolarían y la carrera
// casi no existiría; así los 30 llegan de verdad a la vez.
func bigLoyaltyRewards(t *testing.T) LoyaltyRewards {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 40
	cfg.MinConns = 40
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return LoyaltyRewards{Pool: pool}
}

// D6: 30 entregas simultáneas con UN solo premio disponible => UNA gana, 29
// reciben «No hay premios disponibles» y queda UNA fila.
func TestLoyaltyRewards_grantRaceOneAvailable(t *testing.T) {
	f := loyFixtures(t)
	f.rewards = bigLoyaltyRewards(t)
	f.saveProgram(t, true, 5, nil)
	f.visitSales(t, f.lia.ID, 5) // exactamente UN premio ganado
	const n = 30
	errs := make([]error, n)
	got := make([]domain.LoyaltyReward, n)
	itdb.Race(n, func(i int) {
		got[i], errs[i] = f.rewards.Grant(grantReq(f.lia.ID, "", time.Now(), time.UTC))
	})
	ok, noReward, other := 0, 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case err.Error() == "No hay premios disponibles":
			noReward++
		default:
			other++
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || noReward != n-1 || other != 0 {
		t.Fatalf("éxitos=%d sin premio=%d otros=%d, quería 1/%d/0", ok, noReward, other, n-1)
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.lia.ID); rows != 1 {
		t.Fatalf("hay %d filas, quería 1", rows)
	}
}

// D6: con DOS premios disponibles y 30 pedidos salen exactamente los vales 1 y 2
// (sin huecos ni repetidos).
func TestLoyaltyRewards_grantRaceTwoAvailable(t *testing.T) {
	f := loyFixtures(t)
	f.rewards = bigLoyaltyRewards(t)
	f.saveProgram(t, true, 5, nil)
	f.visitSales(t, f.lia.ID, 10)
	f.visitSales(t, f.mora.ID, 5) // otra clienta en paralelo: cada una con lo suyo
	const n = 30
	errs := make([]error, 2*n)
	itdb.Race(2*n, func(i int) {
		client := f.lia.ID
		if i%2 == 1 {
			client = f.mora.ID
		}
		_, errs[i] = f.rewards.Grant(grantReq(client, "", time.Now(), time.UTC))
	})
	for _, err := range errs {
		if err != nil && err.Error() != "No hay premios disponibles" {
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1 AND reward_number IN (1,2)`, f.lia.ID); rows != 2 {
		t.Fatalf("Lía: %d vales 1..2, quería 2", rows)
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.lia.ID); rows != 2 {
		t.Fatalf("Lía tiene %d vales, quería 2", rows)
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.mora.ID); rows != 1 {
		t.Fatalf("Mora tiene %d vales, quería 1", rows)
	}
}

// D6: el MISMO client_op_id 16 veces a la vez => una sola fila y todas devuelven
// el mismo vale (hay muchos premios disponibles: sin idempotencia saldrían 16).
func TestLoyaltyRewards_grantRaceSameOp(t *testing.T) {
	f := loyFixtures(t)
	f.rewards = bigLoyaltyRewards(t)
	f.saveProgram(t, true, 2, nil)
	f.visitSales(t, f.lia.ID, 40) // 20 premios disponibles
	const n = 16
	errs := make([]error, n)
	got := make([]domain.LoyaltyReward, n)
	itdb.Race(n, func(i int) {
		got[i], errs[i] = f.rewards.Grant(grantReq(f.lia.ID, "op-race-grant", time.Now(), time.UTC))
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("pedido %d: %v", i, err)
		}
		if got[i].ID != got[0].ID || got[i].RewardNumber != 1 {
			t.Fatalf("pedido %d devolvió otro vale: %+v vs %+v", i, got[i], got[0])
		}
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE client_id=$1`, f.lia.ID); rows != 1 {
		t.Fatalf("hay %d filas, quería 1", rows)
	}
}

// D7: 30 usos simultáneos del MISMO vale => UNO gana, 29 «Ese premio ya se usó».
func TestLoyaltyRewards_redeemRace(t *testing.T) {
	f := loyFixtures(t)
	f.rewards = bigLoyaltyRewards(t)
	now := time.Now().UTC()
	r := f.grantOne(t, nil, now)
	const n = 30
	errs := make([]error, n)
	itdb.Race(n, func(i int) {
		_, errs[i] = f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "", time.Now().UTC()))
	})
	ok, used := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case err.Error() == "Ese premio ya se usó":
			used++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ok != 1 || used != n-1 {
		t.Fatalf("éxitos=%d ya usado=%d, quería 1/%d", ok, used, n-1)
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE id=$1 AND redeemed_at IS NOT NULL`, r.ID); rows != 1 {
		t.Fatalf("filas usadas: %d", rows)
	}
}

// D7: el mismo client_op_id 16 veces a la vez => UN uso y todas devuelven el
// vale usado (ninguna recibe «ya se usó»: es el mismo pedido repetido).
func TestLoyaltyRewards_redeemRaceSameOp(t *testing.T) {
	f := loyFixtures(t)
	f.rewards = bigLoyaltyRewards(t)
	r := f.grantOne(t, nil, time.Now().UTC())
	const n = 16
	errs := make([]error, n)
	got := make([]domain.LoyaltyReward, n)
	itdb.Race(n, func(i int) {
		got[i], errs[i] = f.rewards.Redeem(redeemReq(f.lia.ID, r.ID, "op-race-redeem", time.Now().UTC()))
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("pedido %d: %v", i, err)
		}
		if got[i].ID != r.ID || got[i].RedeemedAt == nil {
			t.Fatalf("pedido %d: %+v", i, got[i])
		}
	}
	if rows := f.count(t, `SELECT count(*) FROM loyalty_rewards WHERE redeem_op_id='op-race-redeem' AND redeemed_at IS NOT NULL`); rows != 1 {
		t.Fatalf("filas usadas con ese op id: %d", rows)
	}
	// Todos devuelven el MISMO instante de uso (el del ganador).
	for i := range got {
		if !got[i].RedeemedAt.Equal(*got[0].RedeemedAt) {
			t.Fatalf("pedido %d con otro redeemed_at", i)
		}
	}
}
