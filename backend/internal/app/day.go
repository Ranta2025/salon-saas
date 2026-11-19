package app

import (
	"time"
)

// Helpers de ventana temporal. El "día" del salón es el día UTC: el calendario
// ya trabaja en UTC y el frontend no debería adivinar zonas.
const occupancyDayMinutes = 12 * 60 // jornada de referencia para ocupación

func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func endOfUTCDay(t time.Time) time.Time { return startOfUTCDay(t).Add(24 * time.Hour) }
