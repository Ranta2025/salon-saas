// F1 del endurecimiento de F2g2 contra Postgres real (SALON_IT=1): dos owners
// que se dan de baja MUTUAMENTE al mismo tiempo no pueden dejar el salón sin
// dueño. Corre el caso de uso completo (app.MemberService + adaptador): el
// chequeo del último owner tiene que leer el estado DENTRO de la transacción,
// bajo el lock por salón, porque el row lock del UPDATE solo serializa dos bajas
// de la MISMA persona.
package pgstore

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

const f1RaceSalon = "s-it-f1-race"

// f1Round es el resultado de una ronda de bajas simultáneas: cuántas
// terminaron sin error (204), cuántas con 400 (ErrValidation: no se puede quitar
// al último dueño), cuántas con 404 (ErrNotFound: la otra ganó) y cualquier
// error crudo (que el test trata como falla).
//
// forbidden cuenta el 403: la baja que pierde puede encontrarse sin acceso si la
// lectura de su actor corre DESPUÉS del commit ganador (que acaba de darla de
// baja a ella). Es un rechazo limpio de la misma carrera, no un 500, y el salón
// conserva su dueño igual: nunca se acepta un final con cero owners.
type f1Round struct {
	ok, rejected, notFound, forbidden int
	raw                               error
}

// f1Fixture deja un salón con `owners` dueños ACTIVOS y sin turnos.
func f1Fixture(t *testing.T, owners int) (*pgxpool.Pool, app.MemberService) {
	t.Helper()
	pool := itPool(t)
	t.Cleanup(pool.Close)
	applyMigrations(t, pool)

	f2g2Exec(t, pool, `DELETE FROM salons WHERE id=$1`, f1RaceSalon)
	f2g2Exec(t, pool, `DELETE FROM users WHERE id LIKE 'u-it-f1-%'`)
	f2g2Exec(t, pool, `INSERT INTO salons(id,slug,name) VALUES($1,$1,'Salón F1')`, f1RaceSalon)
	for i := 0; i < owners; i++ {
		f2g2Exec(t, pool, `INSERT INTO users(id,name,email,pass_hash) VALUES($1,$2,$3,'x')`,
			fmt.Sprintf("u-it-f1-%d", i), fmt.Sprintf("Owner %d", i), fmt.Sprintf("owner%d@f1.test", i))
		f2g2Exec(t, pool, `INSERT INTO memberships(user_id,salon_id,role,attends,trade) VALUES($1,$2,'owner',false,'')`,
			fmt.Sprintf("u-it-f1-%d", i), f1RaceSalon)
	}
	repo := Memberships{Pool: pool}
	return pool, app.MemberService{
		Members: repo, Memberships: repo, Users: Users{Pool: pool}, Tx: repo,
	}
}

// f1Reset deja a todos los dueños del salón ACTIVOS (punto de partida de cada
// ronda; el estado lo escribe el SQL porque la baja en sí tiene sus tests).
func f1Reset(t *testing.T, pool *pgxpool.Pool, owners int) {
	t.Helper()
	f2g2Exec(t, pool, `UPDATE memberships SET ended_at=NULL WHERE salon_id=$1`, f1RaceSalon)
	if n := f2g2Scalar(t, pool, `SELECT count(*)::int FROM memberships WHERE salon_id=$1 AND ended_at IS NULL`, f1RaceSalon); n != owners {
		t.Fatalf("punto de partida de la ronda: %d owners activos, esperaba %d", n, owners)
	}
}

// f1ActiveOwners cuenta los dueños ACTIVOS del salón (la invariante del test).
func f1ActiveOwners(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	return f2g2Scalar(t, pool,
		`SELECT count(*)::int FROM memberships WHERE salon_id=$1 AND role='owner' AND ended_at IS NULL`, f1RaceSalon)
}

// f1BajasSimultaneas corre las bajas a la vez (barrera de arranque) y clasifica
// los resultados en los códigos que el contrato ve: 204 (sin error), 400
// (ErrValidation) y 404 (ErrNotFound).
func f1BajasSimultaneas(svc app.MemberService, actors []domain.Membership, targets []string) f1Round {
	var r f1Round
	errs := make([]error, len(actors))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range actors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = svc.Remove(actors[i], f1RaceSalon, targets[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		switch {
		case err == nil:
			r.ok++
		case errors.Is(err, domain.ErrValidation):
			r.rejected++
		case errors.Is(err, domain.ErrNotFound):
			r.notFound++
		case errors.Is(err, domain.ErrForbidden):
			r.forbidden++
		default:
			if r.raw == nil {
				r.raw = fmt.Errorf("baja %d: %w", i, err)
			}
		}
	}
	return r
}

// F1: dos owners que se dan de baja mutuamente, 30 rondas. SIEMPRE queda al
// menos un owner activo y ninguna baja devuelve un error crudo: una gana (204) y
// la otra recibe 400 (último dueño) o 404 (la fila ya no estaba).
func TestMembers_IT_Deactivate_lastOwnerRace_two_owners(t *testing.T) {
	pool, svc := f1Fixture(t, 2)

	const rounds = 30
	for vuelta := 0; vuelta < rounds; vuelta++ {
		f1Reset(t, pool, 2)
		// Los actores se leen ANTES de la carrera: los dos son owners activos
		// cuando arrancan (en la carrera real los dos JWT son válidos).
		a, err := svc.Members.FindMember("u-it-f1-0", f1RaceSalon)
		if err != nil {
			t.Fatalf("vuelta %d: actor 0: %v", vuelta, err)
		}
		b, err := svc.Members.FindMember("u-it-f1-1", f1RaceSalon)
		if err != nil {
			t.Fatalf("vuelta %d: actor 1: %v", vuelta, err)
		}
		r := f1BajasSimultaneas(svc,
			[]domain.Membership{a, b},
			[]string{"u-it-f1-1", "u-it-f1-0"}, // se dan de baja MUTUAMENTE
		)
		if r.raw != nil {
			t.Fatalf("vuelta %d: error crudo en la carrera: %v", vuelta, r.raw)
		}
		if r.ok != 1 || (r.rejected+r.notFound+r.forbidden) != 1 {
			t.Fatalf("vuelta %d: una baja tiene que ganar y la otra rechazarse (204/400, 204/404 o 204/403), hubo %+v", vuelta, r)
		}
		if activos := f1ActiveOwners(t, pool); activos < 1 {
			t.Fatalf("vuelta %d: el salón quedó SIN dueño activo (204=%d, 400=%d, 404=%d, 403=%d)",
				vuelta, r.ok, r.rejected, r.notFound, r.forbidden)
		}
	}
	if activos := f1ActiveOwners(t, pool); activos != 1 {
		t.Fatalf("después de una ronda queda exactamente un owner activo: %d", activos)
	}
}

// F1: con TRES owners, los tres dándose de baja mutuamente en el mismo instante
// dejan al menos uno activo (y nunca un error crudo).
func TestMembers_IT_Deactivate_lastOwnerRace_three_owners(t *testing.T) {
	pool, svc := f1Fixture(t, 3)

	const rounds = 20
	for vuelta := 0; vuelta < rounds; vuelta++ {
		f1Reset(t, pool, 3)
		actors := make([]domain.Membership, 3)
		for i := range actors {
			m, err := svc.Members.FindMember(fmt.Sprintf("u-it-f1-%d", i), f1RaceSalon)
			if err != nil {
				t.Fatalf("vuelta %d: actor %d: %v", vuelta, i, err)
			}
			actors[i] = m
		}
		// 0 baja a 1, 1 baja a 2, 2 baja a 0: en anillo.
		r := f1BajasSimultaneas(svc, actors, []string{"u-it-f1-1", "u-it-f1-2", "u-it-f1-0"})
		if r.raw != nil {
			t.Fatalf("vuelta %d: error crudo en la carrera: %v", vuelta, r.raw)
		}
		if activos := f1ActiveOwners(t, pool); activos < 1 {
			t.Fatalf("vuelta %d: el salón quedó SIN dueño activo (204=%d, 400=%d, 404=%d, 403=%d)",
				vuelta, r.ok, r.rejected, r.notFound, r.forbidden)
		}
		if r.ok < 1 {
			t.Fatalf("vuelta %d: con tres owners al menos una baja tiene que entrar: %+v", vuelta, r)
		}
	}
	if activos := f1ActiveOwners(t, pool); activos < 1 {
		t.Fatalf("después de las rondas queda al menos un owner activo: %d", activos)
	}
}

// F1: el lock por salón no cambia nada para la baja de SIEMPRE (una persona que
// no es owner, sin turnos futuros) ni para salones distintos, que no se esperan
// entre sí.
func TestMembers_IT_Deactivate_removeUnderLock_preserved(t *testing.T) {
	pool, svc := f1Fixture(t, 2)
	f1Reset(t, pool, 2)
	if err := svc.Remove(actorOf(t, svc, "u-it-f1-0"), f1RaceSalon, "u-it-f1-1"); err != nil {
		t.Fatalf("baja de otro owner con dos activos: %v", err)
	}
	if activos := f1ActiveOwners(t, pool); activos != 1 {
		t.Fatalf("la baja se aplicó: %d owners activos", activos)
	}
	// La misma baja otra vez: para esta ruta ya no está (404).
	if err := svc.Remove(actorOf(t, svc, "u-it-f1-0"), f1RaceSalon, "u-it-f1-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("baja repetida: %v", err)
	}
	// Y el lock se libera: una segunda baja del mismo salón no queda esperando.
	done := make(chan error, 1)
	go func() {
		done <- svc.Remove(actorOf(t, svc, "u-it-f1-0"), f1RaceSalon, "u-it-f1-0")
	}()
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("el último owner activo: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("el lock por salón quedó tomado: la baja no termina")
	}
}

// actorOf lee la membresía activa del actor (lo que hace el handler con el JWT).
func actorOf(t *testing.T, svc app.MemberService, userID string) domain.Membership {
	t.Helper()
	m, err := svc.Memberships.Find(userID, f1RaceSalon)
	if err != nil {
		t.Fatalf("actor %s: %v", userID, err)
	}
	return m
}
