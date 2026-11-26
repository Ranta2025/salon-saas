package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Firma HMAC de los cuerpos que vienen del banco (F11, D6): la cabecera
// `X-Simbank-Signature` es `t=<unix>,v1=<hex HMAC-SHA256 de "<t>.<cuerpo>">`.
//
// Reglas (las mismas del contrato):
//   - el mensaje firmado es el instante en segundos, un punto y el cuerpo EXACTO
//     que llegó (los bytes, sin reserializar: re-serializar un JSON cambia el
//     orden de las claves y la firma dejaría de valer);
//   - la comparación es en TIEMPO CONSTANTE (hmac.Equal) y el rechazo es
//     uniforme: firma mala, cabecera ausente, formato malo, `t` fuera de
//     +-300 s y cuerpo alterado dan el MISMO texto y el mismo status. Un atacante
//     no aprende si acertó el formato, la ventana o la clave;
//   - las claves desconocidas de la cabecera se ignoran (un banco puede agregar
//     campos); `t` y `v1` tienen que estar exactamente una vez.
const (
	// PaymentSignatureTolerance es la ventana aceptada alrededor del `t` de la
	// cabecera: +-300 segundos (inclusive).
	PaymentSignatureTolerance = 300 * time.Second
	// MsgPaymentSignatureInvalid es el ÚNICO texto de una firma que no sirve
	// (mala firma, sin cabecera, formato malo, t viejo, t futuro, cuerpo
	// alterado): no se distingue el motivo.
	MsgPaymentSignatureInvalid = "Firma invalida"
)

// paymentSignatureHeaderLen es el largo de un `v1` en hex (SHA-256: 32 bytes).
const paymentSignatureHeaderLen = 64

// paymentSignature es el HMAC-SHA256 de "<t>.<cuerpo>" con ese secreto, en hex.
func paymentSignature(secret string, unix int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(unix, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignPayment arma la cabecera de un cuerpo en el instante ts con ese secreto.
// Es la forma EXACTA que valida VerifyPaymentSignature (y la que produce el banco
// simulado con el mismo secreto).
func SignPayment(secret string, ts time.Time, body []byte) string {
	unix := ts.Unix()
	return "t=" + strconv.FormatInt(unix, 10) + ",v1=" + paymentSignature(secret, unix, body)
}

// errPaymentSignature es el rechazo uniforme de la firma (400 «Firma invalida»).
func errPaymentSignature() error {
	return NewMessage(ErrValidation, MsgPaymentSignatureInvalid)
}

// VerifyPaymentSignature valida la cabecera contra el cuerpo en el instante now.
// Cualquier motivo de rechazo devuelve el MISMO error (400 «Firma invalida»).
func VerifyPaymentSignature(secret, header string, body []byte, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		// Sin secreto no hay nada que verificar: no se acepta a ciegas.
		return errPaymentSignature()
	}
	var (
		rawT   string
		rawV1  string
		seenT  bool
		seenV1 bool
	)
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return errPaymentSignature() // formato malo
		}
		switch key {
		case "t":
			if seenT {
				return errPaymentSignature() // ambiguous: no se adivina cuál vale
			}
			seenT, rawT = true, value
		case "v1":
			if seenV1 {
				return errPaymentSignature()
			}
			seenV1, rawV1 = true, value
		default:
			// Clave desconocida del banco: se ignora.
		}
	}
	if !seenT || !seenV1 {
		return errPaymentSignature()
	}
	unix, ok := paymentSignatureUnix(rawT)
	if !ok {
		return errPaymentSignature()
	}
	// Ventana de +-300 s inclusive: a los 300 s exactos todavía vale.
	skew := now.Unix() - unix
	if skew > int64(PaymentSignatureTolerance/time.Second) ||
		-skew > int64(PaymentSignatureTolerance/time.Second) {
		return errPaymentSignature()
	}
	if len(rawV1) != paymentSignatureHeaderLen {
		return errPaymentSignature()
	}
	got, err := hex.DecodeString(rawV1)
	if err != nil || len(got) != sha256.Size {
		return errPaymentSignature()
	}
	want, err := hex.DecodeString(paymentSignature(secret, unix, body))
	if err != nil {
		return errPaymentSignature()
	}
	if !hmac.Equal(got, want) {
		return errPaymentSignature()
	}
	return nil
}

// paymentSignatureUnix parsea el `t`: solo dígitos ASCII, sin signo, sin
// espacios y con un largo acotado (un `t` enorme no puede desbordar).
func paymentSignatureUnix(raw string) (int64, bool) {
	if raw == "" || len(raw) > 19 {
		return 0, false
	}
	var n int64
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
