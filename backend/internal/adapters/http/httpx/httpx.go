// Package httpx son los ayudantes HTTP compartidos por los handlers de todas
// las funcionalidades: el mapeo de errores de dominio a status, el cuerpo
// inválido, la distinción entre una clave JSON ausente y un `null` explícito y
// los límites de cuerpo. Vive fuera de `adapters/http` porque cada
// `internal/<feature>/httpapi/` lo usa y ese paquete importa a los handlers (no
// al revés).
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"salon-saas/internal/domain"
	ports "salon-saas/internal/ports"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// StatusOf traduce un error de dominio al status HTTP del contrato.
func StatusOf(err error) int {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// InternalErrorMessage es lo ÚNICO que ve el cliente ante un 500 (B-14): ni
// SQL, ni nombres de tabla, ni mensajes de pgx. El detalle va al log del
// servidor, que es donde sirve.
const InternalErrorMessage = "error interno"

// RespondErr es el único camino de error hacia el cliente. Un 500 se registra
// completo en el log del servidor y se responde genérico; los errores de
// dominio (400/401/403/404/409) llevan su mensaje en español, que es parte del
// contrato.
func RespondErr(c *gin.Context, err error) {
	if StatusOf(err) == http.StatusInternalServerError {
		RespondErrLogf(c, err, log.Printf)
		return
	}
	c.JSON(StatusOf(err), gin.H{"error": err.Error()})
}

// RespondErrLogf permite inyectar el logger (los tests verifican que el detalle
// queda en el log y no en el body).
func RespondErrLogf(c *gin.Context, err error, logf func(string, ...any)) {
	if StatusOf(err) == http.StatusInternalServerError {
		logf("500 %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": InternalErrorMessage})
		return
	}
	c.JSON(StatusOf(err), gin.H{"error": err.Error()})
}

// LoadActor resuelve la membresía del usuario del token en el salón de la ruta.
// Sin membresía no hay acceso y el 403 no distingue por qué.
func LoadActor(memberships ports.MembershipRepository, c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	userID, _ := uid.(string)
	m, err := memberships.Find(userID, salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// PresentText distingue una clave AUSENTE de una clave en `null` (y de un
// texto): los PUT que exigen la clave presente usan Set, y `null` explícito
// significa «sin dato». Un valor que no es string ni null es body inválido.
type PresentText struct {
	Set   bool
	Value *string
}

// UnmarshalJSON marca Set sea cual sea el valor.
func (p *PresentText) UnmarshalJSON(b []byte) error {
	p.Set = true
	if strings.TrimSpace(string(b)) == "null" {
		p.Value = nil
		return nil
	}
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Value = &v
	return nil
}

// String es el texto o "" para null/ausente.
func (p PresentText) String() string {
	if p.Value == nil {
		return ""
	}
	return *p.Value
}

// OptionalID devuelve nil para un id vacío: así un `professional_id` o un
// `service_id` opcional sale como null y no como cadena vacía.
func OptionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// BadBody es la respuesta única de JSON inválido o demasiado grande.
func BadBody(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
}

// PresentInt distingue una clave AUSENTE de una clave en `null` (y de un
// número): el PUT del catálogo exige `validity_days` presente, y `null`
// explícito significa «sin vencimiento». Un campo ausente no llama a
// UnmarshalJSON, así que Set queda en false.
type PresentInt struct {
	Set   bool
	Value *int
}

// UnmarshalJSON marca Set sea cual sea el valor: así el handler sabe si la
// clave vino en el cuerpo.
func (p *PresentInt) UnmarshalJSON(b []byte) error {
	p.Set = true
	if strings.TrimSpace(string(b)) == "null" {
		p.Value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Value = &v
	return nil
}

// PublicBookingJSON es la reserva tal como la ve el público: el turno y su
// estado, sin datos de la clienta.
func PublicBookingJSON(b domain.Booking) gin.H {
	return gin.H{
		"id":              b.ID,
		"professional_id": b.ProfessionalID,
		"starts_at":       b.StartsAt.Format(time.RFC3339),
		"ends_at":         b.EndsAt.Format(time.RFC3339),
		"status":          string(b.Status),
	}
}

// BookingJSON es la reserva completa (calendario del salón y PATCH).
// `service_id` (F2c) es el servicio reservado y puede ser null: el turno que se
// agenda a mano desde el calendario no exige servicio. `covered_from`
// (S-F2d3-6) es quién TENÍA el turno antes de que lo cubriera otra persona, o
// null si nadie lo cubre: solo viaja en el JSON del personal ("Mi agenda" lo usa
// para avisar "lo cubre otra profesional"), nunca en el público ni en el payload
// del websocket.
func BookingJSON(b domain.Booking) gin.H {
	out := PublicBookingJSON(b)
	out["client_name"] = b.ClientName
	out["service_id"] = OptionalID(b.ServiceID)
	out["covered_from"] = OptionalID(b.CoveredFrom)
	// resource_unit_id (F2e) es la unidad de equipo que ocupa el turno, o null si
	// no ocupa ninguna (turno del calendario o servicio sin equipo).
	out["resource_unit_id"] = OptionalID(b.ResourceUnitID)
	return out
}

var ErrUpload = errors.New("se esperaba multipart/form-data con el archivo en el campo \"photo\" (o \"file\") o un body de imagen (jpg, png o webp)")

// uploadFields son los nombres de campo que se aceptan en el multipart. `photo`
// es el que manda el frontend (FormData.append('photo', file)) y `file` el
// genérico de curl/Postman: aceptar los dos evita un contrato frágil por un
// nombre.
var uploadFields = []string{"photo", "file"}

// ReadUpload saca los bytes y el content type declarado de lo que subió el
// cliente. El límite de 8 MB se aplica al leer (no se carga un archivo enorme
// en memoria para después rechazarlo).
func ReadUpload(c *gin.Context) ([]byte, string, error) {
	// Tope del BODY antes de parsear nada (R4-001): c.Request.FormFile parsea y
	// spoolea TODO el multipart en memoria/disco sin límite propio. Sin esta
	// línea, un body de 1 GB con un archivo chico adentro se acepta y el
	// servidor lo procesa entero. El +1 MB es el overhead del propio multipart
	// (boundaries + headers); el archivo sigue acotado por MaxPhotoBytes.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, domain.MaxPhotoBytes+(1<<20))
	// R3-002: el Content-Type se compara sin distinguir mayúsculas ("IMAGE/PNG"
	// es un image/png válido y algunos clientes lo mandan así).
	ct := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		file, header, err := multipartFile(c)
		if err != nil {
			return nil, "", ErrUpload
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, domain.MaxPhotoBytes+1))
		if err != nil {
			return nil, "", ErrUpload
		}
		declared := header.Header.Get("Content-Type")
		if declared == "" {
			declared = http.DetectContentType(data)
		}
		return data, declared, nil
	case strings.HasPrefix(ct, "image/"):
		data, err := io.ReadAll(io.LimitReader(c.Request.Body, domain.MaxPhotoBytes+1))
		if err != nil {
			return nil, "", ErrUpload
		}
		return data, ct, nil
	default:
		return nil, "", ErrUpload
	}
}

// multipartFile busca el archivo en los nombres de campo aceptados.
func multipartFile(c *gin.Context) (multipart.File, *multipart.FileHeader, error) {
	var lastErr error
	for _, field := range uploadFields {
		file, header, err := c.Request.FormFile(field)
		if err == nil {
			return file, header, nil
		}
		lastErr = err
	}
	return nil, nil, lastErr
}
