// Package storage es el adaptador de ports.PhotoStorage para el filesystem
// local. Existe porque MinIO/S3 no se pueden correr en este entorno; la
// interfaz es exactamente la misma que usaría un bucket, así que migrar es
// cambiar una línea en el composition root.
//
// El binario vive fuera de la DB (la fila guarda la ruta y las dimensiones).
// El content type no se persiste acá: es una propiedad de la fila
// (client_photos.content_type), y en S3 viajaría como metadata del objeto.
package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"salon-saas/internal/domain"
)

// Local guarda todo bajo Root, con la misma estructura de carpetas que la key.
type Local struct {
	Root string
}

// sanitizeKey valida que la key sea relativa y no escape del root.
// La key la arma el caso de uso (salon/cliente/foto), pero el adapter no
// confía en nadie: un ".." acá sería escritura arbitraria en el filesystem.
func sanitizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("%w: key vacía", domain.ErrValidation)
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\x00") {
		return "", fmt.Errorf("%w: key inválida %q", domain.ErrValidation, key)
	}
	clean := path.Clean(key)
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: key inválida %q", domain.ErrValidation, key)
	}
	return clean, nil
}

// Put escribe data bajo key y devuelve la ruta estable que se persiste en la
// DB. Escribe a un temporal y renombra: nadie sirve un archivo a medio escribir.
func (l Local) Put(key string, data []byte, contentType string) (string, error) {
	clean, err := sanitizeKey(key)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%w: archivo vacío", domain.ErrValidation)
	}
	full := filepath.Join(l.Root, filepath.FromSlash(clean))
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName) // el temporal es nuestro: se limpia y se propaga el error
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, full); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return clean, nil
}

// Get devuelve el contenido guardado en path (ErrNotFound si no existe).
func (l Local) Get(path string) ([]byte, error) {
	clean, err := sanitizeKey(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(l.Root, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

// Delete borra el archivo (ErrNotFound si ya no está: el borrado es idempotente
// para quien llama, aunque acá se distinga).
func (l Local) Delete(path string) error {
	clean, err := sanitizeKey(path)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(l.Root, filepath.FromSlash(clean)))
	if errors.Is(err, fs.ErrNotExist) {
		return domain.ErrNotFound
	}
	return err
}
