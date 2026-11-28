package app

import (
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
	"salon-saas/internal/testutil"
)

// Los casos de uso que ya existían (reserva, venta, cierre) publican su evento
// en el bus del salón: el frontend se entera sin polling. Además dejan un aviso
// persistido en los casos que valen la pena (turno nuevo, cierre de caja).

func TestBookingService_publica_booking_created(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeNotifier{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems, Events: events, Notifier: notifier}
	rec := testutil.Recepcionista(mems)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "555", day.Add(10*time.Hour), day.Add(11*time.Hour))
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	ev, count := events.Typed("booking.created")
	if count != 1 {
		t.Fatalf("esperaba 1 booking.created, dio %d (%+v)", count, events.All)
	}
	if ev.SalonID != "s1" {
		t.Fatalf("el evento debe ir al salón de la reserva, dio %q", ev.SalonID)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok || data["id"] != b.ID {
		t.Fatalf("el evento debe llevar la reserva creada, dio %+v", ev.Data)
	}
	// Aviso persistido para el salón (bandeja del equipo).
	if notifier.Calls != 1 {
		t.Fatalf("esperaba 1 aviso por la reserva nueva, dio %d", notifier.Calls)
	}
}

func TestBookingService_solape_no_publica_evento(t *testing.T) {
	bookings := &testutil.FakeBookings{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems, Events: events}
	rec := testutil.Recepcionista(mems)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	_, _ = svc.CreateBooking(rec, "s1", "prof1", "Lía", "", day.Add(10*time.Hour), day.Add(11*time.Hour))
	before, _ := events.Typed("booking.created")
	if _, err := svc.CreateBooking(rec, "s1", "prof1", "Ana", "", day.Add(10*time.Hour), day.Add(11*time.Hour)); err == nil {
		t.Fatal("el solape debe fallar")
	}
	after, count := events.Typed("booking.created")
	if count != 1 || after.Type != before.Type {
		t.Fatalf("un alta rechazada no publica evento, dio %d (%+v)", count, events.All)
	}
}

func TestSaleService_publica_sale_created(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	svc := pos.SaleService{Sales: sales, Services: services, Memberships: mems, Events: events}
	testutil.Catalogo(t, services, "s1")
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	testutil.Miembro(mems, "prof1", "s1", domain.RoleProfessional)
	corte := testutil.SvcIDByName(t, services, "s1", "Corte")

	sale, err := svc.RegisterSale(recep, "s1", "prof1", "Lía",
		[]domain.SaleItem{{ServiceID: corte, Qty: 2, UnitPriceCents: 150000}}, domain.MethodCash)
	if err != nil {
		t.Fatalf("venta: %v", err)
	}
	ev, count := events.Typed("sale.created")
	if count != 1 {
		t.Fatalf("esperaba 1 sale.created, dio %d (%+v)", count, events.All)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok || data["id"] != sale.ID || data["total_cents"] != 300000 {
		t.Fatalf("el evento debe llevar el total calculado en el servidor: %+v", ev.Data)
	}
}

func TestCashCloseService_publica_cash_closed_y_avisa(t *testing.T) {
	closes := &testutil.FakeCashCloses{}
	sales := &testutil.FakeSales{}
	mems := &testutil.FakeMemberships{}
	events := &testutil.FakeEvents{}
	notifier := &testutil.FakeNotifier{}
	svc := pos.CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems, Events: events, Notifier: notifier}
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)

	close, err := svc.CloseCash(owner, "s1", 10000, "sin novedad")
	if err != nil {
		t.Fatalf("cierre: %v", err)
	}
	ev, count := events.Typed("cash_closed")
	if count != 1 || ev.SalonID != "s1" {
		t.Fatalf("esperaba 1 cash_closed de s1, dio %d (%+v)", count, events.All)
	}
	data, ok := ev.Data.(map[string]any)
	if !ok || data["id"] != close.ID || data["difference_cents"] != 10000 {
		t.Fatalf("el evento debe llevar la diferencia del cierre: %+v", ev.Data)
	}
	if notifier.Calls != 1 {
		t.Fatalf("el cierre deja un aviso para el salón, dio %d", notifier.Calls)
	}
}
