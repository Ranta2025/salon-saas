package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// F3.1 (S-F3.1-2) en la capa HTTP: la venta que entra por el POS se guarda con
// la FOTO del % de comisión de cada servicio, y el JSON de la venta NO cambia
// (el contrato de F3.1 no agrega ningún campo: commission_pct es interno).
func TestHTTP_sale_commission_snapshot(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "foto")
	base := "/api/v1/salons/" + salonID + "/sales"
	seedProfessional(env, salonID, "prof1")

	seed := func(name string, price, pct int) string {
		body := `{"name":"` + name + `","duration_min":45,"price_cents":` + itoa(price) +
			`,"commission_pct":` + itoa(pct) + `}`
		w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/services", body, ownerTok)
		if w.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", name, w.Code, w.Body.String())
		}
		var out struct {
			ID string `json:"id"`
		}
		mustJSON(t, w.Body.Bytes(), &out)
		return out.ID
	}
	corte := seed("Corte", 150000, 35)
	gratis := seed("Cortesía", 0, 0)

	body := `{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[` +
		`{"service_id":"` + corte + `","qty":1,"unit_price_cents":150000},` +
		`{"service_id":"` + gratis + `","qty":2,"unit_price_cents":0}]}`
	w := doReq(env.r, "POST", base, body, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "commission_pct") {
		t.Fatalf("el POST de la venta no debe exponer commission_pct: %s", w.Body.String())
	}
	var out struct {
		SaleID string `json:"sale_id"`
		Total  int    `json:"total"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Total != 150000 {
		t.Fatalf("total del servidor = 150000, dio %d", out.Total)
	}
	// La foto viajó de la petición al almacenamiento, por ítem.
	if len(env.sales.all) != 1 || len(env.sales.all[0].Items) != 2 {
		t.Fatalf("venta guardada: %+v", env.sales.all)
	}
	guardada := env.sales.all[0]
	if guardada.Items[0].CommissionPct == nil || *guardada.Items[0].CommissionPct != 35 {
		t.Fatalf("la línea del corte debe llevar foto 35, dio %v", guardada.Items[0].CommissionPct)
	}
	if guardada.Items[1].CommissionPct == nil || *guardada.Items[1].CommissionPct != 0 {
		t.Fatalf("comisión 0 debe ser foto 0, no NULL: %v", guardada.Items[1].CommissionPct)
	}

	// El dueño sube la comisión del catálogo: lo vendido no se mueve.
	w = doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/services/"+corte,
		`{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":50,"active":true}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("editar servicio: %d %s", w.Code, w.Body.String())
	}
	if guardada.Items[0].CommissionPct == nil || *guardada.Items[0].CommissionPct != 35 {
		t.Fatalf("editar el catálogo tocó la venta vieja: %v", guardada.Items[0].CommissionPct)
	}

	// Venta nueva: tarifa vigente (50).
	w = doReq(env.r, "POST", base,
		`{"professional_id":"prof1","client_name":"Ana","method":"card","items":[{"service_id":"`+corte+`","qty":1,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("segunda venta: %d %s", w.Code, w.Body.String())
	}
	if len(env.sales.all) != 2 {
		t.Fatalf("esperaba 2 ventas, dio %d", len(env.sales.all))
	}
	if pct := env.sales.all[1].Items[0].CommissionPct; pct == nil || *pct != 50 {
		t.Fatalf("la venta nueva debe llevar 50, dio %v", pct)
	}

	// Repetido por client_op_id: misma venta, misma foto, sin duplicar.
	op := `"client_op_id":"op-foto-http",`
	w = doReq(env.r, "POST", base, `{`+op+`"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"`+corte+`","qty":1,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta con op id: %d %s", w.Code, w.Body.String())
	}
	first := env.sales.all[len(env.sales.all)-1]
	w = doReq(env.r, "POST", base, `{`+op+`"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"`+corte+`","qty":1,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	var replay struct {
		SaleID string `json:"sale_id"`
	}
	mustJSON(t, w.Body.Bytes(), &replay)
	if replay.SaleID != first.ID {
		t.Fatalf("el replay debe devolver la venta original %s, dio %s", first.ID, replay.SaleID)
	}
	if pct := env.sales.all[len(env.sales.all)-1].Items[0].CommissionPct; pct == nil || *pct != 50 {
		t.Fatalf("el replay no debe duplicar ni mover la foto: %v", pct)
	}

	// GET /sales: el JSON de la venta sigue igual (items con 3 campos, sin
	// commission_pct), aunque la DB guarde la foto.
	w = doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "commission_pct") {
		t.Fatalf("el JSON de la venta cambió: %s", w.Body.String())
	}
}
