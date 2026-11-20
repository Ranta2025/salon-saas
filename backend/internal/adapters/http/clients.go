package httpapi

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// ClientHandler: fichas de clientes y sus fotos. El handler solo parsea
// (JSON o multipart), llama al caso de uso y serializa; las reglas del archivo
// viven en app/domain.
type ClientHandler struct {
	Clients     app.ClientService
	Memberships ports.MembershipRepository
}

func (h ClientHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// clientJSON es la forma única de serializar una ficha.
func clientJSON(c domain.Client) gin.H {
	return gin.H{
		"id":         c.ID,
		"salon_id":   c.SalonID,
		"name":       c.Name,
		"phone":      c.Phone,
		"notes":      c.Notes,
		"created_at": c.CreatedAt.Format(time.RFC3339),
	}
}

// photoURL es la ruta pública de la imagen. Es relativa a propósito: el
// frontend la usa tal cual en <img src> y funciona en cualquier host.
func photoURL(p domain.ClientPhoto) string { return "/api/v1/photos/" + p.ID }

// photoJSON: {id, url, w, h, bytes} del contrato + el detalle de la ficha.
func photoJSON(p domain.ClientPhoto) gin.H {
	return gin.H{
		"id":           p.ID,
		"salon_id":     p.SalonID,
		"client_id":    p.ClientID,
		"url":          photoURL(p),
		"w":            p.Width,
		"h":            p.Height,
		"bytes":        p.Bytes,
		"content_type": p.ContentType,
		"created_at":   p.CreatedAt.Format(time.RFC3339),
	}
}

// Create: POST /api/v1/salons/:id/clients -> 201 ficha
func (h ClientHandler) Create(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Notes string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	client, err := h.Clients.Create(actor, salonID, in.Name, in.Phone, in.Notes)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, clientJSON(client))
}

// List: GET /api/v1/salons/:id/clients -> {clients:[...]}
func (h ClientHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Clients.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, cl := range list {
		out = append(out, clientJSON(cl))
	}
	c.JSON(http.StatusOK, gin.H{"clients": out})
}

// AddPhoto: POST /api/v1/salons/:id/clients/:cid/photos -> 201 {photo}
// Acepta multipart/form-data con el campo "file" (lo que manda el navegador) o
// el archivo crudo en el body con Content-Type image/*.
func (h ClientHandler) AddPhoto(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	data, contentType, err := readUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	photo, err := h.Clients.AddPhoto(actor, salonID, c.Param("cid"), data, contentType)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"photo": photoJSON(photo)})
}

// ListPhotos: GET /api/v1/salons/:id/clients/:cid/photos -> {photos:[...]}
func (h ClientHandler) ListPhotos(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Clients.ListPhotos(actor, salonID, c.Param("cid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		out = append(out, photoJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{"photos": out})
}

// DeletePhoto: DELETE /api/v1/salons/:id/clients/:cid/photos/:pid -> 204
func (h ClientHandler) DeletePhoto(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Clients.DeletePhoto(actor, salonID, c.Param("cid"), c.Param("pid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// StreamPhoto: GET /api/v1/photos/:pid -> la imagen.
// Sin Authorization a propósito: un <img src> no puede mandar headers, así que
// el id aleatorio de 128 bits ES la capability (no se puede adivinar ni
// enumerar, y el listado/borrado siguen scopeados por salón).
func (h ClientHandler) StreamPhoto(c *gin.Context) {
	photo, data, err := h.Clients.Photo(c.Param("pid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.Header("Content-Type", photo.ContentType)
	c.Header("Content-Length", strconv.Itoa(len(data)))
	c.Header("Cache-Control", "private, max-age=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(data)
}

var errUpload = errors.New("se esperaba multipart/form-data con el archivo en el campo \"photo\" (o \"file\") o un body de imagen (jpg, png o webp)")

// uploadFields son los nombres de campo que se aceptan en el multipart. `photo`
// es el que manda el frontend (FormData.append('photo', file)) y `file` el
// genérico de curl/Postman: aceptar los dos evita un contrato frágil por un
// nombre.
var uploadFields = []string{"photo", "file"}

// readUpload saca los bytes y el content type declarado de lo que subió el
// cliente. El límite de 8 MB se aplica al leer (no se carga un archivo enorme
// en memoria para después rechazarlo).
func readUpload(c *gin.Context) ([]byte, string, error) {
	// Tope del BODY antes de parsear nada (R4-001): c.Request.FormFile parsea y
	// spoolea TODO el multipart en memoria/disco sin límite propio. Sin esta
	// línea, un body de 1 GB con un archivo chico adentro se acepta y el
	// servidor lo procesa entero. El +1 MB es el overhead del propio multipart
	// (boundaries + headers); el archivo sigue acotado por MaxPhotoBytes.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, domain.MaxPhotoBytes+(1<<20))
	ct := c.GetHeader("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		file, header, err := multipartFile(c)
		if err != nil {
			return nil, "", errUpload
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, domain.MaxPhotoBytes+1))
		if err != nil {
			return nil, "", errUpload
		}
		declared := header.Header.Get("Content-Type")
		if declared == "" {
			declared = http.DetectContentType(data)
		}
		return data, declared, nil
	case strings.HasPrefix(ct, "image/"):
		data, err := io.ReadAll(io.LimitReader(c.Request.Body, domain.MaxPhotoBytes+1))
		if err != nil {
			return nil, "", errUpload
		}
		return data, ct, nil
	default:
		return nil, "", errUpload
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
