package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	httpapi "salon-saas/internal/adapters/http"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newHardenedRouter arma un router con TODOS los middlewares de producción
// (httpapi.CORS, tope de body, logger con redacción) y la misma tabla de rutas.
func newHardenedRouter(origins []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	env := httpkit.NewPOSEnv()
	r := gin.New()
	r.Use(httpapi.CORS(origins...), httpapi.MaxJSONBody(1<<20), httpapi.RequestLogger(gin.DefaultWriter))
	httpapi.RegisterRoutes(r, env.Deps)
	return r
}

// B-01: sin httpapi.CORS el login falla en el navegador (el preflight daba 404).
func TestCORS_preflight_y_respuesta_real(t *testing.T) {
	r := newHardenedRouter([]string{"http://localhost:5173", "http://localhost:4173"})

	// Preflight del navegador antes del login.
	req := httptest.NewRequest("OPTIONS", "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight debe dar 204, dio %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") || !strings.Contains(got, "OPTIONS") {
		t.Fatalf("Allow-Methods debe incluir POST y OPTIONS, dio %q", got)
	}
	headers := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(headers, "authorization") || !strings.Contains(headers, "content-type") {
		t.Fatalf("Allow-Headers debe incluir Authorization y Content-Type, dio %q", headers)
	}
	if w.Header().Get("Vary") == "" {
		t.Fatal("la respuesta httpapi.CORS debe declarar Vary (caché por origen)")
	}

	// La respuesta real (no preflight) también lleva el header, si no el
	// navegador descarta el body.
	req = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@b.co","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4173")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4173" {
		t.Fatalf("respuesta real sin Allow-Origin: %q (%d)", got, w.Code)
	}
}

// Un origen que NO está en la allowlist no recibe permisos: el navegador
// bloquea la lectura y el preflight se corta acá.
func TestCORS_origen_no_permitido(t *testing.T) {
	r := newHardenedRouter([]string{"http://localhost:5173"})

	req := httptest.NewRequest("OPTIONS", "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("preflight de origen ajeno debe dar 403, dio %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("no debe permitir el origen ajeno, dio %q", got)
	}

	// Petición real de un origen ajeno: pasa (es un curl/bot), pero SIN httpapi.CORS,
	// así el navegador no puede leer la respuesta.
	req = httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no debe haber Allow-Origin para un origen ajeno")
	}
}

// B-07: el login y el registro tienen rate limit por IP.
func TestRateLimit_login_por_ip(t *testing.T) {
	// Router nuevo con el mismo cableado y el limiter aplicado a /auth/.
	r := gin.New()
	env := httpkit.NewPOSEnv()
	r.Use(httpapi.RateLimit(2, time.Minute, "/api/v1/auth/"))
	httpapi.RegisterRoutes(r, env.Deps)
	login := `{"email":"nadie@mail.com","password":"otraClave1"}`
	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(login))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
		if i == 2 {
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Fatalf("429 debe traer un mensaje claro: %s", w.Body.String())
			}
		}
	}
	if codes[0] == http.StatusTooManyRequests || codes[1] == http.StatusTooManyRequests {
		t.Fatalf("los primeros intentos no deben limitarse: %v", codes)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("el tercer intento en la ventana debe dar 429, dio %v", codes)
	}
	// Otra IP no está limitada.
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(login))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.7:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusTooManyRequests {
		t.Fatal("otra IP no debe estar limitada")
	}
}

// B-08: el token del websocket viaja por query param y no puede quedar escrito
// en los logs de acceso (quien lee los logs entra como esa persona).
func TestRequestLogger_redacta_token(t *testing.T) {
	var buf bytes.Buffer
	prev := gin.DefaultWriter
	gin.DefaultWriter = &buf
	defer func() { gin.DefaultWriter = prev }()

	r := gin.New()
	r.Use(httpapi.RequestLogger(&buf))
	r.GET("/api/v1/ws", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest("GET", "/api/v1/ws?token=super-secreto-jwt&otro=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	logged := buf.String()
	if strings.Contains(logged, "super-secreto-jwt") {
		t.Fatalf("el token no puede aparecer en el log: %q", logged)
	}
	if !strings.Contains(logged, "ws") {
		t.Fatalf("el log debe registrar la ruta: %q", logged)
	}
	if !strings.Contains(logged, "otro=1") {
		t.Fatalf("los demás parámetros se conservan: %q", logged)
	}
}

// F13 verifier V1 BLOCKER: the review link token travels as a PATH segment, so the
// access log must never print it (a token read from the log redeemed the link and
// posted a review). Matched routes, unmatched ones (trailing slash redirect) and
// both methods are covered; the route prefix stays so the log is still useful.
func TestRequestLogger_redactaElTokenDeResenasEnLaRuta(t *testing.T) {
	const tok = "7eHLkjolJZjCOqtMAteCvVGrKjlxReo-MRsqUkIs2gQ"
	var buf bytes.Buffer
	r := gin.New()
	r.Use(httpapi.RequestLogger(&buf))
	r.GET("/api/v1/public/reviews/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/v1/public/reviews/:token", func(c *gin.Context) { c.Status(http.StatusCreated) })

	for _, target := range []struct{ method, path string }{
		{"GET", "/api/v1/public/reviews/" + tok},
		{"POST", "/api/v1/public/reviews/" + tok},
		{"GET", "/api/v1/public/reviews/" + tok + "/"}, // unmatched: redirect or 404
		{"GET", "/api/v1/public/reviews/" + tok + "?x=1"},
	} {
		buf.Reset()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(target.method, target.path, nil))
		logged := buf.String()
		if strings.Contains(logged, tok) {
			t.Fatalf("%s %s: el token no puede aparecer en el log: %q", target.method, target.path, logged)
		}
		// A trailing-slash redirect is answered by gin before the middlewares run, so
		// it may leave no log line at all; when there is one it keeps the prefix.
		if logged != "" && !strings.Contains(logged, "/api/v1/public/reviews/") {
			t.Fatalf("%s %s: el log conserva la ruta: %q", target.method, target.path, logged)
		}
	}
	// Other routes are logged unchanged.
	buf.Reset()
	r.GET("/api/v1/public/salons/:slug", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/public/salons/mi-salon", nil))
	if !strings.Contains(buf.String(), "/api/v1/public/salons/mi-salon") {
		t.Fatalf("el resto de las rutas se loguea igual: %q", buf.String())
	}
}

// B-11/H1: un JSON de 2 MB se rechaza antes de parsearlo. El tope es por
// RUTA, así que da igual el Content-Type que mande el cliente (el bypass por
// "multipart/form-data" está cubierto en body_limit_test.go).
func TestMaxJSONBody(t *testing.T) {
	r := gin.New()
	r.Use(httpapi.MaxJSONBody(1 << 20))
	r.POST("/x", func(c *gin.Context) {
		var in map[string]any
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
			return
		}
		c.Status(http.StatusOK)
	})
	big := `{"a":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/x", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body de 2 MB debe dar 413, dio %d", w.Code)
	}

	// Un JSON chico sigue entrando.
	req = httptest.NewRequest("POST", "/x", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("body chico debe pasar, dio %d %s", w.Code, w.Body.String())
	}

	// El multipart NO es una exención: mismo tope, misma respuesta.
	req = httptest.NewRequest("POST", "/x", strings.NewReader(big))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("el tope de 1 MB se aplica por ruta, también al multipart: dio %d", w.Code)
	}
}

// B-14: un 500 nunca filtra el detalle interno (SQL, nombre de tabla) y deja el
// detalle en el log.
func TestRespondErr_500_generico(t *testing.T) {
	var buf bytes.Buffer
	logf := func(format string, args ...any) {
		buf.WriteString(fmt.Sprintf(format, args...))
		buf.WriteByte('\n')
	}

	r := gin.New()
	r.GET("/boom", func(c *gin.Context) {
		httpapi.RespondErrLogf(c, httpkit.ErrFakeInfra, logf)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("esperaba 500, dio %d", w.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v (%s)", err, w.Body.String())
	}
	if body.Error != "error interno" {
		t.Fatalf("el 500 debe ser genérico, dio %q", body.Error)
	}
	if strings.Contains(w.Body.String(), "pq:") || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("el body no puede llevar el detalle: %s", w.Body.String())
	}
	if !strings.Contains(buf.String(), "pq:") {
		t.Fatalf("el detalle debe ir al log: %q", buf.String())
	}
}

// TestCORS_permite_todos_los_metodos_registrados: el preflight tiene que
// aceptar CADA método HTTP que el router expone. Un navegador bloquea en el
// cliente cualquier método que falte en Allow-Methods (el PATCH de turnos
// quedó así y no lo cazó ningún test con curl). Si mañana se agrega una ruta
// con otro método, este test la cubre solo.
func TestCORS_permite_todos_los_metodos_registrados(t *testing.T) {
	r := newHardenedRouter([]string{"http://localhost:5173"})

	metodos := map[string]bool{}
	for _, route := range r.Routes() {
		if route.Method != "OPTIONS" && route.Method != "HEAD" {
			metodos[route.Method] = true
		}
	}
	if !metodos["PATCH"] {
		t.Fatal("el router debería exponer PATCH (turnos); si no, este test perdió sentido")
	}
	for metodo := range metodos {
		req := httptest.NewRequest("OPTIONS", "/api/v1/auth/login", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", metodo)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		permitidos := w.Header().Get("Access-Control-Allow-Methods")
		if !strings.Contains(permitidos, metodo) {
			t.Errorf("el preflight no permite %s (Allow-Methods = %q): el navegador bloquearía esas llamadas", metodo, permitidos)
		}
	}
}
