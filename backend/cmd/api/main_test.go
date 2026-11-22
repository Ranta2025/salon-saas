package main

import (
	"strings"
	"testing"

	"salon-saas/internal/adapters/storage"
)

// T6: S3_ENDPOINT elige el adaptador de fotos. Sin endpoint, disco local.
func TestPhotoStorage_seleccion(t *testing.T) {
	local, err := photoStorage(config{MediaDir: "/tmp/media"})
	if err != nil {
		t.Fatalf("local: %v", err)
	}
	if l, ok := local.(storage.Local); !ok || l.Root != "/tmp/media" {
		t.Fatalf("sin S3_ENDPOINT debe usar el filesystem local, dio %T %+v", local, local)
	}

	s3store, err := photoStorage(config{
		S3Endpoint: "http://127.0.0.1:9000", S3AccessKey: "salon", S3SecretKey: "secreto",
		S3Bucket: "salon-media", S3UseSSL: false,
	})
	if err != nil {
		t.Fatalf("s3: %v", err)
	}
	s, ok := s3store.(storage.S3)
	if !ok {
		t.Fatalf("con S3_ENDPOINT debe usar S3, dio %T", s3store)
	}
	if s.Bucket != "salon-media" || s.UseSSL {
		t.Fatalf("S3 mal configurado: %+v", s)
	}
	// Bucket vacío no se inventa.
	if _, err := photoStorage(config{S3Endpoint: "127.0.0.1:9000", S3Bucket: ""}); err == nil {
		t.Fatal("S3 sin bucket debe fallar")
	}
}

// El composition root registra las rutas del contrato ronda 2.
func TestBuildRouter_rutas_ronda2(t *testing.T) {
	cfg := config{
		DatabaseURL: "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable",
		JWTSecret:   strings.Repeat("s", 40),
		CORSOrigins: []string{"http://localhost:5173"},
		MediaDir:    t.TempDir(),
		Port:        "0",
	}
	router, deps, reminders := buildRouter(cfg, nil)
	if reminders.Emails == nil {
		t.Fatal("la tarea de recordatorios tiene que recibir la cola de emails")
	}
	if reminders.Mailer != nil {
		t.Fatal("sin SMTP_HOST no hay sender: el email está deshabilitado")
	}
	if deps.PublicBookingLimit == nil {
		t.Fatal("la reserva pública debe tener rate limit por IP")
	}
	have := map[string]bool{}
	for _, r := range router.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	want := []string{
		"GET /api/v1/public/salons/:slug/availability",
		"POST /api/v1/public/salons/:slug/bookings",
		"PATCH /api/v1/salons/:id/bookings/:bid",
		"GET /api/v1/salons/:id/settings",
		"PUT /api/v1/salons/:id/settings",
		"GET /api/v1/salons/:id/members",
		"POST /api/v1/salons/:id/members",
		"DELETE /api/v1/salons/:id/members/:uid",
		// Las de la ronda 1 siguen montadas.
		"GET /api/v1/public/salons/:slug",
		"POST /api/v1/salons/:id/bookings",
		"GET /api/v1/salons/:id/bookings",
		"GET /api/v1/me",
	}
	for _, r := range want {
		if !have[r] {
			t.Fatalf("falta la ruta %q (registradas: %d)", r, len(have))
		}
	}
}

// Configuración S3: defaults y validación de S3_USE_SSL / S3_BUCKET.
func TestLoadConfig_S3(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}

	cfg, err := loadConfig(loadEnv(base))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.S3Endpoint != "" || cfg.S3Bucket != "salon-media" || cfg.S3UseSSL {
		t.Fatalf("defaults S3: %+v", cfg)
	}

	withS3 := map[string]string{
		"JWT_SECRET": "secreto", "GIN_MODE": "debug",
		"S3_ENDPOINT": "http://127.0.0.1:9000", "S3_ACCESS_KEY": "salon",
		"S3_SECRET_KEY": "clave", "S3_BUCKET": "fotos", "S3_USE_SSL": "true",
	}
	cfg, err = loadConfig(loadEnv(withS3))
	if err != nil {
		t.Fatalf("config S3: %v", err)
	}
	if cfg.S3Endpoint != "http://127.0.0.1:9000" || cfg.S3Bucket != "fotos" || !cfg.S3UseSSL || cfg.S3AccessKey != "salon" {
		t.Fatalf("config S3: %+v", cfg)
	}
	// Booleano inválido: error de configuración, no default silencioso.
	badBool := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug", "S3_USE_SSL": "sí"}
	if _, err := loadConfig(loadEnv(badBool)); err == nil || !strings.Contains(err.Error(), "S3_USE_SSL") {
		t.Fatalf("S3_USE_SSL inválido debe fallar, dio %v", err)
	}
	// Endpoint sin bucket: error claro.
	noBucket := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug", "S3_ENDPOINT": "127.0.0.1:9000", "S3_BUCKET": " "}
	if _, err := loadConfig(loadEnv(noBucket)); err == nil || !strings.Contains(err.Error(), "S3_BUCKET") {
		t.Fatalf("S3 sin bucket debe fallar, dio %v", err)
	}
}
