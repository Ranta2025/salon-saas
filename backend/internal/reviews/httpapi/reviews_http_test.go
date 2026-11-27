// Contrato HTTP de las reseñas posvisita (F13, D4..D7): el enlace público, el
// envío, el panel, la configuración y ocultar/mostrar. Todo se prueba por el
// router completo con los dobles en memoria (la base tiene sus tests IT).
package httpapi_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/auth/security"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/reviews"
	httpkit "salon-saas/internal/testutil/httpkit"
)

type rvWorld struct {
	env         *httpkit.Env
	owner       string
	admin       string
	recep       string
	prof        string
	cli         string
	other       string // owner de OTRO salón
	salon       string
	other_salon string
}

// newRVWorld: un salón con dueño, administración, recepción, una profesional y
// una clienta, más un salón ajeno para el aislamiento.
func newRVWorld(t *testing.T) *rvWorld {
	t.Helper()
	env := httpkit.NewPOSEnv()
	w := &rvWorld{env: env}
	var id string
	w.owner, _ = w.user(t, "owner-rv@mail.com")
	w.salon = env.Salon(t, w.owner, "rv-uno")
	w.admin, id = w.user(t, "admin-rv@mail.com")
	httpkit.SeedRole(t, env, w.salon, id, domain.RoleAdmin)
	w.recep, id = w.user(t, "recep-rv@mail.com")
	httpkit.SeedRole(t, env, w.salon, id, domain.RoleReception)
	w.prof, id = w.user(t, "prof-rv@mail.com")
	httpkit.SeedRole(t, env, w.salon, id, domain.RoleProfessional)
	w.cli, id = w.user(t, "cli-rv@mail.com")
	httpkit.SeedRole(t, env, w.salon, id, domain.RoleClient)
	w.other, _ = w.user(t, "other-rv@mail.com")
	w.other_salon = env.Salon(t, w.other, "rv-dos")
	return w
}

// user da de alta una cuenta con sesión SIN pasar por bcrypt (la autenticación
// tiene sus propios tests) y devuelve (token, userID).
func (w *rvWorld) user(t *testing.T, email string) (string, string) {
	t.Helper()
	u := domain.User{ID: domain.NewID(), Name: email, Email: email, PasswordHash: "x"}
	if err := w.env.Users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, err := security.JWTIssuer{Secret: "test-secret"}.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok, u.ID
}

func (w *rvWorld) path(suffix string) string { return "/api/v1/salons/" + w.salon + suffix }

// call ejecuta una petición contra el router completo y devuelve status y cuerpo.
func (w *rvWorld) call(method, path, body, token string) (int, string) {
	r := httpkit.Do(w.env.Router, method, path, body, token)
	return r.Code, r.Body.String()
}

// addReview siembra una reseña publicada en el salón (o en el que se pida).
func (w *rvWorld) addReview(t *testing.T, salonID, id string, rating int, hidden bool, at time.Time) {
	t.Helper()
	w.env.Reviews.SeedReview(ports.Review{
		ID: id, SalonID: salonID, BookingID: "b-" + id, Rating: rating,
		Comment: "Comentario de " + id, AuthorName: "Lía G.", ServiceName: "Corte",
		Hidden: hidden, CreatedAt: at,
	})
}

// seedLink siembra un pedido de reseña y devuelve el token EN CLARO.
func (w *rvWorld) seedLink(t *testing.T, token string, expiresAt time.Time, used bool) {
	t.Helper()
	sent := time.Now().UTC().Add(-time.Hour)
	row := httpkit.ReviewRequestRow{
		ID: "rq-" + token, SalonID: w.salon, BookingID: "b-" + token, TokenHash: reviews.HashToken(token),
		ClientName: "Lía G.", ServiceName: "Corte",
		StartsAt:  time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC),
		CreatedAt: sent, ExpiresAt: expiresAt, SentAt: &sent,
	}
	if used {
		at := sent
		row.UsedAt = &at
	}
	w.env.Reviews.SeedRequest(row)
}

// --- D4: el enlace público ------------------------------------------------------

func TestPublicReviewLink_uniform404(t *testing.T) {
	w := newRVWorld(t)
	tok, err := reviews.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	expired, _ := reviews.NewToken()
	used, _ := reviews.NewToken()
	w.seedLink(t, expired, time.Now().UTC().Add(-time.Hour), false)
	w.seedLink(t, used, time.Now().UTC().Add(time.Hour), true)

	for _, bad := range []string{tok, expired, used, "no-es-un-token", strings.Repeat("a", 43)} {
		code, body := w.call("GET", "/api/v1/public/reviews/"+bad, "", "")
		if code != http.StatusNotFound {
			t.Fatalf("token %q: %d %s", bad, code, body)
		}
		var out struct {
			Error string `json:"error"`
		}
		httpkit.MustJSON(t, []byte(body), &out)
		if out.Error != reviews.MsgLinkInvalid {
			t.Fatalf("token %q: mensaje %q", bad, out.Error)
		}
	}
}

func TestPublicReviewLink_shape(t *testing.T) {
	w := newRVWorld(t)
	tok, err := reviews.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	w.seedLink(t, tok, time.Now().UTC().Add(time.Hour), false)
	code, body := w.call("GET", "/api/v1/public/reviews/"+tok, "", "")
	if code != http.StatusOK {
		t.Fatalf("enlace: %d %s", code, body)
	}
	var got map[string]any
	httpkit.MustJSON(t, []byte(body), &got)
	if strings.Join(httpkit.KeysOf(got), ",") != "salon_name,service_name,starts_at,timezone" {
		t.Fatalf("claves: %v", httpkit.KeysOf(got))
	}
	if got["service_name"] != "Corte" || got["starts_at"] != "2026-11-03T18:00:00Z" || got["timezone"] != "UTC" {
		t.Fatalf("payload: %v", got)
	}
	if strings.Contains(body, "Lía") || strings.Contains(body, "@") {
		t.Fatalf("el enlace público no lleva datos de la clienta: %s", body)
	}
}

// --- D4: el envío ------------------------------------------------------------------

func validLink(t *testing.T, w *rvWorld) string {
	t.Helper()
	tok, err := reviews.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	w.seedLink(t, tok, time.Now().UTC().Add(time.Hour), false)
	return tok
}

func TestPublicReviewSubmit_createdAndSingleUse(t *testing.T) {
	w := newRVWorld(t)
	tok := validLink(t, w)
	code, body := w.call("POST", "/api/v1/public/reviews/"+tok, `{"rating":5,"comment":"  Excelente  "}`, "")
	if code != http.StatusCreated || strings.TrimSpace(body) != `{"ok":true}` {
		t.Fatalf("enviar: %d %s", code, body)
	}
	rows := w.env.Reviews.Rows()
	if len(rows) != 1 || rows[0].Rating != 5 || rows[0].Comment != "Excelente" {
		t.Fatalf("reseña guardada: %+v", rows)
	}
	// El SEGUNDO POST con el mismo enlace es el 404 uniforme.
	code, body = w.call("POST", "/api/v1/public/reviews/"+tok, `{"rating":1,"comment":"otra"}`, "")
	if code != http.StatusNotFound || !strings.Contains(body, reviews.MsgLinkInvalid) {
		t.Fatalf("reuso: %d %s", code, body)
	}
	if len(w.env.Reviews.Rows()) != 1 {
		t.Fatal("el segundo POST escribió una reseña")
	}
}

func TestPublicReviewSubmit_validation(t *testing.T) {
	w := newRVWorld(t)
	tok := validLink(t, w)
	cases := []struct {
		name string
		body string
		want int
		msg  string
	}{
		{"sin rating", `{"comment":"hola"}`, http.StatusBadRequest, reviews.MsgRatingInvalid},
		{"rating 0", `{"rating":0}`, http.StatusBadRequest, reviews.MsgRatingInvalid},
		{"rating 6", `{"rating":6}`, http.StatusBadRequest, reviews.MsgRatingInvalid},
		{"rating decimal", `{"rating":4.5}`, http.StatusBadRequest, "body inválido"},
		{"rating de texto", `{"rating":"cinco"}`, http.StatusBadRequest, "body inválido"},
		{"cuerpo roto", `{`, http.StatusBadRequest, "body inválido"},
		{"NUL en el comentario", `{"rating":5,"comment":"hola\u0000mundo"}`, http.StatusBadRequest, reviews.MsgCommentInvalid},
		{"salto CR", `{"rating":5,"comment":"uno\rdos"}`, http.StatusBadRequest, reviews.MsgCommentInvalid},
		{"control C1", `{"rating":5,"comment":"hola\u0085mundo"}`, http.StatusBadRequest, reviews.MsgCommentInvalid},
		{"501 caracteres", `{"rating":5,"comment":"` + strings.Repeat("a", 501) + `"}`, http.StatusBadRequest, reviews.MsgCommentTooLong},
	}
	for _, c := range cases {
		code, body := w.call("POST", "/api/v1/public/reviews/"+tok, c.body, "")
		if code != c.want || !strings.Contains(body, c.msg) {
			t.Fatalf("%s: %d %s (esperaba %d %q)", c.name, code, body, c.want, c.msg)
		}
		if code == http.StatusInternalServerError {
			t.Fatalf("%s: un 500 nunca es la respuesta de un dato inválido", c.name)
		}
	}
	// Ninguno de los 400 consumió el enlace.
	rows := w.env.Reviews.Requests()
	if len(rows) != 1 || rows[0].UsedAt != nil {
		t.Fatalf("un 400 consumió el enlace: %+v", rows)
	}
	// 500 RUNAS de emoji (2000 bytes) ENTRAN: el tope es de caracteres.
	big := strings.Repeat("💅", 500)
	body, _ := json.Marshal(map[string]any{"rating": 5, "comment": big})
	code, resp := w.call("POST", "/api/v1/public/reviews/"+tok, string(body), "")
	if code != http.StatusCreated {
		t.Fatalf("500 emoji: %d %s", code, resp)
	}
	if got := w.env.Reviews.Rows()[0].Comment; got != big {
		t.Fatalf("el comentario se guardó distinto: %d runas", len([]rune(got)))
	}
	// 501 runas (un emoji más) NO entra.
	tok2 := validLink(t, w)
	body, _ = json.Marshal(map[string]any{"rating": 5, "comment": big + "💅"})
	if code, resp := w.call("POST", "/api/v1/public/reviews/"+tok2, string(body), ""); code != http.StatusBadRequest ||
		!strings.Contains(resp, reviews.MsgCommentTooLong) {
		t.Fatalf("501 emoji: %d %s", code, resp)
	}
	// El comentario es opcional y el cuerpo puede ir vacío de comentario.
	tok3 := validLink(t, w)
	if code, resp := w.call("POST", "/api/v1/public/reviews/"+tok3, `{"rating":3}`, ""); code != http.StatusCreated {
		t.Fatalf("sin comentario: %d %s", code, resp)
	}
}

func TestPublicReviewSubmit_expiredIsUniform404(t *testing.T) {
	w := newRVWorld(t)
	expired, _ := reviews.NewToken()
	w.seedLink(t, expired, time.Now().UTC().Add(-time.Minute), false)
	code, body := w.call("POST", "/api/v1/public/reviews/"+expired, `{"rating":4}`, "")
	if code != http.StatusNotFound || !strings.Contains(body, reviews.MsgLinkInvalid) {
		t.Fatalf("vencido: %d %s", code, body)
	}
	if len(w.env.Reviews.Rows()) != 0 {
		t.Fatal("un enlace vencido escribió una reseña")
	}
}

// --- D6: la lista del panel ---------------------------------------------------------

func TestPanelReviews_permissionsBeforeParameters(t *testing.T) {
	w := newRVWorld(t)
	w.addReview(t, w.salon, "rv1", 5, false, time.Now().UTC())

	for name, tok := range map[string]string{"owner": w.owner, "admin": w.admin, "recep": w.recep} {
		code, body := w.call("GET", w.path("/reviews"), "", tok)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	// Profesional, clienta y otro salón: 403 ANTES de mirar el límite (que está mal).
	for name, tok := range map[string]string{"prof": w.prof, "cliente": w.cli, "otro salón": w.other} {
		code, body := w.call("GET", w.path("/reviews")+"?limit=abc&before=basura", "", tok)
		if code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	// Una membresía dada de baja tampoco es miembro.
	end := time.Now().UTC()
	w.env.Mems.ByKey[w.prof+"|"+w.salon] = domain.Membership{
		UserID: w.prof, SalonID: w.salon, Role: domain.RoleProfessional, EndedAt: &end,
	}
	if code, body := w.call("GET", w.path("/reviews"), "", w.prof); code != http.StatusForbidden {
		t.Fatalf("baja: %d %s", code, body)
	}
}

func TestPanelReviews_shapePaginationAndValidation(t *testing.T) {
	w := newRVWorld(t)
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		w.addReview(t, w.salon, "rv"+strings.Repeat("a", i+1), 5, false, base.Add(time.Duration(i)*time.Hour))
	}
	w.addReview(t, "otro-salon", "rv-ajena", 5, false, base)

	code, body := w.call("GET", w.path("/reviews")+"?limit=2", "", w.owner)
	if code != http.StatusOK {
		t.Fatalf("listar: %d %s", code, body)
	}
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	httpkit.MustJSON(t, []byte(body), &page)
	if len(page.Items) != 2 || page.NextCursor == nil || *page.NextCursor == "" {
		t.Fatalf("primera página: %s", body)
	}
	// Forma EXACTA del renglón del panel.
	if strings.Join(httpkit.KeysOf(page.Items[0]), ",") != "author_name,comment,created_at,hidden,id,rating,service_name" {
		t.Fatalf("claves del renglón: %v", httpkit.KeysOf(page.Items[0]))
	}
	// Aislamiento: la reseña del otro salón no aparece.
	if strings.Contains(body, "rv-ajena") {
		t.Fatalf("lista de otro salón: %s", body)
	}
	// Segunda página por el cursor opaco.
	code, body = w.call("GET", w.path("/reviews")+"?limit=2&before="+*page.NextCursor, "", w.owner)
	if code != http.StatusOK {
		t.Fatalf("segunda página: %d %s", code, body)
	}
	httpkit.MustJSON(t, []byte(body), &page)
	if len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("segunda página: %s", body)
	}
	// Límite y cursor inválidos: 400 con sus textos.
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=abc", "?before=no-es-un-cursor"} {
		code, body = w.call("GET", w.path("/reviews")+q, "", w.owner)
		wantMsg := reviews.MsgLimitRange
		if strings.HasPrefix(q, "?before") {
			wantMsg = reviews.MsgCursorInvalid
		}
		if code != http.StatusBadRequest || !strings.Contains(body, wantMsg) {
			t.Fatalf("%s: %d %s", q, code, body)
		}
	}
}

// --- D6: la configuración -----------------------------------------------------------

func TestReviewSettings_defaultAndRoles(t *testing.T) {
	w := newRVWorld(t)
	code, body := w.call("GET", w.path("/review-settings"), "", w.recep)
	if code != http.StatusOK || strings.TrimSpace(body) != `{"delay_hours":24,"enabled":false}` {
		t.Fatalf("sin guardar: %d %s", code, body)
	}
	for _, tok := range []string{w.owner, w.admin, w.recep} {
		if code, body := w.call("GET", w.path("/review-settings"), "", tok); code != http.StatusOK {
			t.Fatalf("GET: %d %s", code, body)
		}
	}
	for name, tok := range map[string]string{"prof": w.prof, "cliente": w.cli, "otro salón": w.other} {
		if code, body := w.call("GET", w.path("/review-settings"), "", tok); code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	// Un salón sin configuración no inventa fila.
	if _, ok := w.env.Reviews.TheSettings(w.salon); ok {
		t.Fatal("el GET de fábrica escribió una fila")
	}
}

func TestReviewSettings_put(t *testing.T) {
	w := newRVWorld(t)
	code, body := w.call("PUT", w.path("/review-settings"), `{"enabled":true,"delay_hours":6}`, w.owner)
	if code != http.StatusOK {
		t.Fatalf("guardar: %d %s", code, body)
	}
	var got map[string]any
	httpkit.MustJSON(t, []byte(body), &got)
	if got["enabled"] != true || got["delay_hours"] != float64(6) {
		t.Fatalf("respuesta: %s", body)
	}
	if code, body := w.call("GET", w.path("/review-settings"), "", w.recep); code != http.StatusOK ||
		strings.TrimSpace(body) != `{"delay_hours":6,"enabled":true}` {
		t.Fatalf("GET tras guardar: %d %s", code, body)
	}
	// REEMPLAZA: las dos claves son obligatorias (y null no es un valor).
	for _, partial := range []string{`{"enabled":true}`, `{"delay_hours":24}`, `{"enabled":true,"delay_hours":null}`, `{}`} {
		code, body := w.call("PUT", w.path("/review-settings"), partial, w.owner)
		if code != http.StatusBadRequest || !strings.Contains(body, reviews.MsgSettingsMissing) {
			t.Fatalf("%s: %d %s", partial, code, body)
		}
	}
	// Rangos y tipos.
	code, body = w.call("PUT", w.path("/review-settings"), `{"enabled":true,"delay_hours":0}`, w.owner)
	if code != http.StatusBadRequest || !strings.Contains(body, reviews.MsgDelayRange) {
		t.Fatalf("delay 0: %d %s", code, body)
	}
	code, body = w.call("PUT", w.path("/review-settings"), `{"enabled":true,"delay_hours":73}`, w.owner)
	if code != http.StatusBadRequest || !strings.Contains(body, reviews.MsgDelayRange) {
		t.Fatalf("delay 73: %d %s", code, body)
	}
	code, body = w.call("PUT", w.path("/review-settings"), `{"enabled":"si","delay_hours":24}`, w.owner)
	if code != http.StatusBadRequest || !strings.Contains(body, "body inválido") {
		t.Fatalf("tipo equivocado: %d %s", code, body)
	}
	// Los 400 no pisan lo guardado.
	if st, _ := w.env.Reviews.TheSettings(w.salon); st.DelayHours != 6 || !st.Enabled {
		t.Fatalf("un 400 escribió: %+v", st)
	}
	// recepción, profesional y clienta: 403 aunque el cuerpo esté mal.
	for name, tok := range map[string]string{"recep": w.recep, "prof": w.prof, "cliente": w.cli} {
		code, body := w.call("PUT", w.path("/review-settings"), `{"nope":1}`, tok)
		if code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
}

// --- D6: ocultar y mostrar ------------------------------------------------------------

func TestReviewHide_permissionsAndSalonScope(t *testing.T) {
	w := newRVWorld(t)
	w.addReview(t, w.salon, "rv1", 4, false, time.Now().UTC())
	w.addReview(t, w.other_salon, "rv-ajena", 4, false, time.Now().UTC())

	code, body := w.call("POST", w.path("/reviews/rv1/hide"), "", w.owner)
	if code != http.StatusOK || strings.TrimSpace(body) != `{"ok":true}` {
		t.Fatalf("ocultar: %d %s", code, body)
	}
	// Idempotente.
	if code, body := w.call("POST", w.path("/reviews/rv1/hide"), "", w.admin); code != http.StatusOK {
		t.Fatalf("ocultar dos veces: %d %s", code, body)
	}
	if rows := w.env.Reviews.Rows(); !rows[0].Hidden {
		t.Fatalf("no quedó oculta: %+v", rows)
	}
	// Mostrar la devuelve.
	if code, body := w.call("POST", w.path("/reviews/rv1/unhide"), "", w.owner); code != http.StatusOK {
		t.Fatalf("mostrar: %d %s", code, body)
	}
	if rows := w.env.Reviews.Rows(); rows[0].Hidden {
		t.Fatalf("sigue oculta: %+v", rows)
	}
	// La reseña de OTRO salón es 404 con su texto (no se filtra su existencia).
	code, body = w.call("POST", w.path("/reviews/rv-ajena/hide"), "", w.owner)
	if code != http.StatusNotFound || !strings.Contains(body, reviews.MsgReviewNotFound) {
		t.Fatalf("ajena: %d %s", code, body)
	}
	// Un id que no puede existir es 404 sin 500 (NUL en la ruta).
	code, body = w.call("POST", w.path("/reviews/a%00b/hide"), "", w.owner)
	if code != http.StatusNotFound || !strings.Contains(body, reviews.MsgReviewNotFound) {
		t.Fatalf("id con NUL: %d %s", code, body)
	}
	// recepción, profesional y clienta: 403 aunque el id sea basura.
	for name, tok := range map[string]string{"recep": w.recep, "prof": w.prof, "cliente": w.cli} {
		code, body := w.call("POST", w.path("/reviews/a%00b/hide"), "", tok)
		if code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
}

// Las rutas del panel exigen sesión: sin token es 401 antes de todo.
func TestPanelReviews_requiresSession(t *testing.T) {
	w := newRVWorld(t)
	for _, r := range []struct{ method, path string }{
		{"GET", "/reviews"},
		{"GET", "/review-settings"},
		{"PUT", "/review-settings"},
		{"POST", "/reviews/rv1/hide"},
		{"POST", "/reviews/rv1/unhide"},
	} {
		if code, body := w.call(r.method, w.path(r.path), `{}`, ""); code != http.StatusUnauthorized {
			t.Fatalf("%s %s: %d %s", r.method, r.path, code, body)
		}
	}
}

// --- D7: la sección de la web pública ---------------------------------------------

// publicSalon lee el payload público del salón de la prueba.
func (w *rvWorld) publicSalon(t *testing.T, slug string) map[string]any {
	t.Helper()
	code, body := w.call("GET", "/api/v1/public/salons/"+slug, "", "")
	if code != http.StatusOK {
		t.Fatalf("público %s: %d %s", slug, code, body)
	}
	var out map[string]any
	httpkit.MustJSON(t, []byte(body), &out)
	return out
}

// D7: `reviews` es ADITIVA, OMITIDA sin reseñas visibles, con el promedio de UNA
// decimal sobre TODAS las visibles y como máximo los 6 renglones más recientes.
func TestPublicSalonPayload_reviewsSection(t *testing.T) {
	w := newRVWorld(t)
	// Sin reseñas la clave NO viaja: el payload es el de hoy, byte a byte.
	if got := w.publicSalon(t, "rv-uno"); hasKey(got, "reviews") {
		t.Fatalf("sin reseñas no debe viajar `reviews`: %v", got["reviews"])
	}
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	ratings := []int{5, 3, 4, 4, 5, 2, 3}
	sum := 0
	for i, rating := range ratings {
		sum += rating
		w.addReview(t, w.salon, "pub-"+string(rune('a'+i)), rating, false, base.Add(time.Duration(i)*time.Hour))
	}
	// Una oculta de 1 estrella: no cuenta ni se muestra.
	w.addReview(t, w.salon, "pub-oculta", 1, true, base.Add(20*time.Hour))

	got := w.publicSalon(t, "rv-uno")
	raw, ok := got["reviews"].(map[string]any)
	if !ok {
		t.Fatalf("falta `reviews`: %v", got)
	}
	if strings.Join(httpkit.KeysOf(raw), ",") != "average,count,items" {
		t.Fatalf("claves de reviews: %v", httpkit.KeysOf(raw))
	}
	if raw["count"] != float64(7) {
		t.Fatalf("count: %v (la oculta no cuenta)", raw["count"])
	}
	want := math.Round(float64(sum)/float64(len(ratings))*10) / 10
	if raw["average"] != want {
		t.Fatalf("average: %v, esperaba %v (una decimal, sobre TODAS las visibles)", raw["average"], want)
	}
	items, _ := raw["items"].([]any)
	if len(items) != 6 {
		t.Fatalf("items: %d, esperaba los 6 más recientes", len(items))
	}
	first, _ := items[0].(map[string]any)
	if strings.Join(httpkit.KeysOf(first), ",") != "author,comment,date,rating" {
		t.Fatalf("claves del renglón público: %v", httpkit.KeysOf(first))
	}
	if first["rating"] != float64(3) || first["author"] != "Lía G." || first["date"] != "2026-11-01" {
		t.Fatalf("renglón: %v", first)
	}
	// Ningún dato del turno ni del correo en la web pública.
	if strings.Contains(mustJSON(t, got), "@") || strings.Contains(mustJSON(t, got), "b-pub") {
		t.Fatal("la web pública filtra datos del turno")
	}

	// Ocultar desde el panel la de 2 estrellas: desaparece de la web Y del
	// promedio (la regla del usuario).
	code, body := w.call("POST", w.path("/reviews/pub-f/hide"), "", w.owner)
	if code != http.StatusOK {
		t.Fatalf("ocultar: %d %s", code, body)
	}
	raw = w.publicSalon(t, "rv-uno")["reviews"].(map[string]any)
	if raw["count"] != float64(6) {
		t.Fatalf("tras ocultar, count: %v", raw["count"])
	}
	if raw["average"] != 4.0 {
		t.Fatalf("tras ocultar, average: %v (24/6 = 4.0)", raw["average"])
	}
	// Y mostrarla la devuelve.
	if code, body := w.call("POST", w.path("/reviews/pub-f/unhide"), "", w.owner); code != http.StatusOK {
		t.Fatalf("mostrar: %d %s", code, body)
	}
	if raw := w.publicSalon(t, "rv-uno")["reviews"].(map[string]any); raw["count"] != float64(7) {
		t.Fatalf("tras mostrar, count: %v", raw["count"])
	}
}

// D7: si la consulta de reseñas falla, la landing NO se cae: el 200 sale igual y
// la clave se omite (degradar, nunca 500).
func TestPublicSalonPayload_reviewsFailureDegrades(t *testing.T) {
	w := newRVWorld(t)
	w.addReview(t, w.salon, "pub-a", 5, false, time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	w.env.Reviews.VisibleErr = errors.New("la tabla no existe todavía")
	got := w.publicSalon(t, "rv-uno")
	if hasKey(got, "reviews") {
		t.Fatalf("con la consulta caída no debe viajar `reviews`: %v", got["reviews"])
	}
	if !hasKey(got, "salon") || !hasKey(got, "services") {
		t.Fatalf("el resto del payload se perdió: %v", got)
	}
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
