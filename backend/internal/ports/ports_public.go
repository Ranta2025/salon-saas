package ports

import "salon-saas/internal/domain"

// ===== F16: web pública fiel al panel (inicio) =====

// SalonProfileRepository: el perfil público del salón (migración 032, D1). Toda
// consulta va scopeada por el id del salón; el slug no se escribe nunca.
type SalonProfileRepository interface {
	// Profile lee nombre, slug y los cinco datos opcionales. ErrNotFound si el
	// salón no existe.
	Profile(salonID string) (domain.SalonProfile, error)
	// SaveProfile reemplaza nombre y los cinco datos en UN UPDATE (un texto vacío
	// es NULL). No toca el slug. ErrNotFound si el salón no existe.
	SaveProfile(p domain.SalonProfile) error
}

// SalonGalleryRepository: la galería pública del salón (tabla salon_photos). Es
// una tabla APARTE de client_photos: ninguna de estas consultas puede devolver
// una foto de clienta. Toda consulta va scopeada por salón.
type SalonGalleryRepository interface {
	// Add inserta la foto bajo un lock por salón y cuenta DENTRO del lock: con
	// `max` fotos ya guardadas no escribe nada y devuelve ErrAlreadyExists con el
	// texto de domain.MsgGalleryFull. Dos subidas simultáneas con 11 fotos dejan 12.
	Add(p domain.SalonPhoto, max int) error
	Count(salonID string) (int, error)
	// List trae la galería en orden de subida (la más vieja primero).
	List(salonID string) ([]domain.SalonPhoto, error)
	// Find resuelve una foto del salón (ErrNotFound si es de otro salón).
	Find(salonID, id string) (domain.SalonPhoto, error)
	// FindPublic resuelve una foto por el SLUG del salón y el id: la ruta pública
	// sin sesión. Un id de otro salón o inexistente es ErrNotFound.
	FindPublic(slug, id string) (domain.SalonPhoto, error)
	// SetCaption reemplaza la leyenda ("" = sin leyenda). ErrNotFound si la foto
	// no es del salón.
	SetCaption(salonID, id, caption string) error
	// Delete borra la fila (el caso de uso borra después el archivo). ErrNotFound
	// si la foto no es del salón.
	Delete(salonID, id string) error
}

// ===== F16: web pública fiel al panel (fin) =====
