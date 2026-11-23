package domain

import (
	"fmt"
	"strings"
	"time"
)

// Nómina y comisiones (F3.2 del contrato F3). Este archivo es PURO: stdlib y
// nada más. Deriva el PERÍODO que se liquida (D5) y calcula la COMISIÓN (D2),
// que son las dos reglas de dinero que no pueden vivir en un handler ni en SQL.

// PayPeriodKind es el tipo de período que el cliente pide liquidar (D5).
type PayPeriodKind string

const (
	// PayWeek es la semana lunes-domingo que contiene la fecha.
	PayWeek PayPeriodKind = "week"
	// PayFortnight es la quincena: 1-15 o 16-fin de mes.
	PayFortnight PayPeriodKind = "fortnight"
	// PayMonth es el mes calendario.
	PayMonth PayPeriodKind = "month"
)

// Valid dice si el tipo está en el catálogo.
func (k PayPeriodKind) Valid() bool {
	switch k {
	case PayWeek, PayFortnight, PayMonth:
		return true
	default:
		return false
	}
}

// Label es el nombre del tipo para mostrar ("semana", "quincena", "mes").
func (k PayPeriodKind) Label() string {
	switch k {
	case PayWeek:
		return "semana"
	case PayFortnight:
		return "quincena"
	case PayMonth:
		return "mes"
	default:
		return "período desconocido"
	}
}

// PayPeriod es el período ya derivado: From y To son la medianoche LOCAL del
// salón del primer día y del ÚLTIMO día INCLUSIVO. La ventana interna es
// [From, End()) y se arma con time.Date en la zona del salón, nunca sumando 24 h:
// un día de cambio de horario de verano dura 23 o 25 horas y el período tiene
// que incluirlo entero.
type PayPeriod struct {
	Kind PayPeriodKind
	From time.Time
	To   time.Time
}

// dateLayout es el formato del contrato para from/to y para el `date` del pedido.
const dateLayout = "2006-01-02"

// NewPayPeriod deriva el período de `kind` que contiene `date` (YYYY-MM-DD) en
// la zona del salón. Cualquier tipo desconocido o fecha que no sea una fecha
// real es 400 con el mensaje exacto del contrato: "Período inválido".
func NewPayPeriod(kind, date string, loc *time.Location) (PayPeriod, error) {
	if loc == nil {
		return PayPeriod{}, NewMessage(ErrValidation, "Período inválido")
	}
	k := PayPeriodKind(strings.TrimSpace(kind))
	if !k.Valid() {
		return PayPeriod{}, NewMessage(ErrValidation, "Período inválido")
	}
	day, err := time.ParseInLocation(dateLayout, strings.TrimSpace(date), loc)
	if err != nil {
		return PayPeriod{}, NewMessage(ErrValidation, "Período inválido")
	}
	return derivePayPeriod(k, day, loc), nil
}

// derivePayPeriod aplica la regla de D5 sobre un día ya resuelto en `loc`.
func derivePayPeriod(kind PayPeriodKind, day time.Time, loc *time.Location) PayPeriod {
	local := day.In(loc)
	y, m, d := local.Date()
	switch kind {
	case PayWeek:
		// Monday..Sunday: Weekday() empieza en domingo (0).
		offset := (int(local.Weekday()) + 6) % 7
		return PayPeriod{Kind: kind, From: localDate(y, m, d-offset, loc), To: localDate(y, m, d-offset+6, loc)}
	case PayFortnight:
		if d <= 15 {
			return PayPeriod{Kind: kind, From: localDate(y, m, 1, loc), To: localDate(y, m, 15, loc)}
		}
		// 16 hasta el último día del mes: time.Date normaliza el día 0 del mes
		// siguiente al último del actual (28, 29, 30 o 31).
		return PayPeriod{Kind: kind, From: localDate(y, m, 16, loc), To: localDate(y, m+1, 0, loc)}
	default:
		return PayPeriod{Kind: kind, From: localDate(y, m, 1, loc), To: localDate(y, m+1, 0, loc)}
	}
}

// localDate arma la medianoche local de un día; time.Date normaliza días fuera
// de rango y resuelve el offset correcto de cada fecha (horario de verano).
func localDate(year int, month time.Month, day int, loc *time.Location) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

// End es la medianoche local del día SIGUIENTE al último día del período: el
// extremo abierto de la ventana. Se calcula con time.Date en la zona del salón,
// no sumando 24 h.
func (p PayPeriod) End() time.Time {
	y, m, d := p.To.Date()
	return localDate(y, m, d+1, p.To.Location())
}

// Window devuelve la ventana [inicio, fin) de instantes que cubre el período
// entero: [From 00:00 local, End() 00:00 local).
func (p PayPeriod) Window() (time.Time, time.Time) {
	return p.From, p.End()
}

// FromDate y ToDate son las fechas locales INCLUSIVAS del contrato (YYYY-MM-DD).
func (p PayPeriod) FromDate() string { return p.From.Format(dateLayout) }
func (p PayPeriod) ToDate() string   { return p.To.Format(dateLayout) }

// Closed dice si el período ya terminó: `to` es anterior a HOY en la zona del
// salón. El período en curso (incluido hoy) no está cerrado (D5).
func (p PayPeriod) Closed(now time.Time) bool {
	y, m, d := now.In(p.To.Location()).Date()
	return p.To.Before(localDate(y, m, d, p.To.Location()))
}

// CommissionItem es una línea de venta ya resuelta para la comisión.
type CommissionItem struct {
	Qty            int
	UnitPriceCents int
	// Pct es el porcentaje que aplica: la FOTO del ítem o, si no tenía (venta
	// anterior a F3.1), la tarifa ACTUAL del servicio (ver PayrollPct).
	Pct int
}

// CommissionBase es Σ qty × precio × pct: el NUMERADOR de la comisión, en
// "centavos × punto porcentual". Se suma en int64 y sin dividir: una sola
// división al final es lo que evita que el redondeo se acumule ítem por ítem.
func CommissionBase(items []CommissionItem) int64 {
	var base int64
	for _, it := range items {
		base += int64(it.Qty) * int64(it.UnitPriceCents) * int64(it.Pct)
	}
	return base
}

// CommissionCents es floor(base / 100): LA única división de la fórmula (D2).
// Enteros: nunca float, nunca redondeo bancario.
func CommissionCents(base int64) int {
	if base <= 0 {
		return 0
	}
	return int(base / 100)
}

// CommissionOf es la comisión de un conjunto de líneas: la base y la división.
func CommissionOf(items []CommissionItem) int { return CommissionCents(CommissionBase(items)) }

// PayrollPct resuelve el porcentaje de una línea para la nómina: la FOTO del
// ítem manda (D1) y, si la línea es anterior a F3.1 (nil), se estima con la
// tarifa ACTUAL del servicio y queda marcada `estimated`. Un 0 guardado es un
// porcentaje real, no "sin foto".
func PayrollPct(snapshot *int, current int) (pct int, estimated bool) {
	if snapshot != nil {
		return *snapshot, false
	}
	return current, true
}

// MemberPay es el modelo de pago de un miembro (D3): sueldo fijo por período,
// comisión o mixto. SIN fila en la DB rige DefaultMemberPay (solo comisión).
type MemberPay struct {
	SalonID    string
	UserID     string
	FixedCents int
	// FixedPeriod es el tipo de período con el que se paga el fijo: se paga UNA
	// vez cuando el período liquidado es de ese tipo (D4).
	FixedPeriod PayPeriodKind
	// CommissionEnabled apagado deja la comisión en 0 para esta persona (fijo
	// puro); sus ventas siguen contando en cantidad y total.
	CommissionEnabled bool
}

// MemberPayView es el modelo de pago con la persona: la forma de la pantalla de
// Equipo y de la vista previa (nombre desde users, rol y baja desde memberships).
type MemberPayView struct {
	MemberPay
	Name   string
	Role   Role
	Active bool
}

// DefaultMemberPay es lo que rige sin fila: solo comisión (fijo 0, mes, comisión
// encendida), exactamente el comportamiento de antes de F3.2.
func DefaultMemberPay(salonID, userID string) MemberPay {
	return MemberPay{SalonID: salonID, UserID: userID, FixedCents: 0, FixedPeriod: PayMonth, CommissionEnabled: true}
}

// NewMemberPay valida y arma el modelo de pago. Los mensajes son los que ve la
// persona (400 en español): un monto negativo, un monto que no cabe en la
// columna o un período que no existe.
func NewMemberPay(salonID, userID string, fixedCents int, period string, commissionEnabled bool) (MemberPay, error) {
	if strings.TrimSpace(salonID) == "" || strings.TrimSpace(userID) == "" {
		return MemberPay{}, NewMessage(ErrValidation, "El salón y la persona son obligatorios")
	}
	if fixedCents < 0 {
		return MemberPay{}, NewMessage(ErrValidation, "El sueldo fijo no puede ser negativo")
	}
	if fixedCents > MaxPayCents {
		return MemberPay{}, NewMessage(ErrValidation,
			fmt.Sprintf("El sueldo fijo no puede superar los %d centavos", MaxPayCents))
	}
	k := PayPeriodKind(strings.TrimSpace(period))
	if !k.Valid() {
		return MemberPay{}, NewMessage(ErrValidation, "Período de pago desconocido: usá semana, quincena o mes")
	}
	return MemberPay{
		SalonID: strings.TrimSpace(salonID), UserID: strings.TrimSpace(userID),
		FixedCents: fixedCents, FixedPeriod: k, CommissionEnabled: commissionEnabled,
	}, nil
}

// PayrollSalesTotal es lo que UN profesional vendió en el período: cantidad de
// ventas, total en centavos y el NUMERADOR de su comisión (CommissionBase). El
// adaptador suma qty × precio × pct en enteros; este paquete hace la división.
// Estimated dice si alguna línea se estimó con la tarifa actual (sin foto).
type PayrollSalesTotal struct {
	UserID          string
	Name            string
	SalesCount      int
	SalesTotalCents int
	CommissionBase  int64
	Estimated       bool
}

// CommissionCents es la comisión de este agregado (la única división, D2).
func (t PayrollSalesTotal) CommissionCents() int { return CommissionCents(t.CommissionBase) }
