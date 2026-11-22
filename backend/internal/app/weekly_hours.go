// Helpers del horario semanal (F2d1): cargar las filas, expandir la ventana única
// y armar la ventana de un día. Lo comparten la disponibilidad, la reserva
// pública, la vidriera y los casos de uso de Ajustes y Equipo.
package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// salonWeeklyHours carga el horario semanal del salón. Repo nil o sin filas = la
// ventana única de /settings todos los días (comportamiento de siempre): la
// ausencia de filas no es un dato faltante, es una configuración.
func salonWeeklyHours(repo ports.WeeklyHoursRepository, salonID string) (domain.WeeklyHours, error) {
	if repo == nil {
		return domain.NewWeeklyHours(nil)
	}
	days, err := repo.SalonHours(salonID)
	if err != nil {
		return domain.WeeklyHours{}, err
	}
	return domain.NewWeeklyHours(days)
}

// memberHours trae los tramos de todo el equipo indexados por persona: una query
// por día, no una por profesional.
func memberHours(repo ports.WeeklyHoursRepository, salonID string) (map[string][]domain.HoursSegment, error) {
	if repo == nil {
		return nil, nil
	}
	return repo.AllMemberHours(salonID)
}

// singleWindow es la ventana única de /settings (apertura y cierre) en minutos.
func singleWindow(settings domain.SalonSettings) (domain.Interval, error) {
	return settings.EveryDayWindow()
}

// salonWindow es la ventana del salón para un día ISO. La lista vuelve vacía
// cuando ese día está cerrado (con horario propio y sin fila).
func salonWindow(weekly domain.WeeklyHours, single domain.Interval, weekday int) ([]domain.Interval, error) {
	return domain.DayPlan{Single: single, Weekly: weekly}.Windows(weekday)
}

// expandedDays arma los 7 días con la ventana única: es la respuesta de
// custom=false de GET /salons/:id/hours y el horario de la vidriera de un salón
// que no configuró días.
func expandedDays(settings domain.SalonSettings) []domain.WeekdayHours {
	days := make([]domain.WeekdayHours, 0, domain.LastWeekday)
	for wd := domain.FirstWeekday; wd <= domain.LastWeekday; wd++ {
		days = append(days, domain.WeekdayHours{Weekday: wd, Open: settings.OpenTime, Close: settings.CloseTime})
	}
	return days
}
