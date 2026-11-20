package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	httpapi "salon-saas/internal/adapters/http"
)

// minJWTSecretLen es el mínimo en release: HS256 con menos de 32 caracteres se
// rompe por fuerza bruta.
const minJWTSecretLen = 32

// config es la configuración del proceso, leída UNA vez al arrancar.
// Cada variable tiene un default de desarrollo salvo JWT_SECRET: sin secreto el
// proceso no arranca (B-02), porque el default histórico permitía forjar tokens.
type config struct {
	DatabaseURL string
	JWTSecret   string
	CORSOrigins []string
	MediaDir    string
	Port        string
	GinMode     string
	// Almacenamiento de fotos: si S3_ENDPOINT está definido se usa S3/MinIO.
	// Sin endpoint, el adaptador de disco local (MEDIA_DIR).
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
}

// loadConfig lee la configuración desde getenv (os.Getenv en producción; un
// mapa en los tests) y valida lo que no puede faltar.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		DatabaseURL: envOr(getenv, "DATABASE_URL", "postgres://salon:salon@localhost:5432/salon?sslmode=disable"),
		JWTSecret:   strings.TrimSpace(getenv("JWT_SECRET")),
		MediaDir:    envOr(getenv, "MEDIA_DIR", "./media"),
		Port:        envOr(getenv, "PORT", "8080"),
		GinMode:     getenv("GIN_MODE"),
		CORSOrigins: httpapi.ParseOrigins(getenv("CORS_ORIGINS")),
		S3Endpoint:  strings.TrimSpace(getenv("S3_ENDPOINT")),
		S3AccessKey: strings.TrimSpace(getenv("S3_ACCESS_KEY")),
		S3SecretKey: strings.TrimSpace(getenv("S3_SECRET_KEY")),
		S3Bucket:    envOr(getenv, "S3_BUCKET", "salon-media"),
	}
	useSSL, err := envBool(getenv, "S3_USE_SSL", false)
	if err != nil {
		return config{}, err
	}
	cfg.S3UseSSL = useSSL
	// Con endpoint S3, el bucket no puede quedar vacío (S3_BUCKET="" explícito).
	if cfg.S3Endpoint != "" && strings.TrimSpace(cfg.S3Bucket) == "" {
		return config{}, fmt.Errorf("S3_BUCKET es obligatoria cuando S3_ENDPOINT está definida")
	}
	if cfg.JWTSecret == "" {
		return config{}, fmt.Errorf("JWT_SECRET es obligatoria: sin secreto no se pueden emitir ni validar tokens")
	}
	// El modo por defecto es release (producción), así que el largo mínimo vale
	// salvo que alguien pida explícitamente debug/test.
	if !isDevMode(cfg.GinMode) && len(cfg.JWTSecret) < minJWTSecretLen {
		return config{}, fmt.Errorf("JWT_SECRET debe tener al menos %d caracteres en release (tiene %d)", minJWTSecretLen, len(cfg.JWTSecret))
	}
	return cfg, nil
}

// isDevMode dice si el proceso corre en modo desarrollo (debug o test).
func isDevMode(ginMode string) bool {
	return ginMode == "debug" || ginMode == "test"
}

// envBool lee un booleano ("true"/"false", "1"/"0") con default. Un valor
// inválido es un error de configuración, no un default silencioso.
func envBool(getenv func(string) string, k string, def bool) (bool, error) {
	v := strings.TrimSpace(getenv(k))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s debe ser true o false (dio %q)", k, v)
	}
	return b, nil
}

// envOr devuelve la variable o el default si está vacía.
func envOr(getenv func(string) string, k, d string) string {
	if v := getenv(k); v != "" {
		return v
	}
	return d
}

// mustLoadConfig es la versión de arranque: un error de configuración termina el
// proceso en vez de seguir con valores por defecto inseguros.
func mustLoadConfig() config {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}
