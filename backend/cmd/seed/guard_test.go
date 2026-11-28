package main

import (
	"errors"
	"net/url"
	"testing"
)

// baseConfig es una configuración válida de desarrollo; cada test la rompe en
// una sola cosa.
func baseConfig() config {
	return config{
		APIURL: "http://localhost:8080",
		DBURL:  "postgres://salon:salon@localhost:5432/salon?sslmode=disable",
		Demo:   true,
		Seed:   defaultSeed,
	}
}

func TestCheckGuards(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr error
	}{
		{name: "config de desarrollo", mutate: func(*config) {}},
		{name: "sin SEED_DEMO", mutate: func(c *config) { c.Demo = false }, wantErr: ErrDemoDisabled},
		{
			name:    "base remota sin SEED_FORCE",
			mutate:  func(c *config) { c.DBURL = "postgres://salon:secreto@db.produccion.com:5432/salon" },
			wantErr: ErrNotLocalDB,
		},
		{
			name:   "base remota con SEED_FORCE",
			mutate: func(c *config) { c.DBURL = "postgres://salon:secreto@db.produccion.com:5432/salon"; c.Force = true },
		},
		{
			name:   "host del compose",
			mutate: func(c *config) { c.DBURL = "postgres://salon:salon@postgres:5432/salon?sslmode=disable" },
		},
		{
			name:   "host.docker.internal",
			mutate: func(c *config) { c.DBURL = "postgres://salon:salon@host.docker.internal:5432/salon" },
		},
		{
			name:   "loopback IPv6",
			mutate: func(c *config) { c.DBURL = "postgres://salon:salon@[::1]:5432/salon" },
		},
		{
			name:    "URL sin host",
			mutate:  func(c *config) { c.DBURL = "no-es-una-url" },
			wantErr: ErrBadDBURL,
		},
		{
			name:    "esquema que no es postgres",
			mutate:  func(c *config) { c.DBURL = "mysql://salon:salon@localhost/salon" },
			wantErr: ErrBadDBURL,
		},
		{
			name:    "API que no es http",
			mutate:  func(c *config) { c.APIURL = "ftp://localhost:8080" },
			wantErr: ErrBadAPIURL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			tt.mutate(&cfg)
			err := CheckGuards(cfg)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("esperaba que pasara: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("esperaba %v y dio %v", tt.wantErr, err)
			}
		})
	}
}

func TestCheckGuardsMencionaElHost(t *testing.T) {
	cfg := baseConfig()
	u, _ := url.Parse(cfg.DBURL)
	u.Host = "db.example.com:5432"
	cfg.DBURL = u.String()
	err := CheckGuards(cfg)
	if err == nil || !contains(err.Error(), "db.example.com") {
		t.Fatalf("el error tiene que nombrar el host: %v", err)
	}
}

func TestParseAPIURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://localhost:8080/", "http://localhost:8080"},
		{"http://localhost:8080/api/v1", "http://localhost:8080"},
		{"http://localhost:8080/api/v1/", "http://localhost:8080"},
		{" http://api:8080 ", "http://api:8080"},
	}
	for _, tt := range tests {
		got, err := ParseAPIURL(tt.in)
		if err != nil {
			t.Fatalf("ParseAPIURL(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseAPIURL(%q) = %q, quería %q", tt.in, got, tt.want)
		}
	}
	if _, err := ParseAPIURL(""); !errors.Is(err, ErrBadAPIURL) {
		t.Fatalf("una URL vacía tiene que fallar: %v", err)
	}
}

func TestEnvConfigDefaults(t *testing.T) {
	env := map[string]string{"SEED_DEMO": "TRUE", "SEED_RNG_SEED": "7"}
	cfg, err := envConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("envConfig: %v", err)
	}
	if !cfg.Demo {
		t.Fatal("SEED_DEMO=TRUE tiene que habilitar la demo")
	}
	if cfg.Seed != 7 {
		t.Fatalf("semilla %d, quería 7", cfg.Seed)
	}
	if cfg.DBURL == "" || cfg.APIURL != "http://localhost:8080" {
		t.Fatalf("defaults inesperados: %+v", cfg)
	}
	if _, err := envConfig(func(key string) string {
		if key == "SEED_RNG_SEED" {
			return "no-es-un-numero"
		}
		return ""
	}); err == nil {
		t.Fatal("una semilla ilegible tiene que ser error")
	}
}

// contains es un contains de strings sin importar strings en el test.
func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
