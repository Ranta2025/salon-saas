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

// salonTimeAt arma el instante de un minuto local (09:00 = 540) del día de
// reference, en la zona del salón. Es cómo se traduce "09:00 del martes" a un
// instante: el día es el del salón, no el de UTC.
func salonTimeAt(reference time.Time, minutes int, loc *time.Location) time.Time {
	local := reference.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), minutes/60, minutes%60, 0, 0, loc)
}

// localMinutes es el minuto del día local (09:00 = 540) de un instante, en la
// zona del salón. Sirve para comparar un turno contra las ventanas del día.
func localMinutes(t time.Time, loc *time.Location) int {
	local := t.In(loc)
	return local.Hour()*60 + local.Minute()
}
