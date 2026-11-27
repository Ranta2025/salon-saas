package httpapi_test

import (
	"net/http"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
)

// Verificador independiente (MEDIUM): un id o slug con NUL o UTF-8 inválido en la
// ruta pública de imágenes (y en PUT/DELETE de la galería) llegaba a Postgres, que
// responde 22021 y salía un 500. El contrato pide el 404 uniforme: la referencia
// inválida se rechaza ANTES de consultar la base.
func TestGallery_invalid_references_are_a_uniform_404_without_touching_the_store(t *testing.T) {
	w := newF16World(t)
	const slug = "f16-uno"
	photo := w.addPhoto(t, w.Salon, w.Owner)
	badIDs := []string{"%00", "%ff%fe", "a%00b", strings.Repeat("a", 200)}

	for _, id := range badIDs {
		before := w.Gallery.lookups
		rec := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+slug+"/gallery/"+id, "", "")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Foto no encontrada") {
			t.Fatalf("público id %q: %d %s", id, rec.Code, rec.Body.String())
		}
		if w.Gallery.lookups != before {
			t.Fatalf("público id %q: no debe consultar la base", id)
		}
		for _, m := range []struct{ method, body string }{{"PUT", `{"caption":"x"}`}, {"DELETE", ""}} {
			before = w.Gallery.lookups
			rec := httpkit.Do(w.Env.Router, m.method, w.galleryPath()+"/"+id, m.body, w.Owner)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Foto no encontrada") {
				t.Fatalf("%s id %q: %d %s", m.method, id, rec.Code, rec.Body.String())
			}
			if w.Gallery.lookups != before {
				t.Fatalf("%s id %q: no debe consultar la base", m.method, id)
			}
		}
	}
	// Slug inválido: mismo 404 y sin consultar.
	for _, slug := range []string{"%00", "%ff", "sal%00uno"} {
		before := w.Gallery.lookups
		rec := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+slug+"/gallery/"+photo.ID, "", "")
		if rec.Code != http.StatusNotFound || w.Gallery.lookups != before {
			t.Fatalf("slug %q: %d %s (consultas %d->%d)", slug, rec.Code, rec.Body.String(), before, w.Gallery.lookups)
		}
	}
	// La foto real sigue sirviéndose.
	if rec := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+slug+"/gallery/"+photo.ID, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("la foto real: %d", rec.Code)
	}
}
