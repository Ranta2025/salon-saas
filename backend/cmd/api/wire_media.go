package main

import (
	"log"

	"salon-saas/internal/adapters/storage"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/ports"
)

// photoStorage elige el adaptador de fotos por configuración: S3/MinIO si
// S3_ENDPOINT está definido, filesystem local si no. Aislado para poder probar
// la selección sin arrancar el servidor.
func photoStorage(cfg config) (ports.PhotoStorage, error) {
	if cfg.S3Endpoint == "" {
		return storage.Local{Root: cfg.MediaDir}, nil
	}
	return storage.NewS3(storage.S3Config{
		Endpoint:  cfg.S3Endpoint,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Bucket:    cfg.S3Bucket,
		UseSSL:    cfg.S3UseSSL,
	})
}

// wireMedia arma el almacenamiento de adjuntos y la firma de sus URLs.
func wireMedia(w *wiring) {
	// Adjuntos: S3 si S3_ENDPOINT está definido; si no, filesystem local. El
	// puerto es el mismo (ports.PhotoStorage): cambiar de adapter es esta línea.
	store, err := photoStorage(w.cfg)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	w.store = store
	// Firma de las URLs de fotos (24 h): clave derivada del secreto del proceso.
	w.photoSigner = security.NewPhotoSigner(w.cfg.JWTSecret)
}
