package httpapi

import (
	"net/http"
	"testing"

	"salon-saas/internal/domain"
)

// F3.3: con liquidaciones de nómina, el PUT que CAMBIA la zona horaria responde
// 409 con la frase del contrato y no escribe nada (la otra configuración
// tampoco se aplica). Es el borde HTTP del bloqueante de la zona congelada.
func TestHTTP_settings_zona_congelada(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "congelada")
	base := "/api/v1/salons/" + salonID + "/settings"
	// El fake del salón simula que ya hay una liquidación calculada.
	env.settings.frozen = map[string]bool{salonID: true}

	w := doReq(env.r, "PUT", base, `{"timezone":"Pacific/Auckland","open_time":"08:00","close_time":"17:00","slot_step_min":20}`, ownerTok)
	if w.Code != http.StatusConflict {
		t.Fatalf("zona congelada: %d %s, esperaba 409", w.Code, w.Body.String())
	}
	var out struct {
		Error string `json:"error"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	if out.Error != domain.MsgPayrollTimezoneFrozen {
		t.Fatalf("mensaje = %q, esperaba %q", out.Error, domain.MsgPayrollTimezoneFrozen)
	}

	// NADA se escribió: siguen los defaults de fábrica.
	var got struct {
		Timezone    string `json:"timezone"`
		OpenTime    string `json:"open_time"`
		CloseTime   string `json:"close_time"`
		SlotStepMin int    `json:"slot_step_min"`
	}
	w = doReq(env.r, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &got)
	if got.Timezone != domain.DefaultTimezone || got.OpenTime != domain.DefaultOpenTime ||
		got.CloseTime != domain.DefaultCloseTime || got.SlotStepMin != domain.DefaultSlotStepMin {
		t.Fatalf("el 409 escribió parte del cuerpo: %+v", got)
	}
}
