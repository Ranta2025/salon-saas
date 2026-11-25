package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// Vectores de prueba de RFC 6238, Apéndice B (SHA1, secreto ASCII de 20 bytes).
var rfc6238Secret = []byte("12345678901234567890")

func TestTOTPCodeRFC6238Vectors(t *testing.T) {
	vectors := []struct {
		unix  int64
		eight string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
	}
	for _, v := range vectors {
		step := TOTPStepAt(time.Unix(v.unix, 0))
		if got := TOTPCode(rfc6238Secret, step, 8); got != v.eight {
			t.Errorf("T=%d 8 dígitos: got %q want %q", v.unix, got, v.eight)
		}
		want6 := v.eight[2:]
		if got := TOTPCode(rfc6238Secret, step, TOTPDigits); got != want6 {
			t.Errorf("T=%d 6 dígitos: got %q want %q", v.unix, got, want6)
		}
	}
	if s := TOTPStepAt(time.Unix(59, 0)); s != 1 {
		t.Errorf("T=59 debe ser el paso 1, got %d", s)
	}
	if s := TOTPStepAt(time.Unix(60, 0)); s != 2 {
		t.Errorf("T=60 debe ser el paso 2, got %d", s)
	}
}

func TestMatchTOTPWindowAndReplay(t *testing.T) {
	now := time.Unix(1111111109, 0) // paso 37037036
	step := TOTPStepAt(now)
	cur := TOTPCode(rfc6238Secret, step, TOTPDigits)
	prev := TOTPCode(rfc6238Secret, step-1, TOTPDigits)
	next := TOTPCode(rfc6238Secret, step+1, TOTPDigits)
	far := TOTPCode(rfc6238Secret, step+2, TOTPDigits)
	older := TOTPCode(rfc6238Secret, step-2, TOTPDigits)

	for name, c := range map[string]struct {
		code string
		step int64
	}{"actual": {cur, step}, "anterior": {prev, step - 1}, "siguiente": {next, step + 1}} {
		got, ok := MatchTOTP(rfc6238Secret, c.code, now, 0)
		if !ok || got != c.step {
			t.Errorf("%s: got (%d,%v) want (%d,true)", name, got, ok, c.step)
		}
	}
	if _, ok := MatchTOTP(rfc6238Secret, far, now, 0); ok {
		t.Error("el paso +2 está fuera de la ventana ±1")
	}
	if _, ok := MatchTOTP(rfc6238Secret, older, now, 0); ok {
		t.Error("el paso -2 está fuera de la ventana ±1")
	}
	// Anti-replay: un código de un paso <= last_step se rechaza.
	if _, ok := MatchTOTP(rfc6238Secret, cur, now, step); ok {
		t.Error("el código del paso == last_step es un replay")
	}
	if _, ok := MatchTOTP(rfc6238Secret, prev, now, step); ok {
		t.Error("un paso anterior a last_step es un replay")
	}
	if got, ok := MatchTOTP(rfc6238Secret, next, now, step); !ok || got != step+1 {
		t.Errorf("el paso siguiente a last_step sí vale: got (%d,%v)", got, ok)
	}
	// Formato inválido.
	for _, bad := range []string{"", "12345", "1234567", "abcdef", cur + " ", "00000a"} {
		if _, ok := MatchTOTP(rfc6238Secret, bad, now, 0); ok {
			t.Errorf("%q no debería validar", bad)
		}
	}
	if _, ok := MatchTOTP(nil, cur, now, 0); ok {
		t.Error("sin secreto no valida")
	}
}

func TestNewTOTPSecretAndEncoding(t *testing.T) {
	a, err := NewTOTPSecret()
	if err != nil || len(a) != TOTPSecretBytes {
		t.Fatalf("secreto: %v len=%d", err, len(a))
	}
	b, _ := NewTOTPSecret()
	if bytes.Equal(a, b) {
		t.Error("dos secretos iguales")
	}
	enc := EncodeTOTPSecret(a)
	if len(enc) != 32 || strings.ContainsAny(enc, "=abcdefghijklmnopqrstuvwxyz0189 ") {
		t.Errorf("base32 sin relleno y en mayúsculas esperado, got %q", enc)
	}
	// Vector conocido de RFC 4648: "12345678901234567890" -> GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ
	if got := EncodeTOTPSecret(rfc6238Secret); got != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Errorf("base32: %q", got)
	}
}

func TestOTPAuthURL(t *testing.T) {
	got := OTPAuthURL("ana@mail.com", "ABC234")
	want := "otpauth://totp/Salon:ana@mail.com?secret=ABC234&issuer=Salon"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if u := OTPAuthURL("a b+c@x.com", "S"); strings.Contains(u, " ") || strings.Contains(u, "+c") {
		t.Errorf("el email debe ir escapado: %q", u)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil || len(codes) != 8 {
		t.Fatalf("codes: %v %d", err, len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("forma XXXXX-XXXXX esperada: %q", c)
		}
		n, ok := NormalizeRecoveryCode(c)
		if !ok || len(n) != 10 {
			t.Errorf("no normaliza: %q", c)
		}
		if seen[n] {
			t.Errorf("repetido %q", c)
		}
		seen[n] = true
	}
	// Normalización: minúsculas, espacios y con/sin guion.
	n1, ok1 := NormalizeRecoveryCode("abcde-23456")
	n2, ok2 := NormalizeRecoveryCode(" ABCDE 23456 ")
	n3, ok3 := NormalizeRecoveryCode("ABCDE23456")
	if !ok1 || !ok2 || !ok3 || n1 != "ABCDE23456" || n2 != n1 || n3 != n1 {
		t.Errorf("normalización: %q %q %q", n1, n2, n3)
	}
	for _, bad := range []string{"", "ABCDE", "ABCDE-2345", "ABCDE-234567", "ABCDE-2345!", "ABCDE-23451" /* 1 no es base32 */, "123456"} {
		if _, ok := NormalizeRecoveryCode(bad); ok {
			t.Errorf("%q no es un código de recuperación", bad)
		}
	}
	h := HashRecoveryCode("ABCDE23456")
	if len(h) != 64 || h != HashRecoveryCode("ABCDE23456") || h == HashRecoveryCode("ABCDE23457") {
		t.Errorf("hash SHA-256 hex determinista esperado: %q", h)
	}
}

func TestTOTPCodeFormat(t *testing.T) {
	if !IsTOTPCodeFormat("123456") || !IsTOTPCodeFormat("000000") {
		t.Error("6 dígitos es formato TOTP")
	}
	for _, bad := range []string{"", "12345", "1234567", "12345a", "ABCDE-23456", "١٢٣٤٥٦"} {
		if IsTOTPCodeFormat(bad) {
			t.Errorf("%q no es formato TOTP", bad)
		}
	}
	if NormalizeTOTPCode(" 123 456\t") != "123456" {
		t.Error("los espacios se descartan")
	}
}

func TestSecretBoxRoundTripAndTamper(t *testing.T) {
	box, err := NewTOTPSecretBox("jwt-secret-for-tests")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("12345678901234567890")
	blob, err := box.Seal(plain, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("el secreto no puede quedar en claro")
	}
	if len(blob) != 12+len(plain)+16 {
		t.Errorf("nonce(12)+texto+tag(16) esperado, len=%d", len(blob))
	}
	got, err := box.Open(blob, "u1")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("ida y vuelta: %v %q", err, got)
	}
	// Nonce aleatorio: dos cifrados del mismo texto difieren.
	blob2, _ := box.Seal(plain, "u1")
	if bytes.Equal(blob, blob2) {
		t.Error("el nonce debe ser aleatorio")
	}
	// Manipulado: cualquier bit cambiado falla cerrado (error, no pánico).
	for i := range blob {
		bad := append([]byte(nil), blob...)
		bad[i] ^= 0x01
		if _, err := box.Open(bad, "u1"); err == nil {
			t.Fatalf("byte %d manipulado descifró", i)
		}
	}
	for _, short := range [][]byte{nil, {}, blob[:5], blob[:12], blob[:20]} {
		if _, err := box.Open(short, "u1"); err == nil {
			t.Errorf("texto cifrado corto (%d) descifró", len(short))
		}
	}
	// Ligado al usuario: el secreto de una fila no sirve en la de otro.
	if _, err := box.Open(blob, "u2"); err == nil {
		t.Error("el texto cifrado de u1 abrió para u2")
	}
	// Otra clave (otro JWT_SECRET) no descifra: rotar JWT_SECRET invalida los 2FA.
	other, _ := NewTOTPSecretBox("otro-secreto")
	if _, err := other.Open(blob, "u1"); err == nil {
		t.Error("otra clave descifró")
	}
}

func TestSecretBoxKeyDerivationAndZeroValue(t *testing.T) {
	if _, err := NewTOTPSecretBox(""); err == nil {
		t.Error("sin JWT_SECRET no hay caja")
	}
	var zero TOTPSecretBox
	if _, err := zero.Seal([]byte("x"), "u"); err == nil {
		t.Error("la caja vacía no cifra")
	}
	if _, err := zero.Open(make([]byte, 40), "u"); err == nil {
		t.Error("la caja vacía no descifra")
	}
	// Clave = HMAC-SHA256(JWT_SECRET, "salon-saas/totp/v1"): otra etiqueta => otra clave.
	a, _ := NewTOTPSecretBox("s")
	blob, _ := a.Seal([]byte("hola"), "u")
	if got, err := a.Open(blob, "u"); err != nil || string(got) != "hola" {
		t.Fatalf("round trip: %v", err)
	}
	if !errors.Is(ErrTooManyAttempts, ErrTooManyAttempts) {
		t.Fatal("sentinel")
	}
}
