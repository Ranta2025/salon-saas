package app

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeClients imita a pgstore.Clients: todo scopeado por salón y cliente.
type fakeClients struct {
	clients       []domain.Client
	photos        []domain.ClientPhoto
	failSavePhoto bool // simula una DB caída justo después de escribir el archivo
}

func (f *fakeClients) Save(c domain.Client) error {
	for i := range f.clients {
		if f.clients[i].ID == c.ID {
			f.clients[i] = c
			return nil
		}
	}
	f.clients = append(f.clients, c)
	return nil
}

func (f *fakeClients) FindByID(salonID, id string) (domain.Client, error) {
	for _, c := range f.clients {
		if c.SalonID == salonID && c.ID == id {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *fakeClients) List(salonID string) ([]domain.Client, error) {
	var out []domain.Client
	for _, c := range f.clients {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeClients) SavePhoto(p domain.ClientPhoto) error {
	if f.failSavePhoto {
		return errors.New("db caída")
	}
	f.photos = append(f.photos, p)
	return nil
}

func (f *fakeClients) ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error) {
	var out []domain.ClientPhoto
	for _, p := range f.photos {
		if p.SalonID == salonID && p.ClientID == clientID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeClients) DeletePhoto(salonID, clientID, id string) error {
	for i, p := range f.photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			f.photos = append(f.photos[:i], f.photos[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeClients) FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error) {
	for _, p := range f.photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

func (f *fakeClients) PhotoByID(id string) (domain.ClientPhoto, error) {
	for _, p := range f.photos {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

var _ ports.ClientRepository = (*fakeClients)(nil)

// fakeStorage: filesystem en memoria, con contador de escrituras.
type fakeStorage struct {
	files map[string][]byte
	puts  int
	dels  int
}

func (f *fakeStorage) Put(key string, data []byte, contentType string) (string, error) {
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[key] = data
	f.puts++
	return key, nil
}

func (f *fakeStorage) Get(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return data, nil
}

func (f *fakeStorage) Delete(path string) error {
	delete(f.files, path)
	f.dels++
	return nil
}

var _ ports.PhotoStorage = (*fakeStorage)(nil)

// fakeImages: no convierte nada, devuelve un WebP de mentira con dimensiones
// fijas. err simula bytes que no son una imagen.
type fakeImages struct {
	out          []byte
	w, h         int
	err          error
	gotContentIn []byte
}

func (f *fakeImages) ToWebP(data []byte) ([]byte, int, int, error) {
	f.gotContentIn = data
	if f.err != nil {
		return nil, 0, 0, f.err
	}
	if f.out == nil {
		f.out = []byte("RIFF....WEBP")
	}
	out := f.out
	w, h := f.w, f.h
	if w == 0 {
		w, h = 1280, 960
	}
	return out, w, h, nil
}

var _ ports.ImageProcessor = (*fakeImages)(nil)

func clientEnv() (*ClientService, *fakeClients, *fakeStorage, *fakeImages, *fakeMemberships) {
	clients := &fakeClients{}
	store := &fakeStorage{}
	images := &fakeImages{}
	mems := &fakeMemberships{}
	return &ClientService{Clients: clients, Memberships: mems, Photos: store, Images: images}, clients, store, images, mems
}

func TestClientService_create_permisos(t *testing.T) {
	svc, clients, _, _, mems := clientEnv()
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	admin := miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	cliente := miembro(mems, "u-cli", "s1", domain.RoleClient)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	for _, actor := range []domain.Membership{owner, admin, recep} {
		c, err := svc.Create(actor, "s1", "Cliente "+actor.UserID, "555", "")
		if err != nil {
			t.Fatalf("%s debe poder crear clientes: %v", actor.Role, err)
		}
		if c.SalonID != "s1" || c.ID == "" {
			t.Fatalf("cliente mal creado: %+v", c)
		}
	}
	for _, actor := range []domain.Membership{profe, cliente, ajeno} {
		if _, err := svc.Create(actor, "s1", "X", "", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe poder crear clientes, dio %v", actor.Role, err)
		}
	}
	if _, err := svc.Create(owner, "s1", "   ", "", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nombre vacío debe dar ErrValidation, dio %v", err)
	}
	if len(clients.clients) != 3 {
		t.Fatalf("solo deberían persistirse los 3 válidos, hay %d", len(clients.clients))
	}
}

func TestClientService_list_scoped(t *testing.T) {
	svc, _, _, _, mems := clientEnv()
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := svc.Create(owner, "s1", "Lía", "", ""); err != nil {
		t.Fatalf("crear: %v", err)
	}
	// El profesional mira la ficha, no la administra: leer sí, escribir no.
	if list, err := svc.List(profe, "s1"); err != nil || len(list) != 1 {
		t.Fatalf("profesional debe listar: %d %v", len(list), err)
	}
	if _, err := svc.List(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón debe dar Forbidden, dio %v", err)
	}
}

func TestClientService_addPhoto_convierte_y_guarda(t *testing.T) {
	svc, clients, store, images, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, err := svc.Create(recep, "s1", "Lía", "555", "")
	if err != nil {
		t.Fatalf("crear cliente: %v", err)
	}
	raw := []byte("jpeg-original")
	photo, err := svc.AddPhoto(recep, "s1", c.ID, raw, "image/jpeg")
	if err != nil {
		t.Fatalf("subir foto: %v", err)
	}
	if string(images.gotContentIn) != string(raw) {
		t.Fatal("el procesador debe recibir el original")
	}
	if photo.ContentType != domain.PhotoTypeWebP {
		t.Fatalf("se persiste la variante webp, dio %q", photo.ContentType)
	}
	if photo.Width != 1280 || photo.Height != 960 || photo.Bytes != len(images.out) {
		t.Fatalf("dimensiones/bytes de la variante: %+v", photo)
	}
	if got := store.files[photo.Path]; string(got) != string(images.out) {
		t.Fatalf("el storage debe tener la variante y no el original (%d bytes)", len(got))
	}
	if strings.Contains(photo.Path, "..") || !strings.Contains(photo.Path, c.ID) {
		t.Fatalf("la ruta debe quedar bajo el cliente: %q", photo.Path)
	}
	if len(clients.photos) != 1 || clients.photos[0].ID != photo.ID {
		t.Fatalf("no persistió la foto: %+v", clients.photos)
	}
}

func TestClientService_addPhoto_rechaza_lo_que_no_es_imagen(t *testing.T) {
	svc, clients, store, images, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "")

	// Tipo declarado fuera del catálogo: 400 antes de procesar.
	for _, ct := range []string{"application/pdf", "image/gif", "text/plain", ""} {
		if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), ct); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q debe dar ErrValidation, dio %v", ct, err)
		}
	}
	// Bytes que no son una imagen (el procesador lo detecta): 400.
	images.err = domain.ErrValidation
	if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("no soy imagen"), "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bytes inválidos deben dar ErrValidation, dio %v", err)
	}
	images.err = nil
	// Vacío y más de 8 MB: 400.
	if _, err := svc.AddPhoto(recep, "s1", c.ID, nil, "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("archivo vacío debe dar ErrValidation, dio %v", err)
	}
	big := make([]byte, domain.MaxPhotoBytes+1)
	if _, err := svc.AddPhoto(recep, "s1", c.ID, big, "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("más de 8 MB debe dar ErrValidation, dio %v", err)
	}
	if len(clients.photos) != 0 || store.puts != 0 {
		t.Fatalf("nada inválido debe persistirse: photos=%d puts=%d", len(clients.photos), store.puts)
	}
}

func TestClientService_addPhoto_scoped_y_permisos(t *testing.T) {
	svc, clients, store, _, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajenoOwner := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	c, _ := svc.Create(recep, "s1", "Lía", "", "")

	// Actor de otro salón: 403 aunque el cliente exista en s1.
	if _, err := svc.AddPhoto(ajenoOwner, "s1", c.ID, []byte("x"), "image/png"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón debe dar Forbidden, dio %v", err)
	}
	// Profesional: no administra clientes.
	if _, err := svc.AddPhoto(profe, "s1", c.ID, []byte("x"), "image/png"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional debe dar Forbidden, dio %v", err)
	}
	// Cliente inexistente en el propio salón: 404.
	if _, err := svc.AddPhoto(recep, "s1", "no-existe", []byte("x"), "image/png"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cliente inexistente debe dar NotFound, dio %v", err)
	}
	if len(clients.photos) != 0 || store.puts != 0 {
		t.Fatalf("nada debe persistirse: photos=%d puts=%d", len(clients.photos), store.puts)
	}
}

// Si la DB falla después de escribir el archivo, el archivo no puede quedar
// huérfano en el storage: se borra y el error sube.
func TestClientService_addPhoto_rollback_del_archivo(t *testing.T) {
	svc, clients, store, _, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "")
	clients.failSavePhoto = true
	if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), "image/png"); err == nil {
		t.Fatal("esperaba error de persistencia")
	}
	if store.puts != 1 || store.dels != 1 {
		t.Fatalf("el archivo escrito debe borrarse: puts=%d dels=%d", store.puts, store.dels)
	}
	if len(store.files) != 0 {
		t.Fatalf("no debe quedar basura: %+v", store.files)
	}
}

func TestClientService_listPhotos_y_delete(t *testing.T) {
	svc, _, store, _, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	c, _ := svc.Create(recep, "s1", "Lía", "", "")
	p, err := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), "image/png")
	if err != nil {
		t.Fatalf("subir: %v", err)
	}

	// Listar: cualquier miembro del salón.
	if list, err := svc.ListPhotos(profe, "s1", c.ID); err != nil || len(list) != 1 {
		t.Fatalf("listar fotos: %d %v", len(list), err)
	}
	if _, err := svc.ListPhotos(ajeno, "s1", c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón debe dar Forbidden, dio %v", err)
	}

	// Borrar: solo quien administra clientes, y el archivo se va con la fila.
	if err := svc.DeletePhoto(profe, "s1", c.ID, p.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional borrando debe dar Forbidden, dio %v", err)
	}
	if err := svc.DeletePhoto(recep, "s1", c.ID, "no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foto inexistente debe dar NotFound, dio %v", err)
	}
	if err := svc.DeletePhoto(recep, "s1", c.ID, p.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if _, ok := store.files[p.Path]; ok {
		t.Fatal("el archivo debe borrarse del storage")
	}
	if list, _ := svc.ListPhotos(recep, "s1", c.ID); len(list) != 0 {
		t.Fatalf("la foto no debe seguir listada: %+v", list)
	}
}

func TestClientService_photo_para_streaming(t *testing.T) {
	svc, _, store, images, mems := clientEnv()
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "")
	p, _ := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), "image/png")

	got, data, err := svc.Photo(p.ID)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.ID != p.ID || got.ContentType != domain.PhotoTypeWebP {
		t.Fatalf("metadata: %+v", got)
	}
	if string(data) != string(images.out) {
		t.Fatalf("bytes servidos distintos a los guardados (%q vs %q)", data, store.files[p.Path])
	}
	if _, _, err := svc.Photo("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foto inexistente debe dar NotFound, dio %v", err)
	}
}
