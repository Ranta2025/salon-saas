// ExportService: las tres exportaciones CSV del salón (F4 del contrato Fase 6).
// Solo dueño/administración (D1: una exportación es una salida masiva, no una
// consulta); el rango son días LOCALES del salón (D2) y el tope de filas es un
// campo para que las pruebas lo puedan bajar (D3). El armado del archivo y las
// frases viven en el dominio: acá solo se juntan permiso, ventana y lectura.
package app

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ExportFile es lo que el handler escribe en la respuesta: el nombre saneado
// (D6), los bytes con BOM y la cantidad de filas (para el log de D7).
type ExportFile struct {
	Name string
	Data []byte
	Rows int
}

// ExportService orquesta las tres exportaciones.
type ExportService struct {
	Exports     ports.ExportRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón: el rango son días locales y las
	// fechas del archivo se escriben en esa zona. nil = configuración de fábrica.
	Settings ports.SalonSettingsRepository
	// Now es el reloj de «hoy» (el día del nombre de clientes). nil = time.Now.
	Now func() time.Time
	// RowCap es el tope de filas por archivo (D3). 0 = 50000; las pruebas
	// inyectan un tope chico porque 50000 filas reales no se cargan.
	RowCap int
}

// cap es el tope efectivo de filas por archivo.
func (s ExportService) cap() int {
	if s.RowCap > 0 {
		return s.RowCap
	}
	return domain.ExportRowsCap
}

// now es el reloj de «hoy».
func (s ExportService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Sales arma el CSV de ventas del rango. Una fila por VENTA.
//
// Orden de las reglas: (1) permisos, (2) rango válido en la zona del salón, (3)
// la lectura con tope + 1, (4) más filas que el tope es 400 SIN archivo (D3).
func (s ExportService) Sales(actor domain.Membership, salonID, from, to string) (ExportFile, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return ExportFile{}, err
	}
	loc, err := SalonLocation(s.Settings, salonID)
	if err != nil {
		return ExportFile{}, err
	}
	window, err := domain.ExportRange(from, to, loc)
	if err != nil {
		return ExportFile{}, err
	}
	rows, err := s.Exports.ListExportSales(salonID, window.From, window.To, s.cap()+1)
	if err != nil {
		return ExportFile{}, err
	}
	if len(rows) > s.cap() {
		return ExportFile{}, domain.NewMessage(domain.ErrValidation, domain.MsgExportRowsTooMany)
	}
	return ExportFile{
		Name: domain.SalesCSVFilename(window.FromDate, window.ToDate),
		Data: domain.SalesCSV(rows, loc),
		Rows: len(rows),
	}, nil
}

// CashCloses arma el CSV de cierres de caja del rango (closed_at en la ventana).
func (s ExportService) CashCloses(actor domain.Membership, salonID, from, to string) (ExportFile, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return ExportFile{}, err
	}
	loc, err := SalonLocation(s.Settings, salonID)
	if err != nil {
		return ExportFile{}, err
	}
	window, err := domain.ExportRange(from, to, loc)
	if err != nil {
		return ExportFile{}, err
	}
	rows, err := s.Exports.ListExportCashCloses(salonID, window.From, window.To, s.cap()+1)
	if err != nil {
		return ExportFile{}, err
	}
	if len(rows) > s.cap() {
		return ExportFile{}, domain.NewMessage(domain.ErrValidation, domain.MsgExportRowsTooMany)
	}
	return ExportFile{
		Name: domain.CashClosesCSVFilename(window.FromDate, window.ToDate),
		Data: domain.CashClosesCSV(rows, loc),
		Rows: len(rows),
	}, nil
}

// Clients arma el CSV de clientes: sin rango (D2) y con el día local de HOY en
// el nombre del archivo. El tope tiene su propio mensaje porque una lista de
// clientes no se recorta por fecha: es la cartera completa del salón.
func (s ExportService) Clients(actor domain.Membership, salonID string) (ExportFile, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return ExportFile{}, err
	}
	loc, err := SalonLocation(s.Settings, salonID)
	if err != nil {
		return ExportFile{}, err
	}
	rows, err := s.Exports.ListExportClients(salonID, s.cap()+1)
	if err != nil {
		return ExportFile{}, err
	}
	if len(rows) > s.cap() {
		return ExportFile{}, domain.NewMessage(domain.ErrValidation, domain.MsgExportClientsTooMany)
	}
	today := domain.ExportDay(s.now(), loc)
	return ExportFile{
		Name: domain.ClientsCSVFilename(today),
		Data: domain.ClientsCSV(rows, loc),
		Rows: len(rows),
	}, nil
}
