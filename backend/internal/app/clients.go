// ClientService: fichas de clientes y sus fotos. Quién administra clientes
// (owner/admin/recepción) crea fichas y sube fotos; cualquier miembro del salón
// las lee, porque en el mostrador todos atienden al cliente.
package app

import (
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type ClientService struct {
	Clients     ports.ClientRepository
	Memberships ports.MembershipRepository
	Photos      ports.PhotoStorage
	Images      ports.ImageProcessor
}

// puedeGestionarClientes: el mostrador y quien administra. El profesional mira
// la ficha (le sirve para atender) pero no la edita: así una foto o una nota
// interna no se toca desde cualquier cuenta. Regla en un solo lugar.
func puedeGestionarClientes(m domain.Membership) bool {
	return m.CanManage() || m.Role == domain.RoleReception
}

// Create abre la ficha de un cliente del salón. El email es opcional y se
// guarda normalizado (minúsculas, sin espacios), igual que el del login.
func (s ClientService) Create(actor domain.Membership, salonID, name, phone, email, notes string) (domain.Client, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.Client{}, err
	}
	c, err := domain.NewClient(salonID, name, phone, email, notes)
	if err != nil {
		return domain.Client{}, err
	}
	if err := s.Clients.Save(c); err != nil {
		return domain.Client{}, err
	}
	return c, nil
}

// List trae las fichas del salón. Cualquier miembro puede leer.
func (s ClientService) List(actor domain.Membership, salonID string) ([]domain.Client, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, err
	}
	return s.Clients.List(salonID)
}

// AddPhoto normaliza la subida (WebP, lado mayor acotado), la guarda en el
// storage y registra la foto. Valida acá las reglas del archivo: tipo, tamaño
// y que el cliente exista EN ESTE salón (un id de otro tenant no existe).
func (s ClientService) AddPhoto(actor domain.Membership, salonID, clientID string, data []byte, contentType string) (domain.ClientPhoto, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return domain.ClientPhoto{}, err
	}
	if _, err := s.Clients.FindByID(salonID, clientID); err != nil {
		return domain.ClientPhoto{}, err
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !domain.PhotoTypeAllowed(contentType) {
		return domain.ClientPhoto{}, domain.ErrValidation
	}
	if len(data) == 0 || len(data) > domain.MaxPhotoBytes {
		return domain.ClientPhoto{}, domain.ErrValidation
	}
	webp, width, height, err := s.Images.ToWebP(data)
	if err != nil {
		return domain.ClientPhoto{}, err
	}
	photo, err := domain.NewClientPhoto(salonID, clientID, domain.PhotoTypeWebP, width, height, len(webp))
	if err != nil {
		return domain.ClientPhoto{}, err
	}
	// La clave la arma el caso de uso (salón/cliente/foto) y la ruta final la
	// decide el adapter: el dominio no sabe si es un filesystem o un bucket.
	stored, err := s.Photos.Put(salonID+"/"+clientID+"/"+photo.ID+".webp", webp, photo.ContentType)
	if err != nil {
		return domain.ClientPhoto{}, err
	}
	photo, err = photo.StoredAt(stored)
	if err != nil {
		_ = s.Photos.Delete(stored)
		return domain.ClientPhoto{}, err
	}
	if err := s.Clients.SavePhoto(photo); err != nil {
		// La fila no quedó: el archivo tampoco (si no, basura sin dueño).
		_ = s.Photos.Delete(photo.Path)
		return domain.ClientPhoto{}, err
	}
	return photo, nil
}

// ListPhotos trae la galería de una ficha. Cualquier miembro puede leer.
func (s ClientService) ListPhotos(actor domain.Membership, salonID, clientID string) ([]domain.ClientPhoto, error) {
	if _, err := s.member(actor, salonID); err != nil {
		return nil, err
	}
	if _, err := s.Clients.FindByID(salonID, clientID); err != nil {
		return nil, err
	}
	return s.Clients.ListPhotos(salonID, clientID)
}

// DeletePhoto borra la fila y después el archivo. Primero la DB (fuente de
// verdad): si el borrado del archivo falla queda basura en disco, nunca una
// foto listada que el navegador no puede abrir.
func (s ClientService) DeletePhoto(actor domain.Membership, salonID, clientID, photoID string) error {
	if _, err := s.manager(actor, salonID); err != nil {
		return err
	}
	photo, err := s.Clients.FindPhoto(salonID, clientID, photoID)
	if err != nil {
		return err
	}
	if err := s.Clients.DeletePhoto(salonID, clientID, photoID); err != nil {
		return err
	}
	_ = s.Photos.Delete(photo.Path)
	return nil
}

// Photo resuelve una foto y sus bytes para servirla por HTTP. El id es la
// capability que el navegador usa en <img src>: no hay membership en juego
// porque el navegador no puede mandar el header Authorization en una imagen.
func (s ClientService) Photo(photoID string) (domain.ClientPhoto, []byte, error) {
	photo, err := s.Clients.PhotoByID(photoID)
	if err != nil {
		return domain.ClientPhoto{}, nil, err
	}
	data, err := s.Photos.Get(photo.Path)
	if err != nil {
		return domain.ClientPhoto{}, nil, err
	}
	return photo, data, nil
}

// manager exige membership real y permiso de gestión de clientes.
func (s ClientService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !puedeGestionarClientes(m) {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// member valida que el actor sea miembro del salón en la DB.
func (s ClientService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
