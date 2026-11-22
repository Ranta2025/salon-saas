package main

import (
	"strings"
	"testing"
	"time"
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

// T12: configuración del email y del intervalo de recordatorios.
func TestLoadConfig_SMTP(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}
	cfg, err := loadConfig(loadEnv(base))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	// Sin SMTP_HOST el email está deshabilitado, pero el resto de defaults
	// existen para que docker-compose y la documentación sean coherentes.
	if cfg.SMTPHost != "" || cfg.SMTPPort != "25" || cfg.SMTPTLS != "none" ||
		cfg.SMTPFrom != "turnos@salon.local" || cfg.ReminderInterval != 60*time.Second {
		t.Fatalf("defaults SMTP: %+v", cfg)
	}

	full := map[string]string{
		"JWT_SECRET": "secreto", "GIN_MODE": "debug",
		"SMTP_HOST": "mailpit", "SMTP_PORT": "1025", "SMTP_USER": "u",
		"SMTP_PASS": "p", "SMTP_FROM": "turnos@salon.local", "SMTP_TLS": "starttls",
		"REMINDER_INTERVAL": "2s",
	}
	cfg, err = loadConfig(loadEnv(full))
	if err != nil {
		t.Fatalf("config SMTP: %v", err)
	}
	if cfg.SMTPHost != "mailpit" || cfg.SMTPPort != "1025" || cfg.SMTPUser != "u" ||
		cfg.SMTPPass != "p" || cfg.SMTPFrom != "turnos@salon.local" || cfg.SMTPTLS != "starttls" ||
		cfg.ReminderInterval != 2*time.Second {
		t.Fatalf("config SMTP: %+v", cfg)
	}

	// Valores inválidos: error de configuración, no default silencioso.
	for name, values := range map[string]map[string]string{
		"SMTP_TLS": {
			"JWT_SECRET": "secreto", "GIN_MODE": "debug", "SMTP_TLS": "ssl",
		},
		"REMINDER_INTERVAL": {
			"JWT_SECRET": "secreto", "GIN_MODE": "debug", "REMINDER_INTERVAL": "cada rato",
		},
	} {
		_, err := loadConfig(loadEnv(values))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s inválido debe fallar y nombrar la variable, dio %v", name, err)
		}
	}
	// Un intervalo negativo o cero no tiene sentido (ticker imposible).
	if _, err := loadConfig(loadEnv(map[string]string{
		"JWT_SECRET": "secreto", "GIN_MODE": "debug", "REMINDER_INTERVAL": "0s",
	})); err == nil {
		t.Fatal("REMINDER_INTERVAL=0s debe fallar")
	}
}

// S-F2g1-5: APP_URL es la base del enlace de invitación. Tiene default de
// desarrollo, se le quita la barra final (el enlace agrega "/invitacion#...") y
// un valor que no es una URL http(s) impide arrancar: un enlace roto en un correo
// no se puede arreglar después.
func TestLoadConfig_app_url(t *testing.T) {
	base := map[string]string{"JWT_SECRET": strings.Repeat("s", 32), "GIN_MODE": "release"}

	cfg, err := loadConfig(loadEnv(base))
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if cfg.AppURL != "http://localhost:8081" {
		t.Fatalf("APP_URL por defecto: %q", cfg.AppURL)
	}
	withApp := map[string]string{}
	for k, v := range base {
		withApp[k] = v
	}
	withApp["APP_URL"] = "https://turnos.misalon.com/"
	cfg, err = loadConfig(loadEnv(withApp))
	if err != nil {
		t.Fatalf("APP_URL propia: %v", err)
	}
	if cfg.AppURL != "https://turnos.misalon.com" {
		t.Fatalf("la barra final se quita: %q", cfg.AppURL)
	}
	for _, bad := range []string{"misalon.com", "ftp://misalon.com", "http://", "/invitacion"} {
		values := map[string]string{}
		for k, v := range base {
			values[k] = v
		}
		values["APP_URL"] = bad
		if _, err := loadConfig(loadEnv(values)); err == nil {
			t.Fatalf("APP_URL %q tiene que rechazarse", bad)
		} else if !strings.Contains(err.Error(), "APP_URL") {
			t.Fatalf("el error tiene que nombrar la variable: %v", err)
		}
	}
}
