package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Liquidaciones de nómina (F3.3, D6 del contrato F3). Este archivo es PURO:
// stdlib y nada más. La corrida congela lo que la vista previa calculó y la
// línea guarda su foto (nombre, ventas, comisión, fijo, ajuste y total); desde
// que se paga, nada de eso se puede mover.

// PayrollRunStatus es el estado de una liquidación. `open` se puede editar
// (ajuste por línea) y borrar; `paid` no admite NINGÚN cambio.
type PayrollRunStatus string

const (
	PayrollRunOpen PayrollRunStatus = "open"
	PayrollRunPaid PayrollRunStatus = "paid"
)

// Valid dice si el estado está en el catálogo.
func (s PayrollRunStatus) Valid() bool {
	return s == PayrollRunOpen || s == PayrollRunPaid
}

// Mensajes EXACTOS del contrato F3.3 (los ve la persona tal cual). Viven acá
// para que el caso de uso, el adaptador y los tests digan la misma frase.
const (
	// MsgPayrollRunOverlaps: la EXCLUDE de la DB (23P01) traducida. Dos
	// liquidaciones de cualquier tipo no pueden pisar los mismos días.
	MsgPayrollRunOverlaps = "Ya hay una liquidación que cubre esos días"
	// MsgPayrollRunPaid: la pagada es inmutable (ajuste, borrado y segundo pago).
	MsgPayrollRunPaid = "La liquidación ya está pagada: no se puede modificar"
	// MsgPayrollPeriodNotClosed: crear exige un período terminado (D5).
	MsgPayrollPeriodNotClosed = "El período todavía no terminó: liquidá cuando haya pasado"
	// MsgPayrollNothingToPay: sin líneas no hay corrida.
	MsgPayrollNothingToPay = "No hay nada para liquidar en ese período"
	// MsgPayrollNoteRequired: un ajuste distinto de 0 necesita su explicación.
	MsgPayrollNoteRequired = "Explicá el ajuste en una nota"
	// MsgPayrollNegativeTotal: la línea nunca queda por debajo de cero.
	MsgPayrollNegativeTotal = "El ajuste dejaría el total en negativo"
	// MsgPayrollAdjustmentMissing: `adjustment_cents` ausente o null (lección de
	// F3.2: nunca un valor por defecto en silencio).
	MsgPayrollAdjustmentMissing = "Faltan datos del ajuste: indicá el monto del ajuste"
)

// MaxPayrollNote es el tope de la nota del ajuste (200 caracteres, sin saltos
// de línea). También vive en la DB (CHECK de la migración 025).
const MaxPayrollNote = 200

// PayrollLine es una línea CONGELADA de la liquidación: lo que se le paga a UNA
// persona en el período. TotalCents = comisión + fijo + ajuste y nunca es
// negativo.
type PayrollLine struct {
	UserID          string
	Name            string
	Active          bool
	SalesCount      int
	SalesTotalCents int
	CommissionCents int
	FixedCents      int
	AdjustmentCents int
	AdjustmentNote  string
	TotalCents      int
	Estimated       bool
}

// PayrollTotals es la suma de las líneas de la corrida (incluye el ajuste, que
// la vista previa no tiene).
type PayrollTotals struct {
	CommissionCents int
	FixedCents      int
	AdjustmentCents int
	TotalCents      int
}

// PayrollRun es la liquidación completa con sus líneas (detalle). From y To son
// las fechas locales INCLUSIVAS del salón (YYYY-MM-DD), las mismas del contrato.
type PayrollRun struct {
	ID        string
	SalonID   string
	Kind      PayPeriodKind
	From      string
	To        string
	Status    PayrollRunStatus
	CreatedBy string
	CreatedAt time.Time
	PaidAt    *time.Time
	PaidBy    string
	Lines     []PayrollLine
}

// PayrollRunSummary es la fila del listado: la cabecera con la cantidad de
// líneas y los totales, sin traer el detalle.
type PayrollRunSummary struct {
	ID         string
	Kind       PayPeriodKind
	From       string
	To         string
	Status     PayrollRunStatus
	CreatedAt  time.Time
	PaidAt     *time.Time
	LinesCount int
	Totals     PayrollTotals
}

// Open dice si la liquidación todavía se puede editar o borrar.
func (r PayrollRun) Open() bool { return r.Status == PayrollRunOpen }

// Paid dice si la liquidación está pagada (inmutable).
func (r PayrollRun) Paid() bool { return r.Status == PayrollRunPaid }

// Totals suma las líneas, con el ajuste incluido.
func (r PayrollRun) Totals() PayrollTotals {
	var t PayrollTotals
	for _, l := range r.Lines {
		t.CommissionCents += l.CommissionCents
		t.FixedCents += l.FixedCents
		t.AdjustmentCents += l.AdjustmentCents
		t.TotalCents += l.TotalCents
	}
	return t
}

// FindLine busca la línea de una persona dentro de la corrida.
func (r PayrollRun) FindLine(userID string) (PayrollLine, bool) {
	for _, l := range r.Lines {
		if l.UserID == userID {
			return l, true
		}
	}
	return PayrollLine{}, false
}

// NewPayrollRun arma la corrida OPEN que congela el período ya derivado y las
// líneas ya calculadas por la vista previa. Valida lo que la DB también exige:
// tipo y fechas del período, al menos una línea, una sola línea por persona y
// montos dentro de rango con total no negativo.
func NewPayrollRun(id, salonID string, p PayPeriod, createdBy string, now time.Time, lines []PayrollLine) (PayrollRun, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(salonID) == "" {
		return PayrollRun{}, NewMessage(ErrValidation, "La liquidación y el salón son obligatorios")
	}
	if !p.Kind.Valid() {
		return PayrollRun{}, NewMessage(ErrValidation, "Período inválido")
	}
	if p.To.Before(p.From) {
		return PayrollRun{}, NewMessage(ErrValidation, "Período inválido")
	}
	if len(lines) == 0 {
		return PayrollRun{}, NewMessage(ErrValidation, MsgPayrollNothingToPay)
	}
	seen := make(map[string]bool, len(lines))
	frozen := make([]PayrollLine, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l.UserID) == "" {
			return PayrollRun{}, NewMessage(ErrValidation, "La línea de la liquidación necesita una persona")
		}
		if seen[l.UserID] {
			return PayrollRun{}, NewMessage(ErrValidation, "Hay dos líneas de la misma persona en la liquidación")
		}
		seen[l.UserID] = true
		if utf8.RuneCountInString(l.Name) > MaxTextName {
			return PayrollRun{}, NewMessage(ErrValidation,
				fmt.Sprintf("El nombre no puede superar los %d caracteres", MaxTextName))
		}
		frozenLine, err := frozenPayrollLine(l)
		if err != nil {
			return PayrollRun{}, err
		}
		frozen = append(frozen, frozenLine)
	}
	return PayrollRun{
		ID: id, SalonID: salonID, Kind: p.Kind,
		From: p.FromDate(), To: p.ToDate(), Status: PayrollRunOpen,
		CreatedBy: createdBy, CreatedAt: now, Lines: frozen,
	}, nil
}

// frozenPayrollLine valida y normaliza una línea congelada: montos no
// negativos, ajuste con signo, tope de dinero y total = comisión + fijo +
// ajuste, que nunca queda negativo.
func frozenPayrollLine(l PayrollLine) (PayrollLine, error) {
	amounts := []struct {
		name  string
		value int
	}{
		{"las ventas", l.SalesTotalCents},
		{"la comisión", l.CommissionCents},
		{"el fijo", l.FixedCents},
		{"el total", l.TotalCents},
	}
	for _, a := range amounts {
		if a.value < 0 {
			return PayrollLine{}, NewMessage(ErrValidation, fmt.Sprintf("%s no pueden ser negativos", a.name))
		}
		if a.value > MaxPayCents {
			return PayrollLine{}, NewMessage(ErrValidation,
				fmt.Sprintf("%s no pueden superar los %d centavos", a.name, MaxPayCents))
		}
	}
	if l.SalesCount < 0 {
		return PayrollLine{}, NewMessage(ErrValidation, "La cantidad de ventas no puede ser negativa")
	}
	if l.AdjustmentCents > MaxPayCents || l.AdjustmentCents < -MaxPayCents {
		return PayrollLine{}, NewMessage(ErrValidation,
			fmt.Sprintf("El ajuste no puede superar los %d centavos", MaxPayCents))
	}
	if _, _, err := NewPayrollAdjustment(l.AdjustmentCents, l.AdjustmentNote); err != nil {
		return PayrollLine{}, err
	}
	want := l.CommissionCents + l.FixedCents + l.AdjustmentCents
	if want < 0 {
		return PayrollLine{}, NewMessage(ErrValidation, MsgPayrollNegativeTotal)
	}
	if want != l.TotalCents {
		return PayrollLine{}, NewMessage(ErrValidation, "El total de la línea no coincide con comisión + fijo + ajuste")
	}
	return l, nil
}

// NewPayrollAdjustment valida el ajuste de una línea y devuelve la nota
// recortada solo de espacios sobrantes en los extremos (el texto se guarda tal
// cual lo escribió la persona, sin saltos de línea). Reglas del contrato:
// entero con signo dentro de ±MaxPayCents, nota obligatoria si no es 0, de
// hasta 200 caracteres y sin caracteres de control.
func NewPayrollAdjustment(adjustment int, note string) (int, string, error) {
	if adjustment > MaxPayCents || adjustment < -MaxPayCents {
		return 0, "", NewMessage(ErrValidation,
			fmt.Sprintf("El ajuste no puede superar los %d centavos", MaxPayCents))
	}
	if utf8.RuneCountInString(note) > MaxPayrollNote {
		return 0, "", NewMessage(ErrValidation,
			fmt.Sprintf("La nota del ajuste no puede superar los %d caracteres", MaxPayrollNote))
	}
	for _, r := range note {
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return 0, "", NewMessage(ErrValidation,
				"La nota del ajuste no puede tener saltos de línea ni caracteres de control")
		}
	}
	if adjustment != 0 && strings.TrimSpace(note) == "" {
		return 0, "", NewMessage(ErrValidation, MsgPayrollNoteRequired)
	}
	return adjustment, note, nil
}

// WithAdjustment devuelve la línea con el ajuste aplicado y su total
// recalculado. La original no se toca. Un total negativo es 400 con el mensaje
// del contrato: la línea nunca queda por debajo de cero.
func (l PayrollLine) WithAdjustment(adjustment int, note string) (PayrollLine, error) {
	adj, note, err := NewPayrollAdjustment(adjustment, note)
	if err != nil {
		return PayrollLine{}, err
	}
	total := l.CommissionCents + l.FixedCents + adj
	if total < 0 {
		return PayrollLine{}, NewMessage(ErrValidation, MsgPayrollNegativeTotal)
	}
	out := l
	out.AdjustmentCents = adj
	out.AdjustmentNote = note
	out.TotalCents = total
	return out, nil
}
