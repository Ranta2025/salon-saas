package httpapi

import "github.com/gin-gonic/gin"

// registerSimbankPublicRoutes: las dos rutas públicas del banco simulado (F11,
// D6). Sin banco configurado NO existen (D12: 404).
func registerSimbankPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F11: el banco simulado (D6). Sus dos rutas son públicas y tienen SU PROPIO
	// cubo por IP; sin banco configurado no existen (D12: 404).
	if d.SimbankLookup != nil && d.SimbankPay != nil {
		simbank := func(h gin.HandlerFunc) []gin.HandlerFunc {
			if d.SimbankLimit == nil {
				return []gin.HandlerFunc{h}
			}
			return []gin.HandlerFunc{d.SimbankLimit, h}
		}
		v1.POST("/simbank/charges/lookup", simbank(d.SimbankLookup)...)
		v1.POST("/simbank/charges/pay", simbank(d.SimbankPay)...)
	}
}

// registerPaymentsCallbackRoute: el callback del banco (F11, D7), firmado. Sin
// banco configurado la ruta NO existe (D12).
func registerPaymentsCallbackRoute(v1 *gin.RouterGroup, d Deps) {
	// F11: el callback del banco (D7). Es la primera ruta pública no GET firmada:
	// cuerpo máximo 8 KB, firma verificada por el adaptador y 200 en el reenvío.
	// Sin banco configurado la ruta NO existe (D12: 404).
	if d.PaymentsCallback != nil {
		callback := []gin.HandlerFunc{d.PaymentsCallback}
		if d.PaymentCallbackLimit != nil {
			callback = append([]gin.HandlerFunc{d.PaymentCallbackLimit}, callback...)
		}
		v1.POST("/payments/callback", callback...)
	}
}

// registerDepositsPublicRoutes: el estado público de un pago (F11, D8).
func registerDepositsPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F11: el estado público de un pago (D8). El id del cargo es la credencial y
	// va en el CUERPO (nunca en la ruta ni en el query), con su propio cubo por IP.
	publicDepositLookup := []gin.HandlerFunc{d.Deposits.Lookup}
	if d.PublicDepositLookupLimit != nil {
		publicDepositLookup = append([]gin.HandlerFunc{d.PublicDepositLookupLimit}, publicDepositLookup...)
	}
	v1.POST("/public/deposits/lookup", publicDepositLookup...)
}

// registerDepositRoutes: la política de señas y la pantalla «Señas» (F11).
func registerDepositRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F11: política de señas (D3) y pantalla «Señas» (D10). Leer la política:
	// cualquier miembro activo; guardarla: owner/admin, envuelta en la auditoría
	// como `deposit.policy`. El permiso de las dos escrituras va ANTES del cuerpo
	// y de los parámetros (vive en el caso de uso).
	authed.GET("/salons/:id/deposit-policy", d.DepositPolicy.Get)
	authed.PUT("/salons/:id/deposit-policy", audit("deposit.policy"), d.DepositPolicy.Put)
	authed.GET("/salons/:id/deposits", d.Deposits.List)
}
