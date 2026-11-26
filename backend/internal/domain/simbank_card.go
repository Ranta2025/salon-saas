package domain

import (
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// El banco simulado (F11, D6): tarjetas de prueba, validación de los datos y
// decisión. El número de tarjeta NUNCA se guarda ni se loguea: solo salen
// `last4` y la marca.
//
// Reglas de seguridad:
//   - el PAN no tiene campo exportado (BankCard con campos no exportados), no
//     viaja en CardDecision y String/GoString/LogValue lo redactan: ni un log
//     accidental ni un %+v del struct lo filtran;
//   - Luhn, formato (13..19 dígitos), vencimiento y CVC se validan ANTES de
//     decidir: un dato inválido es 400 «Datos de tarjeta invalidos» y no crea
//     ningún intento;
//   - la decisión depende SOLO del número: las tres tarjetas del contrato y,
//     para cualquier otra con Luhn válido, rechazo.
const (
	// TestCardNumberApproved aprueba el pago.
	TestCardNumberApproved = "4242424242424242"
	// TestCardNumberDeclined rechaza el pago («Tarjeta rechazada»).
	TestCardNumberDeclined = "4000000000000002"
	// TestCardNumberInsufficient rechaza por fondos («Fondos insuficientes»).
	TestCardNumberInsufficient = "4000000000009995"
)

// Textos EXACTOS del contrato D6 (sin tildes tal cual están escritos).
const (
	MsgCardInvalid      = "Datos de tarjeta invalidos"
	MsgCardDeclined     = "Tarjeta rechazada"
	MsgCardInsufficient = "Fondos insuficientes"
)

// Marcas que se guardan en simbank_charges.brand (texto canónico corto).
const (
	CardBrandVisa       = "visa"
	CardBrandMastercard = "mastercard"
	CardBrandAmex       = "amex"
	CardBrandOther      = "otra"
)

// Resultados de la tarjeta: los mismos `kind` del callback (D5).
const (
	CardApproved     = "approved"
	CardDeclined     = "declined"
	CardInsufficient = "insufficient"
)

// bankCardMinDigits y bankCardMaxDigits acotan el número con formato de tarjeta.
const (
	bankCardMinDigits = 13
	bankCardMaxDigits = 19
)

// BankCard es una tarjeta tal como la recibe el banco simulado. Los campos son
// NO EXPORTADOS a propósito: el número (PAN) no puede copiarse a otra struct ni
// imprimirse sin querer. Lo único que sale de acá es CardDecision (last4 y
// marca); String/GoString/LogValue imprimen una forma redactada.
type BankCard struct {
	number string
	exp    string
	cvc    string
	name   string
}

// NewBankCard arma la tarjeta SIN validar. Validar y decidir es DecideCard.
func NewBankCard(number, exp, cvc, name string) BankCard {
	return BankCard{number: number, exp: exp, cvc: cvc, name: name}
}

// String es la ÚNICA forma de imprimir una tarjeta: nunca el número.
func (c BankCard) String() string { return "BankCard(****)" }

// GoString cubre %#v.
func (c BankCard) GoString() string { return c.String() }

// LogValue cubre log/slog: un log de la tarjeta no filtra el PAN.
func (c BankCard) LogValue() slog.Value { return slog.StringValue(c.String()) }

// CardDecision es lo que la decisión deja salir: el resultado, el last4, la
// marca y el texto que ve la persona. Nunca el número.
type CardDecision struct {
	Outcome string
	Last4   string
	Brand   string
	Message string
}

// DecideCard valida los datos y decide el resultado por el número (D6).
//
// Errores (todos 400 «Datos de tarjeta invalidos»): número con formato inválido o
// Luhn malo, vencimiento con formato inválido o ya pasado (la tarjeta vale hasta
// el último día del mes), CVC de 3 dígitos (4 en American Express).
//
// `now` es el instante del servidor: el vencimiento NO se confía al navegador.
func DecideCard(card BankCard, now time.Time) (CardDecision, error) {
	number := strings.TrimSpace(card.number)
	if !validCardNumber(number) {
		return CardDecision{}, errCardInvalid()
	}
	brand := cardBrand(number)
	month, year, ok := parseCardExpiry(strings.TrimSpace(card.exp), now)
	if !ok {
		return CardDecision{}, errCardInvalid()
	}
	// Vale hasta el último instante del mes del vencimiento.
	expiresAfter := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)
	if !now.UTC().Before(expiresAfter) {
		return CardDecision{}, errCardInvalid()
	}
	if !validCardCVC(strings.TrimSpace(card.cvc), brand) {
		return CardDecision{}, errCardInvalid()
	}
	last4 := number[len(number)-4:]
	outcome, message := CardDeclined, MsgCardDeclined
	switch number {
	case TestCardNumberApproved:
		outcome, message = CardApproved, ""
	case TestCardNumberDeclined:
		outcome, message = CardDeclined, MsgCardDeclined
	case TestCardNumberInsufficient:
		outcome, message = CardInsufficient, MsgCardInsufficient
	}
	return CardDecision{Outcome: outcome, Last4: last4, Brand: brand, Message: message}, nil
}

// errCardInvalid es el rechazo uniforme de los datos de la tarjeta.
func errCardInvalid() error { return NewMessage(ErrValidation, MsgCardInvalid) }

// validCardNumber exige 13..19 dígitos ASCII y Luhn correcto.
func validCardNumber(number string) bool {
	if len(number) < bankCardMinDigits || len(number) > bankCardMaxDigits {
		return false
	}
	sum := 0
	double := false
	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// cardBrand clasifica por el prefijo (BIN): solo lo que se GUARDA y se muestra.
func cardBrand(number string) string {
	switch {
	case strings.HasPrefix(number, "34"), strings.HasPrefix(number, "37"):
		return CardBrandAmex
	case strings.HasPrefix(number, "4"):
		return CardBrandVisa
	case strings.HasPrefix(number, "5"), hasNumericRange(number, 4, 2221, 2720):
		return CardBrandMastercard
	default:
		return CardBrandOther
	}
}

// hasNumericRange dice si los primeros `digits` dígitos caen en [lo, hi].
func hasNumericRange(number string, digits, lo, hi int) bool {
	if len(number) < digits {
		return false
	}
	n, err := strconv.Atoi(number[:digits])
	if err != nil {
		return false
	}
	return n >= lo && n <= hi
}

// parseCardExpiry acepta "MM/YY" y "MM/YYYY" y devuelve mes y año completo.
func parseCardExpiry(exp string, now time.Time) (month, year int, ok bool) {
	left, right, found := strings.Cut(exp, "/")
	if !found || len(left) != 2 {
		return 0, 0, false
	}
	if len(right) != 2 && len(right) != 4 {
		return 0, 0, false
	}
	for _, part := range []string{left, right} {
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return 0, 0, false
			}
		}
	}
	month, _ = strconv.Atoi(left)
	if month < 1 || month > 12 {
		return 0, 0, false
	}
	year, _ = strconv.Atoi(right)
	if len(right) == 2 {
		year += 2000
	}
	if year < 2000 || year > 2100 {
		return 0, 0, false
	}
	return month, year, true
}

// validCardCVC exige 3 dígitos (4 en American Express).
func validCardCVC(cvc, brand string) bool {
	want := 3
	if brand == CardBrandAmex {
		want = 4
	}
	if len(cvc) != want {
		return false
	}
	for i := 0; i < len(cvc); i++ {
		if cvc[i] < '0' || cvc[i] > '9' {
			return false
		}
	}
	return true
}
