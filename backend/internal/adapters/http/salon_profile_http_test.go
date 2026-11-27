package httpapi_test

import (
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// mprofiles imita a pgstore.SalonProfiles: nombre y slug salen del fake de
// salones (la misma fuente que FindBySlug), los cinco datos viven acá, el slug no
// se escribe y un salón inexistente es ErrNotFound.
type mprofiles struct {
	Salons *httpkit.Salons
	Rows   map[string]domain.SalonProfile
	Saves  int
}

func (f *mprofiles) Profile(salonID string) (domain.SalonProfile, error) {
	for _, s := range f.Salons.BySlug {
		if s.ID != salonID {
			continue
		}
		p := f.Rows[salonID]
		p.SalonID, p.Slug, p.Name = s.ID, s.Slug, s.Name
		return p, nil
	}
	return domain.SalonProfile{}, domain.ErrNotFound
}

func (f *mprofiles) SaveProfile(p domain.SalonProfile) error {
	cur, err := f.Profile(p.SalonID)
	if err != nil {
		return err
	}
	if f.Rows == nil {
		f.Rows = map[string]domain.SalonProfile{}
	}
	p.Slug = cur.Slug
	f.Rows[p.SalonID] = p
	s := f.Salons.BySlug[cur.Slug]
	s.Name = p.Name
	f.Salons.BySlug[cur.Slug] = s
	f.Saves++
	return nil
}

var _ ports.SalonProfileRepository = (*mprofiles)(nil)

// f16World es el router con el perfil, la galería y la vidriera de F16 cableados
// sobre los fakes del httpkit.Env.
type f16World struct {
	Env      *httpkit.Env
	profiles *mprofiles
	Gallery  *mgallery
	Packages *httpkit.PkgCatalog
	Salon    string
	other    string
	// Tokens: dueño del salón, administración, recepción, profesional, ajeno.
	Owner, Admin, Recep, Prof, Outsider string
	OwnerID, ProfID                     string
}

func newF16World(t *testing.T) *f16World {
	t.Helper()
	env := httpkit.NewPOSEnv()
	w := &f16World{Env: env}
	w.profiles = &mprofiles{Salons: env.Salons}
	w.Gallery = &mgallery{Salons: env.Salons, byID: map[string]domain.SalonPhoto{}}
	w.Packages = &httpkit.PkgCatalog{Services: env.Services}
	env.Deps.Profile = httpapi.SalonProfileHandler{
		Profile:     app.SalonProfileService{Profiles: w.profiles, Memberships: env.Mems},
		Memberships: env.Mems,
	}
	env.Deps.Gallery = httpapi.GalleryHandler{
		Gallery: app.GalleryService{
			Gallery: w.Gallery, Memberships: env.Mems, Salons: env.Salons, Photos: env.Storage, Images: httpkit.Images{},
		},
		Memberships: env.Mems,
	}
	env.Deps.Public.Public = app.PublicSalonService{
		Salons: env.Salons, Services: env.Services, Settings: env.Settings, Hours: env.Weekly,
		Profiles: w.profiles, Staff: env.Mems, Professionals: env.Professionals, Gallery: w.Gallery, Packages: w.Packages,
	}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)
	var id string
	w.Owner, w.OwnerID = w.fastUser(t, "owner-f16@mail.com")
	w.Salon = env.Salon(t, w.Owner, "f16-uno")
	w.Admin, id = w.fastUser(t, "admin-f16@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleAdmin)
	w.Recep, id = w.fastUser(t, "recep-f16@mail.com")
	httpkit.SeedRole(t, env, w.Salon, id, domain.RoleReception)
	w.Prof, w.ProfID = w.fastUser(t, "prof-f16@mail.com")
	httpkit.SeedRole(t, env, w.Salon, w.ProfID, domain.RoleProfessional)
	w.Outsider, _ = w.fastUser(t, "other-f16@mail.com")
	w.other = env.Salon(t, w.Outsider, "f16-dos")
	return w
}

// fastUser da de alta una cuenta y su token SIN pasar por bcrypt (register +
// login cuestan dos hashes por persona, y bajo -race las suites con muchos
// mundos se vuelven lentas): la autenticación ya la prueban sus propios tests,
// acá solo hace falta alguien con sesión.
func (w *f16World) fastUser(t *testing.T, email string) (string, string) {
	t.Helper()
	u := domain.User{ID: domain.NewID(), Name: email, Email: email, PasswordHash: "x"}
	if err := w.Env.Users.Save(u); err != nil {
		t.Fatal(err)
	}
	tok, err := security.JWTIssuer{Secret: "test-secret"}.Issue(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok, u.ID
}

func (w *f16World) profilePath() string { return "/api/v1/salons/" + w.Salon + "/profile" }

const fullProfileBody = `{"name":"Navajas de Oro","description":"Barbería clásica","address":"Calle 1 #23","phone":"+52 55","email":"Hola@Navajas.com","instagram":"navajas.oro"}`

// D1: GET lo ve cualquier miembro activo con la forma exacta; los datos sin
// cargar salen como null.
func TestProfile_Get_shapeAndRoles(t *testing.T) {
	w := newF16World(t)
	for name, tok := range map[string]string{"owner": w.Owner, "admin": w.Admin, "recep": w.Recep, "prof": w.Prof} {
		r := httpkit.Do(w.Env.Router, "GET", w.profilePath(), "", tok)
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body.String())
		}
		var got map[string]any
		httpkit.MustJSON(t, r.Body.Bytes(), &got)
		if strings.Join(keysOf(got), ",") != "address,description,email,instagram,name,phone,slug" {
			t.Fatalf("%s: claves %v", name, keysOf(got))
		}
		if got["name"] != "Salón f16-uno" || got["slug"] != "f16-uno" || got["description"] != nil || got["address"] != nil ||
			got["phone"] != nil || got["email"] != nil || got["instagram"] != nil {
			t.Fatalf("%s: %v", name, got)
		}
	}
	// Sin token 401; ajeno (miembro de otro salón) 403; salón inexistente 403.
	if r := httpkit.Do(w.Env.Router, "GET", w.profilePath(), "", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", r.Code)
	}
	if r := httpkit.Do(w.Env.Router, "GET", w.profilePath(), "", w.Outsider); r.Code != http.StatusForbidden {
		t.Fatalf("ajeno: %d", r.Code)
	}
}

// D1: el PUT reemplaza, devuelve el perfil nuevo y el slug no se edita (se ignora).
func TestProfile_Put_replacesAndIgnoresSlug(t *testing.T) {
	w := newF16World(t)
	body := strings.Replace(fullProfileBody, `{"name"`, `{"slug":"hackeado","name"`, 1)
	r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), body, w.Admin)
	if r.Code != http.StatusOK {
		t.Fatalf("put: %d %s", r.Code, r.Body.String())
	}
	var got map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &got)
	want := map[string]any{"name": "Navajas de Oro", "slug": "f16-uno", "description": "Barbería clásica", "address": "Calle 1 #23",
		"phone": "+52 55", "email": "hola@navajas.com", "instagram": "navajas.oro"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %v != %v (%v)", k, got[k], v, got)
		}
	}
	// El GET siguiente lo ve igual y el slug público sigue siendo el original.
	r = httpkit.Do(w.Env.Router, "GET", w.profilePath(), "", w.Recep)
	var again map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &again)
	if again["name"] != "Navajas de Oro" || again["slug"] != "f16-uno" {
		t.Fatalf("get: %v", again)
	}
	if _, ok := w.Env.Salons.BySlug["hackeado"]; ok {
		t.Fatal("el slug no se edita")
	}
	// null explícito borra el dato; "" también.
	r = httpkit.Do(w.Env.Router, "PUT", w.profilePath(), `{"name":"X","description":null,"address":"","phone":null,"email":null,"instagram":null}`, w.Owner)
	var cleared map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &cleared)
	if r.Code != http.StatusOK || cleared["description"] != nil || cleared["address"] != nil || cleared["name"] != "X" {
		t.Fatalf("null/vacío: %d %v", r.Code, cleared)
	}
}

// D1: solo owner/admin escriben; el permiso se mira ANTES de leer el cuerpo
// (lección de F8): un body roto da 403 a quien no puede, no 400.
func TestProfile_Put_permissionBeforeBody(t *testing.T) {
	w := newF16World(t)
	for name, tok := range map[string]string{"recep": w.Recep, "prof": w.Prof, "outsider": w.Outsider} {
		for _, body := range []string{fullProfileBody, `{"name":`, `no es json`, ``} {
			if r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), body, tok); r.Code != http.StatusForbidden {
				t.Fatalf("%s con %q: %d %s", name, body, r.Code, r.Body.String())
			}
		}
	}
	if r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), fullProfileBody, ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", r.Code)
	}
	if w.profiles.Saves != 0 {
		t.Fatalf("un rechazo no escribe: %d", w.profiles.Saves)
	}
}

// D1: faltan claves -> 400 con el texto exacto y no se escribe nada; null
// explícito sí se acepta (ya probado arriba).
func TestProfile_Put_requiresEverySixKeys(t *testing.T) {
	w := newF16World(t)
	keys := []string{"name", "description", "address", "phone", "email", "instagram"}
	for _, missing := range keys {
		parts := []string{}
		for _, k := range keys {
			if k != missing {
				parts = append(parts, `"`+k+`":"valor"`)
			}
		}
		body := `{` + strings.Join(parts, ",") + `}`
		r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), body, w.Owner)
		wantHTTP(t, r, http.StatusBadRequest, "Faltan datos del salón: nombre, descripción, dirección, teléfono, email e instagram")
	}
	r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), `{}`, w.Owner)
	wantHTTP(t, r, http.StatusBadRequest, "Faltan datos del salón: nombre, descripción, dirección, teléfono, email e instagram")
	if w.profiles.Saves != 0 {
		t.Fatalf("un 400 no escribe: %d", w.profiles.Saves)
	}
}

// D1: email inválido, NUL, saltos de línea y largos son 400 (nunca 500) y no
// cambian nada, ni los campos que sí venían bien.
func TestProfile_Put_validationsWriteNothing(t *testing.T) {
	w := newF16World(t)
	build := func(field, value string) string {
		m := map[string]string{"name": `"Ok"`, "description": `"d"`, "address": `"a"`, "phone": `"1"`, "email": `"a@b.com"`, "instagram": `"ig"`}
		m[field] = value
		parts := []string{}
		for k, v := range m {
			parts = append(parts, `"`+k+`":`+v)
		}
		return `{` + strings.Join(parts, ",") + `}`
	}
	q := func(s string) string { return `"` + s + `"` }
	cases := []struct{ name, field, value, msg string }{
		{"email sin arroba", "email", q("mal"), "Email inválido"},
		{"email con coma", "email", q("a@b.com,c@d.com"), "Email inválido"},
		{"NUL en la dirección", "address", `"a\u0000b"`, domain.MsgSalonInvalidChars},
		{"salto en la descripción", "description", `"a\nb"`, domain.MsgSalonInvalidChars},
		{"retorno en el teléfono", "phone", `"a\rb"`, domain.MsgSalonInvalidChars},
		{"NUL en el nombre", "name", `"a\u0000b"`, domain.MsgSalonInvalidChars},
		{"salto en el email", "email", `"a@b.com\n"`, domain.MsgSalonInvalidChars},
		{"nombre vacío", "name", q(" "), domain.MsgSalonNameRequired},
		{"nombre null", "name", `null`, domain.MsgSalonNameRequired},
		{"nombre largo", "name", q(strings.Repeat("a", 121)), domain.MsgSalonNameTooLong},
		{"descripción larga", "description", q(strings.Repeat("a", 501)), domain.MsgSalonDescriptionLong},
		{"dirección larga", "address", q(strings.Repeat("a", 201)), domain.MsgSalonAddressLong},
		{"teléfono largo", "phone", q(strings.Repeat("1", 41)), domain.MsgSalonPhoneLong},
		{"instagram con @", "instagram", q("@navajas"), domain.MsgSalonInstagramBad},
		{"instagram largo", "instagram", q(strings.Repeat("a", 61)), domain.MsgSalonInstagramLong},
	}
	for _, c := range cases {
		r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), build(c.field, c.value), w.Owner)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", c.name, r.Code, r.Body.String())
		}
		if got := errText(t, r); got != c.msg {
			t.Fatalf("%s: %q != %q", c.name, got, c.msg)
		}
	}
	// Tipos equivocados: 400 «body inválido».
	if r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), build("phone", `5`), w.Owner); r.Code != http.StatusBadRequest {
		t.Fatalf("número en un texto: %d", r.Code)
	}
	if w.profiles.Saves != 0 {
		t.Fatalf("un 400 no escribe: %d", w.profiles.Saves)
	}
	got, _ := w.profiles.Profile(w.Salon)
	if got.Name != "Salón f16-uno" || got.Address != "" {
		t.Fatalf("nada cambió: %+v", got)
	}
}

// Tenant: el dueño de otro salón no puede escribir (ni leer) este perfil.
func TestProfile_tenantIsolation(t *testing.T) {
	w := newF16World(t)
	if r := httpkit.Do(w.Env.Router, "PUT", w.profilePath(), fullProfileBody, w.Outsider); r.Code != http.StatusForbidden {
		t.Fatalf("put ajeno: %d", r.Code)
	}
	otherPath := "/api/v1/salons/" + w.other + "/profile"
	if r := httpkit.Do(w.Env.Router, "PUT", otherPath, fullProfileBody, w.Outsider); r.Code != http.StatusOK {
		t.Fatalf("put propio: %d", r.Code)
	}
	mine, _ := w.profiles.Profile(w.Salon)
	if mine.Name == "Navajas de Oro" {
		t.Fatalf("el perfil de f16-uno cambió")
	}
}

// ---- D2: descripción de servicio ---------------------------------------

func (w *f16World) servicesPath() string { return "/api/v1/salons/" + w.Salon + "/services" }

func TestServices_description(t *testing.T) {
	w := newF16World(t)
	r := httpkit.Do(w.Env.Router, "POST", w.servicesPath(), `{"name":"Corte","duration_min":30,"price_cents":1000,"commission_pct":10,"description":"Corte clásico"}`, w.Owner)
	if r.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", r.Code, r.Body.String())
	}
	var created map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &created)
	if created["description"] != "Corte clásico" {
		t.Fatalf("alta devuelve la descripción: %v", created)
	}
	id := created["id"].(string)

	// Alta SIN la clave (cliente anterior a F16): description null y todo lo demás igual.
	r = httpkit.Do(w.Env.Router, "POST", w.servicesPath(), `{"name":"Barba","duration_min":20,"price_cents":500,"commission_pct":0}`, w.Owner)
	var plain map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &plain)
	if r.Code != http.StatusCreated || plain["description"] != nil || plain["price_cents"].(float64) != 500 {
		t.Fatalf("alta sin descripción: %d %v", r.Code, plain)
	}

	put := func(body string) (int, map[string]any) {
		r := httpkit.Do(w.Env.Router, "PUT", w.servicesPath()+"/"+id, body, w.Owner)
		var out map[string]any
		httpkit.MustJSON(t, r.Body.Bytes(), &out)
		return r.Code, out
	}
	// PUT sin la clave: se CONSERVA la descripción (no se borra).
	code, out := put(`{"name":"Corte","duration_min":30,"price_cents":1200,"commission_pct":10}`)
	if code != http.StatusOK || out["description"] != "Corte clásico" || out["price_cents"].(float64) != 1200 {
		t.Fatalf("PUT sin clave conserva: %d %v", code, out)
	}
	// Con texto reemplaza; null y "" quitan.
	code, out = put(`{"name":"Corte","duration_min":30,"price_cents":1200,"commission_pct":10,"description":"Nueva"}`)
	if code != http.StatusOK || out["description"] != "Nueva" {
		t.Fatalf("reemplaza: %d %v", code, out)
	}
	for _, v := range []string{`null`, `""`} {
		code, out = put(`{"name":"Corte","duration_min":30,"price_cents":1200,"commission_pct":10,"description":` + v + `}`)
		if code != http.StatusOK || out["description"] != nil {
			t.Fatalf("quita con %s: %d %v", v, code, out)
		}
	}
	// Inválida: 400 con mensaje y no escribe.
	put(`{"name":"Corte","duration_min":30,"price_cents":1200,"commission_pct":10,"description":"Queda"}`)
	for _, bad := range []string{`"` + strings.Repeat("a", 301) + `"`, `"a\nb"`, `"a\u0000b"`} {
		r := httpkit.Do(w.Env.Router, "PUT", w.servicesPath()+"/"+id, `{"name":"Corte","duration_min":30,"price_cents":9999,"commission_pct":10,"description":`+bad+`}`, w.Owner)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d %s", bad, r.Code, r.Body.String())
		}
	}
	list := httpkit.Do(w.Env.Router, "GET", w.servicesPath(), "", w.Recep)
	var l struct {
		Services []map[string]any `json:"services"`
	}
	httpkit.MustJSON(t, list.Body.Bytes(), &l)
	var corte map[string]any
	for _, s := range l.Services {
		if s["id"] == id {
			corte = s
		}
	}
	if corte == nil || corte["description"] != "Queda" || corte["price_cents"].(float64) != 1200 {
		t.Fatalf("un 400 no escribe y la lista trae la descripción: %v", corte)
	}
	// Los permisos no cambiaron: recepción no edita.
	if r := httpkit.Do(w.Env.Router, "PUT", w.servicesPath()+"/"+id, `{"name":"Corte","duration_min":30,"price_cents":1,"commission_pct":10,"description":"x"}`, w.Recep); r.Code != http.StatusForbidden {
		t.Fatalf("recepción: %d", r.Code)
	}
}
