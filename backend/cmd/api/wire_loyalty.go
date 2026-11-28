package main

import (
	"salon-saas/internal/loyalty"
	loyaltystore "salon-saas/internal/loyalty/postgres"
)

// wireLoyalty arma la fidelidad (F9).
func wireLoyalty(w *wiring) {
	// F9: fidelidad (programa del salón y vales entregados; migración 033).
	w.loyaltyProgramsRepo = loyaltystore.LoyaltyPrograms{Pool: w.pool}
	w.loyaltyRewardsRepo = loyaltystore.LoyaltyRewards{Pool: w.pool}
	// F9: fidelidad «cada N visitas, un servicio de regalo». Usar un vale NO es una
	// venta: el POS, la caja y la nómina no se tocan. Leer el programa: cualquier
	// miembro; guardarlo: owner/admin; ficha, entregar y usar: owner/admin/recepción.
	w.loyaltySvc = loyalty.LoyaltyService{
		Programs: w.loyaltyProgramsRepo, Rewards: w.loyaltyRewardsRepo, Services: w.servicesRepo,
		Clients: w.clientsRepo, Memberships: w.mems, Settings: w.salons,
	}
}
