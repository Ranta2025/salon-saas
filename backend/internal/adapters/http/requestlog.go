package httpapi

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// routePhotoUpload identifica la ÚNICA ruta con tope de body propio (más
// grande que el JSON): la subida de fotos, que valida readUpload.
// MaxJSONBody la salta por RUTA, no por Content-Type.
const routePhotoUpload = "/api/v1/salons/:id/clients/:cid/photos"

// MaxJSONBody acota el body a `limit` por RUTA (H1). El Content-Type lo elige el
// cliente, así que decidir por él era un bypass trivial: un body de gigabytes
// con "multipart/form-data" o "image/png" apagaba el tope en cualquier endpoint
// JSON. Con Content-Length declarado se rechaza antes de leer nada; sin él, el
// MaxBytesReader corta la lectura igual (el binding falla y la ruta responde
// 400). La subida de fotos queda exenta porque tiene su propio tope (8 MB) en
// readUpload, que además rechaza lo que no es imagen.
func MaxJSONBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if hasOwnBodyLimit(c.FullPath()) {
			c.Next()
			return
		}
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body demasiado grande"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// hasOwnBodyLimit dice si la ruta aplica su propio tope (subida de fotos).
func hasOwnBodyLimit(fullPath string) bool {
	return fullPath == routePhotoUpload
}

// RequestLogger es el log de acceso, con el token del websocket redactado
// (B-08). El WS no puede mandar el header Authorization, así que el token viaja
// en el query param; escribirlo en el log equivale a publicar credenciales
// válidas de esa persona en un archivo que lee medio mundo.
//
// Formato: method path?query status latency ip (una línea por request).
func RequestLogger(out io.Writer) gin.HandlerFunc {
	if out == nil {
		out = gin.DefaultWriter
	}
	logger := log.New(out, "", 0)
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		if raw != "" {
			path = path + "?" + redactQuery(raw)
		}
		logger.Printf("%s %s %d %s %s",
			c.Request.Method, path, c.Writer.Status(), time.Since(start).Round(time.Millisecond), c.ClientIP())
	}
}

// redactQuery reemplaza el valor de los parámetros sensibles por "***".
// Los demás se conservan (sirven para debuggear el resto de la API).
func redactQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		// Query ilegible: no se loguea nada que pueda ser un secreto.
		return "redacted"
	}
	changed := false
	for _, key := range []string{"token", "access_token", "password", "jwt"} {
		if _, ok := values[key]; ok {
			values[key] = []string{"***"}
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return values.Encode()
}
