// Integración con Postgres real de la galería pública del salón (F16, D3). Se
// saltea sin SALON_IT=1. Lo que se prueba acá SOLO se prueba contra la DB: el
// tope de 12 bajo concurrencia (el lock por salón), el scope por salón y por
// slug, la separación con client_photos y el CASCADE.
package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil/itdb"
)

func newPhoto(t *testing.T, salon string) domain.SalonPhoto {
	t.Helper()
	return itdb.NewPhoto(t, salon)
}

func resetGallery(t *testing.T) SalonGallery {
	t.Helper()
	pool := f16Fixtures(t)
	return SalonGallery{Pool: pool}
}

func TestSalonGallery_crud(t *testing.T) {
	g := resetGallery(t)
	first := newPhoto(t, "s-it-f16")
	first.CreatedAt = time.Now().UTC().Add(-time.Minute)
	second := newPhoto(t, "s-it-f16")
	foreign := newPhoto(t, "s-it-f162")
	for _, p := range []domain.SalonPhoto{second, first, foreign} {
		if err := g.Add(p, domain.MaxSalonPhotos); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	list, err := g.List("s-it-f16")
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("orden de subida: %+v %v", list, err)
	}
	if list[0].StorageKey != first.StorageKey || list[0].ContentType != domain.PhotoTypeWebP || list[0].Caption != "" {
		t.Fatalf("campos: %+v", list[0])
	}
	if n, _ := g.Count("s-it-f16"); n != 2 {
		t.Fatalf("count: %d", n)
	}

	// Scope por salón: la foto de otro salón no existe para este.
	if _, err := g.Find("s-it-f16", foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("find ajena: %v", err)
	}
	if err := g.SetCaption("s-it-f16", foreign.ID, "hack"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("caption ajena: %v", err)
	}
	if err := g.Delete("s-it-f16", foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete ajena: %v", err)
	}
	if got, err := g.Find("s-it-f162", foreign.ID); err != nil || got.Caption != "" {
		t.Fatalf("la ajena sigue intacta: %+v %v", got, err)
	}

	// Leyenda: se reemplaza y "" vuelve a NULL.
	if err := g.SetCaption("s-it-f16", first.ID, "Mi local"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Find("s-it-f16", first.ID); got.Caption != "Mi local" {
		t.Fatalf("leyenda: %+v", got)
	}
	if err := g.SetCaption("s-it-f16", first.ID, ""); err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := g.Pool.QueryRow(context.Background(), `SELECT caption IS NULL FROM salon_photos WHERE id=$1`, first.ID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("leyenda vacía debe ser NULL: %v %v", isNull, err)
	}

	// Delete.
	if err := g.Delete("s-it-f16", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := g.Delete("s-it-f16", first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segundo delete: %v", err)
	}
}

// La ruta pública resuelve por SLUG + id: un id de otro salón, un id inexistente
// y la foto de una CLIENTA son el mismo ErrNotFound.
func TestSalonGallery_FindPublic(t *testing.T) {
	g := resetGallery(t)
	mine, foreign := newPhoto(t, "s-it-f16"), newPhoto(t, "s-it-f162")
	_ = g.Add(mine, 12)
	_ = g.Add(foreign, 12)

	got, err := g.FindPublic("s-it-f16", mine.ID)
	if err != nil || got.ID != mine.ID || got.SalonID != "s-it-f16" || got.StorageKey != mine.StorageKey {
		t.Fatalf("pública: %+v %v", got, err)
	}
	for _, c := range []struct{ slug, id string }{
		{"s-it-f16", foreign.ID}, {"s-it-f162", mine.ID}, {"s-it-f16", "no-existe"}, {"no-existe", mine.ID},
	} {
		if _, err := g.FindPublic(c.slug, c.id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%v: %v", c, err)
		}
	}

	// Foto de clienta: vive en client_photos, que esta consulta ni mira.
	ctx := context.Background()
	if _, err := g.Pool.Exec(ctx, `INSERT INTO clients(id,salon_id,name) VALUES('c-it-f16','s-it-f16','Clienta')`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Pool.Exec(ctx, `INSERT INTO client_photos(id,salon_id,client_id,path,width,height,bytes,content_type) VALUES('cp-it-f16','s-it-f16','c-it-f16','s-it-f16/c/cp.webp',10,10,10,'image/webp')`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FindPublic("s-it-f16", "cp-it-f16"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la foto de una clienta NO se sirve: %v", err)
	}
	if list, _ := g.List("s-it-f16"); len(list) != 1 {
		t.Fatalf("la galería no mezcla fotos de clientas: %+v", list)
	}
}

// El tope: la foto 13 es 409 con el texto exacto y no escribe nada.
func TestSalonGallery_capOf12(t *testing.T) {
	g := resetGallery(t)
	for i := 0; i < 12; i++ {
		if err := g.Add(newPhoto(t, "s-it-f16"), 12); err != nil {
			t.Fatalf("foto %d: %v", i+1, err)
		}
	}
	err := g.Add(newPhoto(t, "s-it-f16"), 12)
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != "La galería ya tiene 12 fotos" {
		t.Fatalf("13ª: %v", err)
	}
	if n, _ := g.Count("s-it-f16"); n != 12 {
		t.Fatalf("count: %d", n)
	}
	// El tope es por salón.
	if err := g.Add(newPhoto(t, "s-it-f162"), 12); err != nil {
		t.Fatalf("otro salón: %v", err)
	}
}

// Dos subidas simultáneas con 11 fotos dejan 12, nunca 13: el conteo corre
// DENTRO del lock por salón (sin él, las dos veían 11 y las dos entraban).
func TestSalonGallery_capHoldsUnderConcurrency(t *testing.T) {
	g := resetGallery(t)
	for i := 0; i < 11; i++ {
		if err := g.Add(newPhoto(t, "s-it-f16"), 12); err != nil {
			t.Fatal(err)
		}
	}
	const racers = 12
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = g.Add(newPhoto(t, "s-it-f16"), 12)
		}(i)
	}
	close(start)
	wg.Wait()
	wins, full := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrAlreadyExists) && err.Error() == domain.MsgGalleryFull:
			full++
		default:
			t.Fatalf("resultado inesperado: %v", err)
		}
	}
	if n, _ := g.Count("s-it-f16"); wins != 1 || full != racers-1 || n != 12 {
		t.Fatalf("wins=%d full=%d count=%d", wins, full, n)
	}
}

// Borrar el salón borra sus filas (CASCADE) y no toca las de otro.
func TestSalonGallery_cascadeOnSalonDelete(t *testing.T) {
	g := resetGallery(t)
	_ = g.Add(newPhoto(t, "s-it-f16"), 12)
	keep := newPhoto(t, "s-it-f162")
	_ = g.Add(keep, 12)
	if _, err := g.Pool.Exec(context.Background(), `DELETE FROM salons WHERE id='s-it-f16'`); err != nil {
		t.Fatal(err)
	}
	if n, _ := g.Count("s-it-f16"); n != 0 {
		t.Fatalf("cascade: %d", n)
	}
	if _, err := g.Find("s-it-f162", keep.ID); err != nil {
		t.Fatalf("la del otro salón sigue: %v", err)
	}
}
