package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"time"
)

// PhotoSigner firma las URLs de fotos con HMAC-SHA256. La clave se deriva del
// secreto del proceso (JWT_SECRET): no hace falta otra variable de entorno y
// rotar el secreto invalida todas las URLs viejas.
//
// No usa el mismo valor que la firma de JWT: la derivación con un contexto
// distinto evita que un token filtrado sirva para firmar fotos o viceversa.
type PhotoSigner struct {
	key []byte
	// now permite fijar el reloj en los tests.
	now func() time.Time
}

// NewPhotoSigner deriva la clave de firma del secreto del proceso.
func NewPhotoSigner(secret string) PhotoSigner {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("salon-saas/photo-url/v1"))
	return PhotoSigner{key: mac.Sum(nil), now: time.Now}
}

// Sign devuelve la firma URL-safe de photoID con ese vencimiento (unix).
func (p PhotoSigner) Sign(photoID string, expiresUnix int64) string {
	mac := hmac.New(sha256.New, p.key)
	mac.Write([]byte(photoID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strconv.FormatInt(expiresUnix, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify valida la firma y el vencimiento. Una URL sin exp o vencida no sirve.
func (p PhotoSigner) Verify(photoID string, expiresUnix int64, sig string) bool {
	if photoID == "" || sig == "" || expiresUnix <= 0 {
		return false
	}
	clock := p.now
	if clock == nil {
		clock = time.Now
	}
	if clock().Unix() > expiresUnix {
		return false
	}
	want := p.Sign(photoID, expiresUnix)
	return hmac.Equal([]byte(want), []byte(sig))
}
