// Exportación CSV de ventas, cierres de caja y clientes (F4 del contrato Fase
// 6). El handler es DELGADO: permisos, rango y tope viven en el caso de uso; acá
// se ponen las cabeceras de D6 (Content-Type, Content-Disposition saneado,
// Cache-Control no-store y nosniff) y se deja UNA línea de log por export (D7:
// tipo, salón, usuario y filas; ningún dato personal, monto ni contenido).
package httpapi

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/exports"
	"salon-saas/internal/ports"
)

// ExportHandler agrupa las tres rutas de exportación de F4.
type ExportHandler struct {
	Exports     exports.ExportService
	Memberships ports.MembershipRepository
	// Logf registra la línea de D7. nil = log.Printf (y en los tests unitarios
	// del router se puede capturar sin ensuciar la salida).
	Logf func(format string, args ...any)
}

// logExport deja la ÚNICA línea del export: tipo, salón, usuario y cantidad de
// filas. Nunca el nombre de una clienta, un teléfono ni un monto.
func (h ExportHandler) logExport(kind, salonID, userID string, rows int) {
	logf := h.Logf
	if logf == nil {
		logf = log.Printf
	}
	logf("export csv: tipo=%s salon=%s usuario=%s filas=%d", kind, salonID, userID, rows)
}

// writeCSV escribe el archivo con las cabeceras de D6. Son datos personales y
// de dinero: no se cachean en ningún intermediario y no se adivina el tipo.
func writeCSV(c *gin.Context, file exports.ExportFile) {
	// El nombre ya viene saneado (solo [a-z0-9-], guiones y punto): no puede
	// inyectar una cabecera en Content-Disposition.
	c.Header("Content-Disposition", `attachment; filename="`+file.Name+`"`)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", file.Data)
}

// Sales: GET /api/v1/salons/:id/export/sales.csv?from=&to= -> 200 CSV.
func (h ExportHandler) Sales(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
	if !ok {
		return
	}
	file, err := h.Exports.Sales(actor, salonID, c.Query("from"), c.Query("to"))
	if err != nil {
		respondErr(c, err)
		return
	}
	writeCSV(c, file)
	h.logExport("ventas", salonID, actor.UserID, file.Rows)
}

// CashCloses: GET /api/v1/salons/:id/export/cash-closes.csv?from=&to=.
func (h ExportHandler) CashCloses(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
	if !ok {
		return
	}
	file, err := h.Exports.CashCloses(actor, salonID, c.Query("from"), c.Query("to"))
	if err != nil {
		respondErr(c, err)
		return
	}
	writeCSV(c, file)
	h.logExport("cierres", salonID, actor.UserID, file.Rows)
}

// Clients: GET /api/v1/salons/:id/export/clients.csv (sin rango).
func (h ExportHandler) Clients(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := loadActor(h.Memberships, c, salonID)
	if !ok {
		return
	}
	file, err := h.Exports.Clients(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	writeCSV(c, file)
	h.logExport("clientes", salonID, actor.UserID, file.Rows)
}
