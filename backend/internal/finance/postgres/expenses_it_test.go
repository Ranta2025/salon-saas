// Integración contra Postgres real de los gastos (F3b, D3/D4/D5). Corre con:
//
//	SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/ -run ExpensesIT -v
//
// Lo que SOLO se prueba contra la DB: los CHECK y las FK de la migración 026, el
// scope por salón en las cuatro operaciones, el orden y el tope del listado, y
// que las lecturas concurrentes con altas y borrados no fallen.
package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

// expensesFixtures deja tres salones limpios: el principal, uno de OTRO tenant y
// uno sin movimientos. Ids fijos para que el test sea re-ejecutable.
func expensesFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	// Los dos fixtures de F3b (gastos y ganancia) comparten la limpieza: el
	// reporte inserta gastos, así que borrar los salones primero (cascade) deja a
	// los usuarios libres para borrarse aunque una corrida anterior haya dejado
	// filas del otro archivo.
	for _, q := range []string{
		`DELETE FROM salons WHERE id LIKE 's-it-exp%' OR id LIKE 's-it-profit%'`,
		`DELETE FROM users WHERE id LIKE 'u-it-exp%' OR id LIKE 'u-it-profit%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-exp-owner','Dueña Exp','exp-owner@test.com','x')`,
		`INSERT INTO users(id,name,email,pass_hash) VALUES('u-it-exp-otro','Otro Exp','exp-otro@test.com','x')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-exp','s-it-exp','Salón Gastos IT','America/Mexico_City')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-exp2','s-it-exp2','Salón Gastos IT 2','America/Mexico_City')`,
		`INSERT INTO salons(id,slug,name,timezone) VALUES('s-it-exp-vacio','s-it-exp-vacio','Salón Gastos IT Vacío','America/Mexico_City')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mems := pgstore.Memberships{Pool: pool}
	for _, m := range []domain.Membership{
		{UserID: "u-it-exp-owner", SalonID: "s-it-exp", Role: domain.RoleOwner},
		{UserID: "u-it-exp-otro", SalonID: "s-it-exp2", Role: domain.RoleOwner},
	} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	return pool, ctx
}

func expExpense(id, salonID, category, spentOn string, amount int, created time.Time) domain.Expense {
	return domain.Expense{
		ID: id, SalonID: salonID, Category: domain.ExpenseCategory(category),
		AmountCents: amount, SpentOn: spentOn, Description: "gasto " + id,
		CreatedBy: "u-it-exp-owner", CreatedAt: created,
	}
}

// Ciclo completo con scope por salón: el gasto de otro tenant no se encuentra,
// no se edita y no se borra (ErrNotFound -> 404 en la API).
func TestExpensesIT_crud_y_aislamiento_de_tenant(t *testing.T) {
	pool, _ := expensesFixtures(t)
	repo := Expenses{Pool: pool}
	now := time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

	e := expExpense("e-it-exp-1", "s-it-exp", "rent", "2026-03-19", 150000, now)
	if err := repo.Save(e); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	got, err := repo.FindByID("s-it-exp", e.ID)
	if err != nil {
		t.Fatalf("leer: %v", err)
	}
	if got.Category != domain.ExpenseRent || got.AmountCents != 150000 ||
		got.SpentOn != "2026-03-19" || got.Description != e.Description ||
		got.CreatedBy != "u-it-exp-owner" || !got.CreatedAt.Equal(now) {
		t.Fatalf("leído = %+v", got)
	}

	// El gasto de otro salón es ErrNotFound en las tres operaciones.
	if _, err := repo.FindByID("s-it-exp2", e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("leer de otro salón = %v", err)
	}
	ajeno := got
	ajeno.SalonID = "s-it-exp2"
	ajeno.AmountCents = 1
	if err := repo.Update(ajeno); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("editar de otro salón = %v", err)
	}
	if err := repo.Delete("s-it-exp2", e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar de otro salón = %v", err)
	}
	if _, err := repo.FindByID("s-it-exp", e.ID); err != nil {
		t.Fatalf("el gasto ajeno desapareció: %v", err)
	}

	// Update reemplaza los cuatro campos y conserva creador y alta.
	upd := got
	upd.Category = domain.ExpenseUtilities
	upd.AmountCents = 1
	upd.SpentOn = "2026-03-01"
	upd.Description = ""
	if err := repo.Update(upd); err != nil {
		t.Fatalf("editar: %v", err)
	}
	got, err = repo.FindByID("s-it-exp", e.ID)
	if err != nil {
		t.Fatalf("leer editado: %v", err)
	}
	if got.Category != domain.ExpenseUtilities || got.AmountCents != 1 ||
		got.SpentOn != "2026-03-01" || got.Description != "" ||
		got.CreatedBy != "u-it-exp-owner" || !got.CreatedAt.Equal(now) {
		t.Fatalf("editado = %+v", got)
	}

	// Delete físico: después no existe (ErrNotFound, nunca un borrado silencioso).
	if err := repo.Delete("s-it-exp", e.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if err := repo.Delete("s-it-exp", e.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar dos veces = %v", err)
	}
}

// Listado: rango INCLUSIVO, orden por spent_on y created_at descendentes y tope.
func TestExpensesIT_listado_rango_orden_y_tope(t *testing.T) {
	pool, _ := expensesFixtures(t)
	repo := Expenses{Pool: pool}
	base := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	for i, e := range []domain.Expense{
		expExpense("e-it-exp-l1", "s-it-exp", "rent", "2026-03-01", 100, base),
		expExpense("e-it-exp-l2", "s-it-exp", "rent", "2026-03-15", 200, base),
		expExpense("e-it-exp-l3", "s-it-exp", "rent", "2026-03-15", 300, base.Add(time.Hour)),
		expExpense("e-it-exp-l4", "s-it-exp", "rent", "2026-03-31", 400, base),
		expExpense("e-it-exp-l5", "s-it-exp", "rent", "2026-02-28", 500, base),
		// Otro salón: nunca aparece.
		expExpense("e-it-exp-l6", "s-it-exp2", "rent", "2026-03-15", 999, base.Add(2*time.Hour)),
	} {
		if err := repo.Save(e); err != nil {
			t.Fatalf("guardar %d: %v", i, err)
		}
	}

	list, truncated, err := repo.ListRange("s-it-exp", "2026-03-01", "2026-03-31", 500)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if truncated || len(list) != 4 {
		t.Fatalf("listado = %d filas, truncated=%v", len(list), truncated)
	}
	// 31/3, después los dos del 15/3 con created_at descendente, después el 1/3.
	want := []string{"e-it-exp-l4", "e-it-exp-l3", "e-it-exp-l2", "e-it-exp-l1"}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("orden = %v, esperaba %v", idsOf(list), want)
		}
	}

	// El borde es INCLUSIVO en los dos extremos.
	list, _, err = repo.ListRange("s-it-exp", "2026-03-15", "2026-03-15", 500)
	if err != nil || len(list) != 2 {
		t.Fatalf("un solo día: %d filas, err=%v", len(list), err)
	}

	// El tope: con limit=2 hay más filas -> truncated.
	list, truncated, err = repo.ListRange("s-it-exp", "2026-03-01", "2026-03-31", 2)
	if err != nil || !truncated || len(list) != 2 {
		t.Fatalf("tope: %d filas, truncated=%v, err=%v", len(list), truncated, err)
	}
}

func idsOf(list []domain.Expense) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

// Los CHECK y las FK de la migración 026 son la red de seguridad de los límites
// (otro camino de escritura no puede guardar un gasto imposible).
func TestExpensesIT_check_y_fk(t *testing.T) {
	pool, ctx := expensesFixtures(t)
	insert := func(id, salonID, category string, amount int, spentOn, description, createdBy string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO expenses(id,salon_id,category,amount_cents,spent_on,description,created_by)
			 VALUES($1,$2,$3,$4,$5::date,$6,$7)`,
			id, salonID, category, amount, spentOn, description, createdBy)
		return err
	}
	cases := []struct {
		name                 string
		category             string
		amount               int
		spentOn, description string
		createdBy            string
		want                 bool
	}{
		{"válido", "rent", 1, "2026-03-10", "", "u-it-exp-owner", true},
		{"tope válido", "other", 2000000000, "2026-03-10", "", "u-it-exp-owner", true},
		{"categoría fuera del catálogo", "salary", 100, "2026-03-10", "", "u-it-exp-owner", false},
		{"monto cero", "rent", 0, "2026-03-10", "", "u-it-exp-owner", false},
		{"monto negativo", "rent", -1, "2026-03-10", "", "u-it-exp-owner", false},
		{"monto sobre el tope", "rent", 2000000001, "2026-03-10", "", "u-it-exp-owner", false},
		{"fecha anterior a 2000", "rent", 100, "1999-12-31", "", "u-it-exp-owner", false},
		{"descripción 201", "rent", 100, "2026-03-10", strings.Repeat("ñ", 201), "u-it-exp-owner", false},
		{"descripción 200", "rent", 100, "2026-03-10", strings.Repeat("ñ", 200), "u-it-exp-owner", true},
		{"usuario creador inexistente", "rent", 100, "2026-03-10", "", "u-it-exp-nadie", false},
	}
	for i, tc := range cases {
		id := "e-it-exp-c" + string(rune('a'+i))
		err := insert(id, "s-it-exp", tc.category, tc.amount, tc.spentOn, tc.description, tc.createdBy)
		if tc.want && err != nil {
			t.Fatalf("%s debería entrar: %v", tc.name, err)
		}
		if !tc.want && err == nil {
			t.Fatalf("%s debería ser rechazado por la DB", tc.name)
		}
	}
	// FK del salón: un gasto no puede colgar de un salón inexistente.
	if err := insert("e-it-exp-nosal", "s-it-exp-nadie", "rent", 100, "2026-03-10", "", "u-it-exp-owner"); err == nil {
		t.Fatal("un salón inexistente debería rechazarse")
	}
	// El índice del listado existe.
	var hasIndex bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=current_schema()
		                AND indexname='expenses_salon_spent_idx')`).Scan(&hasIndex); err != nil {
		t.Fatalf("verificar índice: %v", err)
	}
	if !hasIndex {
		t.Fatal("falta el índice expenses_salon_spent_idx (migración 026)")
	}
}

// La migración 026 aplicada OTRA VEZ sobre una base con datos no cambia ninguna
// fila (idempotencia con datos previos, no solo en limpio).
func TestExpensesIT_migracion_idempotente_con_datos(t *testing.T) {
	pool, ctx := expensesFixtures(t)
	repo := Expenses{Pool: pool}
	e := expExpense("e-it-exp-mig", "s-it-exp", "supplies", "2026-03-19", 1234,
		time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC))
	if err := repo.Save(e); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	var before string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(md5(string_agg(t::text, '|' ORDER BY t.id)), '') FROM expenses t`).Scan(&before); err != nil {
		t.Fatalf("md5 previo: %v", err)
	}

	sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "026_expenses.sql"))
	if err != nil {
		t.Fatalf("leer 026: %v", err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("aplicar 026 (pasada %d) sobre datos: %v", pass, err)
		}
	}
	var after string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(md5(string_agg(t::text, '|' ORDER BY t.id)), '') FROM expenses t`).Scan(&after); err != nil {
		t.Fatalf("md5 posterior: %v", err)
	}
	if before != after {
		t.Fatalf("la migración cambió los datos: %s != %s", before, after)
	}
	// Y la tabla entera sigue igual: el gasto se lee tal cual.
	got, err := repo.FindByID("s-it-exp", e.ID)
	if err != nil || got.AmountCents != 1234 || got.SpentOn != "2026-03-19" {
		t.Fatalf("el gasto cambió tras la migración: %+v %v", got, err)
	}
}

// Lecturas y escrituras concurrentes: listar mientras se crea y se borra no puede
// devolver un 500 (ni una lectura a medias).
func TestExpensesIT_lecturas_concurrentes(t *testing.T) {
	pool, _ := expensesFixtures(t)
	repo := Expenses{Pool: pool}
	for i := 0; i < 10; i++ {
		if err := repo.Save(expExpense("e-it-exp-race-"+string(rune('a'+i)), "s-it-exp", "rent",
			"2026-03-10", 100+i, time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC))); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				id := "e-it-exp-live-" + string(rune('a'+w)) + string(rune('a'+i%10))
				if err := repo.Save(expExpense(id, "s-it-exp", "other", "2026-03-11", 50, time.Now().UTC())); err != nil {
					errs <- err
				}
				if _, _, err := repo.ListRange("s-it-exp", "2026-03-01", "2026-03-31", 500); err != nil {
					errs <- err
				}
				if err := repo.Delete("s-it-exp", id); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrencia: %v", err)
	}
}
