package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// RouteGalleryUpload es la ruta de la subida de la galería (multipart de hasta
// 8 MB): MaxJSONBody la deja pasar por RUTA, como a la de fotos de clientas.
const RouteGalleryUpload = "/api/v1/salons/:id/gallery"

// GalleryHandler: la galería pública del salón (F16, D3).
type GalleryHandler struct {
	Gallery     app.GalleryService
	Memberships ports.MembershipRepository
}

func (h GalleryHandler) loadActor(c *gin.Context, salonID string) (domain.Membership, bool) {
	uid, _ := c.Get("userID")
	m, err := h.Memberships.Find(uid.(string), salonID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "sin acceso al salón"})
		return domain.Membership{}, false
	}
	return m, true
}

// MaxJSONBodyExceptGalleryUpload es MaxJSONBody con la subida de la galería
// exenta (tiene su propio tope de 8 MB en ReadUpload, que además rechaza lo que
// no es imagen). Se decide por RUTA, no por Content-Type: ninguna otra ruta se
// libra del tope de 1 MB.
func MaxJSONBodyExceptGalleryUpload(limit int64) gin.HandlerFunc {
	limited := MaxJSONBody(limit)
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.FullPath() == RouteGalleryUpload {
			c.Next()
			return
		}
		limited(c)
	}
}

// galleryPhotoJSON: {id, url, caption, created_at}; la leyenda sin cargar es null.
func galleryPhotoJSON(p app.GalleryPhoto) gin.H {
	return gin.H{
		"id":         p.ID,
		"url":        p.URL,
		"caption":    optionalID(p.Caption),
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// Add: POST /salons/:id/gallery -> 201 {"photo":{...}}. Multipart con el campo
// `file` (o `photo`), o el archivo crudo con Content-Type image/*: el mismo
// lector y el mismo tope de 8 MB que la foto de una clienta. El permiso se mira
// ANTES de leer el archivo.
func (h GalleryHandler) Add(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Gallery.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	data, contentType, err := ReadUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	photo, err := h.Gallery.Add(actor, salonID, data, contentType)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"photo": galleryPhotoJSON(photo)})
}

// List: GET /salons/:id/gallery -> {"photos":[{id, url, caption, created_at}]}
// en orden de subida.
func (h GalleryHandler) List(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	list, err := h.Gallery.List(actor, salonID)
	if err != nil {
		respondErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		out = append(out, galleryPhotoJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{"photos": out})
}

// SetCaption: PUT /salons/:id/gallery/:pid {caption} -> 200 {"photo":{...}}. La
// clave es obligatoria; `null` o "" = sin leyenda.
func (h GalleryHandler) SetCaption(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Gallery.RequireManager(actor, salonID); err != nil {
		respondErr(c, err)
		return
	}
	var in struct {
		Caption presentText `json:"caption"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	if !in.Caption.Set {
		respondErr(c, domain.NewMessage(domain.ErrValidation, domain.MsgPhotoCaptionMissing))
		return
	}
	photo, err := h.Gallery.SetCaption(actor, salonID, c.Param("pid"), in.Caption.Value)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"photo": galleryPhotoJSON(photo)})
}

// Delete: DELETE /salons/:id/gallery/:pid -> 204 (borra también el archivo).
func (h GalleryHandler) Delete(c *gin.Context) {
	salonID := c.Param("id")
	actor, ok := h.loadActor(c, salonID)
	if !ok {
		return
	}
	if err := h.Gallery.Delete(actor, salonID, c.Param("pid")); err != nil {
		respondErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// PublicImage: GET /public/salons/:slug/gallery/:pid -> la imagen. Sin sesión ni
// firma: pública por diseño (es la foto que el salón eligió mostrar en su web).
// Content-Type FIJO del tipo guardado (nunca el que declaró quien la subió),
// nosniff y cache de 5 minutos. Un id inexistente, de otro salón o de una
// clienta es el mismo 404 sin cache. Solo lee: no escribe nada sin autenticar.
func (h GalleryHandler) PublicImage(c *gin.Context) {
	photo, data, err := h.Gallery.PublicPhoto(c.Param("slug"), c.Param("pid"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.Header("Content-Type", photo.ContentType)
	c.Header("Content-Length", strconv.Itoa(len(data)))
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(data)
}
