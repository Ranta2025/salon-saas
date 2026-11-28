package main

import (
	"salon-saas/internal/payroll"
	payrollpostgres "salon-saas/internal/payroll/postgres"
)

// wirePayroll arma la nómina: modelo de pago, comisiones y liquidaciones
// (F3.2, F3.3).
func wirePayroll(w *wiring) {
	// F3.2: modelo de pago por miembro (member_pay, migración 024) y los
	// agregados de comisión por profesional de una ventana.
	w.payModelsRepo = payrollpostgres.PayModels{Pool: w.pool}
	w.payrollSalesRepo = payrollpostgres.PayrollSales{Pool: w.pool}
	// F3.3: liquidaciones (migración 025), con la transacción y el advisory lock
	// del salón en el mismo adaptador.
	w.payrollRunsRepo = payrollpostgres.PayrollRuns{Pool: w.pool}
	// F3.2: nómina. El período se deriva en la zona del salón (settings) y la
	// comisión sale de los agregados de ventas; el fijo se paga una vez cuando el
	// tipo de período coincide con el del miembro (D4).
	w.payModelSvc = payroll.PayModelService{Pay: w.payModelsRepo, Memberships: w.mems}
	w.payrollSvc = payroll.PayrollService{
		Pay: w.payModelsRepo, Sales: w.payrollSalesRepo, Memberships: w.mems, Settings: w.salons,
	}
	// F3.3: crear congela la vista previa; ajustar, pagar y borrar solo con la
	// corrida abierta (la DB repite la condición status='open').
	w.payrollRunSvc = payroll.PayrollRunService{
		Runs: w.payrollRunsRepo, Tx: w.payrollRunsRepo,
		PayModels: w.payModelsRepo, Sales: w.payrollSalesRepo, Memberships: w.mems, Settings: w.salons,
	}
}
