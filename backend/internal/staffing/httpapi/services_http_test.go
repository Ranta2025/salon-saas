package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
)

func TestHTTP_services(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "servicios")
	base := "/api/v1/salons/" + salonID + "/services"

	// Crear: 201 con la entidad completa (el frontend necesita el id a mano).
	w := httpkit.Do(env.Router, "POST", base, `{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear servicio: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID            string `json:"id"`
		SalonID       string `json:"salon_id"`
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
		Active        bool   `json:"active"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &created)
	if created.ID == "" || created.SalonID != salonID || created.Name != "Corte" ||
		created.DurationMin != 45 || created.PriceCents != 150000 || created.CommissionPct != 20 || !created.Active {
		t.Fatalf("respuesta de creación incompleta: %s", w.Body.String())
	}

	// Duplicado activo (mismo nombre, otra caja): 409.
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"corte","duration_min":30,"price_cents":90000,"commission_pct":10}`, ownerTok); w.Code != http.StatusConflict {
		t.Fatalf("duplicado debe dar 409, dio %d %s", w.Code, w.Body.String())
	}
	// Cuerpo inválido: 400 (dos sabores: tipo malo y regla de dominio).
	if w := httpkit.Do(env.Router, "POST", base, `{"name":123}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("json con tipo malo debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"","duration_min":0}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("servicio inválido debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// No-miembro: 403 (ni leer ni escribir).
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"Golpe","duration_min":30,"price_cents":1,"commission_pct":0}`, strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro leyendo debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Profesional miembro: lee sí, escribe no.
	profTok, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"Golpe","duration_min":30,"price_cents":1,"commission_pct":0}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional creando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", profTok); w.Code != http.StatusOK {
		t.Fatalf("profesional leyendo: %d %s", w.Code, w.Body.String())
	}

	// Actualizar: 200 y refleja los cambios.
	put := base + "/" + created.ID
	w = httpkit.Do(env.Router, "PUT", put, `{"name":"Corte y brushing","duration_min":60,"price_cents":180000,"commission_pct":35,"active":false}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("actualizar: %d %s", w.Code, w.Body.String())
	}
	var updated struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		DurationMin   int    `json:"duration_min"`
		PriceCents    int    `json:"price_cents"`
		CommissionPct int    `json:"commission_pct"`
		Active        bool   `json:"active"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &updated)
	if updated.ID != created.ID || updated.Name != "Corte y brushing" || updated.DurationMin != 60 ||
		updated.PriceCents != 180000 || updated.CommissionPct != 35 || updated.Active {
		t.Fatalf("no aplicó el update: %s", w.Body.String())
	}
	// Desactivado: fuera del listado por defecto, adentro con include_inactive.
	var list struct {
		Services []map[string]any `json:"services"`
	}
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Services) != 0 {
		t.Fatalf("sin inactivos esperaba [], dio %s", w.Body.String())
	}
	w = httpkit.Do(env.Router, "GET", base+"?include_inactive=true", "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Services) != 1 {
		t.Fatalf("con inactivos esperaba 1, dio %s", w.Body.String())
	}
	// Servicio inexistente: 404. Profesional editando: 403.
	if w := httpkit.Do(env.Router, "PUT", base+"/no-existe", `{"name":"X","duration_min":30,"price_cents":1,"commission_pct":0}`, ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("inexistente debe dar 404, dio %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "PUT", put, `{"name":"X","duration_min":30,"price_cents":1,"commission_pct":0}`, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional editando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	// Sin token: 401.
	if w := httpkit.Do(env.Router, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}
