package httpapi_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"salon-saas/internal/adapters/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestUpload_rechaza_body_gigante_con_archivo_chico: el límite tiene que estar
// en el BODY (antes de parsear el multipart), no solo en el archivo.
// Acá el archivo es un PNG mínimo válido pero el body trae 10 MB de relleno:
// sin guarda de body, el servidor lo acepta (parsea y spoolea todo) y solo
// después mira el tamaño. Con la guarda, el POST se rechaza de entrada.
func TestUpload_rechaza_body_gigante_con_archivo_chico(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("photo", "mini.png")
	if err != nil {
		t.Fatal(err)
	}
	// PNG 1x1 válido (firma + IHDR + IDAT + IEND mínimos).
	fw.Write(png1x1())
	// Relleno que infla el body muy por encima del tope sin tocar el archivo.
	if err := mw.WriteField("relleno", strings.Repeat("x", 10<<20)); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", &body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())

	_, _, err = httpapi.ReadUpload(c)
	if err == nil {
		t.Fatal("body de 10 MB con archivo chico debe rechazarse")
	}
}

// TestUpload_acepta_foto_normal: la guarda no debe romper el camino feliz.
func TestUpload_acepta_foto_normal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("photo", "mini.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(png1x1())
	mw.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", &body)
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())

	data, ct, err := httpapi.ReadUpload(c)
	if err != nil {
		t.Fatalf("foto chica debe aceptarse: %v", err)
	}
	if len(data) == 0 || ct == "" {
		t.Fatal("esperaba datos y content-type")
	}
}

// png1x1: PNG válido de 1x1 rojo (firma + IHDR + IDAT + IEND).
func png1x1() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // firma
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR len+tipo
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, // resto IHDR+CRC
		0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54, // IDAT
		0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x00, 0x03, 0x00, 0x01,
		0x00, 0x05, 0xfe, 0xd4, 0x0a,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, // IEND
		0xae, 0x42, 0x60, 0x82,
	}
}
