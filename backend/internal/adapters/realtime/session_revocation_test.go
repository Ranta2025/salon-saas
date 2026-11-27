// F15 (D4), segunda mitad: la compuerta de revocación no alcanza con aplicarla
// en el handshake. Un websocket abierto ANTES de que la contraseña cambie
// seguiría recibiendo los eventos del salón para siempre (hasta que el cliente se
// vaya o el proceso reinicie). Mientras la conexión está abierta el hub vuelve a
// validar la sesión cada `sessionCheck` y, si ya no vale o la consulta falla,
// cierra la conexión por el MISMO camino que un ping fallido (writeLoop ->
// conn.Close -> readLoop -> leave).
//
// Los tests abren el socket con un JWT real, cambian lo que reporta la compuerta
// (la «contraseña cambió» del reinicio) y miran el socket, no el hub: lo que se
// prueba es el contrato, no la implementación.
package realtime

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"salon-saas/internal/auth/security"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// checkEvery es el intervalo de re-verificación de los tests: unos pocos
// milisegundos para no esperar los 30 s del valor por defecto.
const checkEvery = 5 * time.Millisecond

// mutableGate es la compuerta de revocación bajo control del test: `at` es
// users.password_changed_at (nil = nunca cambió) y `err` simula una base caída.
type mutableGate struct {
	mu    sync.Mutex
	at    *time.Time
	err   error
	calls int
}

func (g *mutableGate) PasswordChangedAt(string) (*time.Time, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	if g.at == nil {
		return nil, nil
	}
	at := *g.at
	return &at, nil
}

func (g *mutableGate) set(at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.at = &at
	g.err = nil
}

func (g *mutableGate) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

func (g *mutableGate) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

var _ ports.SessionGate = (*mutableGate)(nil)

// revocationServer arma un hub con la compuerta MUTABLE y el intervalo de test.
func revocationServer(t *testing.T) (*Hub, *httptest.Server, security.JWTIssuer, *mutableGate) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	salons := &fakeSalons{byUser: map[string][]domain.Membership{
		"u-ana": {{UserID: "u-ana", SalonID: "s-a", Role: domain.RoleOwner}},
	}}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	gate := &mutableGate{}
	hub := NewHub(tokens, salons,
		HubOrigins("http://localhost:5173"),
		HubSessionGate(gate),
		HubSessionCheckInterval(checkEvery),
	)
	r := gin.New()
	r.GET("/api/v1/ws", hub.ServeWS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return hub, srv, tokens, gate
}

// waitClosed espera a que el servidor CIERRE el socket. Devuelve cuánto tardó.
// Un deadline de lectura que vence NO cuenta como cierre: eso es «sigue abierto».
func waitClosed(t *testing.T, conn *websocket.Conn, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := start.Add(within)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("deadline: %v", err)
		}
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue // un evento: la conexión sigue viva, seguimos esperando
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("el socket siguió abierto %v después (la sesión revocada no lo cerró)", within)
		}
		return time.Since(start)
	}
}

// staysOpen comprueba que en `window` no llegó NINGÚN frame ni el cierre: el
// caso de una sesión que sigue siendo válida no cambia en nada.
func staysOpen(t *testing.T, conn *websocket.Conn, window time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(window)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("llegó un frame inesperado a una conexión válida: %s", msg)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("la conexión válida se cerró: %v", err)
	}
}

// El caso del verificador: socket abierto con una sesión válida, después el
// reinicio de contraseña. La conexión tiene que cerrarse (en unos pocos
// intervalos) y el usuario sale de la room: no recibe ni un evento más.
func TestHub_cierra_el_socket_cuando_la_sesion_se_revoca(t *testing.T) {
	hub, srv, tokens, gate := revocationServer(t)
	tok, err := tokens.Issue("u-ana")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	conn := dial(t, srv, tok)
	waitRoom(t, hub, "s-a", 1)

	// Antes del cambio el socket funciona: recibe los eventos del salón.
	_ = hub.Publish(ports.Event{Type: "sale.created", SalonID: "s-a", Data: map[string]any{"id": "v-1"}})
	if ev := mustEvent(t, conn, "venta con la sesión válida"); ev["salon_id"] != "s-a" {
		t.Fatalf("evento: %+v", ev)
	}

	// El reinicio: la contraseña cambia AHORA, después del `ia` del token.
	gate.set(time.Now())
	took := waitClosed(t, conn, 2*time.Second)
	if took > 60*checkEvery { // unos pocos intervalos, no el minuto del ping
		t.Errorf("tardó %v en cerrar: se esperaba ~%v", took, checkEvery)
	}
	waitRoom(t, hub, "s-a", 0) // fuera de la room: ningún Publish le escribe
	if gate.count() < 2 {
		t.Errorf("la compuerta se consultó %d veces: se esperaba el handshake + al menos una re-verificación", gate.count())
	}
}

// Fail closed: si la consulta de la compuerta falla, la conexión se cierra (el
// mismo criterio que el handshake, que responde 401).
func TestHub_cierra_el_socket_si_la_compuerta_falla(t *testing.T) {
	hub, srv, tokens, gate := revocationServer(t)
	tok, _ := tokens.Issue("u-ana")
	conn := dial(t, srv, tok)
	waitRoom(t, hub, "s-a", 1)

	gate.fail(errors.New("base caída"))
	waitClosed(t, conn, 2*time.Second)
	waitRoom(t, hub, "s-a", 0)
}

// Control: una sesión que sigue válida no cambia en NADA. Ni frames extra (el
// keepalive sigue siendo el ping de siempre), ni cierre, ni salida de la room.
// Ojo: un deadline de lectura que vence deja la conexión de gorilla en un estado
// inutilizable, así que «sigue abierta» es SIEMPRE la última comprobación de un
// socket y un cambio de estado usa OTRO socket.
func TestHub_la_conexion_con_sesion_valida_sigue_igual(t *testing.T) {
	hub, srv, tokens, gate := revocationServer(t)
	tok, _ := tokens.Issue("u-ana")
	conn := dial(t, srv, tok)
	waitRoom(t, hub, "s-a", 1)

	// Muchos intervalos de re-verificación sin ningún cambio de contraseña: la
	// conexión sigue recibiendo eventos normalmente.
	_ = hub.Publish(ports.Event{Type: "cash_closed", SalonID: "s-a", Data: map[string]any{"id": "c-1"}})
	if ev := mustEvent(t, conn, "cierre con la sesión válida"); ev["type"] != "cash_closed" {
		t.Fatalf("evento: %+v", ev)
	}
	time.Sleep(40 * checkEvery)
	if n := hub.Rooms()["s-a"]; n != 1 {
		t.Fatalf("la conexión válida salió de la room: %d", n)
	}
	// Última comprobación de este socket: ningún frame (ni el cierre) en la ventana.
	staysOpen(t, conn, 40*checkEvery)
	if gate.count() < 2 {
		t.Errorf("la compuerta se consultó %d veces: se esperaba el handshake + re-verificaciones", gate.count())
	}
}

// Un token emitido ANTES de esta versión (sin `ia`) sigue abriendo el socket y
// sigue abierto mientras la contraseña nunca haya cambiado.
func TestHub_token_sin_ia_vale_si_la_contrasena_nunca_cambio(t *testing.T) {
	hub, srv, _, _ := revocationServer(t)
	conn := dial(t, srv, legacySession(t, "test-secret", "u-ana"))
	waitRoom(t, hub, "s-a", 1)
	_ = hub.Publish(ports.Event{Type: "sale.created", SalonID: "s-a", Data: map[string]any{"id": "v-1"}})
	if ev := mustEvent(t, conn, "venta con un token sin ia"); ev["salon_id"] != "s-a" {
		t.Fatalf("evento: %+v", ev)
	}
	time.Sleep(40 * checkEvery)
	staysOpen(t, conn, 40*checkEvery)
}

// El mismo token sin `ia`, pero con la contraseña cambiada: no vale y la conexión
// abierta se cierra (fail closed).
func TestHub_token_sin_ia_se_cierra_si_la_contrasena_cambio(t *testing.T) {
	hub, srv, _, gate := revocationServer(t)
	conn := dial(t, srv, legacySession(t, "test-secret", "u-ana"))
	waitRoom(t, hub, "s-a", 1)

	gate.set(time.Now())
	waitClosed(t, conn, 2*time.Second)
	waitRoom(t, hub, "s-a", 0)
}

// legacySession firma una sesión como la versión anterior a F15: sin `ia`.
func legacySession(t *testing.T, secret, userID string) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// El token del query param sigue siendo obligatorio y el reintento tras el
// cierre por revocación con la MISMA sesión vieja da 401 (el handshake no relaja
// nada): la re-verificación no es la única defensa.
func TestHub_tras_el_cierre_la_sesion_vieja_no_reconecta(t *testing.T) {
	_, srv, tokens, gate := revocationServer(t)
	tok, _ := tokens.Issue("u-ana")
	gate.set(time.Now())
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(tok)
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("una sesión revocada no puede abrir un socket")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, got %+v (%v)", resp, err)
	}
}

// El ticker de re-verificación se detiene con la conexión: cuando el cliente se
// va no queda ninguna goroutine ni ticker consultando la compuerta (sin fugas).
func TestHub_la_reverificacion_se_detiene_al_cerrar_el_cliente(t *testing.T) {
	hub, srv, tokens, gate := revocationServer(t)
	tok, _ := tokens.Issue("u-ana")
	conn := dial(t, srv, tok)
	waitRoom(t, hub, "s-a", 1)

	time.Sleep(20 * checkEvery) // varias re-verificaciones mientras está abierta
	if gate.count() < 2 {
		t.Fatalf("la compuerta se consultó %d veces: no hubo re-verificación", gate.count())
	}
	_ = conn.Close()
	waitRoom(t, hub, "s-a", 0)
	time.Sleep(10 * checkEvery) // deja drenar cualquier consulta en vuelo
	before := gate.count()
	time.Sleep(30 * checkEvery)
	if after := gate.count(); after != before {
		t.Fatalf("la compuerta siguió consultándose después del cierre: %d -> %d", before, after)
	}
}
