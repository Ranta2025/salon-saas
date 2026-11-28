package main

import (
	"salon-saas/internal/products"
	productspostgres "salon-saas/internal/products/postgres"
)

// wireProducts arma el catálogo de productos y su inventario (F6).
func wireProducts(w *wiring) {
	// F6: catálogo de productos (con su libro de stock).
	w.productsRepo = productspostgres.Products{Pool: w.pool}
	// F6: productos e inventario del POS. Leer owner/admin/recepción; escribir
	// solo owner/admin (D7). El aviso de stock bajo sale DESPUÉS del commit.
	w.productSvc = products.ProductService{
		Products: w.productsRepo, Memberships: w.mems, Events: w.hub, Notifier: w.notifSvc,
	}
}
