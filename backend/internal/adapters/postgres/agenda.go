package pgstore

import (
	"context"
	"time"
)

// Agregados de agenda para el dashboard (ports.AgendaStats). Viven acá y no en
// bookings.go para no tocar el adaptador de reservas ya probado: el método es
// del mismo tipo, en el mismo paquete.

// StatsRange cuenta turnos activos y suma minutos reservados en [from,to).
// Canceladas y no-shows liberaron el hueco: no cuentan como ocupación.
func (r Bookings) StatsRange(salonID string, from, to time.Time) (int, int, error) {
	var appointments, minutes int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int,
		        COALESCE(SUM(EXTRACT(EPOCH FROM (ends_at - starts_at)) / 60), 0)::int
		 FROM bookings
		 WHERE salon_id=$1 AND starts_at >= $2 AND starts_at < $3
		   AND status IN ('booked','done')`,
		salonID, from, to).Scan(&appointments, &minutes)
	if err != nil {
		return 0, 0, err
	}
	return appointments, minutes, nil
}
