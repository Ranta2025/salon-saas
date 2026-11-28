package main

import (
	"salon-saas/internal/adapters/imaging"
	"salon-saas/internal/clients"
	clientspostgres "salon-saas/internal/clients/postgres"
	"salon-saas/internal/domain"
)

// wireClientRepos arma los repositorios de clientas: fichas, vínculo con
// turnos/ventas e historial.
func wireClientRepos(w *wiring) {
	// F5: el vínculo de turnos/ventas con la ficha es un UPDATE aparte (D1), y
	// el historial se lee en UNA transacción REPEATABLE READ con tope de 25 s.
	w.clientLinks = clientspostgres.ClientLinks{Pool: w.pool}
	w.clientHistoryRepo = clientspostgres.ClientHistory{Pool: w.pool}
	w.clientsRepo = clientspostgres.Clients{Pool: w.pool}
}

// wireClients arma fichas, fotos e historial. Va después del almacenamiento.
func wireClients(w *wiring) {
	w.clientSvc = clients.ClientService{
		Clients:     w.clientsRepo,
		Memberships: w.mems,
		Photos:      w.store,                                    // filesystem local
		Images:      imaging.WebP{MaxSide: domain.MaxPhotoSide}, // cwebp
	}
	w.clientHistorySvc = clients.ClientHistoryService{
		Clients: w.clientsRepo, Source: w.clientHistoryRepo,
		Memberships: w.mems, Settings: w.salons,
	}
}
