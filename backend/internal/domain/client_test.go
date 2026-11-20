package domain

import (
	"errors"
	"testing"
)

func TestNewClient_ok(t *testing.T) {
	c, err := NewClient("s1", "  Lía Fernández  ", "  11 5555-1234  ", "  prefiere turnos de mañana  ")
	if err != nil {
		t.Fatalf("cliente válido: %v", err)
	}
	if c.ID == "" {
		t.Fatal("el cliente debe nacer con id")
	}
	if c.SalonID != "s1" {
		t.Fatalf("salon_id: %q", c.SalonID)
	}
	if c.Name != "Lía Fernández" {
		t.Fatalf("el nombre se recorta, dio %q", c.Name)
	}
	if c.Phone != "11 5555-1234" {
		t.Fatalf("el teléfono se recorta, dio %q", c.Phone)
	}
	if c.Notes != "prefiere turnos de mañana" {
		t.Fatalf("las notas se recortan, dio %q", c.Notes)
	}
	if c.CreatedAt.IsZero() {
		t.Fatal("created_at debe venir seteado")
	}
}

func TestNewClient_rechaza_invalidos(t *testing.T) {
	cases := []struct{ name, salonID, clientName string }{
		{"sin salón", "", "Lía"},
		{"nombre vacío", "s1", "   "},
	}
	for _, c := range cases {
		if _, err := NewClient(c.salonID, c.clientName, "", ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
	// Teléfono y notas son opcionales.
	if _, err := NewClient("s1", "Sin datos", "", ""); err != nil {
		t.Fatalf("teléfono y notas vacíos deben ser válidos: %v", err)
	}
}

func TestNewClientPhoto_ok(t *testing.T) {
	p, err := NewClientPhoto("s1", "c1", "image/webp", 1280, 960, 180000)
	if err != nil {
		t.Fatalf("foto válida: %v", err)
	}
	if p.ID == "" || p.SalonID != "s1" || p.ClientID != "c1" {
		t.Fatalf("campos mal copiados: %+v", p)
	}
	p, err = p.StoredAt("s1/c1/abc.webp")
	if err != nil {
		t.Fatalf("ruta válida: %v", err)
	}
	if p.Path != "s1/c1/abc.webp" || p.ContentType != "image/webp" {
		t.Fatalf("path/content_type: %+v", p)
	}
	if p.Width != 1280 || p.Height != 960 || p.Bytes != 180000 {
		t.Fatalf("dimensiones/bytes mal copiados: %+v", p)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("created_at debe venir seteado")
	}
}

// Reglas duras de adjuntos: solo imágenes conocidas y hasta 8 MB.
func TestNewClientPhoto_rechaza_invalidos(t *testing.T) {
	cases := []struct {
		name              string
		salonID, clientID string
		contentType       string
		width, height     int
		bytes             int
	}{
		{"sin salón", "", "c1", "image/webp", 10, 10, 100},
		{"sin cliente", "s1", "", "image/webp", 10, 10, 100},
		{"tipo no imagen", "s1", "c1", "application/pdf", 10, 10, 100},
		{"gif no permitido", "s1", "c1", "image/gif", 10, 10, 100},
		{"tipo vacío", "s1", "c1", "", 10, 10, 100},
		{"ancho cero", "s1", "c1", "image/webp", 0, 10, 100},
		{"alto negativo", "s1", "c1", "image/webp", 10, -1, 100},
		{"bytes cero", "s1", "c1", "image/webp", 10, 10, 0},
		{"más de 8 MB", "s1", "c1", "image/webp", 10, 10, MaxPhotoBytes + 1},
	}
	for _, c := range cases {
		if _, err := NewClientPhoto(c.salonID, c.clientID, c.contentType, c.width, c.height, c.bytes); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: esperaba ErrValidation, dio %v", c.name, err)
		}
	}
	// La ruta la aporta el storage después: vacía o con escape no se acepta.
	ok, _ := NewClientPhoto("s1", "c1", "image/webp", 10, 10, 100)
	for _, bad := range []string{"", "   ", "../fuera.webp", "/etc/passwd"} {
		if _, err := ok.StoredAt(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("ruta %q debe dar ErrValidation, dio %v", bad, err)
		}
	}
	// Fronteras: exactamente 8 MB es válido; los tres tipos permitidos también.
	if _, err := NewClientPhoto("s1", "c1", "image/webp", 1, 1, MaxPhotoBytes); err != nil {
		t.Fatalf("8 MB exactos debe ser válido: %v", err)
	}
	for _, ct := range []string{"image/jpeg", "image/png", "image/webp"} {
		if !PhotoTypeAllowed(ct) {
			t.Fatalf("%q debería estar permitido", ct)
		}
	}
	for _, ct := range []string{"image/gif", "image/svg+xml", "text/plain", ""} {
		if PhotoTypeAllowed(ct) {
			t.Fatalf("%q no debería estar permitido", ct)
		}
	}
}
