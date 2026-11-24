package domain

import (
	"strings"
	"testing"
)

func sp(v string) *string { return &v }

func baseProfile() SalonProfile {
	return SalonProfile{SalonID: "s1", Slug: "navajas", Name: "Navajas"}
}

func fullInput() SalonProfileInput {
	return SalonProfileInput{
		Name: "  Navajas de Oro ", Description: sp("Barbería clásica"), Address: sp("Calle 1 #23"),
		Phone: sp("+52 55 1234 5678"), Email: sp("  Hola@Navajas.COM "), Instagram: sp("navajas.oro"),
	}
}

// D1: el perfil válido se recorta, el email se normaliza y SalonID/Slug no se
// mueven.
func TestSalonProfile_Updated_valid(t *testing.T) {
	got, err := baseProfile().Updated(fullInput())
	if err != nil {
		t.Fatalf("válido: %v", err)
	}
	want := SalonProfile{SalonID: "s1", Slug: "navajas", Name: "Navajas de Oro", Description: "Barbería clásica",
		Address: "Calle 1 #23", Phone: "+52 55 1234 5678", Email: "hola@navajas.com", Instagram: "navajas.oro"}
	if got != want {
		t.Fatalf("perfil: %+v", got)
	}
}

// D1: null y "" (también solo espacios) son «sin dato».
func TestSalonProfile_Updated_nullAndEmptyAreNoData(t *testing.T) {
	in := SalonProfileInput{Name: "X", Description: nil, Address: sp(""), Phone: sp("   "), Email: nil, Instagram: sp("")}
	cur := SalonProfile{SalonID: "s1", Slug: "x", Name: "Y", Description: "viejo", Email: "a@b.com"}
	got, err := cur.Updated(in)
	if err != nil {
		t.Fatalf("válido: %v", err)
	}
	if got.Description != "" || got.Address != "" || got.Phone != "" || got.Email != "" || got.Instagram != "" {
		t.Fatalf("null/vacío debe borrar el dato (nada de «conservar el anterior»): %+v", got)
	}
}

func TestSalonProfile_Updated_nameRules(t *testing.T) {
	in := fullInput()
	in.Name = "   "
	_, err := baseProfile().Updated(in)
	if got := msgOf(t, err, ErrValidation); got != MsgSalonNameRequired {
		t.Fatalf("nombre vacío: %q", got)
	}
	in.Name = strings.Repeat("ñ", 121)
	_, err = baseProfile().Updated(in)
	if got := msgOf(t, err, ErrValidation); got != MsgSalonNameTooLong {
		t.Fatalf("nombre largo: %q", got)
	}
	in.Name = strings.Repeat("ñ", 120)
	if _, err := baseProfile().Updated(in); err != nil {
		t.Fatalf("120 runes es válido: %v", err)
	}
}

func TestSalonProfile_Updated_lengthLimits(t *testing.T) {
	cases := []struct {
		field string
		set   func(*SalonProfileInput, string)
		max   int
		msg   string
	}{
		{"description", func(i *SalonProfileInput, v string) { i.Description = &v }, 500, MsgSalonDescriptionLong},
		{"address", func(i *SalonProfileInput, v string) { i.Address = &v }, 200, MsgSalonAddressLong},
		{"phone", func(i *SalonProfileInput, v string) { i.Phone = &v }, 40, MsgSalonPhoneLong},
		{"instagram", func(i *SalonProfileInput, v string) { i.Instagram = &v }, 60, MsgSalonInstagramLong},
	}
	for _, c := range cases {
		in := fullInput()
		c.set(&in, strings.Repeat("a", c.max))
		if _, err := baseProfile().Updated(in); err != nil {
			t.Fatalf("%s: el tope exacto es válido: %v", c.field, err)
		}
		c.set(&in, strings.Repeat("a", c.max+1))
		_, err := baseProfile().Updated(in)
		if got := msgOf(t, err, ErrValidation); got != c.msg {
			t.Fatalf("%s: %q", c.field, got)
		}
	}
}

// D1: NUL y saltos de línea son 400 en CADA campo de texto, nunca un 500 de la DB.
func TestSalonProfile_Updated_rejectsNULAndLineBreaks(t *testing.T) {
	setters := map[string]func(*SalonProfileInput, string){
		"name":        func(i *SalonProfileInput, v string) { i.Name = v },
		"description": func(i *SalonProfileInput, v string) { i.Description = &v },
		"address":     func(i *SalonProfileInput, v string) { i.Address = &v },
		"phone":       func(i *SalonProfileInput, v string) { i.Phone = &v },
		"email":       func(i *SalonProfileInput, v string) { i.Email = &v },
		"instagram":   func(i *SalonProfileInput, v string) { i.Instagram = &v },
	}
	for field, set := range setters {
		for _, bad := range []string{"a\nb", "a\rb", "a\x00b", "a\u2028b", "ok\n"} {
			in := fullInput()
			set(&in, bad)
			_, err := baseProfile().Updated(in)
			if err == nil {
				t.Fatalf("%s %q: debía rechazarse", field, bad)
			}
			if got := msgOf(t, err, ErrValidation); got == "" {
				t.Fatalf("%s %q: sin mensaje", field, bad)
			}
		}
	}
	// El mensaje de un texto con salto es el de caracteres no válidos.
	in := fullInput()
	in.Description = sp("a\nb")
	_, err := baseProfile().Updated(in)
	if got := msgOf(t, err, ErrValidation); got != MsgSalonInvalidChars {
		t.Fatalf("mensaje: %q", got)
	}
}

func TestSalonProfile_Updated_emailFormat(t *testing.T) {
	for _, bad := range []string{"sin-arroba", "a@", "@b.com", "a b@x.com", "a@b.com,c@d.com", "a@@b.com", strings.Repeat("a", 250) + "@x.com"} {
		in := fullInput()
		in.Email = sp(bad)
		_, err := baseProfile().Updated(in)
		if got := msgOf(t, err, ErrValidation); got != MsgSalonEmailInvalid {
			t.Fatalf("%q: %q", bad, got)
		}
	}
}

func TestSalonProfile_Updated_instagramIsOnlyTheUser(t *testing.T) {
	for _, bad := range []string{"@navajas", "https://instagram.com/navajas", "na vajas", "navajas/oro"} {
		in := fullInput()
		in.Instagram = sp(bad)
		_, err := baseProfile().Updated(in)
		if got := msgOf(t, err, ErrValidation); got != MsgSalonInstagramBad {
			t.Fatalf("%q: %q", bad, got)
		}
	}
	in := fullInput()
	in.Instagram = sp("Navajas_Oro.2")
	got, err := baseProfile().Updated(in)
	if err != nil || got.Instagram != "Navajas_Oro.2" {
		t.Fatalf("usuario válido: %+v %v", got, err)
	}
}

// D3: la foto solo admite jpg/png/webp y la leyenda tiene tope y una sola línea.
func TestNewSalonPhoto_types(t *testing.T) {
	for _, ct := range []string{"image/webp", "image/jpeg", "image/png"} {
		p, err := NewSalonPhoto("s1", ct)
		if err != nil || p.ID == "" || p.SalonID != "s1" || p.ContentType != ct || p.CreatedAt.IsZero() {
			t.Fatalf("%s: %+v %v", ct, p, err)
		}
	}
	for _, ct := range []string{"image/svg+xml", "image/gif", "text/html", ""} {
		_, err := NewSalonPhoto("s1", ct)
		if got := msgOf(t, err, ErrValidation); got != MsgPhotoType {
			t.Fatalf("%q: %q", ct, got)
		}
	}
	if _, err := NewSalonPhoto("", "image/webp"); err == nil {
		t.Fatal("sin salón debe fallar")
	}
}

func TestSalonPhoto_StoredAt(t *testing.T) {
	p, _ := NewSalonPhoto("s1", "image/webp")
	got, err := p.StoredAt(" s1/gallery/x.webp ")
	if err != nil || got.StorageKey != "s1/gallery/x.webp" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "  ", "../x", "/abs", "a/../b"} {
		if _, err := p.StoredAt(bad); err == nil {
			t.Fatalf("%q debía rechazarse", bad)
		}
	}
}

func TestSalonPhoto_WithCaption(t *testing.T) {
	p, _ := NewSalonPhoto("s1", "image/webp")
	got, err := p.WithCaption(sp("  Mi local  "))
	if err != nil || got.Caption != "Mi local" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, clear := range []*string{nil, sp(""), sp("  ")} {
		got, err := got.WithCaption(clear)
		if err != nil || got.Caption != "" {
			t.Fatalf("sin leyenda: %+v %v", got, err)
		}
	}
	if _, err := p.WithCaption(sp(strings.Repeat("a", 120))); err != nil {
		t.Fatalf("120 es válido: %v", err)
	}
	_, err = p.WithCaption(sp(strings.Repeat("a", 121)))
	if got := msgOf(t, err, ErrValidation); got != MsgPhotoCaptionLong {
		t.Fatalf("largo: %q", got)
	}
	for _, bad := range []string{"a\nb", "a\x00b"} {
		_, err = p.WithCaption(sp(bad))
		if got := msgOf(t, err, ErrValidation); got != MsgSalonInvalidChars {
			t.Fatalf("%q: %q", bad, got)
		}
	}
}

// D2: descripción de servicio opcional, hasta 300, una línea.
func TestService_WithDescription(t *testing.T) {
	svc, _ := NewService("s1", "Corte", 30, 1000, 0)
	got, err := svc.WithDescription("  Corte clásico  ")
	if err != nil || got.Description != "Corte clásico" || got.Name != "Corte" || got.ID != svc.ID {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = got.WithDescription("")
	if err != nil || got.Description != "" {
		t.Fatalf("vacía: %+v %v", got, err)
	}
	if _, err := svc.WithDescription(strings.Repeat("a", 300)); err != nil {
		t.Fatalf("300 es válido: %v", err)
	}
	_, err = svc.WithDescription(strings.Repeat("a", 301))
	if got := msgOf(t, err, ErrValidation); got != MsgServiceDescription {
		t.Fatalf("largo: %q", got)
	}
	for _, bad := range []string{"a\nb", "a\x00b"} {
		_, err = svc.WithDescription(bad)
		if got := msgOf(t, err, ErrValidation); got != MsgSalonInvalidChars {
			t.Fatalf("%q: %q", bad, got)
		}
	}
	// PRESERVE: NewService/WithDetails siguen sin tocar la descripción.
	kept, err := got.WithDetails("Corte 2", 40, 2000, 10, true)
	if err != nil || kept.Description != got.Description {
		t.Fatalf("WithDetails conserva la descripción: %+v %v", kept, err)
	}
}
