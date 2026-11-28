package main

import (
	"salon-saas/internal/deposits"
	depositspostgres "salon-saas/internal/deposits/postgres"
	"salon-saas/internal/ports"
	"salon-saas/internal/simbank"

	depositshttpapi "salon-saas/internal/deposits/httpapi"
)

// wireDeposits arma la seña online (F11) y, solo con PAYMENT_SIM_SECRET, el
// banco simulado como un adaptador más del mismo puerto.
func wireDeposits(w *wiring) {
	w.depositPoliciesRepo = depositspostgres.DepositPolicies{Pool: w.pool}
	w.depositRepo = depositspostgres.Deposits{Pool: w.pool}
	var gateway ports.PaymentGateway = ports.NoopGateway{}
	if w.cfg.PaymentSimSecret != "" {
		bank := &simbank.Bank{Pool: w.pool, Secret: w.cfg.PaymentSimSecret, CallbackURL: w.cfg.PaymentCallbackURL}
		gateway = bank
		w.simbankHandler = &simbank.Handler{Bank: bank}
	}
	w.gateway = gateway
	// El barrido cancela la reserva por el camino EXISTENTE de la agenda
	// (SetStatus(cancelled) bajo el lock `booking-write:`): no reimplementa turnos.
	w.depositsSvc = deposits.DepositService{
		Policies: w.depositPoliciesRepo, Deposits: w.depositRepo, Gateway: gateway,
		Bookings: depositspostgres.BookingCanceller{Bookings: w.bookingRepo},
		Notices:  w.depositRepo, Mailer: w.mailer, Links: w.bookingLinks,
		Settings: w.salons, Memberships: w.mems, AppURL: w.cfg.AppURL,
	}
	w.depositPolicySvc = deposits.DepositPolicyService{
		Policies: w.depositPoliciesRepo, Memberships: w.mems, Gateway: gateway,
	}
	// El payload público (D11) lee la misma política de señas.
	w.publicSvc.Deposits = w.depositPoliciesRepo
}

// wireSimbankRoutes resuelve los handlers gin del banco simulado y del callback.
// Sin banco configurado quedan nil y las rutas NO se montan (D12).
func wireSimbankRoutes(w *wiring) {
	if w.simbankHandler == nil {
		return
	}
	w.simbankLookup, w.simbankPay = w.simbankHandler.Lookup, w.simbankHandler.Pay
	w.paymentsCallback = depositshttpapi.PaymentsHandler{Deposits: w.depositsSvc}.Callback
}
