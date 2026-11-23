package domain

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Exportación CSV de una liquidación (F3.3, D8 del contrato F3). Puro: arma los
// bytes a partir de la corrida congelada, sin tocar la DB ni el HTTP.
//
// Reglas del contrato:
//   - UTF-8 con BOM (Excel abre bien los acentos).
//   - Encabezado y orden de columnas EXACTOS.
//   - Montos como decimales con punto y 2 decimales, calculados desde los
//     centavos ENTEROS (nunca float: 0.1+0.2 no suma 0.3).
//   - Comillas RFC 4180 (comillas dobles, comas y saltos de línea) y líneas
//     terminadas en CRLF.
//   - NEUTRALIZACIÓN de fórmulas: toda celda de TEXTO que empiece con = + - @
//     tab o CR se prefija con una comilla simple. Un nombre "=SUM(A1)" no se
//     ejecuta al abrir el archivo.

// PayrollCSVHeader es el encabezado EXACTO del contrato (no se toca).
const PayrollCSVHeader = "Profesional,Estado,Ventas,Total vendido,Comisión,Fijo,Ajuste,Nota del ajuste,Total"

const csvBOM = "\xef\xbb\xbf"

// csvLineEnd es el fin de línea de RFC 4180.
const csvLineEnd = "\r\n"

// CentsDecimal formatea centavos enteros como decimal con punto y 2 dígitos:
// 0 -> "0.00", 5 -> "0.05", 123456789 -> "1234567.89", -250 -> "-2.50".
func CentsDecimal(cents int) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	// División y módulo enteros: el decimal sale exacto, sin float.
	return sign + strconv.Itoa(cents/100) + "." + twoDigits(cents%100)
}

// twoDigits completa con un cero a la izquierda (5 -> "05").
func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// CSVCell serializa una celda de TEXTO: primero neutraliza la inyección de
// fórmulas (D8) y después aplica las comillas de RFC 4180.
func CSVCell(text string) string {
	if neutralizesFormula(text) {
		text = "'" + text
	}
	return csvQuote(text)
}

// neutralizesFormula dice si la celda se interpretaría como fórmula al abrirla
// en una planilla. Reglas:
//   - tab o CR como PRIMER carácter: se neutralizan igual que siempre (regla
//     original de D8);
//   - si no, se saltan los espacios Unicode iniciales (espacio, NBSP U+00A0,
//     em space y demás) y se decide con el primero que queda: = + - @ o su
//     forma de ancho completo ＝ ＋ － ＠. " =SUM(A1)" también se ejecuta al
//     abrir el archivo, así que el espacio inicial no salva la celda;
//   - una celda solo de blancos, un número o un nombre común no se tocan.
func neutralizesFormula(text string) bool {
	if text == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(text)
	if first == '\t' || first == '\r' {
		return true
	}
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	if trimmed == "" {
		return false
	}
	leading, _ := utf8.DecodeRuneInString(trimmed)
	switch leading {
	case '=', '+', '-', '@', '＝', '＋', '－', '＠':
		return true
	default:
		return false
	}
}

// csvQuote aplica RFC 4180: comillas dobles si la celda tiene coma, comilla o
// salto de línea; una comilla interna se duplica.
func csvQuote(text string) string {
	if !strings.ContainsAny(text, "\",\r\n") {
		return text
	}
	return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
}

// PayrollCSVStatus es el estado de la persona en el archivo.
func PayrollCSVStatus(active bool) string {
	if active {
		return "Activo"
	}
	return "Dado de baja"
}

// PayrollCSV arma el archivo completo: BOM, encabezado y una fila por línea en
// el orden en que vienen (el adaptador las ordena como la vista previa).
func PayrollCSV(run PayrollRun) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	b.WriteString(PayrollCSVHeader)
	b.WriteString(csvLineEnd)
	for _, l := range run.Lines {
		cells := []string{
			CSVCell(l.Name),
			csvQuote(PayrollCSVStatus(l.Active)),
			strconv.Itoa(l.SalesCount),
			CentsDecimal(l.SalesTotalCents),
			CentsDecimal(l.CommissionCents),
			CentsDecimal(l.FixedCents),
			CentsDecimal(l.AdjustmentCents),
			CSVCell(l.AdjustmentNote),
			CentsDecimal(l.TotalCents),
		}
		b.WriteString(strings.Join(cells, ","))
		b.WriteString(csvLineEnd)
	}
	return []byte(b.String())
}

// PayrollCSVFilename arma SOLO con el tipo y las dos fechas el nombre del
// archivo: `nomina-<kind>-<from>-<to>.csv`. Cualquier carácter que no sea
// [a-z0-9-] se descarta (minúsculas), así ni un salto de línea ni una comilla
// del nombre llegan a Content-Disposition.
func PayrollCSVFilename(kind PayPeriodKind, from, to string) string {
	return "nomina-" + csvSafe(string(kind)) + "-" + csvSafe(from) + "-" + csvSafe(to) + ".csv"
}

// csvSafe deja pasar solo el alfabeto del nombre del archivo.
func csvSafe(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
