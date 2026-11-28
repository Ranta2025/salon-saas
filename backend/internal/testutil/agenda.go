package testutil

import (
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// LinkAppURL es la URL pública que usan los enlaces de la reserva y la lista de
// espera en los tests.
const LinkAppURL = "http://localhost:8081"

// Recepcionista siembra y devuelve la membresía de recepción del salón s1.
func Recepcionista(m *FakeMemberships) domain.Membership {
	_ = m.Save(domain.Membership{UserID: "recep", SalonID: "s1", Role: domain.RoleReception})
	mm, _ := m.Find("recep", "s1")
	return mm
}

// Catalogo deja el catálogo mínimo del salón (Corte y Brushing) para los casos
// de uso que validan ítems contra él.
func Catalogo(t *testing.T, services *FakeServices, salonID string) {
	t.Helper()
	for _, s := range []struct {
		name  string
		price int
	}{{"Corte", 150000}, {"Brushing", 80000}} {
		svc, err := domain.NewService(salonID, s.name, 45, s.price, 20)
		if err != nil {
			t.Fatalf("catálogo: %v", err)
		}
		if err := services.Save(svc); err != nil {
			t.Fatalf("catálogo save: %v", err)
		}
	}
}

// SvcIDByName devuelve el id del servicio con ese nombre en el catálogo.
func SvcIDByName(t *testing.T, services *FakeServices, salonID, name string) string {
	t.Helper()
	list, err := services.List(salonID, true)
	if err != nil {
		t.Fatalf("listar catálogo: %v", err)
	}
	for _, s := range list {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("no hay servicio %q en %s", name, salonID)
	return ""
}

// MxLocation es la zona del salón de prueba (America/Mexico_City).
func MxLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	return loc
}

// LabelOf es la hora local del salón "HH:MM" de un instante.
func LabelOf(t *testing.T, at time.Time) string {
	t.Helper()
	return at.In(MxLocation(t)).Format("15:04")
}
