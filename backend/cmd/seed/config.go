// Package main es el seed de DEMO de Salón SaaS.
//
// Crea, por la API real y por SQL cuando la API no puede (fechas pasadas), un
// salón completo para que el panel y la landing se vean con datos de verdad: un
// equipo con roles y logins que funcionan, doce servicios, clientas con
// historial, turnos de los últimos 60 días y de los próximos 14, ventas, caja,
// gastos, nómina, paquetes, fidelidad, lista de espera, señas con sus estados,
// reseñas y avisos.
//
// Es IDEMPOTENTE: detecta la demo ya sembrada y no escribe nada. Y es una
// herramienta de DESARROLLO: los tres seguros (SEED_DEMO, host de la base y
// SEED_FORCE) están en guard.go.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// config es lo que el seed lee del entorno. Todo tiene default de desarrollo
// salvo el flag de seguridad: sin SEED_DEMO=true el programa no hace nada.
type config struct {
	// APIURL es la base de la API SIN /api/v1 (se agrega acá).
	APIURL string
	// DBURL es DATABASE_URL: la misma base que usa la API.
	DBURL string
	// Demo habilita el seed (SEED_DEMO=true).
	Demo bool
	// Force saltea el control de host de la base (SEED_FORCE=1).
	Force bool
	// AssetsDir es la carpeta con las fotos versionadas (scripts/demo-assets).
	AssetsDir string
	// AppURL es la base del frontend, solo para el enlace final del reporte.
	AppURL string
	// Seed es la semilla del RNG: el dataset es siempre el mismo.
	Seed int64
	// Timeout corta una corrida colgada.
	Timeout time.Duration
}

const (
	// demoSuffix marca los emails de las clientas ficticias.
	demoEmailDomain = "@demo.test"
	// demoPassword es la clave de TODOS los logins de la demo.
	demoPassword = "SalonDemo2026!"
	// demoSalonSlug es el slug público (la landing abre con él).
	demoSalonSlug = "aurelia"
	// demoSolTimezones...
	// demoTZ es la zona horaria del salón en la demo.
	demoTZ = "America/Argentina/Buenos_Aires"
	// defaultSeed es la semilla por defecto: cambiar el valor cambia el dataset.
	defaultSeed = 20260101
)

// envConfig arma la configuración desde el entorno (más los flags que se pasan).
func envConfig(getenv func(string) string) (config, error) {
	cfg := config{
		APIURL:    strings.TrimRight(envOr(getenv, "SEED_API_URL", envOr(getenv, "API_URL", "http://localhost:8080")), "/"),
		DBURL:     strings.TrimSpace(getenv("DATABASE_URL")),
		Demo:      strings.EqualFold(strings.TrimSpace(getenv("SEED_DEMO")), "true"),
		Force:     isTruthy(getenv("SEED_FORCE")),
		AssetsDir: strings.TrimSpace(getenv("SEED_ASSETS_DIR")),
		AppURL:    strings.TrimSpace(getenv("APP_URL")),
		Seed:      defaultSeed,
		Timeout:   10 * time.Minute,
	}
	if cfg.DBURL == "" {
		cfg.DBURL = "postgres://salon:salon@localhost:5432/salon?sslmode=disable"
	}
	if raw := strings.TrimSpace(getenv("SEED_RNG_SEED")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return config{}, fmt.Errorf("SEED_RNG_SEED debe ser un entero (dio %q)", raw)
		}
		cfg.Seed = n
	}
	if raw := strings.TrimSpace(getenv("SEED_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("SEED_TIMEOUT debe ser una duración de Go (por ejemplo 10m): %w", err)
		}
		cfg.Timeout = d
	}
	return cfg, nil
}

// isTruthy acepta las formas habituales de decir "sí" en un .env.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "si", "sí", "on":
		return true
	}
	return false
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := log.New(os.Stdout, "seed: ", log.LstdFlags)

	flagHelp := flag.Bool("h", false, "muestra esta ayuda")
	flag.CommandLine.Usage = func() {
		fmt.Fprintf(os.Stderr, `seed de demo de Salón SaaS

Crea (o saltea, si ya existe) un salón de demo completo contra una API y su
Postgres. Pensado para desarrollo: no se corre en producción.

Variables:
  SEED_DEMO=true        obligatorio: sin esto no hace nada
  DATABASE_URL=...      base de la API (host local, o SEED_FORCE=1)
  SEED_API_URL=...      base de la API (default http://localhost:8080)
  SEED_ASSETS_DIR=...   carpeta de fotos (default: busca scripts/demo-assets)
  SEED_FORCE=1          permite una base que NO es local (¡peligro!)
  SEED_RNG_SEED=...     semilla del dataset determinista (default %d)
  SEED_TIMEOUT=10m      corte de seguridad de la corrida

Uso:
  SEED_DEMO=true DATABASE_URL=... go run ./cmd/seed      # desde backend/
  make seed                                              # stack de Docker
`, defaultSeed)
	}
	_ = flagHelp
	flag.Parse()

	if *flagHelp {
		flag.Usage()
		return
	}

	cfg, err := envConfig(os.Getenv)
	if err != nil {
		logger.Fatalf("%v", err)
	}
	if err := CheckGuards(cfg); err != nil {
		logger.Fatalf("%v", err)
	}

	if err := run(cfg, logger); err != nil {
		logger.Fatalf("error: %v", err)
	}
	logger.Printf("listo")
}
