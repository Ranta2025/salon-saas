package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// cardNow es el instante fijo de los tests de tarjeta (marzo 2026).
var cardNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func TestDecideCard_tarjetas_de_prueba(t *testing.T) {
	cases := []struct {
		number  string
		outcome string
		last4   string
		brand   string
		message string
	}{
		{TestCardNumberApproved, CardApproved, "4242", CardBrandVisa, ""},
		{TestCardNumberDeclined, CardDeclined, "0002", CardBrandVisa, MsgCardDeclined},
		{TestCardNumberInsufficient, CardInsufficient, "9995", CardBrandVisa, MsgCardInsufficient},
		// Cualquier otra con Luhn válido se rechaza (no hay tercera excepción).
		{"4111111111111111", CardDeclined, "1111", CardBrandVisa, MsgCardDeclined},
	}
	for _, tc := range cases {
		t.Run(tc.number, func(t *testing.T) {
			d, err := DecideCard(NewBankCard(tc.number, "12/30", "123", "Ana Pérez"), cardNow)
			if err != nil {
				t.Fatalf("tarjeta válida rechazada: %v", err)
			}
			if d.Outcome != tc.outcome || d.Last4 != tc.last4 || d.Brand != tc.brand || d.Message != tc.message {
				t.Fatalf("decisión = %+v, quiero outcome=%s last4=%s brand=%s message=%q",
					d, tc.outcome, tc.last4, tc.brand, tc.message)
			}
		})
	}
}

func TestDecideCard_marcas(t *testing.T) {
	cases := []struct {
		number string
		brand  string
		cvc    string
	}{
		{"4242424242424242", CardBrandVisa, "123"},
		{"5555555555554444", CardBrandMastercard, "123"},
		{"2223003122003222", CardBrandMastercard, "123"},
		{"378282246310005", CardBrandAmex, "1234"},
		{"6011111111111117", CardBrandOther, "123"},
		{"3530111333300000", CardBrandOther, "123"}, // JCB: no es ninguna de las tres
	}
	for _, tc := range cases {
		d, err := DecideCard(NewBankCard(tc.number, "12/30", tc.cvc, ""), cardNow)
		if err != nil {
			t.Fatalf("%s: tarjeta válida rechazada: %v", tc.brand, err)
		}
		if d.Brand != tc.brand {
			t.Fatalf("%s: marca = %q, quiero %q", tc.number, d.Brand, tc.brand)
		}
	}
}

func TestDecideCard_datos_invalidos(t *testing.T) {
	cases := []struct {
		name             string
		number, exp, cvc string
	}{
		{"Luhn malo", "4242424242424241", "12/30", "123"},
		{"con letras", "42424242424242ab", "12/30", "123"},
		{"espacios", "4242 4242 4242 4242", "12/30", "123"},
		{"corto", "424242", "12/30", "123"},
		{"vacío", "", "12/30", "123"},
		{"vencida", "4242424242424242", "02/26", "123"},
		{"mes 0", "4242424242424242", "00/30", "123"},
		{"mes 13", "4242424242424242", "13/30", "123"},
		{"sin barra", "4242424242424242", "1230", "123"},
		{"año corto", "4242424242424242", "12/3", "123"},
		{"cvc de 2", "4242424242424242", "12/30", "12"},
		{"cvc de 5", "4242424242424242", "12/30", "12345"},
		{"cvc con letras", "4242424242424242", "12/30", "12a"},
		{"amex con cvc de 3", "378282246310005", "12/30", "123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecideCard(NewBankCard(tc.number, tc.exp, tc.cvc, "Ana"), cardNow)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("quería error de validación, es %v", err)
			}
			if err.Error() != MsgCardInvalid {
				t.Fatalf("texto = %q, quiero %q", err.Error(), MsgCardInvalid)
			}
		})
	}
}

// El vencimiento vale hasta el ÚLTIMO día de ese mes, como cualquier tarjeta.
func TestDecideCard_vencimiento_borde(t *testing.T) {
	lastDay := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	if _, err := DecideCard(NewBankCard(TestCardNumberApproved, "03/26", "123", ""), lastDay); err != nil {
		t.Fatalf("vence este mes y todavía debe valer: %v", err)
	}
	firstOfNext := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	if _, err := DecideCard(NewBankCard(TestCardNumberApproved, "03/26", "123", ""), firstOfNext); err == nil {
		t.Fatal("la tarjeta ya venció y la aceptó")
	}
	// Año de 4 dígitos también sirve.
	if _, err := DecideCard(NewBankCard(TestCardNumberApproved, "12/2030", "123", ""), cardNow); err != nil {
		t.Fatalf("MM/YYYY rechazado: %v", err)
	}
}

// El PAN no aparece en NINGUNA representación de la tarjeta, de la decisión ni
// de un error: lo único que puede salir es last4.
func TestDecideCard_numero_nunca_sale(t *testing.T) {
	numbers := []string{TestCardNumberApproved, TestCardNumberDeclined, TestCardNumberInsufficient, "4111111111111111"}
	for _, number := range numbers {
		card := NewBankCard(number, "12/30", "123", "Ana Pérez")
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			printed := fmt.Sprintf(format, card)
			if strings.Contains(printed, number) {
				t.Fatalf("%s de la tarjeta filtra el número: %q", format, printed)
			}
			if strings.Contains(printed, number[6:]) {
				t.Fatalf("%s de la tarjeta filtra el final del número: %q", format, printed)
			}
		}
		d, err := DecideCard(card, cardNow)
		if err != nil {
			t.Fatalf("%s: %v", number, err)
		}
		for _, format := range []string{"%v", "%+v", "%#v"} {
			printed := fmt.Sprintf(format, d)
			if strings.Contains(printed, number) {
				t.Fatalf("la decisión filtra el número: %q", printed)
			}
		}
		if !strings.HasSuffix(number, d.Last4) {
			t.Fatalf("last4 = %q no es el final de %s", d.Last4, number)
		}
	}
	// Un error de datos inválidos tampoco lleva el número.
	bad := "4242424242424241"
	if _, err := DecideCard(NewBankCard(bad, "12/30", "123", ""), cardNow); err == nil {
		t.Fatal("Luhn malo aceptado")
	} else if strings.Contains(err.Error(), bad) || strings.Contains(err.Error(), "4242") {
		t.Fatalf("el error filtra la tarjeta: %q", err.Error())
	}
}
