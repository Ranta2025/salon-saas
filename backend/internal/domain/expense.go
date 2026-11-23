package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Gastos del salón (F3b, D3 y D4 del contrato F3b). Este archivo es PURO:
// stdlib y nada más. El gasto guarda un DÍA LOCAL (`spent_on`) y un monto entero
// en centavos; no depende de la zona horaria para agruparse, pero sí para
// decidir qué es «hoy» y qué es futuro.

// ExpenseCategory es la categoría fija del gasto (D3). No hay categorías
// personalizadas: es dinero operativo, no un plan de cuentas.
type ExpenseCategory string

const (
	ExpenseRent      ExpenseCategory = "rent"
	ExpenseSupplies  ExpenseCategory = "supplies"
	ExpenseUtilities ExpenseCategory = "utilities"
	ExpenseOther     ExpenseCategory = "other"
)

// Valid dice si la categoría está en el catálogo.
func (c ExpenseCategory) Valid() bool {
	switch c {
	case ExpenseRent, ExpenseSupplies, ExpenseUtilities, ExpenseOther:
		return true
	default:
		return false
	}
}

// Label es el nombre visible de la categoría.
func (c ExpenseCategory) Label() string {
	switch c {
	case ExpenseRent:
		return "Alquiler"
	case ExpenseSupplies:
		return "Insumos"
	case ExpenseUtilities:
		return "Servicios"
	case ExpenseOther:
		return "Otros"
	default:
		return "Categoría desconocida"
	}
}

// Mensajes EXACTOS del contrato F3b (los ve la persona tal cual). Viven acá para
// que el caso de uso, el adaptador y los tests digan la misma frase.
const (
	// MsgExpenseMissingCreate: faltan las tres claves obligatorias del POST.
	MsgExpenseMissingCreate = "Faltan datos del gasto: categoría, monto y fecha"
	// MsgExpenseMissingUpdate: el PUT REEMPLAZA: las cuatro claves son
	// obligatorias (la descripción puede ser "").
	MsgExpenseMissingUpdate = "Faltan datos del gasto: categoría, monto, fecha y descripción"
	// MsgExpenseNotFound: un gasto de otro salón no existe para este tenant.
	MsgExpenseNotFound = "Gasto no encontrado"
)

// Límites de D3. El monto es un entero de centavos y el techo es el mismo que el
// del resto del dinero (cabe en el INTEGER de la migración 026). La descripción
// se cuenta en CARACTERES, no en bytes.
const (
	MaxExpenseCents       = 2_000_000_000
	MaxExpenseDescription = 200

	// minExpenseYear es la fecha más vieja aceptada: 2000-01-01.
	minExpenseYear = 2000
)

// ExpenseFields son los cuatro campos que escribe la persona. Se validan y
// normalizan juntos porque el POST usa tres y el PUT reemplaza los cuatro.
type ExpenseFields struct {
	Category    string
	AmountCents int
	SpentOn     string
	Description string
}

// Validate normaliza (trim) y valida los campos del gasto. `now` es el reloj del
// servidor y `loc` la zona del SALÓN: la fecha futura se mide contra HOY local,
// no contra el día UTC.
func (f ExpenseFields) Validate(now time.Time, loc *time.Location) (ExpenseFields, error) {
	if loc == nil {
		loc = time.UTC
	}
	f.Category = strings.TrimSpace(f.Category)
	f.SpentOn = strings.TrimSpace(f.SpentOn)
	f.Description = strings.TrimSpace(f.Description)

	if !ExpenseCategory(f.Category).Valid() {
		return ExpenseFields{}, NewMessage(ErrValidation,
			"Categoría inválida: usá alquiler, insumos, servicios u otros")
	}
	if f.AmountCents <= 0 {
		return ExpenseFields{}, NewMessage(ErrValidation, "El monto debe ser mayor a cero")
	}
	if f.AmountCents > MaxExpenseCents {
		return ExpenseFields{}, NewMessage(ErrValidation,
			"El monto no puede superar los 2000000000 centavos")
	}
	day, err := time.Parse(dateLayout, f.SpentOn)
	if err != nil {
		return ExpenseFields{}, NewMessage(ErrValidation, "Fecha inválida: usá AAAA-MM-DD")
	}
	if day.Year() < minExpenseYear {
		return ExpenseFields{}, NewMessage(ErrValidation, "La fecha del gasto es demasiado antigua")
	}
	if day.After(todayLocalDate(now, loc)) {
		return ExpenseFields{}, NewMessage(ErrValidation, "La fecha del gasto no puede ser futura")
	}
	if utf8.RuneCountInString(f.Description) > MaxExpenseDescription {
		return ExpenseFields{}, NewMessage(ErrValidation, "La descripción no puede superar los 200 caracteres")
	}
	f.SpentOn = day.Format(dateLayout)
	return f, nil
}

// todayLocalDate es HOY en la zona del salón, como fecha sin hora: se compara
// contra una DATE, no contra un instante.
func todayLocalDate(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Expense es un gasto del salón (D3). `SpentOn` es un día local YA normalizado
// (YYYY-MM-DD); `CreatedBy` es quién lo cargó y `CreatedAt` cuándo.
type Expense struct {
	ID          string
	SalonID     string
	Category    ExpenseCategory
	AmountCents int
	SpentOn     string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
}

// NewExpense arma el gasto con un id nuevo y la foto del creador. Los campos
// tienen que venir ya validados por ExpenseFields.Validate.
func NewExpense(salonID string, f ExpenseFields, createdBy string, now time.Time) Expense {
	return Expense{
		ID:          NewID(),
		SalonID:     salonID,
		Category:    ExpenseCategory(f.Category),
		AmountCents: f.AmountCents,
		SpentOn:     f.SpentOn,
		Description: f.Description,
		CreatedBy:   createdBy,
		CreatedAt:   now.UTC(),
	}
}

// WithFields devuelve el gasto con los cuatro campos reemplazados (PUT). No toca
// identidad, salón, creador ni fecha de alta: es una corrección del gasto, no un
// gasto nuevo.
func (e Expense) WithFields(f ExpenseFields) Expense {
	out := e
	out.Category = ExpenseCategory(f.Category)
	out.AmountCents = f.AmountCents
	out.SpentOn = f.SpentOn
	out.Description = f.Description
	return out
}

// MaxExpenseRangeDays es el rango máximo del listado (D4): un año inclusive.
const MaxExpenseRangeDays = 366

// ExpenseRange valida el `from`/`to` INCLUSIVO del listado: obligatorios, con
// formato de fecha, `to` >= `from` y hasta 366 días. No hay cota de antigüedad
// acá: mirar el historial viejo es válido.
func ExpenseRange(from, to string) (string, string, error) {
	invalid := NewMessage(ErrValidation,
		"Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días")
	f, err := time.Parse(dateLayout, strings.TrimSpace(from))
	if err != nil {
		return "", "", invalid
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(to))
	if err != nil {
		return "", "", invalid
	}
	if t.Before(f) {
		return "", "", invalid
	}
	if int(t.Sub(f).Hours()/24)+1 > MaxExpenseRangeDays {
		return "", "", invalid
	}
	return f.Format(dateLayout), t.Format(dateLayout), nil
}
