package httpapi

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// IPRateLimiter limita por IP con una ventana fija (B-07): defensa contra
// fuerza bruta de claves y creación masiva de cuentas.
//
// Es un contador en memoria: alcanza para una instancia. Con varias réplicas
// hay que mover el estado a Redis; el contrato (Allow) no cambia.
type IPRateLimiter struct {
	limit  int
	window time.Duration
	mx     sync.Mutex
	hits   map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// NewIPRateLimiter: limit pedidos por IP por ventana.
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	return &IPRateLimiter{limit: limit, window: window, hits: map[string]*bucket{}}
}

// Allow consume un intento de la IP y dice si pasa. Los buckets vencidos se
// reciclan en el mismo paso: no hace falta un goroutine de limpieza para el
// volumen de esta API.
func (rl *IPRateLimiter) Allow(ip string) bool {
	now := time.Now()
	rl.mx.Lock()
	defer rl.mx.Unlock()
	b, ok := rl.hits[ip]
	if !ok || now.After(b.reset) {
		rl.hits[ip] = &bucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// Middleware aplica el límite solo a las rutas que empiezan con alguno de los
// prefijos (p. ej. /api/v1/auth/): el resto de la API no se toca.
func (rl *IPRateLimiter) Middleware(prefixes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasPrefix(c.Request.URL.Path, prefixes) {
			c.Next()
			return
		}
		if !rl.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(429, gin.H{"error": "demasiados intentos, esperá un minuto y volvé a probar"})
			return
		}
		c.Next()
	}
}

// RateLimit es el atajo del composition root y de los tests:
//
//	router.Use(RateLimit(10, time.Minute, "/api/v1/auth/"))
func RateLimit(limit int, window time.Duration, prefixes ...string) gin.HandlerFunc {
	return NewIPRateLimiter(limit, window).Middleware(prefixes...)
}

func hasPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
