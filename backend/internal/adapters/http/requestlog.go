package httpapi

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MaxJSONBody acota el body de los pedidos JSON (B-11): 1 MB es holgadísimo
// para cualquier payload de esta API y evita que un body de gigabytes se parsee
// entero en memoria. El multipart (fotos de hasta 8 MB) tiene su propio tope en
// readUpload, así que este middleware no lo toca.
func MaxJSONBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		ct := strings.ToLower(c.GetHeader("Content-Type"))
		switch {
		case strings.HasPrefix(ct, "multipart/form-data"):
			// El tope de las fotos (8 MB + overhead) lo aplica readUpload.
			c.Next()
			return
		case strings.HasPrefix(ct, "image/"):
			// Subida cruda de una imagen (curl/Postman): también la acota
			// readUpload, no este middleware.
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
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
