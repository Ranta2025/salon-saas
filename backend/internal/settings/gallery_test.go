package settings

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/clients"
	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

type galleryWorld struct {
	svc     GalleryService
	gallery *fakeGallery
	store   *testutil.FakeStorage
	images  *testutil.FakeImages
	salons  *testutil.FakeSalons
	mems    *testutil.FakeMemberships
}

func newGalleryWorld() *galleryWorld {
	mems := &testutil.FakeMemberships{}
	for _, m := range []domain.Membership{
		{UserID: "owner", SalonID: "s1", Role: domain.RoleOwner},
		{UserID: "admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "prof", SalonID: "s1", Role: domain.RoleProfessional},
		{UserID: "owner2", SalonID: "s2", Role: domain.RoleOwner},
	} {
		_ = mems.Save(m)
	}
	salons := &testutil.FakeSalons{}
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "uno", Name: "Uno"})
	_ = salons.Save(domain.Salon{ID: "s2", Slug: "dos", Name: "Dos"})
	gallery := &fakeGallery{slugs: map[string]string{"s1": "uno", "s2": "dos"}}
	store, images := &testutil.FakeStorage{}, &testutil.FakeImages{}
	return &galleryWorld{
		svc:     GalleryService{Gallery: gallery, Memberships: mems, Salons: salons, Photos: store, Images: images},
		gallery: gallery, store: store, images: images, salons: salons, mems: mems,
	}
}

func gActor(user, salon string) domain.Membership {
	return domain.Membership{UserID: user, SalonID: salon}
}

func (w *galleryWorld) add(t *testing.T, salon string) GalleryPhoto {
	t.Helper()
	owner := map[string]string{"s1": "owner", "s2": "owner2"}[salon]
	p, err := w.svc.Add(gActor(owner, salon), salon, []byte("png-bytes"), "image/png")
	if err != nil {
		t.Fatalf("subir: %v", err)
	}
	return p
}

func TestPublicGalleryPath(t *testing.T) {
	if got := PublicGalleryPath("uno", "abc"); got != "/api/v1/public/salons/uno/gallery/abc" {
		t.Fatalf("ruta: %q", got)
	}
}

// D3: subir convierte a WebP, guarda el archivo y registra la fila con la URL pública.
func TestGallery_Add_pipeline(t *testing.T) {
	w := newGalleryWorld()
	p, err := w.svc.Add(gActor("admin", "s1"), "s1", []byte("jpeg-bytes"), " IMAGE/JPEG ")
	if err != nil {
		t.Fatalf("subir: %v", err)
	}
	if p.ID == "" || p.SalonID != "s1" || p.ContentType != domain.PhotoTypeWebP || p.Caption != "" ||
		p.URL != "/api/v1/public/salons/uno/gallery/"+p.ID {
		t.Fatalf("foto: %+v", p)
	}
	if string(w.images.GotContentIn) != "jpeg-bytes" {
		t.Fatalf("el pipeline no convirtió lo subido: %q", w.images.GotContentIn)
	}
	data, ok := w.store.Files[p.StorageKey]
	if !ok || !bytes.Equal(data, []byte("RIFF....WEBP")) || !strings.HasPrefix(p.StorageKey, "s1/gallery/") {
		t.Fatalf("archivo guardado: key=%q files=%v", p.StorageKey, w.store.Files)
	}
	if n, _ := w.gallery.Count("s1"); n != 1 {
		t.Fatalf("filas: %d", n)
	}
}

// D3: solo owner/admin; un rechazo no guarda archivo ni fila.
func TestGallery_permissions(t *testing.T) {
	w := newGalleryWorld()
	own := w.add(t, "s1")
	for _, a := range []domain.Membership{gActor("recep", "s1"), gActor("prof", "s1"), gActor("owner2", "s2"), gActor("nadie", "s1")} {
		if err := w.svc.RequireManager(a, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("RequireManager %v: %v", a, err)
		}
		if _, err := w.svc.Add(a, "s1", []byte("x"), "image/png"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("Add %v: %v", a, err)
		}
		if _, err := w.svc.List(a, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("List %v: %v", a, err)
		}
		if _, err := w.svc.SetCaption(a, "s1", own.ID, strp("x")); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("SetCaption %v: %v", a, err)
		}
		if err := w.svc.Delete(a, "s1", own.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("Delete %v: %v", a, err)
		}
	}
	if w.store.Puts != 1 || w.gallery.adds != 1 || w.gallery.deletes != 0 || w.store.Dels != 0 {
		t.Fatalf("un rechazo no escribe: puts=%d adds=%d dels=%d", w.store.Puts, w.gallery.adds, w.store.Dels)
	}
}

// D3: mismas validaciones que la foto de una clienta: tipo y tamaño.
func TestGallery_Add_validatesTypeAndSize(t *testing.T) {
	w := newGalleryWorld()
	a := gActor("owner", "s1")
	for _, ct := range []string{"image/gif", "image/svg+xml", "text/html", ""} {
		_, err := w.svc.Add(a, "s1", []byte("x"), ct)
		if got := msgOfApp(t, err); got != domain.MsgPhotoType {
			t.Fatalf("%q: %q", ct, got)
		}
	}
	_, err := w.svc.Add(a, "s1", nil, "image/png")
	if got := msgOfApp(t, err); got != domain.MsgPhotoSize {
		t.Fatalf("vacío: %q", got)
	}
	_, err = w.svc.Add(a, "s1", make([]byte, domain.MaxPhotoBytes+1), "image/png")
	if got := msgOfApp(t, err); got != domain.MsgPhotoSize {
		t.Fatalf("grande: %q", got)
	}
	// El tope exacto (8 MB) sí entra.
	if _, err := w.svc.Add(a, "s1", make([]byte, domain.MaxPhotoBytes), "image/png"); err != nil {
		t.Fatalf("8 MB exactos: %v", err)
	}
	// Bytes que no son una imagen: el procesador falla y no queda nada.
	w2 := newGalleryWorld()
	w2.images.Err = domain.ErrValidation
	if _, err := w2.svc.Add(gActor("owner", "s1"), "s1", []byte("no-imagen"), "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no imagen: %v", err)
	}
	if w2.store.Puts != 0 || w2.gallery.adds != 0 {
		t.Fatalf("nada guardado: puts=%d adds=%d", w2.store.Puts, w2.gallery.adds)
	}
}

func msgOfApp(t *testing.T, err error) string {
	t.Helper()
	if err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("se esperaba un error de validación, vino %v", err)
	}
	return err.Error()
}

// D3: tope de 12 por salón con el texto exacto; la 13ª no guarda archivo.
func TestGallery_Add_capOf12(t *testing.T) {
	w := newGalleryWorld()
	for i := 0; i < 12; i++ {
		w.add(t, "s1")
	}
	puts := w.store.Puts
	_, err := w.svc.Add(gActor("owner", "s1"), "s1", []byte("x"), "image/png")
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != "La galería ya tiene 12 fotos" {
		t.Fatalf("13ª: %v", err)
	}
	if w.store.Puts != puts {
		t.Fatalf("la 13ª no debe guardar archivo")
	}
	// El tope es POR salón: el otro salón sigue pudiendo subir.
	w.add(t, "s2")
}

// D3: si la carrera del tope se pierde DESPUÉS de guardar el archivo (el repo
// rechaza), el archivo se borra: nada de basura sin dueño.
func TestGallery_Add_raceLostCleansFile(t *testing.T) {
	w := newGalleryWorld()
	w.gallery.addErr = domain.NewMessage(domain.ErrAlreadyExists, domain.MsgGalleryFull)
	_, err := w.svc.Add(gActor("owner", "s1"), "s1", []byte("x"), "image/png")
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgGalleryFull {
		t.Fatalf("err: %v", err)
	}
	if w.store.Puts != 1 || w.store.Dels != 1 || len(w.store.Files) != 0 {
		t.Fatalf("el archivo debe borrarse: puts=%d dels=%d files=%d", w.store.Puts, w.store.Dels, len(w.store.Files))
	}
	// Un fallo cualquiera de la DB también limpia y se propaga.
	w.gallery.addErr = errBoom
	if _, err := w.svc.Add(gActor("owner", "s1"), "s1", []byte("x"), "image/png"); !errors.Is(err, errBoom) {
		t.Fatalf("err: %v", err)
	}
	if len(w.store.Files) != 0 {
		t.Fatalf("archivo huérfano: %v", w.store.Files)
	}
}

// D3: la galería se lista en orden de subida y solo la del salón.
func TestGallery_List_orderAndTenant(t *testing.T) {
	w := newGalleryWorld()
	first := w.add(t, "s1")
	time.Sleep(2 * time.Millisecond)
	second := w.add(t, "s1")
	w.add(t, "s2")
	list, err := w.svc.List(gActor("owner", "s1"), "s1")
	if err != nil || len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("lista: %+v %v", list, err)
	}
	if list[0].URL != "/api/v1/public/salons/uno/gallery/"+first.ID {
		t.Fatalf("url: %q", list[0].URL)
	}
	empty, err := w.svc.List(gActor("admin", "s1"), "s1")
	if err != nil || empty == nil {
		t.Fatalf("lista no nula: %v", err)
	}
}

// D3: la leyenda se reemplaza; null y "" la quitan; inválida es 400 sin escribir.
func TestGallery_SetCaption(t *testing.T) {
	w := newGalleryWorld()
	p := w.add(t, "s1")
	got, err := w.svc.SetCaption(gActor("owner", "s1"), "s1", p.ID, strp("  Mi local "))
	if err != nil || got.Caption != "Mi local" || w.gallery.All[0].Caption != "Mi local" {
		t.Fatalf("poner: %+v %v", got, err)
	}
	for _, c := range []*string{nil, strp(""), strp(" ")} {
		got, err = w.svc.SetCaption(gActor("owner", "s1"), "s1", p.ID, c)
		if err != nil || got.Caption != "" || w.gallery.All[0].Caption != "" {
			t.Fatalf("quitar: %+v %v", got, err)
		}
	}
	w.gallery.All[0].Caption = "queda"
	for _, bad := range []string{strings.Repeat("a", 121), "a\nb", "a\x00b"} {
		if _, err := w.svc.SetCaption(gActor("owner", "s1"), "s1", p.ID, strp(bad)); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if w.gallery.All[0].Caption != "queda" {
		t.Fatalf("un 400 no escribe: %q", w.gallery.All[0].Caption)
	}
	// Foto de OTRO salón: 404 y nada cambia.
	other := w.add(t, "s2")
	if _, err := w.svc.SetCaption(gActor("owner", "s1"), "s1", other.ID, strp("hack")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
	if w.gallery.All[1].Caption != "" {
		t.Fatalf("la foto de s2 cambió")
	}
}

// D3: borrar quita la fila Y el archivo; la foto de otro salón no se toca.
func TestGallery_Delete(t *testing.T) {
	w := newGalleryWorld()
	p := w.add(t, "s1")
	other := w.add(t, "s2")
	if err := w.svc.Delete(gActor("owner", "s1"), "s1", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
	if _, ok := w.store.Files[other.StorageKey]; !ok || len(w.gallery.All) != 2 {
		t.Fatalf("la foto de s2 debe seguir")
	}
	if err := w.svc.Delete(gActor("owner", "s1"), "s1", p.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if _, ok := w.store.Files[p.StorageKey]; ok || w.store.Dels != 1 {
		t.Fatalf("el archivo también se borra: %v dels=%d", w.store.Files, w.store.Dels)
	}
	if err := w.svc.Delete(gActor("owner", "s1"), "s1", p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segunda vez: %v", err)
	}
}

// D3: la ruta pública sirve SOLO fotos del salón del slug, con 404 uniforme.
func TestGallery_PublicPhoto(t *testing.T) {
	w := newGalleryWorld()
	p := w.add(t, "s1")
	other := w.add(t, "s2")
	got, data, err := w.svc.PublicPhoto("uno", p.ID)
	if err != nil || got.ContentType != domain.PhotoTypeWebP || string(data) != "RIFF....WEBP" {
		t.Fatalf("pública: %+v %q %v", got, data, err)
	}
	uniform := func(slug, id string) string {
		_, _, err := w.svc.PublicPhoto(slug, id)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s/%s: %v", slug, id, err)
		}
		return err.Error()
	}
	a := uniform("uno", other.ID)    // id de otro salón
	b := uniform("uno", "no-existe") // id inexistente
	c := uniform("no-existe", p.ID)  // slug inexistente
	d := uniform("dos", p.ID)        // el id de s1 con el slug de s2
	if a != domain.MsgPhotoNotFound || a != b || b != c || c != d {
		t.Fatalf("el 404 debe ser uniforme: %q %q %q %q", a, b, c, d)
	}
	// El archivo se perdió del storage: también el mismo 404 (no un 500).
	delete(w.store.Files, p.StorageKey)
	if got := uniform("uno", p.ID); got != domain.MsgPhotoNotFound {
		t.Fatalf("archivo perdido: %q", got)
	}
}

// D3: una foto de CLIENTA nunca sale por la ruta pública: vive en otra tabla, y
// pedir su id con un slug real es 404.
func TestGallery_PublicPhoto_neverServesClientPhotos(t *testing.T) {
	w := newGalleryWorld()
	fichas := &testutil.FakeClients{}
	clientSvc := clients.ClientService{Clients: fichas, Memberships: w.mems, Photos: w.store, Images: w.images}
	_ = fichas.Save(domain.Client{ID: "c1", SalonID: "s1", Name: "Ana"})
	photo, err := clientSvc.AddPhoto(gActor("owner", "s1"), "s1", "c1", []byte("x"), "image/png")
	if err != nil {
		t.Fatalf("foto de clienta: %v", err)
	}
	if _, _, err := w.svc.PublicPhoto("uno", photo.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("la foto de una clienta no se sirve: %v", err)
	}
	if list, _ := w.svc.List(gActor("owner", "s1"), "s1"); len(list) != 0 {
		t.Fatalf("la galería no mezcla fotos de clientas: %+v", list)
	}
}
