// Adaptador S3/MinIO de ports.PhotoStorage. Se elige en el composition root
// cuando S3_ENDPOINT está definido (si no, queda el filesystem local). La
// interfaz es idéntica: el caso de uso no sabe dónde vive el binario.
//
// La key que arma el caso de uso (salon/cliente/foto.webp) es el nombre del
// objeto en el bucket; lo que se persiste en la DB es esa key, así migrar de
// disco a bucket no cambia el modelo.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// s3Timeout acota cada operación: sin deadline, una caída de red deja el
// request HTTP colgado hasta el write timeout del servidor.
const s3Timeout = 30 * time.Second

// S3Config es lo que sale de las variables S3_* del entorno.
type S3Config struct {
	Endpoint  string // host[:puerto], con o sin esquema http(s)
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string // opcional; MinIO no lo exige
}

// S3 guarda los binarios en un bucket compatible con S3.
type S3 struct {
	Client *minio.Client
	Bucket string
	UseSSL bool
}

// NewS3 valida la configuración y arma el cliente. No contacta al servidor:
// el arranque de la API no depende de que MinIO esté arriba (en docker-compose
// es opcional); un error de red aparece en la primera subida.
func NewS3(cfg S3Config) (S3, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	useSSL := cfg.UseSSL
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		endpoint, useSSL = strings.TrimPrefix(endpoint, "https://"), true
	case strings.HasPrefix(endpoint, "http://"):
		endpoint, useSSL = strings.TrimPrefix(endpoint, "http://"), false
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	if endpoint == "" {
		return S3{}, fmt.Errorf("%w: falta S3_ENDPOINT", domain.ErrValidation)
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return S3{}, fmt.Errorf("%w: falta S3_BUCKET", domain.ErrValidation)
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(strings.TrimSpace(cfg.AccessKey), strings.TrimSpace(cfg.SecretKey), ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return S3{}, fmt.Errorf("%w: configuración S3 inválida: %v", domain.ErrValidation, err)
	}
	return S3{Client: client, Bucket: bucket, UseSSL: useSSL}, nil
}

// Put sube data bajo key y devuelve la key como ruta persistida.
func (s S3) Put(key string, data []byte, contentType string) (string, error) {
	clean, err := sanitizeKey(key)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%w: archivo vacío", domain.ErrValidation)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	if _, err := s.Client.PutObject(ctx, s.Bucket, clean, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType}); err != nil {
		return "", fmt.Errorf("storage s3: subir %s: %w", clean, err)
	}
	return clean, nil
}

// Get devuelve el objeto. Un objeto inexistente es domain.ErrNotFound (misma
// semántica que el adaptador local).
func (s S3) Get(path string) ([]byte, error) {
	clean, err := sanitizeKey(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	obj, err := s.Client.GetObject(ctx, s.Bucket, clean, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage s3: abrir %s: %w", clean, err)
	}
	defer obj.Close()
	// GetObject es perezoso: el NoSuchKey y el tamaño aparecen recién al
	// leer/stat, que es lo que hace readPhotoObject.
	return readPhotoObject(obj, clean)
}

// objectReader es el objeto abierto de minio (GetObject es perezoso).
type objectReader interface {
	io.Reader
	Close() error
	Stat() (minio.ObjectInfo, error)
}

// readPhotoObject hace el trabajo de Get sobre un objeto ya abierto: valida
// que exista (Stat) y lee con tope (H5). El tope es el mismo que acepta la
// subida: un objeto más grande es un dato roto (key corrupta o bucket tocado
// fuera de la API), no una foto, y no se carga entero en memoria.
func readPhotoObject(obj objectReader, key string) ([]byte, error) {
	if _, err := obj.Stat(); err != nil {
		if isNoSuchKey(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("storage s3: leer %s: %w", key, err)
	}
	data, err := io.ReadAll(io.LimitReader(obj, domain.MaxPhotoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("storage s3: leer %s: %w", key, err)
	}
	if int64(len(data)) > domain.MaxPhotoBytes {
		return nil, fmt.Errorf("storage s3: el objeto %s supera el tope de %d bytes", key, domain.MaxPhotoBytes)
	}
	return data, nil
}

// Delete borra el objeto. Borrar algo que no está es ErrNotFound, igual que el
// adaptador local (el borrado del caso de uso es idempotente).
func (s S3) Delete(path string) error {
	clean, err := sanitizeKey(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	if _, err := s.Client.StatObject(ctx, s.Bucket, clean, minio.StatObjectOptions{}); err != nil {
		if isNoSuchKey(err) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("storage s3: buscar %s: %w", clean, err)
	}
	if err := s.Client.RemoveObject(ctx, s.Bucket, clean, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage s3: borrar %s: %w", clean, err)
	}
	return nil
}

// isNoSuchKey distingue "el objeto no existe" de una caída del storage.
func isNoSuchKey(err error) bool {
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NotFound":
		return true
	default:
		return false
	}
}

var _ ports.PhotoStorage = S3{}
