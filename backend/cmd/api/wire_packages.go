package main

import (
	"salon-saas/internal/packages"
	packagespostgres "salon-saas/internal/packages/postgres"
)

// wirePackages arma paquetes y bonos prepagos (F8).
func wirePackages(w *wiring) {
	// F8: catálogo de paquetes y paquetes comprados por cada clienta (con su libro
	// de saldo). Comprar escribe la venta, el paquete y el movimiento en UNA
	// transacción; consumir es un UPDATE condicional más su movimiento.
	w.packagesRepo = packagespostgres.Packages{Pool: w.pool}
	w.clientPackagesRepo = packagespostgres.ClientPackages{Pool: w.pool}
	// F8: paquetes y bonos prepagos. Comprar es una VENTA (el dinero entra hoy);
	// consumir una sesión no lo es. Vender, consumir y leer: owner/admin/recepción;
	// catálogo (crear/editar) y ajustar el saldo: solo owner/admin (D5/D7/D9/D10).
	w.packageSvc = packages.PackageService{
		Packages: w.packagesRepo, ClientPackages: w.clientPackagesRepo, Services: w.servicesRepo,
		Clients: w.clientsRepo, Memberships: w.mems, Settings: w.salons, Events: w.hub,
	}
}
