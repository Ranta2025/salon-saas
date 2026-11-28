package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Los tres seguros del seed. Los dos primeros son la red de seguridad: sin
// SEED_DEMO=true el programa no hace nada, y contra una base que no es local
// tampoco (a menos que se pise con SEED_FORCE=1, porque escribir la demo en una
// base compartida es exactamente el accidente que hay que evitar).
var (
	// ErrDemoDisabled: falta SEED_DEMO=true.
	ErrDemoDisabled = errors.New("seed: SEED_DEMO no está en true: no hago nada (es el seguro para que este binario nunca escriba en producción)")
	// ErrNotLocalDB: el host de DATABASE_URL no es de desarrollo.
	ErrNotLocalDB = errors.New("seed: DATABASE_URL no apunta a una base local: solo permito localhost, 127.0.0.1, ::1, postgres o host.docker.internal (para pisarlo a propósito: SEED_FORCE=1)")
	// ErrBadDBURL: DATABASE_URL no es una URL de Postgres válida.
	ErrBadDBURL = errors.New("seed: DATABASE_URL no es una URL válida")
	// ErrBadAPIURL: SEED_API_URL no es una URL http(s) válida.
	ErrBadAPIURL = errors.New("seed: SEED_API_URL no es una URL http(s) válida")
)

// localDBHosts son los hosts que el seed acepta sin SEED_FORCE: el developer
// local y el nombre del servicio dentro del compose.
var localDBHosts = []string{"localhost", "127.0.0.1", "::1", "postgres", "host.docker.internal"}

// CheckGuards valida la configuración ANTES de tocar la red o la base. Es puro:
// no lee el entorno, no abre conexiones y por eso se puede testear entero.
func CheckGuards(cfg config) error {
	if !cfg.Demo {
		return ErrDemoDisabled
	}
	host, err := DBHost(cfg.DBURL)
	if err != nil {
		return err
	}
	if !cfg.Force && !IsLocalDBHost(host) {
		return fmt.Errorf("%w (host %q)", ErrNotLocalDB, host)
	}
	if _, err := ParseAPIURL(cfg.APIURL); err != nil {
		return err
	}
	return nil
}

// DBHost devuelve el host de DATABASE_URL sin puerto. Una URL sin host (o un
// error de parseo) es ErrBadDBURL: no se adivina a dónde conectarse.
func DBHost(dbURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(dbURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ErrBadDBURL
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		return "", fmt.Errorf("%w (esquema %q, se esperaba postgres)", ErrBadDBURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", ErrBadDBURL
	}
	return strings.ToLower(host), nil
}

// IsLocalDBHost dice si un host es uno de los permitidos sin SEED_FORCE.
func IsLocalDBHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, allowed := range localDBHosts {
		if host == allowed {
			return true
		}
	}
	return false
}

// ParseAPIURL valida la base de la API y la devuelve normalizada (sin la barra
// final y sin /api/v1, que lo agrega el cliente).
func ParseAPIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrBadAPIURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", ErrBadAPIURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrBadAPIURL
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api/v1")
	return u.Scheme + "://" + u.Host + path, nil
}
