package main

import (
	"salon-saas/internal/finance"
	financestore "salon-saas/internal/finance/postgres"
)

// wireFinance arma gastos y ganancia neta (F3b).
func wireFinance(w *wiring) {
	// F3b: gastos (migración 026) y el reporte de ganancia neta, que lee ingresos,
	// gastos y nómina PAGADA en UNA sola transacción REPEATABLE READ de lectura.
	w.expenseRepo = financestore.Expenses{Pool: w.pool}
	w.profitRepo = financestore.Profit{Pool: w.pool}
	// F3b: gastos operativos (solo owner/administración) y ganancia neta del
	// período en la zona del salón. Las fechas de los gastos se miden contra el
	// día LOCAL del salón, no UTC.
	w.expenseSvc = finance.ExpenseService{Expenses: w.expenseRepo, Memberships: w.mems, Settings: w.salons}
	w.profitSvc = finance.ProfitService{Profit: w.profitRepo, Memberships: w.mems, Settings: w.salons}
}
