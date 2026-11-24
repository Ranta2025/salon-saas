package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/adapters/mail"
)

// minJWTSecretLen es el mínimo en release: HS256 con menos de 32 caracteres se
// rompe por fuerza bruta.
const minJWTSecretLen = 32

// trustedProxiesPrivate es el valor de TRUSTED_PROXIES que mantiene el
// comportamiento histórico: loopback + redes privadas (el nginx del compose o
// de la red Docker están ahí).
const trustedProxiesPrivate = "private"

// privateProxies son los rangos de trustedProxiesPrivate.
var privateProxies = []string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// config es la configuración del proceso, leída UNA vez al arrancar.
// Cada variable tiene un default de desarrollo salvo JWT_SECRET: sin secreto el
// proceso no arranca (B-02), porque el default histórico permitía forjar tokens.
type config struct {
	DatabaseURL string
	JWTSecret   string
	CORSOrigins []string
	// TrustedProxies: de qué peers se acepta X-Forwarded-For (TRUSTED_PROXIES).
	// Vacío = no se confía en nadie: la IP del cliente es el peer directo.
	TrustedProxies []string
	MediaDir       string
	Port           string
	GinMode        string
	// Almacenamiento de fotos: si S3_ENDPOINT está definido se usa S3/MinIO.
	// Sin endpoint, el adaptador de disco local (MEDIA_DIR).
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
	// Email a clientes (T12): confirmación de la reserva y recordatorios.
	// SMTP_HOST vacío = email deshabilitado (no-op): el resto de valores igual
	// existen para que la configuración sea explícita.
	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	SMTPTLS  string
	// ReminderInterval es la cadencia de la tarea de recordatorios, en duración
	// de Go: 60s en producción, 2s para probar en desarrollo.
	ReminderInterval time.Duration
	// WaitlistInterval es la cadencia del barrido de la lista de espera (F7),
	// en duración de Go: 60s en producción. CERO la DESHABILITA (el contrato lo
	// pide así): con 0 la goroutine no arranca y la lista queda solo como
	// registro, sin avisos por email.
	WaitlistInterval time.Duration
	// AppURL es la base pública del frontend: con ella se arma el enlace de la
	// invitación de equipo (F2g1), "<APP_URL>/invitacion#<token>".
	AppURL string
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
		SMTPHost:    strings.TrimSpace(getenv("SMTP_HOST")),
		SMTPPort:    envOr(getenv, "SMTP_PORT", "25"),
		SMTPUser:    strings.TrimSpace(getenv("SMTP_USER")),
		SMTPPass:    getenv("SMTP_PASS"),
		SMTPFrom:    envOr(getenv, "SMTP_FROM", "turnos@salon.local"),
		SMTPTLS:     strings.ToLower(envOr(getenv, "SMTP_TLS", mail.TLSNone)),
	}
	useSSL, err := envBool(getenv, "S3_USE_SSL", false)
	if err != nil {
		return config{}, err
	}
	cfg.S3UseSSL = useSSL
	// Proxies confiables (H3): vacío = no confiar en nadie (la IP del cliente
	// es el peer directo), "private" = los rangos privados de siempre, o una
	// lista de IPs/CIDR separada por comas. Se valida acá: un valor inválido
	// impide arrancar en vez de confiar en un proxy que no existe.
	proxies, err := parseTrustedProxies(getenv("TRUSTED_PROXIES"))
	if err != nil {
		return config{}, err
	}
	cfg.TrustedProxies = proxies
	// El transporte del correo se elige por env: un valor desconocido es error
	// de configuración, no un default silencioso (mandaría en claro cuando se
	// pedía TLS).
	switch cfg.SMTPTLS {
	case mail.TLSNone, mail.TLSStartTLS, mail.TLSImplicit:
	default:
		return config{}, fmt.Errorf("SMTP_TLS debe ser none, starttls o tls (dio %q)", cfg.SMTPTLS)
	}
	interval, err := time.ParseDuration(envOr(getenv, "REMINDER_INTERVAL", "60s"))
	if err != nil {
		return config{}, fmt.Errorf("REMINDER_INTERVAL debe ser una duración de Go (por ejemplo 60s o 2s): %w", err)
	}
	if interval <= 0 {
		return config{}, fmt.Errorf("REMINDER_INTERVAL debe ser mayor que cero (dio %s)", interval)
	}
	cfg.ReminderInterval = interval
	// WAITLIST_INTERVAL: el barrido de la lista de espera (F7). A diferencia del
	// de recordatorios, CERO es un valor válido (deshabilita la tarea); un valor
	// negativo o ilegible es error de configuración, no un default silencioso.
	waitlist, err := time.ParseDuration(envOr(getenv, "WAITLIST_INTERVAL", "60s"))
	if err != nil {
		return config{}, fmt.Errorf("WAITLIST_INTERVAL debe ser una duración de Go (por ejemplo 60s o 2s; 0 la deshabilita): %w", err)
	}
	if waitlist < 0 {
		return config{}, fmt.Errorf("WAITLIST_INTERVAL no puede ser negativo (0 deshabilita la tarea, dio %s)", waitlist)
	}
	cfg.WaitlistInterval = waitlist
	// APP_URL es la base del enlace de la invitación (F2g1): sin una URL http(s)
	// válida el correo llevaría a un lugar que no existe, y eso no se arregla
	// después de enviado. La barra final se quita porque el enlace agrega
	// "/invitacion#<token>".
	appURL, err := parseAppURL(envOr(getenv, "APP_URL", "http://localhost:8081"))
	if err != nil {
		return config{}, err
	}
	cfg.AppURL = appURL
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

// parseAppURL valida la base pública del frontend. Tiene que ser una URL
// absoluta http(s) con host: un enlace de invitación roto no se puede corregir
// después de mandado el correo.
func parseAppURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("APP_URL debe ser una URL http(s) absoluta (por ejemplo http://localhost:8081): %q", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

// parseTrustedProxies lee TRUSTED_PROXIES: lista de IPs/CIDR separadas por
// comas, "private" para los rangos privados de siempre, o vacío = no confiar en
// ningún proxy (la IP del cliente es el peer directo). Cualquier otra cosa es
// error de configuración: confiar en un proxy mal escrito es no confiar en
// nadie y enterarse recién cuando el rate limit cuenta a todos igual.
func parseTrustedProxies(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.EqualFold(raw, trustedProxiesPrivate) {
		return append([]string(nil), privateProxies...), nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		ippart := strings.TrimSpace(part)
		if ippart == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(ippart); err != nil && net.ParseIP(ippart) == nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q no es una IP ni un CIDR (ejemplo: 172.18.0.0/16,203.0.113.7; o \"private\")", ippart)
		}
		out = append(out, ippart)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %q no tiene ninguna IP ni CIDR (ejemplo: 172.18.0.0/16,203.0.113.7; o \"private\")", raw)
	}
	return out, nil
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
