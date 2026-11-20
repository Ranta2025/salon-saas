package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SalonSettings es la configuración operativa del salón: en qué zona horaria
// vive, a qué hora abre y cierra, y cada cuántos minutos empieza un turno.
//
// La zona horaria no es un detalle de presentación: define el día del salón
// (la ventana del cierre de caja) y el horario de la reserva pública. Si esto
// se calculara en UTC, un salón de CDMX cerraría la caja a las 18:00 locales.
type SalonSettings struct {
	SalonID     string
	Timezone    string // nombre IANA, ej: "America/Mexico_City"
	OpenTime    string // "HH:MM" (hora local del salón)
	CloseTime   string // "HH:MM" (hora local del salón)
	SlotStepMin int    // cada cuántos minutos se ofrece un turno
}

// Defaults de fábrica: los de la migración. Un salón viejo (o una DB sin la
// fila de settings) se comporta igual que uno nuevo.
const (
	DefaultTimezone    = "America/Mexico_City"
	DefaultOpenTime    = "09:00"
	DefaultCloseTime   = "19:00"
	DefaultSlotStepMin = 30

	// Rango del paso de turnos: menos de 5 minutos no es agenda de salón (es
	// una lista de espera) y más de 4 h no entra en la jornada.
	MinSlotStepMin = 5
	MaxSlotStepMin = 240
)

var clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// DefaultSalonSettings devuelve la configuración de fábrica de un salón.
func DefaultSalonSettings(salonID string) SalonSettings {
	return SalonSettings{
		SalonID:     salonID,
		Timezone:    DefaultTimezone,
		OpenTime:    DefaultOpenTime,
		CloseTime:   DefaultCloseTime,
		SlotStepMin: DefaultSlotStepMin,
	}
}

// NewSalonSettings normaliza (trim) y valida una configuración recibida.
func NewSalonSettings(salonID, timezone, openTime, closeTime string, slotStepMin int) (SalonSettings, error) {
	s := SalonSettings{
		SalonID:     strings.TrimSpace(salonID),
		Timezone:    strings.TrimSpace(timezone),
		OpenTime:    strings.TrimSpace(openTime),
		CloseTime:   strings.TrimSpace(closeTime),
		SlotStepMin: slotStepMin,
	}
	if err := s.Validate(); err != nil {
		return SalonSettings{}, err
	}
	return s, nil
}

// Validate concentra las reglas: crear y actualizar no pueden divergir.
func (s SalonSettings) Validate() error {
	if s.SalonID == "" {
		return fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if s.Timezone == "" {
		return fmt.Errorf("%w: la zona horaria es obligatoria", ErrValidation)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("%w: zona horaria inválida %q (usá un nombre IANA, por ejemplo America/Mexico_City)", ErrValidation, s.Timezone)
	}
	open, err := clockMinutes(s.OpenTime, "la hora de apertura")
	if err != nil {
		return err
	}
	closeAt, err := clockMinutes(s.CloseTime, "la hora de cierre")
	if err != nil {
		return err
	}
	if open >= closeAt {
		return fmt.Errorf("%w: la hora de apertura debe ser anterior a la de cierre", ErrValidation)
	}
	if s.SlotStepMin < MinSlotStepMin || s.SlotStepMin > MaxSlotStepMin {
		return fmt.Errorf("%w: el paso de turnos debe estar entre %d y %d minutos", ErrValidation, MinSlotStepMin, MaxSlotStepMin)
	}
	return nil
}

// Location resuelve la zona horaria (solo válida si Validate pasó).
func (s SalonSettings) Location() (*time.Location, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: zona horaria inválida %q", ErrValidation, s.Timezone)
	}
	return loc, nil
}

// Window devuelve el inicio y el fin del horario para la fecha local de day.
// Los dos instantes conservan la zona del salón: el día del salón es el día
// local, no el día UTC.
func (s SalonSettings) Window(day time.Time) (time.Time, time.Time, error) {
	loc, err := s.Location()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	open, err := s.OpenMinute()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	closeMin, err := s.CloseMinute()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if open >= closeMin {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: la hora de apertura debe ser anterior a la de cierre", ErrValidation)
	}
	local := day.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), open/60, open%60, 0, 0, loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), closeMin/60, closeMin%60, 0, 0, loc)
	return start, end, nil
}

// OpenMinute y CloseMinute devuelven los minutos desde la medianoche local.
func (s SalonSettings) OpenMinute() (int, error) {
	return clockMinutes(s.OpenTime, "la hora de apertura")
}

// CloseMinute es el minuto local en que el salón deja de aceptar turnos.
func (s SalonSettings) CloseMinute() (int, error) {
	return clockMinutes(s.CloseTime, "la hora de cierre")
}

// clockMinutes parsea "HH:MM" a minutos desde la medianoche.
func clockMinutes(value, field string) (int, error) {
	if !clockRe.MatchString(value) {
		return 0, fmt.Errorf("%w: %s debe tener formato HH:MM (00:00 a 23:59)", ErrValidation, field)
	}
	h, _ := strconv.Atoi(value[:2])
	m, _ := strconv.Atoi(value[3:])
	return h*60 + m, nil
}
