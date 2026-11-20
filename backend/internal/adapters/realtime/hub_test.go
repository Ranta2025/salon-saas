package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"salon-saas/internal/adapters/security"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeSalons: memberships en memoria (el hub solo necesita "a qué salones
// pertenece este usuario").
type fakeSalons struct {
	byUser map[string][]domain.Membership
}

func (f *fakeSalons) ListByUser(userID string) ([]domain.Membership, error) {
	return f.byUser[userID], nil
}

var _ ports.MembershipLister = (*fakeSalons)(nil)

func hubServer(t *testing.T) (*Hub, *httptest.Server, ports.TokenIssuer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	salons := &fakeSalons{byUser: map[string][]domain.Membership{
		// Ana trabaja en el salón A; Bruno en el salón B; Cami en los dos.
		"u-ana":   {{UserID: "u-ana", SalonID: "s-a", Role: domain.RoleOwner}},
		"u-bruno": {{UserID: "u-bruno", SalonID: "s-b", Role: domain.RoleOwner}},
		"u-cami": {
			{UserID: "u-cami", SalonID: "s-a", Role: domain.RoleReception},
			{UserID: "u-cami", SalonID: "s-b", Role: domain.RoleReception},
		},
	}}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	hub := NewHub(tokens, salons)

	r := gin.New()
	r.GET("/api/v1/ws", hub.ServeWS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return hub, srv, tokens
}

// dial abre el websocket con el token en el query param (como define el
// contrato: el navegador no puede mandar Authorization en un WS).
func dial(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(token)
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("no se pudo conectar (status %d): %v", status, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn, timeout time.Duration) (map[string]any, error) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var ev map[string]any
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("evento no es JSON: %s", msg)
	}
	return ev, nil
}

func mustEvent(t *testing.T, conn *websocket.Conn, what string) map[string]any {
	t.Helper()
	ev, err := readEvent(t, conn, 2*time.Second)
	if err != nil {
		t.Fatalf("%s: no llegó el evento: %v", what, err)
	}
	return ev
}

// waitRoom espera a que el hub registre al cliente en la room del salón. Sin
// esto el test sería una carrera entre el upgrade y el primer Publish.
func waitRoom(t *testing.T, hub *Hub, salonID string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Rooms()[salonID] == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("la room %q nunca llegó a %d conexiones (tiene %d)", salonID, want, hub.Rooms()[salonID])
}

// EL test del contrato multi-tenant del realtime: una conexión del salón A
// NUNCA recibe un evento publicado para el salón B.
func TestHub_aislamiento_entre_salones(t *testing.T) {
	hub, srv, tokens := hubServer(t)
	tokA, _ := tokens.Issue("u-ana")
	tokB, _ := tokens.Issue("u-bruno")
	connA := dial(t, srv, tokA)
	connB := dial(t, srv, tokB)
	waitRoom(t, hub, "s-a", 1)
	waitRoom(t, hub, "s-b", 1)

	// 1) Un evento de B le llega a B, con la forma del contrato.
	if err := hub.Publish(ports.Event{Type: "sale.created", SalonID: "s-b", Data: map[string]any{"id": "venta-b"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	evB := mustEvent(t, connB, "salón B")
	if evB["type"] != "sale.created" || evB["salon_id"] != "s-b" {
		t.Fatalf("evento del salón B mal formado: %+v", evB)
	}
	data, ok := evB["data"].(map[string]any)
	if !ok || data["id"] != "venta-b" {
		t.Fatalf("el payload se conserva tal cual: %+v", evB["data"])
	}
	if ts, _ := evB["ts"].(string); ts == "" {
		t.Fatalf("el evento debe llevar ts: %+v", evB)
	}

	// 2) A está vivo y en SU room: recibe lo de A.
	if err := hub.Publish(ports.Event{Type: "booking.created", SalonID: "s-a", Data: map[string]any{"id": "turno-a"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	evA := mustEvent(t, connA, "salón A")
	if evA["salon_id"] != "s-a" || evA["type"] != "booking.created" {
		t.Fatalf("evento del salón A mal formado: %+v", evA)
	}

	// 3) La prueba negativa: sale un evento de B y A no ve NADA. A ya demostró
	//    que la conexión funciona, así que el timeout no es un falso positivo.
	if err := hub.Publish(ports.Event{Type: "notify", SalonID: "s-b", Data: map[string]any{"marca": "solo-b"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if evB2 := mustEvent(t, connB, "salón B segunda vez"); evB2["type"] != "notify" {
		t.Fatalf("B debe recibir el segundo evento: %+v", evB2)
	}
	if ev, err := readEvent(t, connA, 300*time.Millisecond); err == nil {
		t.Fatalf("AISLAMIENTO ROTO: el salón A recibió un evento del salón B: %+v", ev)
	}
}

// Un usuario con dos salones escucha los dos: las rooms se arman por
// membership, no por "el salón del token".
func TestHub_usuario_multi_salon(t *testing.T) {
	hub, srv, tokens := hubServer(t)
	tokC, _ := tokens.Issue("u-cami")
	conn := dial(t, srv, tokC)
	waitRoom(t, hub, "s-a", 1)
	waitRoom(t, hub, "s-b", 1)

	_ = hub.Publish(ports.Event{Type: "cash_closed", SalonID: "s-b", Data: map[string]any{"id": "c-1"}})
	if ev := mustEvent(t, conn, "cierre de s-b"); ev["salon_id"] != "s-b" {
		t.Fatalf("evento: %+v", ev)
	}
	_ = hub.Publish(ports.Event{Type: "sale.created", SalonID: "s-a", Data: map[string]any{"id": "v-1"}})
	if ev := mustEvent(t, conn, "venta de s-a"); ev["salon_id"] != "s-a" {
		t.Fatalf("evento: %+v", ev)
	}
}

func TestHub_rechaza_sin_token_y_sin_salon(t *testing.T) {
	hub, srv, tokens := hubServer(t)
	_ = hub

	cases := []struct {
		name   string
		query  string
		status int
	}{
		{"sin token", "", http.StatusUnauthorized},
		{"token inválido", "?token=no-es-un-jwt", http.StatusUnauthorized},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/v1/ws"+c.query, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		w := httptest.NewRecorder()
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.GET("/api/v1/ws", hub.ServeWS)
		r.ServeHTTP(w, req)
		if w.Code != c.status {
			t.Fatalf("%s: esperaba %d, dio %d (%s)", c.name, c.status, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "error") {
			t.Fatalf("%s: la respuesta debe traer {\"error\":...}: %s", c.name, w.Body.String())
		}
	}

	// Usuario autenticado pero sin memberships: no hay room a la que unirlo.
	tok, _ := tokens.Issue("u-fantasma")
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(tok)
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("un usuario sin salones no debería conectar")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("esperaba 403, dio %d", got)
	}
}

// Un cliente que se desconecta sale de la room (si no, el hub filtra la lista).
func TestHub_al_desconectar_libera_la_room(t *testing.T) {
	hub, srv, tokens := hubServer(t)
	tok, _ := tokens.Issue("u-ana")
	conn := dial(t, srv, tok)
	waitRoom(t, hub, "s-a", 1)
	_ = conn.Close()
	waitRoom(t, hub, "s-a", 0)
}

// B-01: el handshake del websocket respeta la MISMA allowlist que la API.
// Un origen ajeno no puede abrir el socket (403 antes del upgrade).
func TestHub_rechaza_origin_ajeno(t *testing.T) {
	gin.SetMode(gin.TestMode)
	salons := &fakeSalons{byUser: map[string][]domain.Membership{
		"u-ana": {{UserID: "u-ana", SalonID: "s-a", Role: domain.RoleOwner}},
	}}
	tokens := security.JWTIssuer{Secret: "test-secret"}
	hub := NewHub(tokens, salons, HubOrigins("http://localhost:5173"))
	r := gin.New()
	r.GET("/api/v1/ws", hub.ServeWS)
	srv := httptest.NewServer(r)
	defer srv.Close()

	tok, err := tokens.Issue("u-ana")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(tok)

	// Origen permitido: conecta.
	conn, _, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": {"http://localhost:5173"}})
	if err != nil {
		t.Fatalf("origen permitido debe conectar: %v", err)
	}
	_ = conn.Close()

	// Origen ajeno: el upgrade se rechaza con 403.
	_, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": {"https://evil.example"}})
	if err == nil {
		t.Fatal("un origen ajeno no debe poder abrir el socket")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("esperaba 403, dio %d (%v)", got, err)
	}
}
