package domain

import (
	"strconv"
	"strings"
	"time"
)

// Exportación CSV de ventas, cierres de caja y clientes (F4 del contrato Fase
// 6). Este archivo es PURO: arma los bytes a partir de filas ya unidas por el
// adaptador, sin tocar la DB ni el HTTP.
//
// Reglas del contrato (F4):
//   - D2: from/to son días LOCALES del salón; la ventana es [from 00:00 local,
//     (to+1) 00:00 local) con time.Date (correcta en los días de 23 y 25 horas).
//     Las fechas del archivo se escriben en la zona del salón como
//     «AAAA-MM-DD HH:MM» y el alta de un cliente como «AAAA-MM-DD».
//   - D3: tope de 50000 filas por archivo; el caso de uso pide una fila más.
//   - D4: mínimo necesario (las ventas no llevan teléfono ni email), montos en
//     centavos crudos como decimales con punto y método de pago en español.
//   - D5: toda celda de TEXTO pasa por CSVCell (la regla de la nómina); los
//     números se escriben crudos, así una diferencia de caja negativa sigue
//     siendo un número.
//   - D6: nombres de archivo saneados con csvSafe.
//
// Reutiliza SIN MODIFICAR los helpers de payroll_csv.go (CSVCell, CentsDecimal,
// csvQuote, csvSafe, el BOM y el CRLF): una sola regla de CSV en el dominio.

// Cabeceras EXACTAS del contrato (no se tocan).
const (
	ExportSalesCSVHeader      = "Fecha,Venta,Profesional,Cliente,Método,Ítems,Total"
	ExportCashClosesCSVHeader = "Apertura,Cierre,Efectivo del sistema,Efectivo contado,Diferencia,Nota"
	ExportClientsCSVHeader    = "Nombre,Teléfono,Email,Notas,Alta"
)

const (
	// ExportRowsCap es el tope por archivo (D3): el adaptador recibe cap+1 y el
	// caso de uso convierte «hay más» en 400, nunca en un archivo truncado.
	ExportRowsCap = 50000
	// MaxExportRangeDays es el rango máximo de días inclusivos (D2).
	MaxExportRangeDays = 366
)

// Mensajes EXACTOS del contrato (los ve la persona tal cual).
const (
	// MsgExportRangeInvalid: faltan from/to, formato inválido, to < from o más
	// de 366 días.
	MsgExportRangeInvalid = "Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días"
	// MsgExportRowsTooMany: ventas o cierres superan el tope (D3).
	MsgExportRowsTooMany = "El archivo tiene más de 50000 filas: elegí un rango más corto"
	// MsgExportClientsTooMany: la lista de clientes supera el tope (D3).
	MsgExportClientsTooMany = "Hay más de 50000 clientes: no se puede exportar de una vez"
)

// exportStampLayout es el formato de las fechas con hora en el archivo.
const exportStampLayout = "2006-01-02 15:04"

// ExportSaleRow es una VENTA ya unida con el nombre de la cuenta profesional y
// la suma de cantidades de sus ítems. Una fila por venta.
type ExportSaleRow struct {
	CreatedAt        time.Time
	SaleID           string
	ProfessionalName string
	ClientName       string
	Method           PaymentMethod
	Items            int
	TotalCents       int
}

// ExportCashCloseRow es un cierre de caja dentro de la ventana.
type ExportCashCloseRow struct {
	OpenedAt        time.Time
	ClosedAt        time.Time
	SystemCashCents int
	CountedCents    int
	DifferenceCents int
	Note            string
}

// ExportClientRow es la ficha de un cliente del salón.
type ExportClientRow struct {
	Name      string
	Phone     string
	Email     string
	Notes     string
	CreatedAt time.Time
}

// PaymentMethodLabel es el método de pago en español (D4). Un método
// desconocido (imposible por el CHECK de la DB) sale tal cual, nunca vacío.
func PaymentMethodLabel(m PaymentMethod) string {
	switch m {
	case MethodCash:
		return "Efectivo"
	case MethodCard:
		return "Tarjeta"
	case MethodTransfer:
		return "Transferencia"
	default:
		return string(m)
	}
}

// ExportTimestamp escribe un instante en la zona del salón como
// «AAAA-MM-DD HH:MM». Sin zona (nil) se usa UTC.
func ExportTimestamp(t time.Time, loc *time.Location) string {
	return t.In(exportLocation(loc)).Format(exportStampLayout)
}

// ExportDay escribe el DÍA local de un instante como «AAAA-MM-DD» (el alta de
// un cliente).
func ExportDay(t time.Time, loc *time.Location) string {
	return t.In(exportLocation(loc)).Format(dateLayout)
}

// exportLocation evita el pánico de time.Time.In(nil): sin zona, UTC.
func exportLocation(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// SalesCSV arma el archivo de ventas: BOM, cabecera y una fila por venta en el
// orden en que vienen (el adaptador las ordena created_at DESC, id).
func SalesCSV(rows []ExportSaleRow, loc *time.Location) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	b.WriteString(ExportSalesCSVHeader)
	b.WriteString(csvLineEnd)
	for _, r := range rows {
		cells := []string{
			csvQuote(ExportTimestamp(r.CreatedAt, loc)),
			csvQuote(r.SaleID),
			CSVCell(r.ProfessionalName),
			CSVCell(r.ClientName),
			CSVCell(PaymentMethodLabel(r.Method)),
			strconv.Itoa(r.Items),
			CentsDecimal(r.TotalCents),
		}
		b.WriteString(strings.Join(cells, ","))
		b.WriteString(csvLineEnd)
	}
	return []byte(b.String())
}

// CashClosesCSV arma el archivo de cierres: apertura y cierre locales, los tres
// montos crudos (la diferencia conserva su signo) y la nota neutralizada.
func CashClosesCSV(rows []ExportCashCloseRow, loc *time.Location) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	b.WriteString(ExportCashClosesCSVHeader)
	b.WriteString(csvLineEnd)
	for _, r := range rows {
		cells := []string{
			csvQuote(ExportTimestamp(r.OpenedAt, loc)),
			csvQuote(ExportTimestamp(r.ClosedAt, loc)),
			CentsDecimal(r.SystemCashCents),
			CentsDecimal(r.CountedCents),
			CentsDecimal(r.DifferenceCents),
			CSVCell(r.Note),
		}
		b.WriteString(strings.Join(cells, ","))
		b.WriteString(csvLineEnd)
	}
	return []byte(b.String())
}

// ClientsCSV arma el archivo de clientes: nombre, teléfono, email, notas y el
// DÍA local del alta (el adaptador los ordena por nombre).
func ClientsCSV(rows []ExportClientRow, loc *time.Location) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	b.WriteString(ExportClientsCSVHeader)
	b.WriteString(csvLineEnd)
	for _, r := range rows {
		cells := []string{
			CSVCell(r.Name),
			CSVCell(r.Phone),
			CSVCell(r.Email),
			CSVCell(r.Notes),
			csvQuote(ExportDay(r.CreatedAt, loc)),
		}
		b.WriteString(strings.Join(cells, ","))
		b.WriteString(csvLineEnd)
	}
	return []byte(b.String())
}

// SalesCSVFilename arma `ventas-<from>_<to>.csv` con las dos fechas saneadas.
func SalesCSVFilename(from, to string) string {
	return "ventas-" + csvSafe(from) + "_" + csvSafe(to) + ".csv"
}

// CashClosesCSVFilename arma `cierres-<from>_<to>.csv`.
func CashClosesCSVFilename(from, to string) string {
	return "cierres-" + csvSafe(from) + "_" + csvSafe(to) + ".csv"
}

// ClientsCSVFilename arma `clientes-<day>.csv` con el día local de hoy.
func ClientsCSVFilename(day string) string {
	return "clientes-" + csvSafe(day) + ".csv"
}

// ExportWindow es el rango ya validado: las dos fechas normalizadas (para el
// nombre del archivo) y los dos instantes UTC de la ventana [From, To).
type ExportWindow struct {
	FromDate string
	ToDate   string
	From     time.Time
	To       time.Time
}

// ExportRange valida from/to (YYYY-MM-DD, inclusivos, to >= from y hasta 366
// días) y calcula la ventana [from 00:00 local, (to+1) 00:00 local) en la zona
// del salón. time.Date resuelve solo los días de 23 y 25 horas: nunca se suma
// 24 h fijas.
func ExportRange(from, to string, loc *time.Location) (ExportWindow, error) {
	invalid := NewMessage(ErrValidation, MsgExportRangeInvalid)
	f, err := time.Parse(dateLayout, strings.TrimSpace(from))
	if err != nil {
		return ExportWindow{}, invalid
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(to))
	if err != nil {
		return ExportWindow{}, invalid
	}
	if t.Before(f) {
		return ExportWindow{}, invalid
	}
	if int(t.Sub(f).Hours()/24)+1 > MaxExportRangeDays {
		return ExportWindow{}, invalid
	}
	location := exportLocation(loc)
	// 00:00 local del primer día y 00:00 local del día siguiente al último.
	start := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, location)
	end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, location)
	return ExportWindow{
		FromDate: f.Format(dateLayout),
		ToDate:   t.Format(dateLayout),
		From:     start.UTC(),
		To:       end.UTC(),
	}, nil
}
