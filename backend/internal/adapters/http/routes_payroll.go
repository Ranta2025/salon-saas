package httpapi

import "github.com/gin-gonic/gin"

// registerPayrollRoutes: nómina y comisiones (F3.2) y liquidaciones (F3.3).
func registerPayrollRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F3.2: nómina y comisiones. El modelo de pago y la vista previa son de
	// dueño/administración; «Mis comisiones» es la única que ve un profesional
	// (y solo lo suyo). La autorización de cada rol vive en el caso de uso.
	authed.GET("/salons/:id/pay-models", d.Payroll.ListPayModels)
	authed.PUT("/salons/:id/members/:uid/pay-model", audit("member.pay_model"), d.Payroll.UpdatePayModel)
	authed.POST("/salons/:id/payroll/preview", d.Payroll.Preview)
	authed.GET("/salons/:id/payroll/me", d.Payroll.Me)

	// F3.3: liquidaciones de nómina. Todas owner/admin (la autorización vive en
	// el caso de uso); el CSV baja con fetch autenticado, no con un enlace.
	authed.POST("/salons/:id/payroll/runs", audit("payroll_run.create"), d.PayrollRuns.Create)
	authed.GET("/salons/:id/payroll/runs", d.PayrollRuns.List)
	authed.GET("/salons/:id/payroll/runs/:rid", d.PayrollRuns.Get)
	authed.PATCH("/salons/:id/payroll/runs/:rid/lines/:uid", audit("payroll_run.adjust"), d.PayrollRuns.AdjustLine)
	authed.POST("/salons/:id/payroll/runs/:rid/pay", audit("payroll_run.pay"), d.PayrollRuns.Pay)
	authed.DELETE("/salons/:id/payroll/runs/:rid", audit("payroll_run.delete"), d.PayrollRuns.Delete)
	authed.GET("/salons/:id/payroll/runs/:rid/export.csv", d.PayrollRuns.ExportCSV)
}
