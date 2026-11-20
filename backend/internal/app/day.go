package app

import (
	"time"
)

// Helpers de ventana temporal. El "día del salón" es el día LOCAL del salón
// (su zona horaria), no el día UTC: el cierre de caja tiene que abrir a
// medianoche local. El calendario sigue trabajando en UTC (los turnos son
// instantes); lo único que cambia de zona es qué día es "hoy" para el negocio.
const occupancyDayMinutes = 12 * 60 // jornada de referencia para ocupación

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func endOfUTCDay(t time.Time) time.Time { return startOfUTCDay(t).Add(24 * time.Hour) }

// startOfSalonDay es la medianoche local de la fecha de t en la zona del salón.
// Si loc es nil, cae al día UTC (compatibilidad y tests sin configuración).
func startOfSalonDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		return startOfUTCDay(t)
	}
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}
