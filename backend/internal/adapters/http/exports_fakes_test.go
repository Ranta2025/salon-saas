package httpapi

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fake de las lecturas masivas del export (F4) para el contrato HTTP: mismo
// scope por salón, misma ventana [from,to) sobre el instante de la fila, mismo
// orden y mismo LIMIT con la fila de más que pide el caso de uso.

type httpStoredSale struct {
	salon string
	row   domain.ExportSaleRow
}

type httpStoredClose struct {
	salon string
	row   domain.ExportCashCloseRow
}

type httpStoredClient struct {
	salon string
	row   domain.ExportClientRow
}

type mExports struct {
	sales   []httpStoredSale
	closes  []httpStoredClose
	clients []httpStoredClient
	// limits guarda el LIMIT de cada lectura: el caso de uso siempre pide el
	// tope + 1 (D3).
	limits []int
}

func newMExports() *mExports { return &mExports{} }

func (f *mExports) addSale(salonID string, row domain.ExportSaleRow) {
	f.sales = append(f.sales, httpStoredSale{salon: salonID, row: row})
}

func (f *mExports) addClose(salonID string, row domain.ExportCashCloseRow) {
	f.closes = append(f.closes, httpStoredClose{salon: salonID, row: row})
}

func (f *mExports) addClient(salonID string, row domain.ExportClientRow) {
	f.clients = append(f.clients, httpStoredClient{salon: salonID, row: row})
}

func (f *mExports) ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error) {
	f.limits = append(f.limits, limit)
	out := []domain.ExportSaleRow{}
	for _, s := range f.sales {
		if s.salon != salonID || s.row.CreatedAt.Before(from) || !s.row.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s.row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].SaleID > out[j].SaleID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *mExports) ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error) {
	f.limits = append(f.limits, limit)
	out := []domain.ExportCashCloseRow{}
	for _, c := range f.closes {
		if c.salon != salonID || c.row.ClosedAt.Before(from) || !c.row.ClosedAt.Before(to) {
			continue
		}
		out = append(out, c.row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClosedAt.After(out[j].ClosedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *mExports) ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error) {
	f.limits = append(f.limits, limit)
	out := []domain.ExportClientRow{}
	for _, c := range f.clients {
		if c.salon != salonID {
			continue
		}
		out = append(out, c.row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ ports.ExportRepository = (*mExports)(nil)
