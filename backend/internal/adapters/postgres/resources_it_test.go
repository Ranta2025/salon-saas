// Equipamiento con capacidad contra Postgres (requiere SALON_IT=1 y una DB
// migrada). Prueba las garantías DURAS de la migración 020: unicidad de nombres
// sin importar mayúsculas, la EXCLUDE por unidad (dos reservas simultáneas nunca
// se llevan la última), la FK RESTRICT al borrar y el scope por salón.
// Corre con: SALON_IT=1 DATABASE_URL=... go test ./internal/adapters/postgres/
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	bookingsapi "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
)

// itResourcesEnv deja un salón limpio y devuelve los repos de equipo y catálogo.
func itResourcesEnv(t *testing.T, pool *pgxpool.Pool, salonID string) (Resources, Services) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `INSERT INTO salons(id,slug,name) VALUES($1,$1,'Salón '||$1)
		ON CONFLICT (id) DO NOTHING`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM bookings WHERE salon_id=$1`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM services WHERE salon_id=$1`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM resource_units WHERE salon_id=$1`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM resource_types WHERE salon_id=$1`, salonID)
	return Resources{Pool: pool}, Services{Pool: pool}
}

// itUser deja un usuario miembro del salón.
func itUser(t *testing.T, pool *pgxpool.Pool, userID, salonID string, role domain.Role, attends bool) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `INSERT INTO users(id,name,email,pass_hash) VALUES($1,$1,$1||'@test.com','x')
		ON CONFLICT (id) DO NOTHING`, userID)
	_, _ = pool.Exec(ctx, `INSERT INTO memberships(user_id,salon_id,role,attends) VALUES($1,$2,$3,$4)
		ON CONFLICT (user_id,salon_id) DO UPDATE SET role=EXCLUDED.role, attends=EXCLUDED.attends`,
		userID, salonID, string(role), attends)
}

// itType crea un tipo con `count` unidades en la DB.
func itType(t *testing.T, repo Resources, salonID, name string, count int) (domain.ResourceType, []domain.ResourceUnit) {
	t.Helper()
	rt, err := domain.NewResourceType(salonID, name)
	if err != nil {
		t.Fatalf("tipo: %v", err)
	}
	units := make([]domain.ResourceUnit, 0, count)
	for i := 1; i <= count; i++ {
		u, err := domain.NewResourceUnit(salonID, rt.ID, rt.UnitName(i))
		if err != nil {
			t.Fatalf("unidad: %v", err)
		}
		units = append(units, u)
	}
	if err := repo.CreateType(rt, units); err != nil {
		t.Fatalf("crear tipo: %v", err)
	}
	return rt, units
}

// S-F2e-1: la 020 deja las columnas y la EXCLUDE con el mismo patrón y nombre de
// guarda que bookings_no_overlap.
func TestIT_resources_migration(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	for _, name := range []string{"bookings_no_overlap", "bookings_resource_no_overlap"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=$1)`, name).Scan(&exists); err != nil {
			t.Fatalf("constraint %s: %v", name, err)
		}
		if !exists {
			t.Fatalf("falta la constraint %s (migración 002/020)", name)
		}
	}
	for _, col := range []struct{ table, column string }{
		{"services", "resource_type_id"},
		{"bookings", "resource_unit_id"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			 WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`,
			col.table, col.column).Scan(&exists); err != nil {
			t.Fatalf("%s.%s: %v", col.table, col.column, err)
		}
		if !exists {
			t.Fatalf("falta la columna %s.%s", col.table, col.column)
		}
	}
}

// S-F2e-2: el nombre es único por salón sin importar mayúsculas y cada salón
// tiene su propio equipamiento (scope por salon_id).
func TestIT_resources_names_and_scope(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	repo, _ := itResourcesEnv(t, pool, "s-f2e-names")

	rt, units := itType(t, repo, "s-f2e-names", "Silla", 1)
	other, _ := itResourcesEnv(t, pool, "s-f2e-other")
	if _, err := other.FindType("s-f2e-other", rt.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el tipo de otro salón no existe: %v", err)
	}
	if _, err := other.FindUnit("s-f2e-other", units[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la unidad de otro salón no existe: %v", err)
	}
	if list, _ := other.ListTypes("s-f2e-other"); len(list) != 0 {
		t.Fatalf("el otro salón no tiene tipos: %+v", list)
	}

	dup, _ := domain.NewResourceType("s-f2e-names", "  silla ")
	dup.ID = "dup-" + rt.ID
	if err := repo.CreateType(dup, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("nombre duplicado case-insensitive: %v", err)
	}
	if err := repo.SaveType(domain.ResourceType{ID: rt.ID, SalonID: "s-f2e-names", Name: "SILLA"}); err != nil {
		t.Fatalf("renombrar a sí mismo: %v", err)
	}
	// La unidad también es única dentro del tipo.
	clash, _ := domain.NewResourceUnit("s-f2e-names", rt.ID, "silla 1")
	clash.ID = "dup-unit"
	if err := repo.SaveUnit(clash); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("unidad duplicada: %v", err)
	}
	// El mismo nombre en OTRO salón no choca.
	if _, err := domain.NewResourceType("s-f2e-other", "silla"); err != nil {
		t.Fatalf("tipo de otro salón: %v", err)
	}
	itType(t, other, "s-f2e-other", "silla", 1)
}

// S-F2e-1/S-F2e-4: dos reservas simultáneas a la MISMA unidad y hora: la
// EXCLUDE deja entrar una y la otra vuelve como conflicto; cancelar libera.
func TestIT_resources_unit_exclusion(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	repo, _ := itResourcesEnv(t, pool, "s-f2e-excl")
	itUser(t, pool, "prof-f2e-excl", "s-f2e-excl", domain.RoleProfessional, true)
	_, units := itType(t, repo, "s-f2e-excl", "Puesto", 1)
	bookings := Bookings{Pool: pool}

	day := time.Date(2027, 3, 2, 10, 0, 0, 0, time.UTC)
	errs := saveSameUnit(t, bookings, "s-f2e-excl", "prof-f2e-excl", units[0].ID, day, 2)
	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrAlreadyExists):
			conflicts++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("esperaba 1 turno por unidad + 1 conflicto, dio wins=%d conflicts=%d errs=%v", wins, conflicts, errs)
	}

	// Cancelar libera la unidad: la EXCLUDE solo mira las reservadas.
	list, err := bookings.ListRange("s-f2e-excl", day.Add(-time.Hour), day.Add(2*time.Hour))
	if err != nil || len(list) != 1 {
		t.Fatalf("turno ganador: %+v %v", list, err)
	}
	cancelled, err := list[0].SetStatus(domain.BookingCancelled)
	if err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if err := bookings.Update(cancelled); err != nil {
		t.Fatalf("guardar cancelación: %v", err)
	}
	again, err := domain.NewBooking("s-f2e-excl", "prof-f2e-excl", "Otra", "", day, day.Add(time.Hour))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	again.ResourceUnitID = units[0].ID
	if err := bookings.Save(again); err != nil {
		t.Fatalf("la unidad cancelada se puede volver a tomar: %v", err)
	}
}

// S-F2e-3: no se borra un tipo que un servicio usa o que tiene una unidad con
// turnos (FK RESTRICT: ErrAlreadyExists, nunca 500); uno libre se borra entero.
func TestIT_resources_delete_restrict(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	repo, services := itResourcesEnv(t, pool, "s-f2e-del")
	itUser(t, pool, "prof-f2e-del", "s-f2e-del", domain.RoleProfessional, true)

	used, _ := itType(t, repo, "s-f2e-del", "Puesto", 1)
	svc, err := domain.NewService("s-f2e-del", "Corte", 30, 100000, 0)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	svc.ResourceTypeID = used.ID
	if err := services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	if err := repo.DeleteType("s-f2e-del", used.ID); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("tipo usado por un servicio: %v", err)
	}
	stored, err := services.FindByID("s-f2e-del", svc.ID)
	if err != nil || stored.ResourceTypeID != used.ID {
		t.Fatalf("el servicio guarda su equipo: %+v %v", stored, err)
	}

	busy, units := itType(t, repo, "s-f2e-del", "Silla", 1)
	bookings := Bookings{Pool: pool}
	day := time.Date(2027, 3, 3, 10, 0, 0, 0, time.UTC)
	b, _ := domain.NewBooking("s-f2e-del", "prof-f2e-del", "Clienta", "", day, day.Add(time.Hour))
	b.ResourceUnitID = units[0].ID
	if err := bookings.Save(b); err != nil {
		t.Fatalf("turno con unidad: %v", err)
	}
	if err := repo.DeleteType("s-f2e-del", busy.ID); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("tipo con turnos: %v", err)
	}
	if n, err := repo.UnitsWithBookings("s-f2e-del", busy.ID); err != nil || n != 1 {
		t.Fatalf("unidades con turnos: %d %v", n, err)
	}

	free, _ := itType(t, repo, "s-f2e-del", "Lavacabezas", 2)
	if err := repo.DeleteType("s-f2e-del", free.ID); err != nil {
		t.Fatalf("tipo libre: %v", err)
	}
	if units, _ := repo.ListUnits("s-f2e-del", free.ID); len(units) != 0 {
		t.Fatalf("las unidades se van con el tipo: %+v", units)
	}
}

// S-F2e-3: desactivar una unidad la saca de ActiveUnits y conserva los turnos ya
// agendados; el conteo de turnos futuros es el aviso de la pantalla.
func TestIT_resources_deactivate_keeps_bookings(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	repo, _ := itResourcesEnv(t, pool, "s-f2e-off")
	itUser(t, pool, "prof-f2e-off", "s-f2e-off", domain.RoleProfessional, true)
	rt, units := itType(t, repo, "s-f2e-off", "Puesto", 2)

	bookings := Bookings{Pool: pool}
	day := time.Now().UTC().Truncate(time.Hour).Add(48 * time.Hour)
	b, _ := domain.NewBooking("s-f2e-off", "prof-f2e-off", "Clienta", "", day, day.Add(time.Hour))
	b.ResourceUnitID = units[0].ID
	if err := bookings.Save(b); err != nil {
		t.Fatalf("turno: %v", err)
	}

	off := units[0]
	off.Active = false
	if err := repo.SaveUnit(off); err != nil {
		t.Fatalf("desactivar: %v", err)
	}
	if active, _ := repo.ActiveUnits("s-f2e-off", rt.ID); len(active) != 1 || active[0].ID != units[1].ID {
		t.Fatalf("la desactivada no se ofrece: %+v", active)
	}
	stored, err := bookings.FindByID("s-f2e-off", b.ID)
	if err != nil || stored.ResourceUnitID != units[0].ID {
		t.Fatalf("el turno conserva su unidad: %+v %v", stored, err)
	}
	if n, err := repo.UnitFutureBookings("s-f2e-off", units[0].ID, time.Now().UTC()); err != nil || n != 1 {
		t.Fatalf("turnos futuros que usan la unidad: %d %v", n, err)
	}
}

// S-F2e-4: dos reservas públicas simultáneas por la ÚLTIMA unidad libre: una
// gana y la otra recibe 409 (o cae en otro par libre); nunca dos en una unidad.
func TestIT_public_booking_concurrent_last_unit(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	env := newITBookingEnv(t, pool, "s-f2e-race", 5, 3)
	start := time.Date(2027, 4, 6, 10, 0, 0, 0, time.UTC)

	// 5 profesionales y 3 unidades: se ocupan 2 unidades (y 2 profesionales) en
	// secuencia, sin carrera; quedan 3 profesionales libres y 1 unidad.
	for i := 0; i < 2; i++ {
		if _, err := env.book.Book("s-f2e-race", itInput(env.serviceID, start, fmt.Sprintf("Previa %d", i))); err != nil {
			t.Fatalf("reserva previa %d: %v", i, err)
		}
	}
	results := raceBook(t, env, start, 2)
	if results.wins != 1 || results.conflicts != 1 {
		t.Fatalf("por la última unidad: 1 gana + 1 conflicto, dio %+v (%v)", results, results.errs)
	}
	assertUnitNotDoubled(t, pool, "s-f2e-race", start, 3)
}

// S-F2e-4: el MISMO caso con el último PROFESIONAL (1 profesional, 3 unidades):
// una reserva gana y la otra 409 (no hay otro par posible).
func TestIT_public_booking_concurrent_last_professional(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	env := newITBookingEnv(t, pool, "s-f2e-race-prof", 1, 3)
	start := time.Date(2027, 4, 7, 10, 0, 0, 0, time.UTC)

	results := raceBook(t, env, start, 2)
	if results.wins != 1 || results.conflicts != 1 {
		t.Fatalf("por el último profesional: 1 gana + 1 conflicto, dio %+v (%v)", results, results.errs)
	}
	assertUnitNotDoubled(t, pool, "s-f2e-race-prof", start, 1)
}

// S-F2e-4: con 2 profesionales y 2 unidades entran las dos reservas simultáneas:
// el caso de uso reintenta con otro par en vez de devolver un 500.
func TestIT_public_booking_concurrent_two_pairs(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	env := newITBookingEnv(t, pool, "s-f2e-race2", 2, 2)
	start := time.Date(2027, 4, 8, 10, 0, 0, 0, time.UTC)

	results := raceBook(t, env, start, 2)
	if results.wins != 2 || results.conflicts != 0 {
		t.Fatalf("2 pares libres: entran las dos, dio %+v (%v)", results, results.errs)
	}
	assertUnitNotDoubled(t, pool, "s-f2e-race2", start, 2)
}

// --- fixture de la reserva pública contra Postgres ---

type itBookingEnv struct {
	book      bookingsapi.PublicBookingService
	slug      string
	serviceID string
}

// newITBookingEnv prepara un salón con `professionals` profesionales que atienden y
// hacen el servicio, un tipo de equipo con `units` unidades y el servicio de 60
// minutos que lo usa.
func newITBookingEnv(t *testing.T, pool *pgxpool.Pool, salonID string, professionals, units int) *itBookingEnv {
	t.Helper()
	repo, services := itResourcesEnv(t, pool, salonID)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `UPDATE salons SET timezone='UTC', open_time='09:00', close_time='18:00', slot_step_min=30
		WHERE id=$1`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM professional_services WHERE salon_id=$1`, salonID)
	_, _ = pool.Exec(ctx, `DELETE FROM memberships WHERE salon_id=$1`, salonID)

	rt, _ := itType(t, repo, salonID, "Puesto", units)
	svc, err := domain.NewService(salonID, "Corte", 60, 100000, 0)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	svc.ResourceTypeID = rt.ID
	if err := services.Save(svc); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	for i := 0; i < professionals; i++ {
		userID := fmt.Sprintf("prof-%s-%d", salonID, i)
		itUser(t, pool, userID, salonID, domain.RoleProfessional, true)
		if _, err := pool.Exec(ctx,
			`INSERT INTO professional_services(salon_id,user_id,service_id) VALUES($1,$2,$3)`,
			salonID, userID, svc.ID); err != nil {
			t.Fatalf("asignar servicio: %v", err)
		}
	}

	return &itBookingEnv{
		slug:      salonID,
		serviceID: svc.ID,
		book: bookingsapi.PublicBookingService{
			Salons: Salons{Pool: pool}, Services: Services{Pool: pool}, Settings: Salons{Pool: pool},
			Staff: Memberships{Pool: pool}, Bookings: Bookings{Pool: pool},
			Professionals: ProfessionalServices{Pool: pool}, Resources: repo,
		},
	}
}

func itInput(serviceID string, start time.Time, name string) bookingsapi.PublicBookingInput {
	return bookingsapi.PublicBookingInput{ServiceID: serviceID, ClientName: name, StartsAt: start}
}

type raceResult struct {
	wins, conflicts, others int
	errs                    []error
}

// raceBook dispara `n` reservas públicas simultáneas al mismo horario.
func raceBook(t *testing.T, env *itBookingEnv, start time.Time, n int) raceResult {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = env.book.Book(env.slug, itInput(env.serviceID, start, fmt.Sprintf("Clienta %d", i)))
		}(i)
	}
	wg.Wait()

	out := raceResult{errs: errs}
	for _, err := range errs {
		switch {
		case err == nil:
			out.wins++
		case errors.Is(err, domain.ErrAlreadyExists):
			out.conflicts++
		default:
			out.others++
		}
	}
	return out
}

// saveSameUnit guarda `n` turnos concurrentes para la misma unidad y horario.
func saveSameUnit(t *testing.T, bookings Bookings, salonID, professionalID, unitID string, start time.Time, n int) []error {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := domain.NewBooking(salonID, professionalID, fmt.Sprintf("Clienta %d", i), "", start, start.Add(time.Hour))
			if err != nil {
				errs[i] = err
				return
			}
			b.ResourceUnitID = unitID
			errs[i] = bookings.Save(b)
		}(i)
	}
	wg.Wait()
	return errs
}

// assertUnitNotDoubled verifica en la DB que ninguna unidad tenga dos turnos
// reservados que se pisen y que haya exactamente `want` turnos en ese horario.
func assertUnitNotDoubled(t *testing.T, pool *pgxpool.Pool, salonID string, start time.Time, want int) {
	t.Helper()
	ctx := context.Background()
	var doubled int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings a
		 JOIN bookings b ON a.salon_id=b.salon_id AND a.resource_unit_id=b.resource_unit_id AND a.id<b.id
		 WHERE a.salon_id=$1 AND a.status='booked' AND b.status='booked'
		   AND a.resource_unit_id IS NOT NULL
		   AND tstzrange(a.starts_at,a.ends_at) && tstzrange(b.starts_at,b.ends_at)`,
		salonID).Scan(&doubled); err != nil {
		t.Fatalf("verificar doble unidad: %v", err)
	}
	if doubled != 0 {
		t.Fatalf("hay %d pares de turnos en la misma unidad y hora", doubled)
	}
	var total int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE salon_id=$1 AND status='booked' AND starts_at=$2`,
		salonID, start).Scan(&total); err != nil {
		t.Fatalf("contar turnos: %v", err)
	}
	if total != want {
		t.Fatalf("turnos en el horario: esperaba %d, hay %d", want, total)
	}
}

// Hallazgo del humo del orquestador: con 8 reservas simultáneas por 3 puestos y
// 5 profesionales, Postgres detectaba un interbloqueo entre las dos EXCLUDE (la
// de la profesional y la de la unidad) y la API devolvía un 500. Un interbloqueo
// es un CONFLICTO de reserva: tiene que reintentarse o terminar en 409, nunca
// en un error interno. Se repite porque la carrera no es determinista.
func TestIT_public_booking_many_concurrent_never_internal_error(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	for round := 0; round < 6; round++ {
		slug := fmt.Sprintf("s-f2e-many-%d", round)
		env := newITBookingEnv(t, pool, slug, 5, 3)
		start := time.Date(2027, 5, 4, 10, 0, 0, 0, time.UTC).Add(time.Duration(round) * time.Hour)
		results := raceBook(t, env, start, 8)
		if results.others != 0 {
			t.Fatalf("ronda %d: %d errores que no son conflicto (un interbloqueo no puede ser un 500): %v", round, results.others, results.errs)
		}
		if results.wins != 3 || results.conflicts != 5 {
			t.Fatalf("ronda %d: 3 puestos = 3 reservas y 5 conflictos, dio %+v (%v)", round, results, results.errs)
		}
		assertUnitNotDoubled(t, pool, slug, start, 3)
	}
}
