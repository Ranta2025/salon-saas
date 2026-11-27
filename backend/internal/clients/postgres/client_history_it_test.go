// Historial de la clienta contra Postgres real (F5, D6/D7): la foto es una sola
// transacción, el gasto suma SOLO las ventas vinculadas, las listas tienen
// LIMIT 20 y la última visita es el día LOCAL del salón.
// Corre con: SALON_IT=1 go test ./internal/adapters/postgres/ -run TestClientHistory -v
package postgres

import (
	"context"
	"strconv"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

func TestClientHistory_foto_gasto_y_dia_local(t *testing.T) {
	pool := itdb.Pool(t)
	defer pool.Close()
	f5Seed(t, pool, "s-f5h", "u-f5h")
	f5Seed(t, pool, "s-f5i", "u-f5i")

	repo := Clients{Pool: pool}
	c, err := domain.NewClientWithAllergies("s-f5h", "Lía Fernández", "11 5555-1234", "lia@mail.com", "nota", "nickel")
	if err != nil {
		t.Fatalf("ficha: %v", err)
	}
	if err := repo.Save(c); err != nil {
		t.Fatalf("guardar ficha: %v", err)
	}
	// Las alergias viajan en el round trip y el listado las trae.
	got, err := repo.FindByID("s-f5h", c.ID)
	if err != nil || got.Allergies != "nickel" {
		t.Fatalf("allergies de la ficha: %+v %v", got, err)
	}
	if list, err := repo.List("s-f5h"); err != nil || len(list) != 1 || list[0].Allergies != "nickel" {
		t.Fatalf("listado con alergias: %+v %v", list, err)
	}

	// Turnos: dos `done` cerca de la medianoche LOCAL de México (UTC-6), uno
	// cancelado, uno no-show, dos `booked` futuros y uno `booked` viejo.
	done1 := time.Date(2026, 3, 7, 23, 0, 0, 0, time.UTC) // 17:00 local del 7
	done2 := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)  // 23:00 local del 7
	future1 := time.Date(2026, 3, 20, 16, 0, 0, 0, time.UTC)
	future2 := time.Date(2026, 3, 21, 16, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	f5Booking(t, pool, "bh-done1", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "done", done1)
	f5Booking(t, pool, "bh-done2", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "done", done2)
	f5Booking(t, pool, "bh-cancel", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "cancelled", future1.Add(48*time.Hour))
	f5Booking(t, pool, "bh-noshow", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "no_show", done1.Add(72*time.Hour))
	f5Booking(t, pool, "bh-next1", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "booked", future1)
	f5Booking(t, pool, "bh-next2", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "booked", future2)
	f5Booking(t, pool, "bh-old", "s-f5h", "u-f5h", "Lía", "11 5555-1234", "booked", done1.Add(-72*time.Hour))
	// Y 22 turnos `done` más (el LIMIT de la lista es 20; los conteos no).
	for i := 0; i < 22; i++ {
		f5Booking(t, pool, "bh-many-"+strconv.Itoa(i), "s-f5h", "u-f5h", "Lía", "11 5555-1234", "done",
			time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(i)*24*time.Hour))
	}
	// Todas las filas menos `bh-old` son de ESTA ficha.
	if _, err := pool.Exec(context.Background(),
		`UPDATE bookings SET client_id=$1 WHERE salon_id='s-f5h' AND id <> 'bh-old'`, c.ID); err != nil {
		t.Fatalf("vincular turnos: %v", err)
	}

	// Ventas: dos vinculadas (cuentan) y una sin vínculo (no se atribuye).
	f5Sale(t, pool, "sh-1", "s-f5h", "u-f5h", 1000, "cash", done1, c.ID)
	f5Sale(t, pool, "sh-2", "s-f5h", "u-f5h", 2500, "card", done2, c.ID)
	f5Sale(t, pool, "sh-old", "s-f5h", "u-f5h", 9000, "cash", done1, "")

	history := ClientHistory{Pool: pool}
	data, err := history.History("s-f5h", c.ID, now, 20)
	if err != nil {
		t.Fatalf("historial: %v", err)
	}
	// 2 + 22 done; el booked viejo NO es próximo; los booked futuros sí.
	if data.Visits != 24 || data.NoShows != 1 || data.Cancelled != 1 || data.Upcoming != 2 {
		t.Fatalf("conteos: %+v", data)
	}
	if data.LastDoneAt == nil || !data.LastDoneAt.Equal(done2) {
		t.Fatalf("última visita hecha: %v, esperaba %v", data.LastDoneAt, done2)
	}
	if data.NextBookedAt == nil || !data.NextBookedAt.Equal(future1) {
		t.Fatalf("próxima: %v, esperaba %v", data.NextBookedAt, future1)
	}
	if len(data.Bookings) != 20 {
		t.Fatalf("la lista de turnos debe tener LIMIT 20, tiene %d", len(data.Bookings))
	}
	// Día LOCAL del salón: el último `done` es el 8 de marzo en UTC pero el 7 en
	// America/Mexico_City (UTC-6).
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	sum := domain.NewClientHistorySummary(data, loc)
	if sum.LastVisit != "2026-03-07" || sum.Timezone != "America/Mexico_City" {
		t.Fatalf("last_visit local: %q (%q)", sum.LastVisit, sum.Timezone)
	}
	if sum.NextVisit == nil || !sum.NextVisit.Equal(future1) {
		t.Fatalf("next_visit: %v", sum.NextVisit)
	}
	// Gasto: SOLO las ventas vinculadas.
	if data.Spend.SalesCount != 2 || data.Spend.TotalCents != 3500 {
		t.Fatalf("gasto: %+v (no debe sumar la venta sin vínculo)", data.Spend)
	}
	if len(data.Sales) != 2 || data.Sales[0].ID != "sh-2" || data.Sales[1].ID != "sh-1" {
		t.Fatalf("ventas más reciente primero: %+v", data.Sales)
	}

	// Aislamiento de tenant: en otro salón la misma ficha no tiene historial.
	otro, err := history.History("s-f5i", c.ID, now, 20)
	if err != nil {
		t.Fatalf("historial ajeno: %v", err)
	}
	if otro.Visits != 0 || len(otro.Bookings) != 0 || len(otro.Sales) != 0 || otro.Spend.TotalCents != 0 {
		t.Fatalf("el otro salón no debe ver nada: %+v", otro)
	}
}
