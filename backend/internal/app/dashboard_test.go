package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func TestDashboardService_today(t *testing.T) {
	sales := &fakeSales{}
	agenda := &fakeAgenda{}
	mems := &fakeMemberships{}
	svc := DashboardService{Sales: sales, Agenda: agenda, Staff: fakeStaff{count: 2}, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)

	now := time.Now().UTC()
	sales.all = append(sales.all,
		domain.Sale{ID: "v1", SalonID: "s1", TotalCents: 120000, Method: domain.MethodCash, CreatedAt: now},
		domain.Sale{ID: "v2", SalonID: "s1", TotalCents: 80000, Method: domain.MethodCard, CreatedAt: now},
		// De ayer: no suma al día de hoy.
		domain.Sale{ID: "v-ayer", SalonID: "s1", TotalCents: 500000, Method: domain.MethodCash, CreatedAt: now.Add(-24 * time.Hour)},
		// De otro salón.
		domain.Sale{ID: "v-otro", SalonID: "s2", TotalCents: 900000, Method: domain.MethodCash, CreatedAt: now},
	)
	sales.top = []domain.TopService{{ServiceID: "svc-1", Name: "Corte", Qty: 3, RevenueCents: 450000}}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	agenda.bookings = []domain.Booking{
		{SalonID: "s1", ProfessionalID: "p1", StartsAt: dayStart.Add(9 * time.Hour), EndsAt: dayStart.Add(10 * time.Hour), Status: domain.BookingBooked},
		{SalonID: "s1", ProfessionalID: "p2", StartsAt: dayStart.Add(10 * time.Hour), EndsAt: dayStart.Add(13 * time.Hour), Status: domain.BookingDone},
		// Cancelada: libera el hueco, no cuenta.
		{SalonID: "s1", ProfessionalID: "p1", StartsAt: dayStart.Add(15 * time.Hour), EndsAt: dayStart.Add(16 * time.Hour), Status: domain.BookingCancelled},
		// De otro salón.
		{SalonID: "s2", ProfessionalID: "p9", StartsAt: dayStart.Add(9 * time.Hour), EndsAt: dayStart.Add(11 * time.Hour), Status: domain.BookingBooked},
	}

	d, err := svc.Today(owner, "s1")
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if d.RevenueTodayCents != 200000 {
		t.Fatalf("ingresos de hoy = 120000+80000, dio %d", d.RevenueTodayCents)
	}
	if d.AppointmentsToday != 2 {
		t.Fatalf("turnos de hoy = 2 activos, dio %d", d.AppointmentsToday)
	}
	// 4h reservadas (60+180 min) sobre 2 profesionales * 720 min = 240/1440 = 0.1667
	if d.OccupancyRate != 0.1667 {
		t.Fatalf("ocupación = 240/(2*720) = 0.1667, dio %v", d.OccupancyRate)
	}
	if len(d.TopServices) != 1 || d.TopServices[0].Name != "Corte" {
		t.Fatalf("top services: %+v", d.TopServices)
	}
	// La ventana es el día UTC de hoy, no "las últimas 24h".
	if !agenda.lastFrom.Equal(dayStart) {
		t.Fatalf("desde = %v, esperaba %v", agenda.lastFrom, dayStart)
	}
	if !agenda.lastTo.Equal(dayStart.Add(24 * time.Hour)) {
		t.Fatalf("hasta = %v", agenda.lastTo)
	}
}

func TestDashboardService_ocupacion_sin_profesionales(t *testing.T) {
	sales := &fakeSales{}
	agenda := &fakeAgenda{}
	mems := &fakeMemberships{}
	svc := DashboardService{Sales: sales, Agenda: agenda, Staff: fakeStaff{count: 0}, Memberships: mems}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)

	d, err := svc.Today(owner, "s1")
	if err != nil {
		t.Fatalf("dashboard vacío: %v", err)
	}
	if d.OccupancyRate != 0 {
		t.Fatalf("sin profesionales la ocupación no se divide por cero, dio %v", d.OccupancyRate)
	}
	if d.RevenueTodayCents != 0 || d.AppointmentsToday != 0 {
		t.Fatalf("salón vacío debe ser 0: %+v", d)
	}
	if d.TopServices == nil {
		t.Fatal("top_services debe ser [] y no null para el frontend")
	}
}

func TestDashboardService_permisos(t *testing.T) {
	mems := &fakeMemberships{}
	svc := DashboardService{Sales: &fakeSales{}, Agenda: &fakeAgenda{}, Staff: fakeStaff{count: 1}, Memberships: mems}
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	// Recepción ve el panel de su salón.
	if _, err := svc.Today(recep, "s1"); err != nil {
		t.Fatalf("recepción debe ver su dashboard: %v", err)
	}
	if _, err := svc.Today(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-salón debe dar Forbidden, dio %v", err)
	}
}
