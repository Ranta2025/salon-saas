package httpapi_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// mgallery imita a pgstore.SalonGallery: tope bajo el lock, orden de subida,
// scope por salón y resolución por slug contra el fake de salones.
type mgallery struct {
	mu     sync.Mutex
	Salons *httpkit.Salons
	byID   map[string]domain.SalonPhoto
	seq    int
	// lookups cuenta las consultas por id/slug: una referencia inválida no debe
	// llegar a la base (Postgres responde 22021 con NUL o UTF-8 roto y eso era un 500).
	lookups int
}

func (f *mgallery) Add(p domain.SalonPhoto, max int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, x := range f.byID {
		if x.SalonID == p.SalonID {
			n++
		}
	}
	if n >= max {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgGalleryFull)
	}
	f.seq++
	p.CreatedAt = time.Unix(1_700_000_000+int64(f.seq), 0).UTC() // orden de subida estable
	f.byID[p.ID] = p
	return nil
}

func (f *mgallery) Count(salonID string) (int, error) {
	n := 0
	for _, x := range f.byID {
		if x.SalonID == salonID {
			n++
		}
	}
	return n, nil
}

func (f *mgallery) List(salonID string) ([]domain.SalonPhoto, error) {
	var out []domain.SalonPhoto
	for _, x := range f.byID {
		if x.SalonID == salonID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *mgallery) Find(salonID, id string) (domain.SalonPhoto, error) {
	f.lookups++
	if p, ok := f.byID[id]; ok && p.SalonID == salonID {
		return p, nil
	}
	return domain.SalonPhoto{}, domain.ErrNotFound
}

func (f *mgallery) FindPublic(slug, id string) (domain.SalonPhoto, error) {
	f.lookups++
	p, ok := f.byID[id]
	if !ok {
		return domain.SalonPhoto{}, domain.ErrNotFound
	}
	if s, ok := f.Salons.BySlug[slug]; !ok || s.ID != p.SalonID {
		return domain.SalonPhoto{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *mgallery) SetCaption(salonID, id, caption string) error {
	p, ok := f.byID[id]
	if !ok || p.SalonID != salonID {
		return domain.ErrNotFound
	}
	p.Caption = caption
	f.byID[id] = p
	return nil
}

func (f *mgallery) Delete(salonID, id string) error {
	p, ok := f.byID[id]
	if !ok || p.SalonID != salonID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

var _ ports.SalonGalleryRepository = (*mgallery)(nil)

func (w *f16World) galleryPath() string { return "/api/v1/salons/" + w.Salon + "/gallery" }

// upload manda un multipart con el archivo en `field` y el Content-Type dado.
func (w *f16World) upload(path, token, field, ctype string, data []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="x.bin"`}
	if ctype != "" {
		h["Content-Type"] = []string{ctype}
	}
	part, _ := mw.CreatePart(h)
	part.Write(data)
	mw.Close()
	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	w.Env.Router.ServeHTTP(rec, req)
	return rec
}

type photoDTO struct {
	ID        string  `json:"id"`
	URL       string  `json:"url"`
	Caption   *string `json:"caption"`
	CreatedAt string  `json:"created_at"`
}

func (w *f16World) addPhoto(t *testing.T, salon, token string) photoDTO {
	t.Helper()
	r := w.upload("/api/v1/salons/"+salon+"/gallery", token, "file", "image/png", png1x1())
	if r.Code != http.StatusCreated {
		t.Fatalf("subir: %d %s", r.Code, r.Body.String())
	}
	var out struct {
		Photo photoDTO `json:"photo"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	return out.Photo
}

// D3: subir -> 201 {"photo":{id,url,caption,created_at}} con la URL pública.
func TestGallery_upload_shape(t *testing.T) {
	w := newF16World(t)
	r := w.upload(w.galleryPath(), w.Admin, "file", "image/png", png1x1())
	if r.Code != http.StatusCreated {
		t.Fatalf("subir: %d %s", r.Code, r.Body.String())
	}
	var raw map[string]map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &raw)
	photo := raw["photo"]
	if strings.Join(httpkit.KeysOf(photo), ",") != "caption,created_at,id,url" {
		t.Fatalf("claves: %v", httpkit.KeysOf(photo))
	}
	id := photo["id"].(string)
	if photo["caption"] != nil || photo["url"] != "/api/v1/public/salons/f16-uno/gallery/"+id {
		t.Fatalf("foto: %v", photo)
	}
	if _, err := time.Parse(time.RFC3339, photo["created_at"].(string)); err != nil {
		t.Fatalf("created_at: %v", err)
	}
	// El campo `photo` (el que manda el frontend de clientas) también se acepta.
	if r := w.upload(w.galleryPath(), w.Owner, "photo", "image/jpeg", []byte("x")); r.Code != http.StatusCreated {
		t.Fatalf("campo photo: %d %s", r.Code, r.Body.String())
	}
}

// D3: permisos antes de leer el archivo; recepción, profesional y ajeno no suben.
func TestGallery_permissionsBeforeBody(t *testing.T) {
	w := newF16World(t)
	for name, tok := range map[string]string{"recep": w.Recep, "prof": w.Prof, "outsider": w.Outsider} {
		if r := w.upload(w.galleryPath(), tok, "file", "image/png", png1x1()); r.Code != http.StatusForbidden {
			t.Fatalf("%s sube: %d", name, r.Code)
		}
		// Cuerpo que ni siquiera es multipart: 403 igual (se mira el permiso primero).
		if r := httpkit.Do(w.Env.Router, "POST", w.galleryPath(), `basura`, tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s cuerpo roto: %d", name, r.Code)
		}
		if r := httpkit.Do(w.Env.Router, "GET", w.galleryPath(), "", tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s lista: %d", name, r.Code)
		}
		if r := httpkit.Do(w.Env.Router, "PUT", w.galleryPath()+"/x", `{"caption":"a"}`, tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s leyenda: %d", name, r.Code)
		}
		if r := httpkit.Do(w.Env.Router, "PUT", w.galleryPath()+"/x", `roto`, tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s leyenda con cuerpo roto: %d", name, r.Code)
		}
		if r := httpkit.Do(w.Env.Router, "DELETE", w.galleryPath()+"/x", "", tok); r.Code != http.StatusForbidden {
			t.Fatalf("%s borra: %d", name, r.Code)
		}
	}
	if r := w.upload(w.galleryPath(), "", "file", "image/png", png1x1()); r.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", r.Code)
	}
	if len(w.Gallery.byID) != 0 || len(w.Env.Storage.Files) != 0 {
		t.Fatalf("un rechazo no escribe: %d filas, %d archivos", len(w.Gallery.byID), len(w.Env.Storage.Files))
	}
}

// D3: tipo inválido, vacío, no multipart: 400 con mensaje; nada guardado.
func TestGallery_upload_invalid(t *testing.T) {
	w := newF16World(t)
	for _, ct := range []string{"image/svg+xml", "image/gif", "text/html", "application/pdf"} {
		r := w.upload(w.galleryPath(), w.Owner, "file", ct, []byte("x"))
		httpkit.WantHTTP(t, r, http.StatusBadRequest, domain.MsgPhotoType)
	}
	httpkit.WantHTTP(t, w.upload(w.galleryPath(), w.Owner, "file", "image/png", nil), http.StatusBadRequest, domain.MsgPhotoSize)
	big := make([]byte, domain.MaxPhotoBytes+1)
	httpkit.WantHTTP(t, w.upload(w.galleryPath(), w.Owner, "file", "image/png", big), http.StatusBadRequest, domain.MsgPhotoSize)
	// Campo equivocado: 400.
	if r := w.upload(w.galleryPath(), w.Owner, "otro", "image/png", png1x1()); r.Code != http.StatusBadRequest {
		t.Fatalf("campo: %d", r.Code)
	}
	if r := httpkit.Do(w.Env.Router, "POST", w.galleryPath(), `{"a":1}`, w.Owner); r.Code != http.StatusBadRequest {
		t.Fatalf("JSON: %d", r.Code)
	}
	if len(w.Gallery.byID) != 0 || len(w.Env.Storage.Files) != 0 {
		t.Fatalf("nada guardado: %d %d", len(w.Gallery.byID), len(w.Env.Storage.Files))
	}
	// El tope exacto de 8 MB entra.
	if r := w.upload(w.galleryPath(), w.Owner, "file", "image/png", make([]byte, domain.MaxPhotoBytes)); r.Code != http.StatusCreated {
		t.Fatalf("8 MB exactos: %d %s", r.Code, r.Body.String())
	}
}

// D3: la foto 13 es 409 con el texto exacto; otro salón sigue pudiendo subir.
func TestGallery_capOf12(t *testing.T) {
	w := newF16World(t)
	for i := 0; i < 12; i++ {
		w.addPhoto(t, w.Salon, w.Owner)
	}
	files := len(w.Env.Storage.Files)
	r := w.upload(w.galleryPath(), w.Owner, "file", "image/png", png1x1())
	httpkit.WantHTTP(t, r, http.StatusConflict, "La galería ya tiene 12 fotos")
	if len(w.Env.Storage.Files) != files || len(w.Gallery.byID) != 12 {
		t.Fatalf("la 13ª no guarda nada")
	}
	w.addPhoto(t, w.other, w.Outsider)
}

// D3: GET -> {"photos":[...]} en orden de subida y solo las del salón.
func TestGallery_list(t *testing.T) {
	w := newF16World(t)
	empty := httpkit.Do(w.Env.Router, "GET", w.galleryPath(), "", w.Owner)
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"photos":[]}` {
		t.Fatalf("vacía: %d %s", empty.Code, empty.Body.String())
	}
	a := w.addPhoto(t, w.Salon, w.Owner)
	b := w.addPhoto(t, w.Salon, w.Admin)
	w.addPhoto(t, w.other, w.Outsider)
	r := httpkit.Do(w.Env.Router, "GET", w.galleryPath(), "", w.Admin)
	var out struct {
		Photos []photoDTO `json:"photos"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	if r.Code != http.StatusOK || len(out.Photos) != 2 || out.Photos[0].ID != a.ID || out.Photos[1].ID != b.ID {
		t.Fatalf("lista: %d %+v", r.Code, out)
	}
	if out.Photos[0].URL != "/api/v1/public/salons/f16-uno/gallery/"+a.ID {
		t.Fatalf("url: %s", out.Photos[0].URL)
	}
}

// D3: PUT {caption} exige la clave; null y "" = sin leyenda; inválida 400.
func TestGallery_caption(t *testing.T) {
	w := newF16World(t)
	p := w.addPhoto(t, w.Salon, w.Owner)
	path := w.galleryPath() + "/" + p.ID
	put := func(body string) (int, photoDTO) {
		r := httpkit.Do(w.Env.Router, "PUT", path, body, w.Owner)
		var out struct {
			Photo photoDTO `json:"photo"`
		}
		if r.Code == http.StatusOK {
			httpkit.MustJSON(t, r.Body.Bytes(), &out)
		}
		return r.Code, out.Photo
	}
	code, got := put(`{"caption":" Mi local "}`)
	if code != http.StatusOK || got.Caption == nil || *got.Caption != "Mi local" || got.ID != p.ID {
		t.Fatalf("poner: %d %+v", code, got)
	}
	for _, v := range []string{`null`, `""`} {
		code, got = put(`{"caption":` + v + `}`)
		if code != http.StatusOK || got.Caption != nil {
			t.Fatalf("quitar con %s: %d %+v", v, code, got)
		}
	}
	put(`{"caption":"queda"}`)
	// Clave ausente: 400 con texto, y NO borra la leyenda.
	httpkit.WantHTTP(t, httpkit.Do(w.Env.Router, "PUT", path, `{}`, w.Owner), http.StatusBadRequest, domain.MsgPhotoCaptionMissing)
	for _, bad := range []string{`"` + strings.Repeat("a", 121) + `"`, `"a\nb"`, `"a\u0000b"`, `5`} {
		if r := httpkit.Do(w.Env.Router, "PUT", path, `{"caption":`+bad+`}`, w.Owner); r.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d", bad, r.Code)
		}
	}
	if w.Gallery.byID[p.ID].Caption != "queda" {
		t.Fatalf("un 400 no escribe: %q", w.Gallery.byID[p.ID].Caption)
	}
	// Foto de otro salón: 404 uniforme.
	foreign := w.addPhoto(t, w.other, w.Outsider)
	httpkit.WantHTTP(t, httpkit.Do(w.Env.Router, "PUT", w.galleryPath()+"/"+foreign.ID, `{"caption":"hack"}`, w.Owner), http.StatusNotFound, domain.MsgPhotoNotFound)
	if w.Gallery.byID[foreign.ID].Caption != "" {
		t.Fatal("la foto ajena cambió")
	}
}

// D3: DELETE -> 204, borra la fila Y el archivo; la de otro salón es 404.
func TestGallery_delete(t *testing.T) {
	w := newF16World(t)
	p := w.addPhoto(t, w.Salon, w.Owner)
	foreign := w.addPhoto(t, w.other, w.Outsider)
	key := w.Gallery.byID[p.ID].StorageKey
	if _, ok := w.Env.Storage.Files[key]; !ok {
		t.Fatal("el archivo debe existir")
	}
	httpkit.WantHTTP(t, httpkit.Do(w.Env.Router, "DELETE", w.galleryPath()+"/"+foreign.ID, "", w.Owner), http.StatusNotFound, domain.MsgPhotoNotFound)
	if len(w.Gallery.byID) != 2 {
		t.Fatal("la ajena no se borra")
	}
	r := httpkit.Do(w.Env.Router, "DELETE", w.galleryPath()+"/"+p.ID, "", w.Owner)
	if r.Code != http.StatusNoContent || r.Body.Len() != 0 {
		t.Fatalf("delete: %d %q", r.Code, r.Body.String())
	}
	if _, ok := w.Env.Storage.Files[key]; ok {
		t.Fatal("el archivo también se borra")
	}
	if _, ok := w.Gallery.byID[p.ID]; ok {
		t.Fatal("la fila también")
	}
	if r := httpkit.Do(w.Env.Router, "DELETE", w.galleryPath()+"/"+p.ID, "", w.Owner); r.Code != http.StatusNotFound {
		t.Fatalf("segundo delete: %d", r.Code)
	}
}

// D3: la imagen pública se sirve SIN firma con el tipo guardado, nosniff y
// cache de 5 minutos.
func TestGallery_publicImage(t *testing.T) {
	w := newF16World(t)
	p := w.addPhoto(t, w.Salon, w.Owner)
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/f16-uno/gallery/"+p.ID, "", "")
	if r.Code != http.StatusOK {
		t.Fatalf("pública: %d %s", r.Code, r.Body.String())
	}
	if got := r.Header().Get("Content-Type"); got != "image/webp" {
		t.Fatalf("Content-Type: %q", got)
	}
	if got := r.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff: %q", got)
	}
	if got := r.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Fatalf("Cache-Control: %q", got)
	}
	if got := r.Header().Get("Content-Length"); got != strconv.Itoa(len("RIFF....WEBP-falso")) || r.Body.String() != "RIFF....WEBP-falso" {
		t.Fatalf("cuerpo: %q len=%q", r.Body.String(), got)
	}
	// El tipo sale de la fila guardada, no de lo que subió la persona.
	w.Gallery.byID[p.ID] = func() domain.SalonPhoto { x := w.Gallery.byID[p.ID]; x.ContentType = "image/png"; return x }()
	r = httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/f16-uno/gallery/"+p.ID, "", "")
	if r.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("tipo guardado: %q", r.Header().Get("Content-Type"))
	}
}

// D3: 404 uniforme (mismo cuerpo y código) para id inexistente, de otro salón,
// slug inexistente o foto de clienta; sin filtrar cuál es cuál.
func TestGallery_publicImage_uniform404(t *testing.T) {
	w := newF16World(t)
	mine := w.addPhoto(t, w.Salon, w.Owner)
	foreign := w.addPhoto(t, w.other, w.Outsider)
	var bodies []string
	for _, path := range []string{
		"/api/v1/public/salons/f16-uno/gallery/" + foreign.ID, // id de otro salón
		"/api/v1/public/salons/f16-uno/gallery/no-existe",
		"/api/v1/public/salons/no-existe/gallery/" + mine.ID,
		"/api/v1/public/salons/f16-dos/gallery/" + mine.ID,
	} {
		r := httpkit.Do(w.Env.Router, "GET", path, "", "")
		if r.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, r.Code)
		}
		if r.Header().Get("Cache-Control") == "public, max-age=300" {
			t.Fatalf("un 404 no se cachea como imagen: %s", path)
		}
		bodies = append(bodies, r.Body.String())
	}
	for _, b := range bodies {
		if b != bodies[0] || !strings.Contains(b, domain.MsgPhotoNotFound) {
			t.Fatalf("el 404 debe ser uniforme: %v", bodies)
		}
	}
}

// D3: una foto de clienta nunca sale por la ruta pública, ni siquiera con su id
// y el slug real del salón.
func TestGallery_publicImage_neverServesClientPhotos(t *testing.T) {
	w := newF16World(t)
	clientID := w.newClientForGallery(t)
	rec := w.upload("/api/v1/salons/"+w.Salon+"/clients/"+clientID+"/photos", w.Owner, "photo", "image/png", png1x1())
	if rec.Code != http.StatusCreated {
		t.Fatalf("foto de clienta: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Photo struct {
			ID string `json:"id"`
		} `json:"photo"`
	}
	httpkit.MustJSON(t, rec.Body.Bytes(), &out)
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/f16-uno/gallery/"+out.Photo.ID, "", "")
	if r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "RIFF") {
		t.Fatalf("la foto de una clienta no se sirve: %d %s", r.Code, r.Body.String())
	}
	// Y la galería del panel tampoco la lista.
	list := httpkit.Do(w.Env.Router, "GET", w.galleryPath(), "", w.Owner)
	if strings.TrimSpace(list.Body.String()) != `{"photos":[]}` {
		t.Fatalf("la galería no mezcla fotos de clientas: %s", list.Body.String())
	}
}

func (w *f16World) newClientForGallery(t *testing.T) string {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "POST", "/api/v1/salons/"+w.Salon+"/clients", `{"name":"Lía"}`, w.Owner)
	if r.Code != http.StatusCreated {
		t.Fatalf("clienta: %d %s", r.Code, r.Body.String())
	}
	var c struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, r.Body.Bytes(), &c)
	return c.ID
}

// El tope de body de 1 MB de httpapi.MaxJSONBody exime a la subida de la galería (que
// tiene el suyo, 8 MB) y NO a ninguna otra ruta.
func TestGallery_uploadBodyLimitIsPerRoute(t *testing.T) {
	w := newF16World(t)
	r := gin.New()
	r.Use(httpapi.MaxJSONBodyExceptGalleryUpload(1 << 20))
	httpapi.RegisterRoutes(r, w.Env.Deps)
	w.Env.Router = r

	// 2 MB de imagen: pasa el tope general y entra por el de la galería.
	if rec := w.upload(w.galleryPath(), w.Owner, "file", "image/png", make([]byte, 2<<20)); rec.Code != http.StatusCreated {
		t.Fatalf("la subida de 2 MB debe entrar: %d %s", rec.Code, rec.Body.String())
	}
	// Más de 8 MB + el margen del multipart: se corta.
	if rec := w.upload(w.galleryPath(), w.Owner, "file", "image/png", make([]byte, 10<<20)); rec.Code == http.StatusCreated {
		t.Fatalf("10 MB no entran")
	}
	// Cualquier otra ruta sigue con 1 MB aunque diga ser multipart.
	big := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest("PUT", w.profilePath(), strings.NewReader(big))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Header.Set("Authorization", "Bearer "+w.Owner)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("el perfil con 2 MB debe dar 413: %d", rec.Code)
	}
	// PRESERVE: la subida de fotos de clientas sigue exenta y el login con 2 MB sigue en 413.
	req = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(bigJSON()))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("login grande: %d", rec.Code)
	}
}
