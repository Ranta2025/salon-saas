package httpapi_test

import (
	"encoding/json"
	"net/http"
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/domain"
	"salon-saas/internal/notifications"
	notificationshttpapi "salon-saas/internal/notifications/httpapi"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHTTP_notifications_unread_failure(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "avisos-caidos")

	d := env.Deps
	d.Notifications = notificationshttpapi.NotificationHandler{
		Notifications: notifications.NotificationService{Notifications: httpkit.FailUnread{Inner: env.Notifications}, Salons: env.Mems, Events: env.Events},
		Memberships:   env.Mems,
	}
	r := gin.New()
	httpapi.RegisterRoutes(r, d)

	// Un aviso para que el listado no esté vacío.
	_ = env.Notifications.Save(domain.Notification{ID: "n1", SalonID: salonID, Title: "Hola", Body: "x", Kind: domain.KindBooking, CreatedAt: time.Now().UTC()})

	w := httpkit.Do(r, "GET", "/api/v1/salons/"+salonID+"/notifications", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar con contador caído debe dar 200, dio %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Notifications []map[string]any `json:"notifications"`
		Unread        int              `json:"unread"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v (%s)", err, w.Body.String())
	}
	if len(out.Notifications) != 1 {
		t.Fatalf("los avisos deben seguir saliendo: %s", w.Body.String())
	}
	if out.Unread != 0 {
		t.Fatalf("sin contador el badge va a 0 (no a un número inventado), dio %d", out.Unread)
	}
}

// Flujo: la reserva deja un aviso -> se lista -> se marca leída -> 204.
func TestHTTP_notifications_flujo(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "avisos")
	base := "/api/v1/salons/" + salonID
	httpkit.SeedProfessional(env, salonID, "prof1")

	// Una reserva crea el aviso "Nuevo turno" para todo el salón.
	if w := httpkit.Do(env.Router, "POST", base+"/bookings",
		`{"professional_id":"prof1","client_name":"Lía","starts_at":"2026-12-03T10:00:00Z","ends_at":"2026-12-03T11:00:00Z"}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("reserva: %d %s", w.Code, w.Body.String())
	}

	w := httpkit.Do(env.Router, "GET", base+"/notifications", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar avisos: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Notifications []map[string]any `json:"notifications"`
		Unread        int              `json:"unread"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Notifications) != 1 {
		t.Fatalf("esperaba 1 aviso, dio %s", w.Body.String())
	}
	n := list.Notifications[0]
	if n["title"] != "Nuevo turno" || n["kind"] != "booking" || n["read"] != false {
		t.Fatalf("aviso mal formado: %+v", n)
	}
	if n["salon_id"] != salonID || n["id"] == "" || n["created_at"] == nil {
		t.Fatalf("faltan campos del contrato: %+v", n)
	}
	if list.Unread != 1 {
		t.Fatalf("contador de no leídas: %d", list.Unread)
	}

	// Marcar leída: 204 sin body.
	nid, _ := n["id"].(string)
	w = httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", ownerTok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("marcar leída: %d %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("204 no debe traer body: %s", w.Body.String())
	}
	// Idempotente: de nuevo 204.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("marcar dos veces: %d", w.Code)
	}
	w = httpkit.Do(env.Router, "GET", base+"/notifications", "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if list.Notifications[0]["read"] != true || list.Unread != 0 {
		t.Fatalf("la marca no se reflejó: %s", w.Body.String())
	}
	// Un aviso inexistente (o de otro salón): 404.
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/no-existe/read", "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("aviso inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	// Sin token: 401.
	if w := httpkit.Do(env.Router, "GET", base+"/notifications", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// No-miembro: 403.
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "GET", base+"/notifications", "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// El aviso de un salón no se puede marcar desde otro: 404 (no se filtra).
	otherTok, _ := env.Register(t, "owner2@mail.com")
	otherSalon := env.Salon(t, otherTok, "avisos-2")
	_ = otherSalon
	if w := httpkit.Do(env.Router, "POST", "/api/v1/notifications/"+nid+"/read", "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("otro salón marcando: %d %s", w.Code, w.Body.String())
	}
}

// El cierre de caja también avisa (y el aviso trae la diferencia en texto).
func TestHTTP_notifications_cierre(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "avisos-cierre")
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/cash-closes", `{"counted_cents":1000,"note":"ok"}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("cierre: %d %s", w.Code, w.Body.String())
	}
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/notifications", "", ownerTok)
	var list struct {
		Notifications []map[string]any `json:"notifications"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Notifications) != 1 || list.Notifications[0]["kind"] != "cash" {
		t.Fatalf("esperaba el aviso del cierre: %s", w.Body.String())
	}
	if title, _ := list.Notifications[0]["title"].(string); title != "Cierre de caja" {
		t.Fatalf("título del aviso: %q", title)
	}
	if body, _ := list.Notifications[0]["body"].(string); !strings.Contains(body, "diferencia") {
		t.Fatalf("el cuerpo del aviso: %q", body)
	}
}
