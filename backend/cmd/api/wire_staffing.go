package main

import (
	"salon-saas/internal/staffing"
	staffingpostgres "salon-saas/internal/staffing/postgres"
)

// wireStaffing arma el catálogo de servicios, qué hace cada profesional y el
// equipamiento con capacidad (F2c, F2e).
func wireStaffing(w *wiring) {
	w.servicesRepo = staffingpostgres.Services{Pool: w.pool}
	// F2c: qué servicios hace cada quien (disponibilidad, reserva pública y
	// pantalla de equipo).
	w.professionals = staffingpostgres.ProfessionalServices{Pool: w.pool}
	// F2e: puestos y equipamiento con capacidad (tipos, unidades y la unidad que
	// ocupa cada turno). Sin backfill: sin tipos el comportamiento es el de antes.
	w.resourceRepo = staffingpostgres.Resources{Pool: w.pool}
	w.svcSvc = staffing.ServiceService{
		Services: w.servicesRepo, Memberships: w.mems, Professionals: w.professionals, Resources: w.resourceRepo,
	}
	w.resourceSvc = staffing.ResourceService{
		Resources: w.resourceRepo, Services: w.servicesRepo, Memberships: w.mems,
	}
}
