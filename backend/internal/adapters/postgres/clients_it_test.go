// Clientes y fotos contra Postgres real (SALON_IT=1). Lo que importa acá es la
// garantía dura: nada de un salón se puede leer ni escribir desde otro.
// Corre con: SALON_IT=1 go test ./internal/adapters/postgres/ -run TestClients -v
package pgstore

import (
	"context"
	"errors"
	"testing"

	"salon-saas/internal/domain"
)

func TestClients_ficha_y_fotos_scopeadas(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	seedTenant(t, "s-cli", "u-cli")
	seedTenant(t, "s-cli2", "u-cli2")
	_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE salon_id IN ('s-cli','s-cli2')`)

	repo := Clients{Pool: pool}
	c, err := domain.NewClient("s-cli", "Lía Fernández", "11 5555-1234", "prefiere turnos de mañana")
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	if err := repo.Save(c); err != nil {
		t.Fatalf("save cliente: %v", err)
	}
	got, err := repo.FindByID("s-cli", c.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Name != c.Name || got.Phone != c.Phone || got.Notes != c.Notes {
		t.Fatalf("round trip incompleto: %+v vs %+v", got, c)
	}
	// Otro salón: no existe.
	if _, err := repo.FindByID("s-cli2", c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant debe dar NotFound, dio %v", err)
	}
	if list, err := repo.List("s-cli2"); err != nil || len(list) != 0 {
		t.Fatalf("el otro salón no debe ver fichas: %d %v", len(list), err)
	}
	if list, err := repo.List("s-cli"); err != nil || len(list) != 1 {
		t.Fatalf("list del salón: %d %v", len(list), err)
	}

	// Fotos: la ruta la decide el storage, acá se persiste.
	p, err := domain.NewClientPhoto("s-cli", c.ID, domain.PhotoTypeWebP, 1280, 960, 180000)
	if err != nil {
		t.Fatalf("dominio foto: %v", err)
	}
	p, err = p.StoredAt("s-cli/" + c.ID + "/" + p.ID + ".webp")
	if err != nil {
		t.Fatalf("ruta: %v", err)
	}
	if err := repo.SavePhoto(p); err != nil {
		t.Fatalf("save foto: %v", err)
	}
	if list, err := repo.ListPhotos("s-cli", c.ID); err != nil || len(list) != 1 {
		t.Fatalf("listar fotos: %d %v", len(list), err)
	}
	if got, err := repo.FindPhoto("s-cli", c.ID, p.ID); err != nil || got.Path != p.Path || got.Width != 1280 {
		t.Fatalf("find foto: %+v %v", got, err)
	}
	if got, err := repo.PhotoByID(p.ID); err != nil || got.ClientID != c.ID {
		t.Fatalf("photo by id: %+v %v", got, err)
	}
	// Scope: con el salón equivocado no existe.
	if _, err := repo.FindPhoto("s-cli2", c.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant debe dar NotFound, dio %v", err)
	}
	if err := repo.DeletePhoto("s-cli2", c.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar cross-tenant debe dar NotFound, dio %v", err)
	}
	if err := repo.DeletePhoto("s-cli", c.ID, p.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if err := repo.DeletePhoto("s-cli", c.ID, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar dos veces debe dar NotFound, dio %v", err)
	}
	if _, err := repo.PhotoByID(p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la foto borrada no debe existir: %v", err)
	}
}

// La FK compuesta (client_id, salon_id) impide colgar una foto de un cliente de
// otro salón aunque el código se equivoque: la DB es la última barrera.
func TestClients_foto_no_cruza_salones(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	seedTenant(t, "s-fk", "u-fk")
	seedTenant(t, "s-fk2", "u-fk2")
	_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE salon_id IN ('s-fk','s-fk2')`)

	repo := Clients{Pool: pool}
	c, _ := domain.NewClient("s-fk", "Lía", "", "")
	if err := repo.Save(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Foto declarada en s-fk2 apuntando a un cliente de s-fk.
	p, err := domain.NewClientPhoto("s-fk2", c.ID, domain.PhotoTypeWebP, 10, 10, 100)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	p, _ = p.StoredAt("s-fk2/" + c.ID + "/x.webp")
	if err := repo.SavePhoto(p); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("la FK compuesta debe rechazar el cruce (ErrValidation), dio %v", err)
	}
}
