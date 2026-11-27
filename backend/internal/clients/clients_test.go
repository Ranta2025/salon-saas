package clients

import (
	"errors"
	"strings"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// fakeClients imita a pgstore.Clients: todo scopeado por salón y cliente.
type fakeClients struct {
	clients       []domain.Client
	photos        []domain.ClientPhoto
	FailSavePhoto bool // simula una DB caída justo después de escribir el archivo
	FailSave      bool // simula una DB caída al guardar la ficha
}

func (f *fakeClients) Save(c domain.Client) error {
	if f.FailSave {
		return errors.New("db caída")
	}
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

// FindByPhone compara por teléfono normalizado, igual que el índice de pgstore.
func (f *fakeClients) FindByPhone(salonID, phone string) (domain.Client, error) {
	for _, c := range f.clients {
		if c.SalonID == salonID && domain.NormalizePhone(c.Phone) == phone {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

// FindByName es el respaldo sin teléfono: nombre exacto dentro del salón.
func (f *fakeClients) FindByName(salonID, name string) (domain.Client, error) {
	for _, c := range f.clients {
		if c.SalonID == salonID && c.Name == name {
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
	if f.FailSavePhoto {
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

// testutil.FakeStorage: filesystem en memoria, con contador de escrituras.
func clientEnv() (*ClientService, *fakeClients, *testutil.FakeStorage, *testutil.FakeImages, *testutil.FakeMemberships) {
	clients := &fakeClients{}
	store := &testutil.FakeStorage{}
	images := &testutil.FakeImages{}
	mems := &testutil.FakeMemberships{}
	return &ClientService{Clients: clients, Memberships: mems, Photos: store, Images: images}, clients, store, images, mems
}

func TestClientService_create_permisos(t *testing.T) {
	svc, clients, _, _, mems := clientEnv()
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)
	admin := testutil.Miembro(mems, "u-admin", "s1", domain.RoleAdmin)
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	cliente := testutil.Miembro(mems, "u-cli", "s1", domain.RoleClient)
	ajeno := testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleOwner)

	for _, actor := range []domain.Membership{owner, admin, recep} {
		c, err := svc.Create(actor, "s1", "Cliente "+actor.UserID, "555", "", "")
		if err != nil {
			t.Fatalf("%s debe poder crear clientes: %v", actor.Role, err)
		}
		if c.SalonID != "s1" || c.ID == "" {
			t.Fatalf("cliente mal creado: %+v", c)
		}
	}
	for _, actor := range []domain.Membership{profe, cliente, ajeno} {
		if _, err := svc.Create(actor, "s1", "X", "", "", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe poder crear clientes, dio %v", actor.Role, err)
		}
	}
	if _, err := svc.Create(owner, "s1", "   ", "", "", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nombre vacío debe dar ErrValidation, dio %v", err)
	}
	if len(clients.clients) != 3 {
		t.Fatalf("solo deberían persistirse los 3 válidos, hay %d", len(clients.clients))
	}
}

func TestClientService_list_scoped(t *testing.T) {
	svc, _, _, _, mems := clientEnv()
	owner := testutil.Miembro(mems, "u-owner", "s1", domain.RoleOwner)
	profe := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	if _, err := svc.Create(owner, "s1", "Lía", "", "", ""); err != nil {
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
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, err := svc.Create(recep, "s1", "Lía", "555", "", "")
	if err != nil {
		t.Fatalf("crear cliente: %v", err)
	}
	raw := []byte("jpeg-original")
	photo, err := svc.AddPhoto(recep, "s1", c.ID, raw, "image/jpeg")
	if err != nil {
		t.Fatalf("subir foto: %v", err)
	}
	if string(images.GotContentIn) != string(raw) {
		t.Fatal("el procesador debe recibir el original")
	}
	if photo.ContentType != domain.PhotoTypeWebP {
		t.Fatalf("se persiste la variante webp, dio %q", photo.ContentType)
	}
	if photo.Width != 1280 || photo.Height != 960 || photo.Bytes != len(images.Out) {
		t.Fatalf("dimensiones/bytes de la variante: %+v", photo)
	}
	if got := store.Files[photo.Path]; string(got) != string(images.Out) {
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
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "", "")

	// Tipo declarado fuera del catálogo: 400 antes de procesar.
	for _, ct := range []string{"application/pdf", "image/gif", "text/plain", ""} {
		if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), ct); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q debe dar ErrValidation, dio %v", ct, err)
		}
	}
	// Bytes que no son una imagen (el procesador lo detecta): 400.
	images.Err = domain.ErrValidation
	if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("no soy imagen"), "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bytes inválidos deben dar ErrValidation, dio %v", err)
	}
	images.Err = nil
	// Vacío y más de 8 MB: 400.
	if _, err := svc.AddPhoto(recep, "s1", c.ID, nil, "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("archivo vacío debe dar ErrValidation, dio %v", err)
	}
	big := make([]byte, domain.MaxPhotoBytes+1)
	if _, err := svc.AddPhoto(recep, "s1", c.ID, big, "image/png"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("más de 8 MB debe dar ErrValidation, dio %v", err)
	}
	if len(clients.photos) != 0 || store.Puts != 0 {
		t.Fatalf("nada inválido debe persistirse: photos=%d puts=%d", len(clients.photos), store.Puts)
	}
}

func TestClientService_addPhoto_scoped_y_permisos(t *testing.T) {
	svc, clients, store, _, mems := clientEnv()
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajenoOwner := testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	c, _ := svc.Create(recep, "s1", "Lía", "", "", "")

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
	if len(clients.photos) != 0 || store.Puts != 0 {
		t.Fatalf("nada debe persistirse: photos=%d puts=%d", len(clients.photos), store.Puts)
	}
}

// Si la DB falla después de escribir el archivo, el archivo no puede quedar
// huérfano en el storage: se borra y el error sube.
func TestClientService_addPhoto_rollback_del_archivo(t *testing.T) {
	svc, clients, store, _, mems := clientEnv()
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "", "")
	clients.FailSavePhoto = true
	if _, err := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), "image/png"); err == nil {
		t.Fatal("esperaba error de persistencia")
	}
	if store.Puts != 1 || store.Dels != 1 {
		t.Fatalf("el archivo escrito debe borrarse: puts=%d dels=%d", store.Puts, store.Dels)
	}
	if len(store.Files) != 0 {
		t.Fatalf("no debe quedar basura: %+v", store.Files)
	}
}

func TestClientService_listPhotos_y_delete(t *testing.T) {
	svc, _, store, _, mems := clientEnv()
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	profe := testutil.Miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	ajeno := testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleOwner)
	c, _ := svc.Create(recep, "s1", "Lía", "", "", "")
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
	if _, ok := store.Files[p.Path]; ok {
		t.Fatal("el archivo debe borrarse del storage")
	}
	if list, _ := svc.ListPhotos(recep, "s1", c.ID); len(list) != 0 {
		t.Fatalf("la foto no debe seguir listada: %+v", list)
	}
}

func TestClientService_photo_para_streaming(t *testing.T) {
	svc, _, store, images, mems := clientEnv()
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)
	c, _ := svc.Create(recep, "s1", "Lía", "", "", "")
	p, _ := svc.AddPhoto(recep, "s1", c.ID, []byte("x"), "image/png")

	got, data, err := svc.Photo(p.ID)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got.ID != p.ID || got.ContentType != domain.PhotoTypeWebP {
		t.Fatalf("metadata: %+v", got)
	}
	if string(data) != string(images.Out) {
		t.Fatalf("bytes servidos distintos a los guardados (%q vs %q)", data, store.Files[p.Path])
	}
	if _, _, err := svc.Photo("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foto inexistente debe dar NotFound, dio %v", err)
	}
}

// E-03: el alta de ficha acepta un email opcional y lo persiste (hoy se
// descartaba en silencio y la ficha quedaba "Sin correo" en la pantalla).
func TestClientService_create_con_email(t *testing.T) {
	svc, clients, _, _, mems := clientEnv()
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)

	c, err := svc.Create(recep, "s1", "Lía", "11 5555-1234", "  Lia@Mail.COM ", "")
	if err != nil {
		t.Fatalf("crear con email: %v", err)
	}
	if c.Email != "lia@mail.com" {
		t.Fatalf("el email se normaliza (minúsculas, sin espacios), dio %q", c.Email)
	}
	if len(clients.clients) != 1 || clients.clients[0].Email != "lia@mail.com" {
		t.Fatalf("el email tiene que persistirse: %+v", clients.clients)
	}

	// Email inválido o demasiado largo: 400 y no se guarda nada.
	for _, bad := range []string{"sin-arroba", strings.Repeat("a", domain.MaxTextEmail) + "@mail.com"} {
		if _, err := svc.Create(recep, "s1", "Ana", "", bad, ""); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("email %q debe dar ErrValidation, dio %v", bad, err)
		}
	}
	if len(clients.clients) != 1 {
		t.Fatalf("un alta inválida no puede persistir: %+v", clients.clients)
	}
}
