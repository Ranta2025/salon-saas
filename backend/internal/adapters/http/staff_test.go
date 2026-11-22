package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
)

// memsCaida simula la DB caída en Memberships.Find: el repo no distingue "no
// es miembro" de "no pude consultar" salvo por el error.
type memsCaida struct{ err error }

func (f memsCaida) Save(domain.Membership) error { return nil }
func (f memsCaida) Find(string, string) (domain.Membership, error) {
	return domain.Membership{}, f.err
}
func (f memsCaida) ListStaff(string) ([]domain.StaffMember, error) { return nil, nil }

// H4: el endpoint de staff colapsaba TODO error de Find en 403, así que una DB
// caída se veía como "no tenés permiso" (y nadie miraba el log). Solo "no es
// miembro" es 403; el resto es 500 genérico con el detalle en el log.
func TestStaff_find_falla_da_500_no_403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	roto := memsCaida{err: errors.New("pq: connection reset by peer")}
	staffSvc := app.StaffService{Staff: roto, Memberships: roto}

	r := gin.New()
	r.GET("/staff/:id", func(c *gin.Context) {
		c.Set("userID", "u-123")
		StaffHandler{Staff: staffSvc, Memberships: roto}.List(c)
	})

	// El log del servidor se captura aparte: el detalle va ahí y no al cliente.
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prev)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/staff/s-1", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("una falla de infraestructura es 500, dio %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v (%s)", err, w.Body.String())
	}
	if body.Error != internalErrorMessage {
		t.Fatalf("el 500 debe ser genérico, dio %q", body.Error)
	}
	for _, fuga := range []string{"pq:", "connection reset", "u-123"} {
		if strings.Contains(w.Body.String(), fuga) {
			t.Fatalf("el body no puede llevar el detalle (%q): %s", fuga, w.Body.String())
		}
	}
	if !strings.Contains(logged.String(), "pq:") {
		t.Fatalf("el detalle tiene que quedar en el log: %q", logged.String())
	}
	// El detalle va al log SIN datos de la persona: el handler no agrega su id
	// ni su email a la línea (queda el método, la ruta y el error).
	if !strings.Contains(logged.String(), "pq:") {
		t.Fatalf("el detalle tiene que quedar en el log: %q", logged.String())
	}
	if strings.Contains(logged.String(), "u-123") || strings.Contains(logged.String(), "@") {
		t.Fatalf("el log no lleva datos de la persona: %q", logged.String())
	}
}

// H4 (PRESERVE): "no es miembro" sigue siendo 403 con el mismo mensaje.
func TestStaff_no_miembro_sigue_dando_403(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "staff-ajeno")
	strangerTok, _ := env.register(t, "stranger@mail.com")

	w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", strangerTok)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro: 403, dio %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sin acceso al salón") {
		t.Fatalf("el mensaje del 403 no cambió: %s", w.Body.String())
	}
}
