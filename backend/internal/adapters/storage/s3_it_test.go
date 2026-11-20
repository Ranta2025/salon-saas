// Integración real contra MinIO (S3_IT=1). Solo toca objetos bajo el prefijo
// it-test/: el bucket y el resto de las claves son del usuario.
package storage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func envOrTest(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func s3ForIT(t *testing.T) S3 {
	t.Helper()
	if os.Getenv("S3_IT") == "" {
		t.Skip("S3_IT no definido: integración con MinIO salteada")
	}
	s, err := NewS3(S3Config{
		Endpoint:  envOrTest("S3_ENDPOINT", "http://127.0.0.1:9000"),
		AccessKey: envOrTest("S3_ACCESS_KEY", "salon"),
		SecretKey: envOrTest("S3_SECRET_KEY", "salon-secret-123"),
		Bucket:    envOrTest("S3_BUCKET", "salon-media"),
		UseSSL:    os.Getenv("S3_USE_SSL") == "true",
	})
	if err != nil {
		t.Fatalf("cliente S3: %v", err)
	}
	return s
}

func TestS3_IT_put_get_delete(t *testing.T) {
	s := s3ForIT(t)
	// Clave única y aislada: el test nunca toca objetos de otro prefijo.
	key := fmt.Sprintf("it-test/round2/%d.webp", time.Now().UnixNano())
	data := []byte("RIFF....WEBP-it-round2")
	t.Cleanup(func() { _ = s.Delete(key) })

	stored, err := s.Put(key, data, domain.PhotoTypeWebP)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if stored != key {
		t.Fatalf("la ruta persistida debe ser la key (%q), dio %q", key, stored)
	}
	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("bytes: %q", got)
	}
	// Sobrescribir la misma key es idempotente (upsert del objeto).
	data2 := []byte("RIFF....WEBP-it-round2-v2")
	if _, err := s.Put(key, data2, domain.PhotoTypeWebP); err != nil {
		t.Fatalf("put 2: %v", err)
	}
	if got, _ := s.Get(key); !bytes.Equal(got, data2) {
		t.Fatalf("sobrescritura: %q", got)
	}
	// Borrar y volver a borrar: la segunda es ErrNotFound (misma semántica que
	// el adaptador local, y el test deja el prefijo limpio).
	if err := s.Delete(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get tras delete: %v", err)
	}
	if err := s.Delete(key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete dos veces: %v", err)
	}
	// Un objeto inexistente tampoco existe para Get.
	if _, err := s.Get(fmt.Sprintf("it-test/round2/no-existe-%d.webp", time.Now().UnixNano())); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get inexistente: %v", err)
	}
	// Tamaño vacío: 400 de dominio, sin escribir un objeto de 0 bytes.
	if _, err := s.Put(fmt.Sprintf("it-test/round2/vacio-%d.webp", time.Now().UnixNano()), nil, domain.PhotoTypeWebP); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("put vacío: %v", err)
	}
}
