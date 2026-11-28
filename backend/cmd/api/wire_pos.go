package main

import (
	"salon-saas/internal/pos"
	pospostgres "salon-saas/internal/pos/postgres"
)

// wirePOSRepos arma los repositorios del POS: ventas, la venta con productos,
// el libro de stock (pospostgres) y los cierres de caja.
func wirePOSRepos(w *wiring) {
	w.salesRepo = pospostgres.Sales{Pool: w.pool}
	w.saleProductsRepo = pospostgres.SaleProducts{Pool: w.pool}
	w.closesRepo = pospostgres.CashCloses{Pool: w.pool}
}

// wirePOSServices arma ventas, cierre de caja y panel del día. Va después de
// los productos y de las fichas de clienta (una venta vincula la ficha).
func wirePOSServices(w *wiring) {
	w.saleSvc = pos.SaleService{
		Sales: w.salesRepo, Services: w.servicesRepo, Memberships: w.mems, Events: w.hub,
		Clients: w.clientsRepo, ClientLinks: w.clientLinks,
		Products: w.productsRepo, ProductSales: w.saleProductsRepo, Notifier: w.notifSvc,
	}
	// Tx: el cierre corre en una transacción con advisory lock por salón (B-09).
	// Settings: la ventana del cierre es el día LOCAL del salón (su zona), no UTC.
	w.cashCloseSvc = pos.CashCloseService{
		CashCloses: w.closesRepo, Sales: w.salesRepo, Memberships: w.mems, Tx: w.closesRepo,
		Settings: w.salons, Events: w.hub, Notifier: w.notifSvc,
	}
	w.dashSvc = pos.DashboardService{Sales: w.salesRepo, Agenda: w.bookingRepo, Staff: w.mems, Memberships: w.mems}
}
