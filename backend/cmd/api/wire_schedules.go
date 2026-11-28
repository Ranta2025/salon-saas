package main

import (
	"salon-saas/internal/schedules"
	schedulespostgres "salon-saas/internal/schedules/postgres"
)

// wireSchedules arma horarios semanales, ausencias y la cobertura de una
// ausencia (F2d1, F2d2, F2d3).
func wireSchedules(w *wiring) {
	// F2d1: horario semanal del salón y de cada profesional (sin filas =
	// comportamiento de siempre).
	w.weeklyHours = schedulespostgres.WeeklyHours{Pool: w.pool}
	// F2d2: ausencias (vacaciones y días libres puntuales) de cada profesional.
	w.timeOffRepo = schedulespostgres.TimeOff{Pool: w.pool}
	w.salonHoursSvc = schedules.SalonHoursService{Hours: w.weeklyHours, Settings: w.salons, Memberships: w.mems}
	w.professionalHoursSvc = schedules.ProfessionalHoursService{Hours: w.weeklyHours, Memberships: w.mems}
	// F2d2: las ausencias se cargan desde Equipo y las consultan la agenda (con
	// los turnos en conflicto) y la disponibilidad pública.
	w.timeOffSvc = schedules.TimeOffService{TimeOff: w.timeOffRepo, Bookings: w.bookingRepo, Memberships: w.mems}
	// F2d3: cubrir una ausencia reasignando los turnos a otra profesional. La
	// transacción con lock por salón es lo que hace el "todo o nada".
	w.coverageSvc = schedules.CoverageService{
		Bookings: w.bookingRepo, Memberships: w.mems, Staff: w.mems, Professionals: w.professionals,
		Hours: w.weeklyHours, TimeOff: w.timeOffRepo, Services: w.servicesRepo,
		Settings: w.salons, Salons: w.salons, Tx: w.bookingRepo,
		Events: w.hub, Notifier: w.notifSvc,
	}
}
