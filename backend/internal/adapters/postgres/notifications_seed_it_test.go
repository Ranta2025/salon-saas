package pgstore

import (
	"context"
	"testing"

	"salon-saas/internal/testutil/itdb"
)

// seedTenant deja un usuario y su salón listos para un IT (los ids son fijos y
// el INSERT es idempotente). La copia canónica viajó con los tests de avisos a
// internal/notifications/postgres; varios IT de este paquete la siguen usando.
func seedTenant(t *testing.T, salonID, userID string) {
	t.Helper()
	pool := itdb.Pool(t)
	defer pool.Close()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO users(id,name,email,pass_hash) VALUES($1,'Usuario IT',$2,'x')
		 ON CONFLICT (id) DO NOTHING`, userID, userID+"@test.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO salons(id,slug,name) VALUES($1,$1,'Salón IT') ON CONFLICT (id) DO NOTHING`, salonID)
	if err != nil {
		t.Fatalf("seed salon: %v", err)
	}
}
