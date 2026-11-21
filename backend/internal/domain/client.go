package domain

import (
	"fmt"
	"strings"
	"time"
)

// Client es la ficha de un cliente del salón: quién viene, cómo se le avisa y
// qué conviene recordar (alergias, color usado, preferencias). Es del salón,
// no del usuario: el cliente puede no tener cuenta en el sistema.
type Client struct {
	ID        string
	SalonID   string
	Name      string
	Phone     string
	Email     string
	Notes     string
	CreatedAt time.Time
}

// NewClient valida y crea una ficha de cliente.
func NewClient(salonID, name, phone, email, notes string) (Client, error) {
	name = strings.TrimSpace(name)
	if salonID == "" {
		return Client{}, fmt.Errorf("%w: el salón es obligatorio", ErrValidation)
	}
	if name == "" {
		return Client{}, fmt.Errorf("%w: el nombre del cliente es obligatorio", ErrValidation)
	}
	if err := validateText("el nombre del cliente", name, MaxTextName); err != nil {
		return Client{}, err
	}
	// El teléfono se guarda COMO SE TIPEÓ (es lo que se muestra en la ficha);
	// para buscar la ficha se usa NormalizePhone. Guardarlo normalizado le
	// sacaría el formato que escribió la persona.
	phone = strings.TrimSpace(phone)
	if err := validateText("el teléfono", phone, MaxTextPhone); err != nil {
		return Client{}, err
	}
	email = NormalizeEmail(email)
	if email != "" {
		// Misma regla que el email de una cuenta (NewUser): forma mínima y tope
		// de largo. Un email inventado no rompe nada, pero tampoco entra.
		if !strings.Contains(email, "@") || len(email) < 5 || len(email) > MaxTextEmail {
			// Texto para la pantalla: el prefijo interno del error de dominio
			// ("validación:") no le dice nada a quien llena el formulario.
			return Client{}, NewMessage(ErrValidation, "Email inválido")
		}
	}
	notes = strings.TrimSpace(notes)
	if err := validateText("las notas", notes, MaxTextNotes); err != nil {
		return Client{}, err
	}
	return Client{
		ID:        NewID(),
		SalonID:   salonID,
		Name:      name,
		Phone:     phone,
		Email:     email,
		Notes:     notes,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// NormalizePhone deja solo los dígitos del teléfono: es la forma de COMPARAR
// dos teléfonos ("11 5555-1234" y "1155551234" son la misma persona). No se
// persiste: la ficha guarda el teléfono como se escribió.
func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Límites de los adjuntos: son regla de negocio (no un detalle del handler),
// porque valen para cualquier camino de escritura que exista mañana.
const (
	MaxPhotoBytes = 8 << 20 // 8 MB de subida
	MaxPhotoSide  = 1280    // lado mayor de la variante que se guarda
	// MaxPhotoPixels acota la BOMBA DE DESCOMPRESIÓN: un PNG/JPEG de pocos KB
	// puede declarar dimensiones gigantes (100000x100000) y obligar al decoder
	// a reservar gigabytes. 25 MP sobran para fotos de salón de verdad.
	MaxPhotoPixels = 25_000_000
	PhotoTypeWebP  = "image/webp"
	PhotoTypeJPEG  = "image/jpeg"
	PhotoTypePNG   = "image/png"
)

// PhotoTypeAllowed dice si el tipo de archivo se acepta como adjunto.
// Solo fotos de verdad: nada de SVG (puede traer scripts) ni GIF animados.
func PhotoTypeAllowed(contentType string) bool {
	switch contentType {
	case PhotoTypeJPEG, PhotoTypePNG, PhotoTypeWebP:
		return true
	default:
		return false
	}
}

// ClientPhoto es una foto guardada de la ficha de un cliente. Path es la clave
// en el storage (adapter de filesystem hoy, S3 mañana): la DB nunca guarda el
// contenido, solo dónde está y cuánto mide.
type ClientPhoto struct {
	ID          string
	SalonID     string
	ClientID    string
	Path        string
	ContentType string
	Width       int
	Height      int
	Bytes       int
	CreatedAt   time.Time
}

// NewClientPhoto valida los datos de una foto ya procesada. La ruta donde el
// storage la guardó se agrega después con StoredAt: quién sabe dónde vive el
// archivo es el adaptador, no el dominio.
func NewClientPhoto(salonID, clientID, contentType string, width, height, bytes int) (ClientPhoto, error) {
	if salonID == "" || clientID == "" {
		return ClientPhoto{}, fmt.Errorf("%w: salón y cliente son obligatorios", ErrValidation)
	}
	if !PhotoTypeAllowed(contentType) {
		return ClientPhoto{}, fmt.Errorf("%w: tipo de archivo no permitido %q (solo jpg, png o webp)", ErrValidation, contentType)
	}
	if width <= 0 || height <= 0 {
		return ClientPhoto{}, fmt.Errorf("%w: dimensiones inválidas %dx%d", ErrValidation, width, height)
	}
	if bytes <= 0 || bytes > MaxPhotoBytes {
		return ClientPhoto{}, fmt.Errorf("%w: el archivo debe pesar entre 1 byte y 8 MB", ErrValidation)
	}
	return ClientPhoto{
		ID:          NewID(),
		SalonID:     salonID,
		ClientID:    clientID,
		ContentType: contentType,
		Width:       width,
		Height:      height,
		Bytes:       bytes,
		CreatedAt:   time.Now().UTC(),
	}, nil
}

// StoredAt devuelve la foto apuntando a la ruta que devolvió el storage.
// Se revalida: una ruta vacía o con ".." dejaría el archivo fuera del root del
// adapter (path traversal), y eso es un error de programación, no del usuario.
func (p ClientPhoto) StoredAt(path string) (ClientPhoto, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ClientPhoto{}, fmt.Errorf("%w: falta la ruta del archivo", ErrValidation)
	}
	if strings.Contains(path, "..") || strings.HasPrefix(path, "/") {
		return ClientPhoto{}, fmt.Errorf("%w: ruta de archivo inválida %q", ErrValidation, path)
	}
	p.Path = path
	return p, nil
}
