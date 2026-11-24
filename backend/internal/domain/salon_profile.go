// Perfil público del salón y su galería (F16, D1 y D3 del contrato de la fase 6).
//
// El perfil es lo que el dueño edita en «Mi salón» y lo que la web pública
// muestra: UNA fuente de verdad. Las columnas son opcionales (NULL = «sin dato»):
// en el dominio un texto vacío es «sin dato» y el adaptador lo guarda como NULL.
//
// El dominio no sabe de HTTP ni de SQL: valida y arma las entidades con los
// textos EXACTOS que ve la persona.
package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxSalonDescription   = 500
	MaxSalonAddress       = 200
	MaxSalonInstagram     = 60
	MaxServiceDescription = 300
	MaxPhotoCaption       = 120
	// MaxSalonPhotos es el tope de la galería pública de un salón (D3).
	MaxSalonPhotos = 12
)

// Textos exactos del contrato F16.
const (
	MsgSalonProfileMissing = "Faltan datos del salón: nombre, descripción, dirección, teléfono, email e instagram"
	MsgSalonEmailInvalid   = "Email inválido"
	MsgGalleryFull         = "La galería ya tiene 12 fotos"

	// Los siguientes textos no están literales en el contrato: siguen el estilo
	// de los de F8.
	MsgSalonNameRequired    = "El nombre del salón es obligatorio"
	MsgSalonNameTooLong     = "El nombre del salón no puede superar los 120 caracteres"
	MsgSalonDescriptionLong = "La descripción no puede superar los 500 caracteres"
	MsgSalonAddressLong     = "La dirección no puede superar los 200 caracteres"
	MsgSalonPhoneLong       = "El teléfono no puede superar los 40 caracteres"
	MsgSalonInstagramLong   = "El instagram no puede superar los 60 caracteres"
	MsgSalonInstagramBad    = "Instagram inválido: escribí solo el usuario, sin @ ni enlaces"
	MsgSalonInvalidChars    = "El texto no puede tener saltos de línea ni caracteres no válidos"
	MsgServiceDescription   = "La descripción del servicio no puede superar los 300 caracteres"
	MsgPhotoCaptionLong     = "La leyenda no puede superar los 120 caracteres"
	MsgPhotoCaptionMissing  = "Falta la leyenda de la foto (caption)"
	MsgPhotoType            = "Tipo de archivo no permitido (solo jpg, png o webp)"
	MsgPhotoSize            = "El archivo debe pesar entre 1 byte y 8 MB"
	MsgPhotoNotFound        = "Foto no encontrada"
)

// SalonProfile es el perfil editable del salón. Slug no se edita.
type SalonProfile struct {
	SalonID     string
	Slug        string
	Name        string
	Description string
	Address     string
	Phone       string
	Email       string
	Instagram   string
}

// SalonProfileInput es lo que manda el PUT: las cinco claves opcionales llegan
// como puntero (nil = `null`) y el nombre es obligatorio.
type SalonProfileInput struct {
	Name        string
	Description *string
	Address     *string
	Phone       *string
	Email       *string
	Instagram   *string
}

// Updated devuelve el perfil con los datos nuevos, revalidando todo. Mantiene
// SalonID y Slug: el slug es la dirección pública y no se edita.
func (p SalonProfile) Updated(in SalonProfileInput) (SalonProfile, error) {
	name, err := cleanSalonText(in.Name, MaxTextName, MsgSalonNameTooLong)
	if err != nil {
		return SalonProfile{}, err
	}
	if name == "" {
		return SalonProfile{}, NewMessage(ErrValidation, MsgSalonNameRequired)
	}
	description, err := cleanSalonText(deref(in.Description), MaxSalonDescription, MsgSalonDescriptionLong)
	if err != nil {
		return SalonProfile{}, err
	}
	address, err := cleanSalonText(deref(in.Address), MaxSalonAddress, MsgSalonAddressLong)
	if err != nil {
		return SalonProfile{}, err
	}
	phone, err := cleanSalonText(deref(in.Phone), MaxTextPhone, MsgSalonPhoneLong)
	if err != nil {
		return SalonProfile{}, err
	}
	email, err := cleanSalonEmail(deref(in.Email))
	if err != nil {
		return SalonProfile{}, err
	}
	instagram, err := cleanSalonText(deref(in.Instagram), MaxSalonInstagram, MsgSalonInstagramLong)
	if err != nil {
		return SalonProfile{}, err
	}
	if !validInstagramUser(instagram) {
		return SalonProfile{}, NewMessage(ErrValidation, MsgSalonInstagramBad)
	}
	p.Name = name
	p.Description = description
	p.Address = address
	p.Phone = phone
	p.Email = email
	p.Instagram = instagram
	return p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// cleanSalonText es la validación común de un texto de UNA línea: rechaza un
// NUL, un salto de línea o un UTF-8 roto ANTES de recortar («Calle 1\n» no se
// «arregla» en silencio: Postgres no guarda un NUL y sería un 500), recorta y
// mide en runes. Devuelve "" para «sin dato».
func cleanSalonText(s string, max int, tooLong string) (string, error) {
	if hasNUL(s) || hasLineBreak(s) || !utf8.ValidString(s) {
		return "", NewMessage(ErrValidation, MsgSalonInvalidChars)
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", NewMessage(ErrValidation, tooLong)
	}
	return s, nil
}

// cleanSalonEmail aplica la regla de las cuentas (ValidateEmail) al email de
// contacto; cualquier fallo de forma o de largo es el mismo «Email inválido».
func cleanSalonEmail(raw string) (string, error) {
	if hasNUL(raw) || hasLineBreak(raw) || !utf8.ValidString(raw) {
		return "", NewMessage(ErrValidation, MsgSalonInvalidChars)
	}
	email := NormalizeEmail(raw)
	if email == "" {
		return "", nil
	}
	if err := ValidateEmail(email); err != nil {
		return "", NewMessage(ErrValidation, MsgSalonEmailInvalid)
	}
	return email, nil
}

// validInstagramUser: solo el usuario (letras, números, punto y guion bajo),
// sin @ ni enlaces. Vacío es «sin dato».
func validInstagramUser(user string) bool {
	for _, r := range user {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// SalonPhoto es una foto de la galería pública del salón. StorageKey es lo que
// devolvió el storage al guardarla.
type SalonPhoto struct {
	ID          string
	SalonID     string
	StorageKey  string
	ContentType string
	Caption     string
	CreatedAt   time.Time
}

// NewSalonPhoto valida el tipo de una foto ya procesada.
func NewSalonPhoto(salonID, contentType string) (SalonPhoto, error) {
	if salonID == "" {
		return SalonPhoto{}, NewMessage(ErrValidation, "El salón es obligatorio")
	}
	if !PhotoTypeAllowed(contentType) {
		return SalonPhoto{}, NewMessage(ErrValidation, MsgPhotoType)
	}
	return SalonPhoto{ID: NewID(), SalonID: salonID, ContentType: contentType, CreatedAt: time.Now().UTC()}, nil
}

// StoredAt devuelve la foto apuntando a la clave que devolvió el storage.
//
// Se revalida: una clave vacía o con «..» dejaría el archivo fuera del root del
// adaptador (path traversal), y eso es un error de programación.
func (p SalonPhoto) StoredAt(key string) (SalonPhoto, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return SalonPhoto{}, NewMessage(ErrValidation, "Ruta de archivo inválida")
	}
	p.StorageKey = key
	return p, nil
}

// WithCaption valida y deja la leyenda (nil o vacía = sin leyenda).
func (p SalonPhoto) WithCaption(caption *string) (SalonPhoto, error) {
	clean, err := cleanSalonText(deref(caption), MaxPhotoCaption, MsgPhotoCaptionLong)
	if err != nil {
		return SalonPhoto{}, err
	}
	p.Caption = clean
	return p, nil
}

// ValidateServiceDescription valida la descripción opcional de un servicio.
func ValidateServiceDescription(s string) (string, error) {
	return cleanSalonText(s, MaxServiceDescription, MsgServiceDescription)
}
