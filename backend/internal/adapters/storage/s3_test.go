package storage

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"salon-saas/internal/domain"
)

// Configuración inválida y keys peligrosas se rechazan ANTES de tocar la red:
// el adaptador no confía en el caso de uso (misma regla que el filesystem).
func TestS3_config_y_keys(t *testing.T) {
	for _, cfg := range []S3Config{
		{Bucket: "salon-media"},
		{Endpoint: "127.0.0.1:9000"},
	} {
		if _, err := NewS3(cfg); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("config %+v debe ser ErrValidation, dio %v", cfg, err)
		}
	}
	s, err := NewS3(S3Config{Endpoint: "127.0.0.1:9000", AccessKey: "a", SecretKey: "b", Bucket: "salon-media"})
	if err != nil {
		t.Fatalf("config válida: %v", err)
	}
	for _, key := range []string{"", "../escape.webp", "/absoluta.webp", "a/../../b.webp"} {
		if _, err := s.Put(key, []byte("x"), "image/webp"); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("key %q debe ser ErrValidation, dio %v", key, err)
		}
	}
	// El esquema del endpoint no molesta: se normaliza.
	if s, err := NewS3(S3Config{Endpoint: "http://127.0.0.1:9000", Bucket: "salon-media"}); err != nil {
		t.Fatalf("endpoint con esquema: %v", err)
	} else if s.UseSSL {
		t.Fatal("http:// no puede activar TLS")
	}
	if s, err := NewS3(S3Config{Endpoint: "https://s3.example.com", Bucket: "salon-media"}); err != nil {
		t.Fatalf("endpoint https: %v", err)
	} else if !s.UseSSL {
		t.Fatal("https:// debe activar TLS")
	}
}

// objetoFalso es un objeto de S3 ya abierto: Stat responde lo que se le pida y
// la lectura cuenta los bytes, para probar que se lee con tope.
type objetoFalso struct {
	data    []byte
	leidos  int
	statErr error
}

func (o *objetoFalso) Read(p []byte) (int, error) {
	if o.leidos >= len(o.data) {
		return 0, io.EOF
	}
	n := copy(p, o.data[o.leidos:])
	o.leidos += n
	return n, nil
}

func (o *objetoFalso) Close() error { return nil }

func (o *objetoFalso) Stat() (minio.ObjectInfo, error) {
	return minio.ObjectInfo{Size: int64(len(o.data))}, o.statErr
}

// H5: Get no puede hacer io.ReadAll de un objeto sin tope. Un objeto más grande
// que la foto máxima (bucket manipulado fuera de la API o key corrupta) se
// rechaza con un error claro y sin cargarlo entero en memoria.
func TestS3_get_lee_con_tope(t *testing.T) {
	chica := &objetoFalso{data: bytes.Repeat([]byte("RIFF"), 64)}
	got, err := readPhotoObject(chica, "s1/c1/foto.webp")
	if err != nil {
		t.Fatalf("un objeto dentro del tope se devuelve entero: %v", err)
	}
	if !bytes.Equal(got, chica.data) {
		t.Fatalf("bytes: %d leídos", len(got))
	}

	grande := &objetoFalso{data: bytes.Repeat([]byte("x"), domain.MaxPhotoBytes+1)}
	got, err = readPhotoObject(grande, "s1/c1/grande.webp")
	if err == nil {
		t.Fatalf("un objeto de %d bytes debe rechazarse", len(grande.data))
	}
	if !strings.Contains(err.Error(), "tope") {
		t.Fatalf("el error tiene que explicar el tope: %v", err)
	}
	if got != nil {
		t.Fatalf("no se devuelven datos de un objeto rechazado: %d bytes", len(got))
	}
	if grande.leidos > domain.MaxPhotoBytes+1 {
		t.Fatalf("se leyeron %d bytes: el tope es %d + 1", grande.leidos, domain.MaxPhotoBytes)
	}

	// Un objeto que no existe sigue siendo ErrNotFound (misma semántica que el
	// adaptador local).
	falta := &objetoFalso{statErr: minio.ErrorResponse{Code: "NoSuchKey"}}
	if _, err := readPhotoObject(falta, "s1/c1/nada.webp"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("objeto inexistente: %v", err)
	}
}
