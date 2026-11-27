// ProfitService: el reporte de ganancia neta del período (F3b, D1/D2/D6/D7 del
// contrato). Deriva el período en la zona del SALÓN y le pide al adaptador UNA
// sola foto de ingresos, gastos y nómina pagada; el armado del reporte (ceros,
// neto y totales) es del dominio.
package finance

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// ProfitService arma el reporte de ganancia. No escribe nada.
type ProfitService struct {
	Profit      ports.ProfitRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón: día/semana/mes son LOCALES, y la
	// zona viaja al SQL para agrupar las ventas y los pagos por día local.
	Settings ports.SalonSettingsRepository
	// Now es el reloj de «hoy» cuando el pedido no trae `date`. nil = time.Now.
	Now func() time.Time
}

// Report devuelve la ganancia neta del período. Solo owner/admin (D5): recepción,
// profesional y cliente reciben 403. Un `kind` o un `date` inválidos son 400
// «Período inválido».
func (s ProfitService) Report(actor domain.Membership, salonID, kind, date string) (domain.ProfitReport, error) {
	if _, err := kernel.ManageActor(s.Memberships, actor, salonID); err != nil {
		return domain.ProfitReport{}, err
	}
	settings, err := kernel.LoadSalonSettings(s.Settings, salonID)
	if err != nil {
		return domain.ProfitReport{}, err
	}
	loc, err := settings.Location()
	if err != nil {
		return domain.ProfitReport{}, err
	}
	p, err := domain.NewProfitPeriod(kind, date, s.now(), loc)
	if err != nil {
		return domain.ProfitReport{}, err
	}
	from, to := p.Window()
	data, err := s.Profit.DailyProfit(salonID, settings.Timezone, from, to)
	if err != nil {
		return domain.ProfitReport{}, err
	}
	return domain.NewProfitReport(p, data), nil
}

// now es el reloj del caso de uso.
func (s ProfitService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
