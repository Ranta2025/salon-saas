package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// H3: TRUSTED_PROXIES elige en qué proxies confiar.
//   - vacío (default): no se confía en nadie, la IP del cliente es el peer;
//   - "private": los rangos privados de siempre (compose/nginx en la misma red);
//   - lista de IPs/CIDR separada por comas.
func TestLoadConfig_trusted_proxies(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}

	cfg, err := loadConfig(loadEnv(base))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("vacío = no confiar en ningún proxy, dio %v", cfg.TrustedProxies)
	}

	privados := loadEnv(map[string]string{
		"JWT_SECRET": "secreto", "GIN_MODE": "debug", "TRUSTED_PROXIES": "Private",
	})
	cfg, err = loadConfig(privados)
	if err != nil {
		t.Fatalf("private: %v", err)
	}
	want := []string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("private debe dar los rangos privados, dio %v", cfg.TrustedProxies)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Fatalf("private = %v, esperaba %v", cfg.TrustedProxies, want)
		}
	}

	lista := loadEnv(map[string]string{
		"JWT_SECRET": "secreto", "GIN_MODE": "debug",
		"TRUSTED_PROXIES": " 172.18.0.0/16 , 203.0.113.7 ,, ",
	})
	cfg, err = loadConfig(lista)
	if err != nil {
		t.Fatalf("lista: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "172.18.0.0/16" || cfg.TrustedProxies[1] != "203.0.113.7" {
		t.Fatalf("IPs/CIDR separados por comas con espacios: %v", cfg.TrustedProxies)
	}

	// Valor inválido: el proceso no arranca y el error nombra la variable.
	for _, malo := range []string{"nginx", "10.0.0.0/33", "172.18.0.0/16, proxy-acme", ",,,"} {
		_, err := loadConfig(loadEnv(map[string]string{
			"JWT_SECRET": "secreto", "GIN_MODE": "debug", "TRUSTED_PROXIES": malo,
		}))
		if err == nil {
			t.Fatalf("TRUSTED_PROXIES=%q debe fallar", malo)
		}
		if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
			t.Fatalf("el error debe nombrar la variable (%q): %v", malo, err)
		}
		if !strings.Contains(err.Error(), "IP") || !strings.Contains(err.Error(), "CIDR") {
			t.Fatalf("el error debe explicar el formato esperado (%q): %v", malo, err)
		}
	}
}

// H3: la IP del cliente sale del peer directo salvo que ese peer sea un proxy
// confiable. Con la lista vacía, X-Forwarded-For es una cabecera que cualquiera
// inventa y no se mira; con el proxy confiable, sí.
func TestClientIP_con_y_sin_proxy_confiable(t *testing.T) {
	casos := []struct {
		nombre  string
		proxies []string
		peer    string
		xff     string
		want    string
	}{
		{
			nombre: "sin proxies: el peer es el cliente y X-Forwarded-For se ignora",
			peer:   "203.0.113.9:1234", xff: "1.2.3.4", want: "203.0.113.9",
		},
		{
			nombre:  "peer confiable: gana X-Forwarded-For",
			proxies: []string{"10.0.0.0/8"}, peer: "10.1.2.3:5678", xff: "198.51.100.7", want: "198.51.100.7",
		},
		{
			nombre:  "peer fuera de la lista: X-Forwarded-For falseado se ignora",
			proxies: []string{"10.0.0.0/8"}, peer: "203.0.113.9:1234", xff: "1.2.3.4", want: "203.0.113.9",
		},
		{
			nombre:  "private (comportamiento viejo): el bridge de Docker es confiable",
			proxies: []string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
			peer:    "172.18.0.5:40000", xff: "198.51.100.7", want: "198.51.100.7",
		},
	}
	for _, c := range casos {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		if err := r.SetTrustedProxies(c.proxies); err != nil {
			t.Fatalf("%s: SetTrustedProxies: %v", c.nombre, err)
		}
		var visto string
		r.GET("/ip", func(ctx *gin.Context) { visto = ctx.ClientIP() })

		req := httptest.NewRequest("GET", "/ip", nil)
		req.RemoteAddr = c.peer
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", c.nombre, w.Code)
		}
		if visto != c.want {
			t.Fatalf("%s: ClientIP() = %q, esperaba %q", c.nombre, visto, c.want)
		}
	}
}
