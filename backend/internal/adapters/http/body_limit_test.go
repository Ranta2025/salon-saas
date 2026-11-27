package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bigJSON es un JSON de 2 MB: el doble del tope y perfectamente parseable.
func bigJSON() string {
	return `{"email":"` + strings.Repeat("x", 2<<20) + `","password":"secreta123"}`
}

// H1: el tope de 1 MB se aplica por RUTA, no por Content-Type. El Content-Type
// lo elige el cliente, así que "multipart/form-data" o "image/png" sobre un
// endpoint JSON apagaba el tope: cualquiera mandaba un body enorme al login
// disfrazado de foto.
func TestMaxJSONBody_por_ruta_no_por_content_type(t *testing.T) {
	r := newHardenedRouter([]string{"http://localhost:5173"})

	for _, ct := range []string{
		"multipart/form-data; boundary=xyz",
		"image/png",
		"application/json",
	} {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(bigJSON()))
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("Content-Type %q en un endpoint JSON: un body de 2 MB debe dar 413, dio %d %s", ct, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "body demasiado grande") {
			t.Errorf("Content-Type %q: el 413 tiene que explicar el motivo, dio %s", ct, w.Body.String())
		}
	}

	// Sin Content-Length (body chunked) el tope igual corta la lectura: el
	// binding falla y la ruta responde 400, nunca 200.
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(bigJSON()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("sin Content-Length el tope debe cortar la lectura (400), dio %d %s", w.Code, w.Body.String())
	}

	// El camino feliz no cambia: un JSON chico entra.
	req = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"nadie@mail.com","password":"otraClave1"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("un JSON chico no puede dar 413: %s", w.Body.String())
	}
}

// H1 (PRESERVE): la subida de fotos conserva SU tope (8 MB), que es más grande
// que el JSON. Si el tope por ruta se comiera esta ruta, ninguna foto de más de
// 1 MB se podría subir.
func TestUpload_conserva_su_tope_de_8MB(t *testing.T) {
	env := httpkit.NewPOSEnv()
	r := gin.New()
	// El mismo middleware de producción: la ruta de fotos queda exenta.
	r.Use(httpapi.MaxJSONBody(1 << 20))
	httpapi.RegisterRoutes(r, env.Deps)

	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "fotos-tope")
	base := "/api/v1/salons/" + salonID + "/clients"
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cl)
	photos := base + "/" + cl.ID + "/photos"

	// Foto válida de más de 1 MB (el tope JSON) y menos de 8 MB: 201.
	// El fake de conversión no decodifica la imagen; lo que importa acá es el
	// tamaño del body, que es lo que el middleware mira.
	mediana := append(pngDePrueba(t, 20, 20), bytes.Repeat([]byte{0}, 2<<20)...)
	if w := doMultipart(t, r, photos, ownerTok, "photo", "foto.png", "image/png", mediana); w.Code != http.StatusCreated {
		t.Fatalf("una foto de 2 MB debe subirse (su tope es 8 MB), dio %d %s", w.Code, w.Body.String())
	}

	// Más de 8 MB: 400, el tope propio de la ruta (no 413 del JSON).
	grande := append(pngDePrueba(t, 20, 20), bytes.Repeat([]byte{0}, 9<<20)...)
	if w := doMultipart(t, r, photos, ownerTok, "photo", "foto.png", "image/png", grande); w.Code != http.StatusBadRequest {
		t.Fatalf("más de 8 MB debe dar 400, dio %d %s", w.Code, w.Body.String())
	}

	// Y sigue rechazando lo que no es imagen.
	if w := doMultipart(t, r, photos, ownerTok, "photo", "cv.png", "application/pdf", []byte("%PDF-1.7 nope")); w.Code != http.StatusBadRequest {
		t.Fatalf("un PDF no es una foto: %d %s", w.Code, w.Body.String())
	}
}
