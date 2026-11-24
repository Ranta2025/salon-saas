package domain

import "testing"

// Verificador independiente (LOW): un email con CR/LF o con un espacio adentro
// se guardaba (el mailer lo rechazaba al enviar, gastando los 3 intentos y
// dejando una fila basura). El alta lo rechaza con el mismo 400 de un email
// inválido; el nombre y el teléfono con caracteres de control, con el de NUL.
func TestNewWaitlistContact_rechaza_controles_y_espacios(t *testing.T) {
	for _, email := range []string{
		"p@x.test\nBcc: otro@x.test", "p@x.test\r\nBcc: otro@x.test", "a@x.test b@x.test", "a@x.test\tb@x.test", "a@x\u00a0.test",
	} {
		_, err := NewWaitlistContact("Lía", email, "")
		if err == nil || err.Error() != "Email inválido" {
			t.Fatalf("email %q: %v", email, err)
		}
	}
	for _, c := range []struct{ name, phone string }{
		{"Lía\nBcc: x", ""}, {"Li\ra", ""}, {"Li\ta", ""}, {"Lía", "55\n1234"}, {"Lía", "55\u00001234"},
	} {
		_, err := NewWaitlistContact(c.name, "a@x.test", c.phone)
		if err == nil || err.Error() != MsgWaitlistInvalidChars {
			t.Fatalf("%q/%q: %v", c.name, c.phone, err)
		}
	}
	// Lo válido sigue entrando (espacios de los bordes se recortan como hoy).
	if got, err := NewWaitlistContact("  Lía  ", " A@X.test ", " 55 1234 "); err != nil || got.Email != "a@x.test" || got.Name != "Lía" {
		t.Fatalf("contacto válido: %+v %v", got, err)
	}
}
