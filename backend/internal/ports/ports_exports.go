package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// --- Exportación CSV (F4) ---

// ExportRepository son las lecturas MASIVAS de la exportación (F4). Cada método
// es UNA sola sentencia SQL en el adaptador (así el archivo es una foto
// consistente) y recibe el salón, la ventana y el LIMIT: el caso de uso pide el
// tope + 1 para saber si hay más filas y devolver 400 sin archivo parcial (D3).
//
// Va aparte de SaleRepository/CashCloseRepository/ClientRepository (ISP): el
// export no reusa los listados existentes (D8) y sus repos no cargan con
// métodos que solo usa esta ruta.
type ExportRepository interface {
	// ListExportSales son las ventas con el nombre de la cuenta profesional
	// (vacío si no hay fila en users) y la suma de cantidades de sus ítems, más
	// reciente primero (created_at DESC, id).
	ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error)
	// ListExportCashCloses son los cierres con closed_at en [from,to), más
	// reciente primero.
	ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error)
	// ListExportClients son las fichas del salón ordenadas por nombre.
	ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error)
}
