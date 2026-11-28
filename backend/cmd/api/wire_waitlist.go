package main

import (
	"salon-saas/internal/waitlist"
	waitliststore "salon-saas/internal/waitlist/postgres"
)

// wireWaitlist arma la lista de espera (F7). Necesita la disponibilidad ya
// construida y el mailer para el aviso.
func wireWaitlist(w *wiring) {
	// F7: lista de espera del salón (migración 030). El barrido periódico reclama
	// avisos con el advisory lock `waitlist-write:<salón>` y manda el email fuera
	// del lock; la pantalla del personal lista y da de baja.
	w.waitlistRepo = waitliststore.Waitlist{Pool: w.pool}
	w.waitlistSvc = waitlist.WaitlistService{
		Waitlist: w.waitlistRepo, Tx: w.waitlistRepo,
		Salons: w.salons, Services: w.servicesRepo, Professionals: w.professionals,
		Settings: w.salons, Availability: w.availabilitySvc, Memberships: w.mems,
		// El sender y APP_URL se completan abajo, cuando el mailer existe.
		AppURL: w.cfg.AppURL,
	}
}
