// GalleryService: la galería pública del salón (F16, D3). Reutiliza el pipeline
// de las fotos de clientas (tipo, tope de 8 MB, conversión a WebP, puerto de
// storage) pero en su propia tabla: una foto de clienta nunca sale por acá.
package app

import (
	"errors"
	"strings"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

type GalleryService struct {
	Gallery     ports.SalonGalleryRepository
	Memberships ports.MembershipRepository
	Salons      ports.SalonRepository
	Photos      ports.PhotoStorage
	Images      ports.ImageProcessor
}

// GalleryPhoto es una foto con su URL pública (la de D3, sin firma).
type GalleryPhoto struct {
	domain.SalonPhoto
	URL string
}

// PublicGalleryPath es la ruta pública de la imagen: relativa a propósito (el
// frontend la usa tal cual en <img src>). El slug es [a-z0-9-]: no hace falta
// escapar nada.
func PublicGalleryPath(slug, photoID string) string {
	return "/api/v1/public/salons/" + slug + "/gallery/" + photoID
}

func photoNotFound() error {
	return domain.NewMessage(domain.ErrNotFound, domain.MsgPhotoNotFound)
}

// RequireManager es el permiso SOLO (sin tocar datos): el handler lo llama ANTES
// de leer el cuerpo o el archivo.
func (s GalleryService) RequireManager(actor domain.Membership, salonID string) error {
	_, err := s.manager(actor, salonID)
	return err
}

// Add normaliza la subida (WebP, lado mayor acotado), la guarda en el storage y
// registra la foto. Solo owner/admin. Valida tipo y tamaño como la foto de una
// clienta; el tope de 12 se chequea ANTES de convertir (no se procesa una imagen
// que no va a entrar) y se repite dentro del lock del adaptador (Add) para que
// dos subidas simultáneas no pasen de 12: si esa segunda barrera falla, el
// archivo ya guardado se borra.
func (s GalleryService) Add(actor domain.Membership, salonID string, data []byte, contentType string) (GalleryPhoto, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return GalleryPhoto{}, err
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !domain.PhotoTypeAllowed(contentType) {
		return GalleryPhoto{}, domain.NewMessage(domain.ErrValidation, domain.MsgPhotoType)
	}
	if len(data) == 0 || len(data) > domain.MaxPhotoBytes {
		return GalleryPhoto{}, domain.NewMessage(domain.ErrValidation, domain.MsgPhotoSize)
	}
	salon, err := s.Salons.FindByID(salonID)
	if err != nil {
		return GalleryPhoto{}, err
	}
	count, err := s.Gallery.Count(salonID)
	if err != nil {
		return GalleryPhoto{}, err
	}
	if count >= domain.MaxSalonPhotos {
		return GalleryPhoto{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgGalleryFull)
	}
	webp, _, _, err := s.Images.ToWebP(data)
	if err != nil {
		return GalleryPhoto{}, err
	}
	photo, err := domain.NewSalonPhoto(salonID, domain.PhotoTypeWebP)
	if err != nil {
		return GalleryPhoto{}, err
	}
	// La clave la arma el caso de uso (salón/gallery/foto) y la ruta final la
	// decide el adaptador: el dominio no sabe si es un filesystem o un bucket.
	stored, err := s.Photos.Put(salonID+"/gallery/"+photo.ID+".webp", webp, photo.ContentType)
	if err != nil {
		return GalleryPhoto{}, err
	}
	photo, err = photo.StoredAt(stored)
	if err != nil {
		_ = s.Photos.Delete(stored)
		return GalleryPhoto{}, err
	}
	if err := s.Gallery.Add(photo, domain.MaxSalonPhotos); err != nil {
		// La fila no quedó: el archivo tampoco (si no, basura sin dueño).
		_ = s.Photos.Delete(photo.StorageKey)
		return GalleryPhoto{}, err
	}
	return withURL(salon.Slug, photo), nil
}

func withURL(slug string, p domain.SalonPhoto) GalleryPhoto {
	return GalleryPhoto{SalonPhoto: p, URL: PublicGalleryPath(slug, p.ID)}
}

// List trae la galería en orden de subida. Solo owner/admin (es la pantalla de
// gestión; la web pública la recibe en el payload de la vidriera).
func (s GalleryService) List(actor domain.Membership, salonID string) ([]GalleryPhoto, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return nil, err
	}
	salon, err := s.Salons.FindByID(salonID)
	if err != nil {
		return nil, err
	}
	photos, err := s.Gallery.List(salonID)
	if err != nil {
		return nil, err
	}
	out := make([]GalleryPhoto, 0, len(photos))
	for _, p := range photos {
		out = append(out, withURL(salon.Slug, p))
	}
	return out, nil
}

// SetCaption reemplaza la leyenda (nil o vacía = sin leyenda). Una foto de otro
// salón es 404 y valida ANTES de escribir.
func (s GalleryService) SetCaption(actor domain.Membership, salonID, photoID string, caption *string) (GalleryPhoto, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return GalleryPhoto{}, err
	}
	photo, err := s.Gallery.Find(salonID, photoID)
	if err != nil {
		return GalleryPhoto{}, photoErr(err)
	}
	photo, err = photo.WithCaption(caption)
	if err != nil {
		return GalleryPhoto{}, err
	}
	if err := s.Gallery.SetCaption(salonID, photoID, photo.Caption); err != nil {
		return GalleryPhoto{}, photoErr(err)
	}
	salon, err := s.Salons.FindByID(salonID)
	if err != nil {
		return GalleryPhoto{}, err
	}
	return withURL(salon.Slug, photo), nil
}

// Delete borra la fila y después el archivo. Primero la DB (fuente de verdad): si
// el borrado del archivo falla queda basura en disco, nunca una foto listada que
// el navegador no puede abrir.
func (s GalleryService) Delete(actor domain.Membership, salonID, photoID string) error {
	if _, err := s.manager(actor, salonID); err != nil {
		return err
	}
	photo, err := s.Gallery.Find(salonID, photoID)
	if err != nil {
		return photoErr(err)
	}
	if err := s.Gallery.Delete(salonID, photoID); err != nil {
		return photoErr(err)
	}
	_ = s.Photos.Delete(photo.StorageKey)
	return nil
}

// photoErr traduce la falta de la foto al texto del contrato: una foto de otro
// salón es «Foto no encontrada» (404), nunca un 403 que confirme que existe.
func photoErr(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return photoNotFound()
	}
	return err
}

// PublicPhoto resuelve la imagen por el slug del salón y el id de la foto: la
// ruta pública, sin sesión. Un id inexistente, de otro salón o cuyo archivo se
// perdió es el MISMO 404 (no se distingue). Solo mira salon_photos: una foto de
// clienta no se sirve por acá.
func (s GalleryService) PublicPhoto(slug, photoID string) (domain.SalonPhoto, []byte, error) {
	photo, err := s.Gallery.FindPublic(slug, photoID)
	if err != nil {
		return domain.SalonPhoto{}, nil, photoErr(err)
	}
	data, err := s.Photos.Get(photo.StorageKey)
	if err != nil {
		return domain.SalonPhoto{}, nil, photoErr(err)
	}
	return photo, data, nil
}

// manager exige membership ACTIVA real y permiso de configuración.
func (s GalleryService) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil || !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
