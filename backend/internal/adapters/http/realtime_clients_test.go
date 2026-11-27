package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strconv"
	"strings"
	"testing"
	"time"
)

var _ ports.NotificationRepository = (*httpkit.Notifications)(nil)

var _ ports.ClientRepository = (*httpkit.Clients)(nil)

var _ ports.PhotoStorage = (*httpkit.Storage)(nil)

var _ ports.ImageProcessor = httpkit.Images{}

var _ ports.EventPublisher = (*httpkit.Events)(nil)

// pngDePrueba arma un PNG real (el handler y el adapter validan el archivo).
func pngDePrueba(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x % 255), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

// doMultipart sube un archivo como multipart/form-data (lo que manda el input
// de archivo del navegador).
func doMultipart(t *testing.T, r http.Handler, path, token, field, filename, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="` + filename + `"`}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Flujo: la reserva deja un aviso -> se lista -> se marca leída -> 204.
func TestHTTP_notifications_flujo(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "avisos")
	base := "/api/v1/salons/" + salonID
	httpkit.SeedProfessional(env, salonID, "prof1")

	// Una reserva crea el aviso "Nuevo turno" para todo el salón.
	if w := httpkit.Do(env.Router, "POST", base+"/bookings",
		`{"professional_id":"prof1","client_name":"Lía","starts_at":"2026-12-03T10:00:00Z","ends_at":"2026-12-03T11:00:00Z"}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("reserva: %d %s", w.Code, w.Body.String())
	}

	w := httpkit.Do(env.Router, "GET", base+"/notifications", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar avisos: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Notifications []map[string]any `json:"notifications"`
		Unread        int              `json:"unread"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Notifications) != 1 {
		t.Fatalf("esperaba 1 aviso, dio %s", w.Body.String())
	}
	n := list.Notifications[0]
	if n["title"] != "Nuevo turno" || n["kind"] != "booking" || n["read"] != false {
		t.Fatalf("aviso mal formado: %+v", n)
	}
	if n["salon_id"] != salonID || n["id"] == "" || n["created_at"] == nil {
		t.Fatalf("faltan campos del contrato: %+v", n)
	}
	if list.Unread != 1 {
		t.Fatalf("contador de no leídas: %d", list.Unread)
	}

	// Marcar leída: 204 sin body.
	nid, _ := n["id"].(string)
	w = httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", ownerTok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("marcar leída: %d %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("204 no debe traer body: %s", w.Body.String())
	}
	// Idempotente: de nuevo 204.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("marcar dos veces: %d", w.Code)
	}
	w = httpkit.Do(env.Router, "GET", base+"/notifications", "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if list.Notifications[0]["read"] != true || list.Unread != 0 {
		t.Fatalf("la marca no se reflejó: %s", w.Body.String())
	}
	// Un aviso inexistente (o de otro salón): 404.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/no-existe/read", "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("aviso inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	// Sin token: 401.
	if w := httpkit.Do(env.Router, "GET", base+"/notifications", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// No-miembro: 403.
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "GET", base+"/notifications", "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// El aviso de un salón no se puede marcar desde otro: 404 (no se filtra).
	otherTok, _ := env.Register(t, "owner2@mail.com")
	otherSalon := env.Salon(t, otherTok, "avisos-2")
	_ = otherSalon
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("otro salón marcando: %d %s", w.Code, w.Body.String())
	}
}

// El cierre de caja también avisa (y el aviso trae la diferencia en texto).
func TestHTTP_notifications_cierre(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "avisos-cierre")
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/cash-closes", `{"counted_cents":1000,"note":"ok"}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("cierre: %d %s", w.Code, w.Body.String())
	}
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/notifications", "", ownerTok)
	var list struct {
		Notifications []map[string]any `json:"notifications"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Notifications) != 1 || list.Notifications[0]["kind"] != "cash" {
		t.Fatalf("esperaba el aviso del cierre: %s", w.Body.String())
	}
	if title, _ := list.Notifications[0]["title"].(string); title != "Cierre de caja" {
		t.Fatalf("título del aviso: %q", title)
	}
	if body, _ := list.Notifications[0]["body"].(string); !strings.Contains(body, "diferencia") {
		t.Fatalf("el cuerpo del aviso: %q", body)
	}
}

func TestHTTP_clients(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "clientes")
	base := "/api/v1/salons/" + salonID + "/clients"

	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía Fernández","phone":"11 5555-1234","notes":"prefiere la mañana"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear cliente: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		SalonID string `json:"salon_id"`
		Name    string `json:"name"`
		Phone   string `json:"phone"`
		Notes   string `json:"notes"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.ID == "" || created.SalonID != salonID || created.Name != "Lía Fernández" ||
		created.Phone != "11 5555-1234" || created.Notes != "prefiere la mañana" {
		t.Fatalf("respuesta de creación incompleta: %s", w.Body.String())
	}

	// Listado.
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	var list struct {
		Clients []map[string]any `json:"clients"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Clients) != 1 || list.Clients[0]["id"] != created.ID {
		t.Fatalf("listado de clientes: %s", w.Body.String())
	}

	// Nombre vacío y body roto: 400.
	for _, bad := range []string{`{"name":"   "}`, `no-json`} {
		if w := httpkit.Do(env.Router, "POST", base, bad, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("%q debe dar 400, dio %d %s", bad, w.Code, w.Body.String())
		}
	}

	// Profesional: lee sí, escribe no. No-miembro: 403 en todo.
	profTok, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := httpkit.Do(env.Router, "GET", base, "", profTok); w.Code != http.StatusOK {
		t.Fatalf("profesional leyendo: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"Otro"}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional creando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d", w.Code)
	}
	// Recepción administra clientes.
	recepTok, recepID := env.Register(t, "recep@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"Ana"}`, recepTok); w.Code != http.StatusCreated {
		t.Fatalf("recepción creando: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

// E-03: el alta de ficha acepta un email opcional, lo normaliza y lo devuelve
// en la MISMA forma de la ficha (el frontend lo muestra sin cambiar nada más).
func TestHTTP_client_create_con_email(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "clientes-email")
	base := "/api/v1/salons/" + salonID + "/clients"

	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía","phone":"11 5555-1234","email":"  Lia@Mail.COM "}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear con email: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.ID == "" || created.Email != "lia@mail.com" {
		t.Fatalf("el email se devuelve normalizado: %s", w.Body.String())
	}

	// Persistido: el listado de la pantalla de clientes lo trae.
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	var list struct {
		Clients []map[string]any `json:"clients"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Clients) != 1 || list.Clients[0]["email"] != "lia@mail.com" {
		t.Fatalf("listado con email: %s", w.Body.String())
	}

	// Sin email sigue entrando, con el campo vacío (texto, nunca null).
	w = httpkit.Do(env.Router, "POST", base, `{"name":"Ana"}`, ownerTok)
	var sinEmail struct {
		Email string `json:"email"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &sinEmail)
	if sinEmail.Email != "" {
		t.Fatalf("sin email el campo va vacío, dio %q", sinEmail.Email)
	}

	// Email inválido: 400 con mensaje para la pantalla, sin el prefijo interno
	// del error de dominio ("validación:").
	w = httpkit.Do(env.Router, "POST", base, `{"name":"Ana","email":"sin-arroba"}`, ownerTok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("email inválido debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "Email inválido") || strings.Contains(body, "validación:") {
		t.Fatalf("mensaje del email inválido: %s", body)
	}
}

func TestHTTP_photos_flujo(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "fotos")
	base := "/api/v1/salons/" + salonID + "/clients"
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cl)
	photos := base + "/" + cl.ID + "/photos"

	// Subida multipart con un PNG real (como el input del navegador).
	png := pngDePrueba(t, 40, 30)
	w = doMultipart(t, env.Router, photos, ownerTok, "file", "foto.png", "image/png", png)
	if w.Code != http.StatusCreated {
		t.Fatalf("subir foto: %d %s", w.Code, w.Body.String())
	}
	var up struct {
		Photo struct {
			ID     string `json:"id"`
			URL    string `json:"url"`
			W      int    `json:"w"`
			H      int    `json:"h"`
			Bytes  int    `json:"bytes"`
			Client string `json:"client_id"`
		} `json:"photo"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &up)
	// Ronda 2: la url viene firmada con vencimiento (24 h). El frontend la usa
	// tal cual; el id solo ya no sirve.
	if up.Photo.ID == "" || !strings.HasPrefix(up.Photo.URL, "/api/v1/photos/"+up.Photo.ID+"?exp=") ||
		!strings.Contains(up.Photo.URL, "&sig=") {
		t.Fatalf("respuesta de subida: %s", w.Body.String())
	}
	if up.Photo.W != 1280 || up.Photo.H != 960 || up.Photo.Bytes == 0 {
		t.Fatalf("dimensiones de la variante: %+v", up.Photo)
	}
	if up.Photo.Client != cl.ID {
		t.Fatalf("client_id: %+v", up.Photo)
	}

	// La galería de la ficha.
	w = httpkit.Do(env.Router, "GET", photos, "", ownerTok)
	var gal struct {
		Photos []map[string]any `json:"photos"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &gal)
	if len(gal.Photos) != 1 || gal.Photos[0]["url"] != up.Photo.URL {
		t.Fatalf("galería: %s", w.Body.String())
	}

	// El archivo se sirve por la url devuelta, SIN Authorization (es lo que
	// puede hacer un <img src>: el id aleatorio es la capability).
	req := httptest.NewRequest("GET", up.Photo.URL, nil)
	rec := httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("servir foto: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/webp") {
		t.Fatalf("content-type: %q", ct)
	}
	data, _ := io.ReadAll(rec.Body)
	if string(data) != "RIFF....WEBP-falso" {
		t.Fatalf("bytes servidos: %q", data)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Fatal("conviene cachear la foto (Cache-Control)")
	}

	// Foto inexistente con firma válida: 404 en JSON (no un body vacío). Sin
	// firma la respuesta es 403 y no confirma si el id existe (ronda 2).
	expNoExiste := httpapi.PhotoURLNow().Add(time.Hour).Unix()
	noExiste := "/api/v1/photos/no-existe?exp=" + strconv.FormatInt(expNoExiste, 10) + "&sig=" + env.Signer.Sign("no-existe", expNoExiste)
	req = httptest.NewRequest("GET", noExiste, nil)
	rec = httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foto inexistente: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/api/v1/photos/no-existe", nil)
	rec = httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sin firma debe dar 403, dio %d", rec.Code)
	}

	// Borrar: 204, la galería queda vacía y la url deja de servir.
	if w := httpkit.Do(env.Router, "DELETE", photos+"/"+up.Photo.ID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("borrar foto: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", photos, "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("galería post delete: %d", w.Code)
	}
	req = httptest.NewRequest("GET", up.Photo.URL, nil)
	rec = httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("la foto borrada no debe servirse: %d", rec.Code)
	}
	if w := httpkit.Do(env.Router, "DELETE", photos+"/"+up.Photo.ID, "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("borrar dos veces: %d", w.Code)
	}
}

// El frontend sube con FormData.append('photo', file); curl/Postman usan
// "file". Los dos nombres de campo tienen que funcionar igual.
func TestHTTP_photos_acepta_campo_photo_y_file(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "fotos-campos")
	base := "/api/v1/salons/" + salonID + "/clients"
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cl)
	photos := base + "/" + cl.ID + "/photos"

	for _, field := range []string{"photo", "file"} {
		w := doMultipart(t, env.Router, photos, ownerTok, field, "foto.png", "image/png", pngDePrueba(t, 20, 20))
		if w.Code != http.StatusCreated {
			t.Fatalf("campo %q debe aceptarse: %d %s", field, w.Code, w.Body.String())
		}
		var up struct {
			Photo struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"photo"`
		}
		httpkit.MustJSON(t, w.Body.Bytes(), &up)
		if up.Photo.ID == "" || up.Photo.URL == "" {
			t.Fatalf("campo %q: respuesta sin foto: %s", field, w.Body.String())
		}
	}
}

// La imagen cruda en el body (Content-Type: image/png) también vale: es lo más
// simple para probar con curl sin multipart.
func TestHTTP_photos_acepta_body_crudo(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "fotos-raw")
	base := "/api/v1/salons/" + salonID + "/clients"
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cl)

	req := httptest.NewRequest("POST", base+"/"+cl.ID+"/photos", bytes.NewReader(pngDePrueba(t, 16, 16)))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("Authorization", "Bearer "+ownerTok)
	rec := httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("body crudo: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHTTP_photos_rechaza_lo_que_no_corresponde(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "fotos-2")
	base := "/api/v1/salons/" + salonID + "/clients"
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Lía"}`, ownerTok)
	var cl struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &cl)
	photos := base + "/" + cl.ID + "/photos"

	// PDF: 400 (aunque se llame .png).
	if w := doMultipart(t, env.Router, photos, ownerTok, "file", "cv.png", "application/pdf", []byte("%PDF-1.7 nope")); w.Code != http.StatusBadRequest {
		t.Fatalf("pdf debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Más de 8 MB: 400.
	big := append(pngDePrueba(t, 20, 20), bytes.Repeat([]byte("x"), domain.MaxPhotoBytes+1)...)
	if w := doMultipart(t, env.Router, photos, ownerTok, "file", "grande.png", "image/png", big); w.Code != http.StatusBadRequest {
		t.Fatalf("más de 8 MB debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Partes que no son la foto / sin archivo: 400.
	if w := doMultipart(t, env.Router, photos, ownerTok, "otro", "x.png", "image/png", pngDePrueba(t, 10, 10)); w.Code != http.StatusBadRequest {
		t.Fatalf("campo equivocado debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// JSON en vez de multipart: 400.
	if w := httpkit.Do(env.Router, "POST", photos, `{"file":"x"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("json debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Profesional: 403. No-miembro: 403.
	profTok, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := doMultipart(t, env.Router, photos, profTok, "file", "foto.png", "image/png", pngDePrueba(t, 10, 10)); w.Code != http.StatusForbidden {
		t.Fatalf("profesional subiendo debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := doMultipart(t, env.Router, photos, strangerTok, "file", "foto.png", "image/png", pngDePrueba(t, 10, 10)); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Cliente de otro salón (la ficha no existe): 404.
	otherTok, _ := env.Register(t, "owner2@mail.com")
	otherSalon := env.Salon(t, otherTok, "fotos-3")
	if w := doMultipart(t, env.Router, "/api/v1/salons/"+otherSalon+"/clients/"+cl.ID+"/photos", otherTok, "file", "foto.png", "image/png", pngDePrueba(t, 10, 10)); w.Code != http.StatusNotFound {
		t.Fatalf("cliente de otro salón debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
}

// La ruta del websocket vive en el mismo router que la API y autentica por
// query param: sin token no hay conexión.
func TestHTTP_ws_registrado_y_autenticado(t *testing.T) {
	env := httpkit.NewPOSEnv()

	req := httptest.NewRequest("GET", "/api/v1/ws", nil)
	rec := httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ws sin token debe dar 401, dio %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Fatalf("la respuesta debe ser {\"error\":...}: %s", rec.Body.String())
	}

	// Con token válido el handler intenta el upgrade: un GET normal recibe 400
	// de gorilla (la ruta existe y llegó al hub, no es un 404).
	tok, _ := env.Register(t, "owner@mail.com")
	_ = env.Salon(t, tok, "ws-salon")
	req = httptest.NewRequest("GET", "/api/v1/ws?token="+tok, nil)
	rec = httptest.NewRecorder()
	env.Router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("la ruta /api/v1/ws no está registrada")
	}
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("con token y con salón no debería rechazar: %d %s", rec.Code, rec.Body.String())
	}
}
