package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sigNow es el instante fijo de los tests de firma.
var sigNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

const sigSecret = "s3creto-de-desarrollo"

// signExpected calcula la firma con crypto/hmac DIRECTO: si el archivo de
// producción cambia el formato ("<t>.<cuerpo>", hex), este test lo delata.
func signExpected(t *testing.T, secret string, ts time.Time, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + strconv.FormatInt(ts.Unix(), 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestPaymentSignature_formato_y_ida_y_vuelta(t *testing.T) {
	body := []byte(`{"event_id":"e1","charge_id":"c1"}`)
	got := SignPayment(sigSecret, sigNow, body)
	want := signExpected(t, sigSecret, sigNow, body)
	if got != want {
		t.Fatalf("SignPayment = %q, quiero %q", got, want)
	}
	if SignPayment(sigSecret, sigNow, body) != got {
		t.Fatal("SignPayment no es determinista")
	}
	if err := VerifyPaymentSignature(sigSecret, got, body, sigNow); err != nil {
		t.Fatalf("firma válida rechazada: %v", err)
	}
	// El borde de la tolerancia es INCLUSIVE: a los 300 s exactos todavía vale.
	for _, at := range []time.Time{sigNow.Add(PaymentSignatureTolerance), sigNow.Add(-PaymentSignatureTolerance)} {
		if err := VerifyPaymentSignature(sigSecret, got, body, at); err != nil {
			t.Fatalf("firma dentro de la tolerancia rechazada (%s): %v", at, err)
		}
	}
}

func TestPaymentSignature_rechazos_uniformes(t *testing.T) {
	body := []byte(`{"event_id":"e1"}`)
	good := SignPayment(sigSecret, sigNow, body)
	if good == "" {
		t.Fatal("SignPayment no firmó el cuerpo")
	}
	// Firma alterada en el ÚLTIMO carácter y en el PRIMERO del hex: las dos
	// tienen que dar el mismo error (la comparación es en tiempo constante, no
	// se corta en el primer byte distinto).
	flipHex := func(at int) string {
		repl := "0"
		if good[at] == '0' {
			repl = "1"
		}
		return good[:at] + repl + good[at+1:]
	}
	ts := strconv.FormatInt(sigNow.Unix(), 10)
	hexStart := len("t=") + len(ts) + len(",v1=")
	flipLast := flipHex(len(good) - 1)
	flipFirst := flipHex(hexStart)

	cases := []struct {
		name   string
		secret string
		header string
		body   []byte
		now    time.Time
	}{
		{"firma alterada al final", sigSecret, flipLast, body, sigNow},
		{"firma alterada al principio", sigSecret, flipFirst, body, sigNow},
		{"otro secreto", "otro", good, body, sigNow},
		{"cuerpo alterado", sigSecret, good, []byte(`{"event_id":"e2"}`), sigNow},
		{"vieja", sigSecret, good, body, sigNow.Add(PaymentSignatureTolerance + time.Second)},
		{"futura", sigSecret, good, body, sigNow.Add(-PaymentSignatureTolerance - time.Second)},
		{"sin cabecera", sigSecret, "", body, sigNow},
		{"sin t", sigSecret, "v1=" + strings.SplitN(good, "v1=", 2)[1], body, sigNow},
		{"sin v1", sigSecret, "t=" + ts, body, sigNow},
		{"t no numérico", sigSecret, "t=abc,v1=" + strings.SplitN(good, "v1=", 2)[1], body, sigNow},
		{"t con signo", sigSecret, "t=+" + ts + ",v1=" + strings.SplitN(good, "v1=", 2)[1], body, sigNow},
		{"v1 corto", sigSecret, "t=" + ts + ",v1=deadbeef", body, sigNow},
		{"v1 no hex", sigSecret, "t=" + ts + ",v1=" + strings.Repeat("z", 64), body, sigNow},
		{"sin separador", sigSecret, good + "basura", body, sigNow},
		{"t repetido", sigSecret, good + ",t=" + ts, body, sigNow},
		{"secreto vacío", "", good, body, sigNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyPaymentSignature(tc.secret, tc.header, tc.body, tc.now)
			if err == nil {
				t.Fatal("firma inválida aceptada")
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("el error tiene que ser de validación (400), es %v", err)
			}
			if err.Error() != MsgPaymentSignatureInvalid {
				t.Fatalf("texto = %q, quiero %q", err.Error(), MsgPaymentSignatureInvalid)
			}
		})
	}
}

// La cabecera puede traer campos extra del banco (k=v desconocidos): se
// ignoran, no invalidan la firma.
func TestPaymentSignature_campos_extra_ignorados(t *testing.T) {
	body := []byte(`{"ok":true}`)
	good := SignPayment(sigSecret, sigNow, body)
	if err := VerifyPaymentSignature(sigSecret, good+",kid=v2", body, sigNow); err != nil {
		t.Fatalf("campo extra rechazado: %v", err)
	}
}
