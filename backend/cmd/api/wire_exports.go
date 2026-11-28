package main

import (
	"salon-saas/internal/exports"
	exportsstore "salon-saas/internal/exports/postgres"
)

// wireExports arma las exportaciones CSV (F4).
func wireExports(w *wiring) {
	// F4: lecturas masivas del export (ventas con el nombre del profesional y la
	// suma de ítems, cierres por closed_at y clientes por nombre), una sentencia
	// SQL por archivo y con LIMIT.
	w.exportRepo = exportsstore.Exports{Pool: w.pool}
	// F4: exportación CSV de ventas, cierres de caja y clientes. Solo dueño y
	// administración (la autorización vive en el caso de uso). Las lecturas son
	// métodos nuevos con LIMIT (una sola sentencia por archivo) y el tope de
	// filas queda en el valor del contrato.
	w.exportSvc = exports.ExportService{
		Exports: w.exportRepo, Memberships: w.mems, Settings: w.salons,
	}
}
