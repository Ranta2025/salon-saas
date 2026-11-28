// Integración con Postgres real del perfil público del salón (F16, D1). Se
// saltea sin SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: el
// NULL de «sin dato», el slug que no se escribe, los CHECK de la migración 032 y
// el aislamiento entre salones.
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

func f16Fixtures(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return itdb.F16Fixtures(t)
}

func TestSalonProfiles_roundTrip(t *testing.T) {
	pool := f16Fixtures(t)
	repo := SalonProfiles{Pool: pool}
	ctx := context.Background()

	// Un salón recién creado: todo «sin dato».
	p, err := repo.Profile("s-it-f16")
	if err != nil || p.Name != "Salón F16" || p.Slug != "s-it-f16" || p.Description != "" || p.Email != "" {
		t.Fatalf("perfil inicial: %+v %v", p, err)
	}

	full := domain.SalonProfile{SalonID: "s-it-f16", Slug: "NO-SE-ESCRIBE", Name: "Navajas de Oro", Description: "Barbería clásica",
		Address: "Calle 1 #23", Phone: "+52 55", Email: "hola@navajas.com", Instagram: "navajas.oro"}
	if err := repo.SaveProfile(full); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	got, err := repo.Profile("s-it-f16")
	if err != nil {
		t.Fatal(err)
	}
	full.Slug = "s-it-f16" // el slug NO se escribe
	if got != full {
		t.Fatalf("perfil: %+v", got)
	}

	// Vacío se guarda como NULL (no como ''): el CHECK de la migración lo exige.
	full.Description, full.Address, full.Phone, full.Email, full.Instagram = "", "", "", "", ""
	if err := repo.SaveProfile(full); err != nil {
		t.Fatalf("guardar vacío: %v", err)
	}
	var nulls int
	if err := pool.QueryRow(ctx, `SELECT (description IS NULL)::int + (address IS NULL)::int + (phone IS NULL)::int + (email IS NULL)::int + (instagram IS NULL)::int FROM salons WHERE id='s-it-f16'`).Scan(&nulls); err != nil || nulls != 5 {
		t.Fatalf("columnas NULL: %d %v", nulls, err)
	}

	// Aislamiento: el otro salón no cambió.
	other, err := repo.Profile("s-it-f162")
	if err != nil || other.Name != "Salón F16 Dos" {
		t.Fatalf("otro salón: %+v %v", other, err)
	}

	// Un salón inexistente es ErrNotFound (no un upsert).
	if err := repo.SaveProfile(domain.SalonProfile{SalonID: "no-existe", Name: "X"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
	if _, err := repo.Profile("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("lectura inexistente: %v", err)
	}
}

// La migración 032 es la última barrera: si otro camino de escritura salta el
// dominio, la DB rechaza (y el adaptador lo traduce a 400, no a 500).
func TestSalonProfiles_checksAreTheLastBarrier(t *testing.T) {
	pool := f16Fixtures(t)
	repo := SalonProfiles{Pool: pool}
	base := domain.SalonProfile{SalonID: "s-it-f16", Name: "Salón F16"}
	for name, mut := range map[string]func(*domain.SalonProfile){
		"descripción larga": func(p *domain.SalonProfile) { p.Description = strings.Repeat("a", 501) },
		"dirección larga":   func(p *domain.SalonProfile) { p.Address = strings.Repeat("a", 201) },
		"teléfono largo":    func(p *domain.SalonProfile) { p.Phone = strings.Repeat("1", 41) },
		"email largo":       func(p *domain.SalonProfile) { p.Email = strings.Repeat("a", 255) },
		"instagram largo":   func(p *domain.SalonProfile) { p.Instagram = strings.Repeat("a", 61) },
		"instagram con @":   func(p *domain.SalonProfile) { p.Instagram = "@navajas" },
		"salto de línea":    func(p *domain.SalonProfile) { p.Address = "a\nb" },
		"retorno de carro":  func(p *domain.SalonProfile) { p.Description = "a\rb" },
	} {
		p := base
		mut(&p)
		if err := repo.SaveProfile(p); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%s: debía ser ErrValidation (400), vino %v", name, err)
		}
	}
	got, _ := repo.Profile("s-it-f16")
	if got.Description != "" || got.Address != "" {
		t.Fatalf("un rechazo no deja nada escrito: %+v", got)
	}
}
