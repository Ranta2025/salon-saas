package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sort"
	"time"
)

// Fake de las lecturas masivas del export (F4) para el contrato HTTP: mismo
// scope por salón, misma ventana [from,to) sobre el instante de la fila, mismo
// orden y mismo LIMIT con la fila de más que pide el caso de uso.

type StoredSale struct {
	Salon string
	Row   domain.ExportSaleRow
}

type StoredClose struct {
	Salon string
	Row   domain.ExportCashCloseRow
}

type StoredClient struct {
	Salon string
	Row   domain.ExportClientRow
}

type Exports struct {
	Sales   []StoredSale
	Closes  []StoredClose
	Clients []StoredClient
	// limits guarda el LIMIT de cada lectura: el caso de uso siempre pide el
	// tope + 1 (D3).
	Limits []int
}

func NewExports() *Exports { return &Exports{} }

func (f *Exports) AddSale(salonID string, row domain.ExportSaleRow) {
	f.Sales = append(f.Sales, StoredSale{Salon: salonID, Row: row})
}

func (f *Exports) AddClose(salonID string, row domain.ExportCashCloseRow) {
	f.Closes = append(f.Closes, StoredClose{Salon: salonID, Row: row})
}

func (f *Exports) AddClient(salonID string, row domain.ExportClientRow) {
	f.Clients = append(f.Clients, StoredClient{Salon: salonID, Row: row})
}

func (f *Exports) ListExportSales(salonID string, from, to time.Time, limit int) ([]domain.ExportSaleRow, error) {
	f.Limits = append(f.Limits, limit)
	out := []domain.ExportSaleRow{}
	for _, s := range f.Sales {
		if s.Salon != salonID || s.Row.CreatedAt.Before(from) || !s.Row.CreatedAt.Before(to) {
			continue
		}
		out = append(out, s.Row)
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

func (f *Exports) ListExportCashCloses(salonID string, from, to time.Time, limit int) ([]domain.ExportCashCloseRow, error) {
	f.Limits = append(f.Limits, limit)
	out := []domain.ExportCashCloseRow{}
	for _, c := range f.Closes {
		if c.Salon != salonID || c.Row.ClosedAt.Before(from) || !c.Row.ClosedAt.Before(to) {
			continue
		}
		out = append(out, c.Row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClosedAt.After(out[j].ClosedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Exports) ListExportClients(salonID string, limit int) ([]domain.ExportClientRow, error) {
	f.Limits = append(f.Limits, limit)
	out := []domain.ExportClientRow{}
	for _, c := range f.Clients {
		if c.Salon != salonID {
			continue
		}
		out = append(out, c.Row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ ports.ExportRepository = (*Exports)(nil)
