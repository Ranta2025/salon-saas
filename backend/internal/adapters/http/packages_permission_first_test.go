package httpapi

import (
	"net/http"
	"testing"
)

// Verificador independiente (MEDIUM): con un cuerpo MALFORMADO, un profesional o la
// recepción recibían el 400 de validación en vez del 403, porque el handler
// validaba el cuerpo antes del permiso. El permiso va primero: quien no puede hacer
// la acción recibe 403 sin importar el cuerpo, y nada se escribe.
func TestHTTP_packages_permission_is_checked_before_the_body(t *testing.T) {
	w := newPkgWorld(t)
	w.withAllRoles(t)
	cpID, _ := w.buy(t, "")
	cl := w.clientPath(w.clientID)

	cases := []struct {
		name, method, path, body string
		denied                   []string
	}{
		{"POST catálogo vacío", "POST", w.catalogPath(), `{}`, []string{w.recep, w.prof, w.client}},
		{"PUT catálogo vacío", "PUT", w.catalogPath() + "/" + w.pkgID, `{}`, []string{w.recep, w.prof, w.client}},
		{"POST ajustar vacío", "POST", cl + "/" + cpID + "/adjust", `{}`, []string{w.recep, w.prof, w.client}},
		{"POST ajustar roto", "POST", cl + "/" + cpID + "/adjust", `{"delta":`, []string{w.recep, w.prof, w.client}},
		{"POST consumir arreglo", "POST", cl + "/" + cpID + "/redeem", `[]`, []string{w.prof, w.client}},
		{"POST comprar roto", "POST", cl, `{"package_id":`, []string{w.prof, w.client}},
	}
	for _, c := range cases {
		for _, tok := range c.denied {
			rec := doReq(w.env.r, c.method, c.path, c.body, tok)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: quien no puede recibe 403 y no %d %s", c.name, rec.Code, rec.Body.String())
			}
		}
	}
	// Los que SÍ pueden siguen recibiendo la validación del cuerpo.
	if rec := doReq(w.env.r, "POST", cl+"/"+cpID+"/adjust", `{}`, w.owner); rec.Code != http.StatusBadRequest {
		t.Fatalf("el dueño con cuerpo vacío sigue siendo 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doReq(w.env.r, "POST", w.catalogPath(), `{}`, w.admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("la admin con cuerpo vacío sigue siendo 400: %d %s", rec.Code, rec.Body.String())
	}
}
