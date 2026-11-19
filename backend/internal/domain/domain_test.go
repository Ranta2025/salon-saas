package domain

import "testing"

// Estos tests prueban el DOMINIO puro: reglas que deben cumplirse sin importar
// si usamos Postgres, Gin o lo que sea. Si esto falla, el bug es de negocio.

func TestNewUser_ok(t *testing.T) {
	u, err := NewUser("Ana", "Ana@Mail.com", "hash-x")
	if err != nil {
		t.Fatalf("inesperado: %v", err)
	}
	if u.ID == "" {
		t.Fatal("esperaba ID generado")
	}
	if u.Email != "ana@mail.com" {
		t.Fatalf("email debe normalizarse a minúsculas, dio %q", u.Email)
	}
}

func TestNewUser_rechaza(t *testing.T) {
	for _, tc := range []struct {
		name, email, hash string
	}{
		{"", "a@b.co", "h"},
		{"Ana", "sin-arroba", "h"},
		{"Ana", "a@b.co", ""},
	} {
		if _, err := NewUser(tc.name, tc.email, tc.hash); err == nil {
			t.Fatalf("debió rechazar %+v", tc)
		}
	}
}

func TestNewSalon_slug(t *testing.T) {
	if _, err := NewSalon("Navajas de Oro", "navajas-oro"); err != nil {
		t.Fatalf("slug válido rechazado: %v", err)
	}
	for _, bad := range []string{"", "Mayúsculas", "con espacios", "-empieza", "termina-"} {
		if _, err := NewSalon("X", bad); err == nil {
			t.Fatalf("debió rechazar slug %q", bad)
		}
	}
}

func TestMembership_roles(t *testing.T) {
	m, err := NewMembership("u1", "s1", RoleBarber)
	if err != nil {
		t.Fatalf("inesperado: %v", err)
	}
	if m.CanManage() {
		t.Fatal("barbero no debe poder configurar el salón")
	}
	o, _ := NewMembership("u1", "s1", RoleOwner)
	if !o.CanManage() {
		t.Fatal("owner debe poder configurar")
	}
	if _, err := NewMembership("u1", "s1", "jefe"); err == nil {
		t.Fatal("debió rechazar rol desconocido")
	}
}
