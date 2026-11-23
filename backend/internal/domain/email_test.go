package domain

import (
	"errors"
	"testing"
)

// Hallazgo B del verificador independiente de F2g1: ValidateEmail solo pedía un
// "@" y 5 caracteres, así que se guardaban direcciones como "a b@x.com",
// "a@b@c.com" o "x@y.com,evil@z". La regla la comparte el registro, por eso hay
// un test de conservación con las direcciones normales.
func TestValidateEmail_rechaza_direcciones_mal_formadas(t *testing.T) {
	bad := []string{
		"a b@x.com", "a@b@c.com", "x@y.com,evil@z.com", "x@y.com;c@d.com",
		"x@y.com\r\nBcc:z@z.com", "x@y.com\nBcc:z@z.com", "a\tb@x.com", "@x.com", "ab@", "a@.com",
		"a@com.", "a@b..com", "<a@b.com>", "a\"b@x.com", "a@b c.com", "(a)@b.com", "a@[b.com]",
		"no-tiene-arroba", "a@b\x00.com",
	}
	for _, e := range bad {
		err := ValidateEmail(e)
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("%q tendría que ser inválido, dio %v", e, err)
		}
	}
}

func TestValidateEmail_conserva_las_direcciones_normales(t *testing.T) {
	good := []string{
		"a@b.co", "user+tag@mail.example.com", "x.y_z@sub.domain.org", "ana@correo.mx",
		"u@salon.test", "o'brien@mail.com", "ñandú@correo.mx", "a-b@mail-server.com", "a@localhost.lan",
	}
	for _, e := range good {
		if err := ValidateEmail(e); err != nil {
			t.Fatalf("%q es una dirección válida: %v", e, err)
		}
	}
}
