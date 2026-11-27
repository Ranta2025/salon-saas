package exports

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de las lecturas masivas del export (F4). Reproducen la semántica de
// pgstore: scope por salón, ventana [from, to) sobre el instante de la fila,
// orden del contrato y LIMIT con la fila de más que pide el caso de uso.

// storedSale es una venta con su salón: la fila no lleva salon_id porque la
// query ya lo filtra, así que el fake lo guarda aparte.
type storedSale struct {
	salon string
	row   domain.ExportSaleRow
}

type storedClose struct {
	salon string
	row   domain.ExportCashCloseRow
}

type storedClient struct {
	salon string
	row   domain.ExportClientRow
}

// fakeExports es ports.ExportRepository en memoria.
type fakeExports struct {
	sales   []storedSale
	closes  []storedClose
	clients []storedClient
	// calls cuenta las lecturas y guarda lo que pidió el caso de uso: la
	// ventana (en UTC) y el LIMIT, que tiene que ser el tope + 1 (D3).
	salesCalls   int
	closesCalls  int
	clientsCalls int
	lastLimit    int
	lastFrom     time.Time
	lastTo       time.Time
	failWith     error
}

func (f *fakeExports) addSale(salonID string, row domain.ExportSaleRow) {
	f.sales = append(f.sales, storedSale{salon: salonID, row: row})
}

func (f *fakeExports) addClose(salonID string, row domain.ExportCashCloseRow) {
	f.closes = append(f.closes, storedClose{salon: salonID, row: row})
}

func (f *fakeExports) addClient(salonID string, row domain.ExportClientRow) {
	f.clients = append(f.clients, storedClient{salon: salonID, row: row})
}

func (f *fakeExports) ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error) {
	f.salesCalls++
	f.lastFrom, f.lastTo, f.lastLimit = from, to, limit
	if f.failWith != nil {
		return nil, f.failWith
	}
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

func (f *fakeExports) ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error) {
	f.closesCalls++
	f.lastFrom, f.lastTo, f.lastLimit = from, to, limit
	if f.failWith != nil {
		return nil, f.failWith
	}
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

func (f *fakeExports) ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error) {
	f.clientsCalls++
	f.lastLimit = limit
	if f.failWith != nil {
		return nil, f.failWith
	}
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

var _ ports.ExportRepository = (*fakeExports)(nil)
