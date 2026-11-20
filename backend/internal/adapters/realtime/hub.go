// Package realtime es el adaptador de salida de ports.EventPublisher: un hub
// de websockets con una room por salón.
//
// La regla que hace que el multi-tenancy no se rompa por el costado del
// realtime: un cliente SOLO se une a las rooms de los salones de sus
// memberships reales (consultadas en la DB, no deducidas del token), y un
// evento se publica a UNA room. Un salón nunca ve lo del otro.
package realtime

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

const (
	// sendBuffer: cuántos eventos puede acumular un cliente lento antes de
	// perder mensajes. El hub NUNCA bloquea al publicador esperando una red.
	sendBuffer = 32
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 45 * time.Second
)

// Hub mantiene las conexiones agrupadas por salon_id.
type Hub struct {
	tokens   ports.TokenIssuer
	salons   ports.MembershipLister
	upgrader websocket.Upgrader

	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}
}

// client es una conexión suscripta. Solo el writeLoop la escribe.
type client struct {
	send chan []byte
}

// NewHub arma el hub. tokens autentica el JWT del query param; salons resuelve
// a qué rooms unir la conexión.
func NewHub(tokens ports.TokenIssuer, salons ports.MembershipLister) *Hub {
	return &Hub{
		tokens: tokens,
		salons: salons,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			// El token (no una cookie) es la credencial, así que no hay CSRF
			// de cookies acá: el navegador de cualquier origen puede conectar.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		rooms: map[string]map[*client]struct{}{},
	}
}

// wireEvent es el JSON que viaja por el socket: {type, salon_id, data, ts}.
type wireEvent struct {
	Type    string `json:"type"`
	SalonID string `json:"salon_id"`
	Data    any    `json:"data"`
	TS      string `json:"ts"`
}

// Publish implementa ports.EventPublisher: serializa una vez y reparte a la
// room del salón. Si un cliente está lleno se descarta el mensaje para ese
// cliente (no se frena la venta porque alguien tenga la red lenta).
func (h *Hub) Publish(ev ports.Event) error {
	ts := ev.TS
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	payload, err := json.Marshal(wireEvent{
		Type:    ev.Type,
		SalonID: ev.SalonID,
		Data:    ev.Data,
		TS:      ts.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for cl := range h.rooms[ev.SalonID] {
		select {
		case cl.send <- payload:
		default: // cliente lento: este mensaje se pierde, la conexión sigue
		}
	}
	return nil
}

// Rooms cuenta las conexiones vivas por salón (introspección: sirve para
// debuggear y para que los tests no dependan de tiempos arbitrarios).
func (h *Hub) Rooms() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]int, len(h.rooms))
	for salonID, clients := range h.rooms {
		out[salonID] = len(clients)
	}
	return out
}

// ServeWS es el handler de GET /api/v1/ws?token=<jwt>.
// Autentica ANTES del upgrade: sin token válido y sin ningún salón no hay
// socket (401/403 en JSON, igual que el resto de la API).
func (h *Hub) ServeWS(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "falta token"})
		return
	}
	userID, err := h.tokens.Parse(token)
	if err != nil || userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token inválido"})
		return
	}
	mems, err := h.salons.ListByUser(userID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "no se pudo resolver el salón"})
		return
	}
	if len(mems) == 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "sin acceso a ningún salón"})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade ya respondió el error HTTP
	}
	cl := &client{send: make(chan []byte, sendBuffer)}
	h.join(cl, mems)
	go h.writeLoop(conn, cl)
	h.readLoop(conn, cl) // bloquea hasta que el cliente se va
	h.leave(cl, mems)    // fuera de las rooms: ningún Publish puede escribirle
	close(cl.send)       // ahora sí, el writeLoop termina
}

// join mete al cliente en la room de cada salón al que pertenece. La lista es
// la que devolvió la DB: un usuario no puede pedir "unime al salón X".
func (h *Hub) join(cl *client, mems []domain.Membership) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range mems {
		if h.rooms[m.SalonID] == nil {
			h.rooms[m.SalonID] = map[*client]struct{}{}
		}
		h.rooms[m.SalonID][cl] = struct{}{}
	}
}

func (h *Hub) leave(cl *client, mems []domain.Membership) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range mems {
		delete(h.rooms[m.SalonID], cl)
		if len(h.rooms[m.SalonID]) == 0 {
			delete(h.rooms, m.SalonID) // sin conexiones no hay room que mirar
		}
	}
}

// writeLoop es el ÚNICO escritor de la conexión (gorilla no soporta escrituras
// concurrentes). Manda ping para detectar conexiones muertas.
func (h *Hub) writeLoop(conn *websocket.Conn, cl *client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()
	for {
		select {
		case msg, ok := <-cl.send:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readLoop consume lo que manda el cliente. El canal es de ida: no hay comandos
// por el socket (todo lo que modifica datos va por la API HTTP), así que solo
// se espera el cierre o el pong.
func (h *Hub) readLoop(conn *websocket.Conn, cl *client) {
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
