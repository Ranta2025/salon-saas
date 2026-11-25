package realtime

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"salon-saas/internal/ports"
)

// F10 D8: el desafío del segundo paso del login (typ:"2fa") NO abre un
// websocket: el hub parsea con TokenIssuer.Parse, que lo rechaza. La sesión de
// siempre (sin typ) sigue entrando.
func TestHub_el_desafio_2fa_no_abre_socket(t *testing.T) {
	hub, srv, tokens := hubServer(t)
	challenge, err := tokens.(ports.ChallengeIssuer).IssueChallenge("u-ana")
	if err != nil {
		t.Fatal(err)
	}
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(challenge)
	conn, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("un desafío de 2FA abrió un websocket")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, got %+v (%v)", resp, err)
	}
	if n := hub.Rooms()["s-a"]; n != 0 {
		t.Errorf("no debió unirse a ninguna room: %d", n)
	}
	// La sesión normal sí entra.
	sess, _ := tokens.Issue("u-ana")
	dial(t, srv, sess)
	waitRoom(t, hub, "s-a", 1)
}
