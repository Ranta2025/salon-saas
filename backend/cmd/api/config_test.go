package main

import (
	"strings"
	"testing"
)

// B-02: sin JWT_SECRET el proceso no arranca. El default viejo
// ("dev-secret-cambiala") permitía forjar tokens: cualquiera que leyera el repo
// entraba como cualquier usuario.
func TestLoadConfig_JWT_secret_obligatorio(t *testing.T) {
	cfg, err := loadConfig(func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://x"
		}
		return ""
	})
	if err == nil {
		t.Fatalf("sin JWT_SECRET debe fallar, dio %+v", cfg)
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("el error debe nombrar la variable: %v", err)
	}
}

// En release, un secreto corto también es un error: HS256 con 8 caracteres se
// rompe por fuerza bruta.
func TestLoadConfig_release_exige_32_caracteres(t *testing.T) {
	short := loadEnv(map[string]string{
		"JWT_SECRET": "corto", "GIN_MODE": "release",
	})
	if _, err := loadConfig(short); err == nil {
		t.Fatal("en release un secreto de 5 caracteres debe rechazarse")
	}
	long := loadEnv(map[string]string{
		"JWT_SECRET": strings.Repeat("s", 32), "GIN_MODE": "release",
	})
	if _, err := loadConfig(long); err != nil {
		t.Fatalf("32 caracteres en release debe aceptarse: %v", err)
	}
	// Solo en desarrollo explícito alcanza con que exista: el default es
	// producción, así que un secreto corto sin GIN_MODE tampoco arranca.
	if _, err := loadConfig(loadEnv(map[string]string{"JWT_SECRET": "corto"})); err == nil {
		t.Fatal("sin GIN_MODE el modo es release: un secreto corto debe rechazarse")
	}
	dev := loadEnv(map[string]string{"JWT_SECRET": "corto", "GIN_MODE": "debug"})
	if _, err := loadConfig(dev); err != nil {
		t.Fatalf("en dev un secreto corto es válido: %v", err)
	}
	test := loadEnv(map[string]string{"JWT_SECRET": "corto", "GIN_MODE": "test"})
	if _, err := loadConfig(test); err != nil {
		t.Fatalf("en test un secreto corto es válido: %v", err)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	cfg, err := loadConfig(loadEnv(map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("PORT default = 8080, dio %q", cfg.Port)
	}
	if cfg.MediaDir != "./media" {
		t.Fatalf("MEDIA_DIR default = ./media, dio %q", cfg.MediaDir)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "http://localhost:5173" || cfg.CORSOrigins[1] != "http://localhost:4173" {
		t.Fatalf("CORS_ORIGINS default: %v", cfg.CORSOrigins)
	}
	if cfg.DatabaseURL == "" {
		t.Fatal("DATABASE_URL tiene default de desarrollo")
	}
}

// CORS_ORIGINS es una lista separada por comas; se recorta y se ignoran vacíos.
func TestLoadConfig_cors_origins(t *testing.T) {
	cfg, err := loadConfig(loadEnv(map[string]string{
		"JWT_SECRET":   "secreto",
		"GIN_MODE":     "debug",
		"CORS_ORIGINS": " https://app.salon.com , https://admin.salon.com ,, ",
	}))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Fatalf("esperaba 2 orígenes, dio %v", cfg.CORSOrigins)
	}
	if cfg.CORSOrigins[0] != "https://app.salon.com" || cfg.CORSOrigins[1] != "https://admin.salon.com" {
		t.Fatalf("orígenes mal parseados: %v", cfg.CORSOrigins)
	}
}

// loadEnv arma el getenv a partir de un mapa (el proceso no toca el entorno real).
func loadEnv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}
