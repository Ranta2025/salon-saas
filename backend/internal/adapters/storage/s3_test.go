package storage

import (
	"errors"
	"testing"

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
