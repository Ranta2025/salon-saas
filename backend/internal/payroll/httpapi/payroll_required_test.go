package httpapi_test

import (
	"net/http"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
)

// El PUT del modelo de pago REEMPLAZA el modelo entero: los tres campos son
// obligatorios. Antes, un cuerpo parcial como {"fixed_period":"week"} guardaba
// fixed_cents=0 en silencio y BORRABA un sueldo fijo (hallazgo de la revisión
// nativa y del verificador independiente de F3.2). Un campo ausente o null es un
// 400 y no escribe nada.
func TestHTTP_pay_model_campos_obligatorios(t *testing.T) {
	e := newPayEnv(t)
	ana := e.Users["ana@pay.com"]
	path := e.url(e.Salon, "/members/"+ana+"/pay-model")

	// Un modelo válido y con sueldo, que ningún cuerpo parcial puede pisar.
	if w := e.do(t, "PUT", path, `{"fixed_cents":150000,"fixed_period":"month","commission_enabled":false}`, "duena@pay.com"); w.Code != http.StatusOK {
		t.Fatalf("alta del modelo: %d %s", w.Code, w.Body.String())
	}

	partial := []struct{ name, body string }{
		{"sin fixed_cents", `{"fixed_period":"week","commission_enabled":true}`},
		{"fixed_cents null", `{"fixed_cents":null,"fixed_period":"week","commission_enabled":true}`},
		{"sin fixed_period", `{"fixed_cents":1,"commission_enabled":true}`},
		{"fixed_period null", `{"fixed_cents":1,"fixed_period":null,"commission_enabled":true}`},
		{"sin commission_enabled", `{"fixed_cents":1,"fixed_period":"week"}`},
		{"commission_enabled null", `{"fixed_cents":1,"fixed_period":"week","commission_enabled":null}`},
		{"cuerpo vacío", `{}`},
	}
	for _, c := range partial {
		w := e.do(t, "PUT", path, c.body, "duena@pay.com")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: esperaba 400, dio %d %s", c.name, w.Code, w.Body.String())
		}
		var out map[string]string
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out["error"] == "" {
			t.Fatalf("%s: el 400 tiene que traer un mensaje en español: %s", c.name, w.Body.String())
		}
	}

	// Nada de lo anterior tocó el modelo guardado.
	w := e.do(t, "GET", e.url(e.Salon, "/pay-models"), "", "duena@pay.com")
	var list struct {
		PayModels []map[string]any `json:"pay_models"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	for _, m := range list.PayModels {
		if m["user_id"] != ana {
			continue
		}
		if m["fixed_cents"] != float64(150000) || m["fixed_period"] != "month" || m["commission_enabled"] != false {
			t.Fatalf("un cuerpo parcial pisó el modelo guardado: %+v", m)
		}
	}
}
