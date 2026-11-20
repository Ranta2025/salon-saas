package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

func newLocal(t *testing.T) (Local, string) {
	t.Helper()
	root := t.TempDir() // el test limpia solo: nunca se escribe dentro del repo
	return Local{Root: root}, root
}

var _ ports.PhotoStorage = Local{}

func TestLocal_put_get_delete(t *testing.T) {
	store, root := newLocal(t)
	data := []byte("RIFF....WEBP")

	stored, err := store.Put("s1/c1/foto-1.webp", data, "image/webp")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if stored != "s1/c1/foto-1.webp" {
		t.Fatalf("la ruta a persistir es relativa y estable, dio %q", stored)
	}
	// El archivo queda bajo el root, con los directorios creados.
	if _, err := os.Stat(filepath.Join(root, "s1", "c1", "foto-1.webp")); err != nil {
		t.Fatalf("el archivo no está donde debe: %v", err)
	}
	got, err := store.Get(stored)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("get devolvió %q", got)
	}
	// Sobrescribir es idempotente (re-subir la misma key no rompe).
	if _, err := store.Put(stored, []byte("otro"), "image/webp"); err != nil {
		t.Fatalf("put repetido: %v", err)
	}
	if got, _ := store.Get(stored); string(got) != "otro" {
		t.Fatalf("no sobrescribió: %q", got)
	}
	if err := store.Delete(stored); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(stored); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("después de borrar debe dar NotFound, dio %v", err)
	}
	if err := store.Delete(stored); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("borrar dos veces debe dar NotFound, dio %v", err)
	}
}

// Path traversal: la key la arma la app, pero el adapter no confía en nadie.
func TestLocal_rechaza_keys_que_escapan_del_root(t *testing.T) {
	store, root := newLocal(t)
	for _, key := range []string{"", "  ", "../fuera.webp", "s1/../../fuera.webp", "/etc/passwd"} {
		if _, err := store.Put(key, []byte("x"), "image/webp"); err == nil {
			t.Fatalf("key %q debería rechazarse", key)
		}
		if _, err := store.Get(key); err == nil {
			t.Fatalf("get %q debería rechazarse", key)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "fuera.webp")); err == nil {
		t.Fatal("escapó del root: hay un archivo afuera")
	}
}
