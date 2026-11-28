package main

import (
	"salon-saas/internal/bookings"
	bookingspostgres "salon-saas/internal/bookings/postgres"
)

// wireBookingRepos arma los repositorios de la agenda: turnos, la cola de
// recordatorios y los enlaces de un solo uso.
func wireBookingRepos(w *wiring) {
	w.bookingRepo = bookingspostgres.Bookings{Pool: w.pool}
}

// wireBookingServices arma la agenda y la disponibilidad. Va después del hub
// (EventPublisher), de las notificaciones y de los repositorios de servicios,
// equipamiento y fichas de clienta.
func wireBookingServices(w *wiring) {
	w.bookSvc = bookings.BookingService{
		Bookings: w.bookingRepo, Memberships: w.mems, Settings: w.salons, Events: w.hub, Notifier: w.notifSvc,
		Services: w.servicesRepo, Resources: w.resourceRepo,
		ClientLinks: w.clientLinks,
	}
	w.availabilitySvc = bookings.AvailabilityService{
		Salons: w.salons, Services: w.servicesRepo, Settings: w.salons, Staff: w.mems,
		Bookings: w.bookingRepo, Professionals: w.professionals, Hours: w.weeklyHours,
		TimeOff: w.timeOffRepo, Resources: w.resourceRepo,
	}
}

// wireBookingLinks arma la cola de recordatorios, los enlaces del turno y las
// cuatro rutas públicas del enlace. Necesita el mailer ya construido.
func wireBookingLinks(w *wiring) {
	// La cola de recordatorios vive en su propia tabla (014).
	w.bookingEmails = bookingspostgres.BookingEmails{Pool: w.pool}
	// F1: el enlace de un solo uso que viaja en la confirmación, el recordatorio y
	// el aviso de cambio. Guarda solo el SHA-256 del token (migración 027).
	w.bookingLinks = bookingspostgres.BookingLinks{Pool: w.pool}
	w.reminders = bookings.ReminderService{
		Emails: w.bookingEmails, Mailer: w.mailer, Settings: w.salons,
		Links: w.bookingLinks, AppURL: w.cfg.AppURL,
	}
}

// wireBookingPublic arma la reserva pública y las cuatro rutas públicas del
// enlace del turno. Es lo último de la agenda: depende de las señas y de las
// reseñas (la vidriera pública las suma).
func wireBookingPublic(w *wiring) {
	// F1: las cuatro rutas públicas del enlace (lookup, horarios, cancelar y
	// reprogramar). Toma el MISMO lock `booking-write:<salón>` de Book y Update en
	// UNA transacción: dos pedidos con el mismo token no pueden ganar los dos.
	w.bookingLinkSvc = bookings.BookingLinkService{
		Links: w.bookingLinks, Tx: w.bookingLinks,
		Bookings: w.bookingRepo, Salons: w.salons, Services: w.servicesRepo, Staff: w.mems,
		Settings: w.salons, Professionals: w.professionals, Hours: w.weeklyHours,
		TimeOff: w.timeOffRepo, Resources: w.resourceRepo, Availability: w.availabilitySvc,
		Events: w.hub, Notifier: w.notifSvc, Mailer: w.mailer, Emails: w.bookingEmails,
		AppURL: w.cfg.AppURL,
	}
	w.publicBookingSvc = bookings.PublicBookingService{
		Salons: w.salons, Services: w.servicesRepo, Settings: w.salons, Staff: w.mems, Bookings: w.bookingRepo,
		Professionals: w.professionals, Hours: w.weeklyHours, TimeOff: w.timeOffRepo,
		Resources:   w.resourceRepo, // el turno ocupa una unidad de equipo (F2e)
		Clients:     w.clientsRepo,  // cada reserva pública deja la ficha de la clienta (E-03)
		ClientLinks: w.clientLinks,  // y el turno se vincula con ESA ficha (F5, D3-a)
		Events:      w.hub, Notifier: w.notifSvc,
		Mailer: w.mailer, Emails: w.bookingEmails,
		// F1: un token nuevo por cada email enviado; solo para turnos con email.
		Links: w.bookingLinks, AppURL: w.cfg.AppURL,
	}
	// F11/D8: con seña activa la reserva pública crea el cargo y devuelve
	// `payment`; sin seña responde y manda el correo exactamente como hoy.
	w.publicBookingSvc.Deposits = w.depositsSvc
}
