package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RouteGalleryUpload es la ruta de la subida de la galería (multipart de hasta
// 8 MB): MaxJSONBody la deja pasar por RUTA, como a la de fotos de clientas.
const RouteGalleryUpload = "/api/v1/salons/:id/gallery"

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
