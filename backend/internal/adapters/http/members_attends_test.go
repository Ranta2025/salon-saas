package httpapi

import (
	"net/http"
	"testing"

	"salon-saas/internal/domain"
)

// S-F2a-5: GET /salons/:id/members agrega `attends`, el POST lo acepta opcional
// y el nuevo PATCH /salons/:id/members/:uid enciende y apaga "atiende clientes".
// Solo owner/admin; el rol decide si la marca se puede cambiar.
func TestHTTP_members_attends(t *testing.T) {
	env := newPOSEnv()
	ownerTok, ownerID := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-attends")
	base := "/api/v1/salons/" + salonID + "/members"
	adminTok, adminID := env.register(t, "admin@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	profTok, profID := env.register(t, "profe@mail.com")
	profM, _ := domain.NewMembership(profID, salonID, domain.RoleProfessional)
	_ = env.mems.Save(profM)
	_, recepID := env.register(t, "recep@mail.com")
	recepM, _ := domain.NewMembership(recepID, salonID, domain.RoleReception)
	_ = env.mems.Save(recepM)

	// El listado dice quién atiende: el owner de un salón nuevo atiende, la
	// administración y recepción no, el profesional sí (por rol).
	var list struct {
		Members []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Attends bool   `json:"attends"`
		} `json:"members"`
	}
	attendsOf := func() map[string]bool {
		t.Helper()
		w := doReq(env.r, "GET", base, "", ownerTok)
		if w.Code != http.StatusOK {
			t.Fatalf("listar: %d %s", w.Code, w.Body.String())
		}
		mustJSON(t, w.Body.Bytes(), &list)
		out := map[string]bool{}
		for _, m := range list.Members {
			out[m.ID] = m.Attends
		}
		return out
	}
	got := attendsOf()
	if !got[ownerID] || !got[profID] || got[adminID] || got[recepID] {
		t.Fatalf("attends inicial: %+v (owner %s, prof %s, admin %s, recep %s)", got, ownerID, profID, adminID, recepID)
	}

	// Encender la administración: 200 con el miembro.
	w := doReq(env.r, "PATCH", base+"/"+adminID, `{"attends":true}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH admin: %d %s", w.Code, w.Body.String())
	}
	var patched struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Role    string `json:"role"`
		Attends bool   `json:"attends"`
	}
	mustJSON(t, w.Body.Bytes(), &patched)
	if patched.ID != adminID || patched.Role != "admin" || patched.Name == "" || patched.Email == "" || !patched.Attends {
		t.Fatalf("miembro devuelto por el PATCH: %s", w.Body.String())
	}
	if !attendsOf()[adminID] {
		t.Fatal("la marca no se persistió")
	}
	// El admin se enciende con su propio token (administra el equipo) y el
	// owner puede apagarse: 200.
	if w := doReq(env.r, "PATCH", base+"/"+adminID, `{"attends":false}`, adminTok); w.Code != http.StatusOK {
		t.Fatalf("admin se apaga: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+ownerID, `{"attends":false}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("owner se apaga: %d %s", w.Code, w.Body.String())
	}
	// La agenda ya no lo ofrece: el dueño apagado sale de /staff.
	var staff struct {
		Staff []struct {
			ID string `json:"id"`
		} `json:"staff"`
	}
	w = doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/staff", "", profTok)
	if w.Code != http.StatusOK {
		t.Fatalf("staff: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &staff)
	for _, s := range staff.Staff {
		if s.ID == ownerID {
			t.Fatalf("el dueño que no atiende no va en la agenda: %s", w.Body.String())
		}
	}
	if len(staff.Staff) != 1 || staff.Staff[0].ID != profID {
		t.Fatalf("solo queda el profesional: %s", w.Body.String())
	}
	// Y al volver a encenderlo, reaparece.
	if w := doReq(env.r, "PATCH", base+"/"+ownerID, `{"attends":true}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("owner se enciende: %d %s", w.Code, w.Body.String())
	}

	casos := []struct {
		nombre string
		uid    string
		body   string
		tok    string
		status int
		errMsg string
	}{
		{"profesional en false", profID, `{"attends":false}`, ownerTok, http.StatusBadRequest, "Las profesionales siempre atienden"},
		{"recepción en true", recepID, `{"attends":true}`, ownerTok, http.StatusBadRequest, "Ese rol no atiende clientes"},
		// F2c: el PATCH acepta attends y/o trade; sin ninguno de los dos el
		// mensaje dice qué falta (antes era "body inválido").
		{"sin campos", adminID, `{}`, ownerTok, http.StatusBadRequest, "Indicá qué cambiar"},
		{"profesional administrando", recepID, `{"attends":true}`, profTok, http.StatusForbidden, "sin permiso"},
	}
	for _, c := range casos {
		w := doReq(env.r, "PATCH", base+"/"+c.uid, c.body, c.tok)
		if w.Code != c.status {
			t.Fatalf("%s: %d %s", c.nombre, w.Code, w.Body.String())
		}
		var body struct {
			Error string `json:"error"`
		}
		mustJSON(t, w.Body.Bytes(), &body)
		if body.Error != c.errMsg {
			t.Fatalf("%s: mensaje %q, esperaba %q", c.nombre, body.Error, c.errMsg)
		}
	}

	// Aislamiento: un miembro de otro salón no existe para este PATCH (404) y el
	// owner de otro salón no puede tocar este equipo (403).
	otherTok, otherID := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "equipo-otro")
	if w := doReq(env.r, "PATCH", "/api/v1/salons/"+otherSalon+"/members/"+profID, `{"attends":true}`, otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+otherID, `{"attends":false}`, ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro ajeno a este salón: %d %s", w.Code, w.Body.String())
	}
	if w := doReq(env.r, "PATCH", base+"/"+adminID, `{"attends":true}`, otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("owner de otro salón: %d %s", w.Code, w.Body.String())
	}
	// La marca del profesional sigue intacta después de todos los rechazos.
	if !attendsOf()[profID] {
		t.Fatal("los rechazos no deben tocar la marca del profesional")
	}
}

// El POST acepta `attends` opcional: la administración puede nacer atendiendo y
// el profesional atiende siempre.
func TestHTTP_members_add_attends(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-alta-attends")
	base := "/api/v1/salons/" + salonID + "/members"
	_, adminID := env.register(t, "nuevo-admin@mail.com")
	_, profID := env.register(t, "nuevo-prof@mail.com")

	w := doReq(env.r, "POST", base, `{"email":"nuevo-admin@mail.com","role":"admin","attends":true}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin que atiende: %d %s", w.Code, w.Body.String())
	}
	var added struct {
		ID      string `json:"id"`
		Attends bool   `json:"attends"`
	}
	mustJSON(t, w.Body.Bytes(), &added)
	if added.ID != adminID || !added.Attends {
		t.Fatalf("miembro agregado: %s", w.Body.String())
	}
	// Sin la marca: el profesional atiende igual.
	w = doReq(env.r, "POST", base, `{"email":"nuevo-prof@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("profesional: %d %s", w.Code, w.Body.String())
	}
	mustJSON(t, w.Body.Bytes(), &added)
	if added.ID != profID || !added.Attends {
		t.Fatalf("profesional agregado: %s", w.Body.String())
	}
}
