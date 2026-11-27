// Endurecimiento de F2g2 por HTTP: la administración no reactiva con un rol
// nuevo a un admin ni a un owner dados de baja (403), y TODA respuesta con forma
// de miembro lleva `active` y `ended_at` coherentes (R3-001).
package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"testing"
	"time"
)

// memberShaped afirma la forma de un miembro en el JSON: las dos claves de la
// baja están SIEMPRE y un miembro activo sale con `active: true` y
// `ended_at: null`.
func memberShaped(t *testing.T, etiqueta string, row map[string]any) {
	t.Helper()
	v, ok := row["active"]
	if !ok {
		t.Fatalf("%s: la respuesta tiene que llevar active: %v", etiqueta, row)
	}
	if v != true {
		t.Fatalf("%s: un miembro activo tiene que salir con active:true: %v", etiqueta, row)
	}
	end, ok := row["ended_at"]
	if !ok {
		t.Fatalf("%s: la respuesta tiene que llevar ended_at (aunque sea null): %v", etiqueta, row)
	}
	if end != nil {
		t.Fatalf("%s: un miembro activo tiene que salir con ended_at:null: %v", etiqueta, row)
	}
	// Las claves de siempre, para que el chequeo no esconda una respuesta vacía.
	for _, k := range []string{"id", "name", "email", "role", "attends", "trade"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("%s: falta la clave %q: %v", etiqueta, k, row)
		}
	}
}

// bajarSiembra una membresía dada de baja (sin pasar por Save, que la deja
// activa) para probar las puertas de reactivación.
func bajarSiembra(env *httpkit.Env, m domain.Membership) {
	at := time.Now().UTC().Add(-time.Hour)
	m.EndedAt = &at
	seedMembership(env, m)
}

// F2: por `POST /members` la administración NO puede reactivar a un admin ni a
// un owner dados de baja (403 "sin permiso") ni cambiarles el rol; el dueño sí.
func TestHTTP_members_add_admin_cannot_reactivate_deactivated_admin_or_owner(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-reactivar-rol")
	base := "/api/v1/salons/" + salonID + "/members"

	adminTok, adminID := env.Register(t, "admin@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	_, oldAdminID := env.Register(t, "old-admin@mail.com")
	bajarSiembra(env, domain.Membership{UserID: oldAdminID, SalonID: salonID, Role: domain.RoleAdmin})
	_, oldOwnerID := env.Register(t, "old-owner@mail.com")
	bajarSiembra(env, domain.Membership{UserID: oldOwnerID, SalonID: salonID, Role: domain.RoleOwner})

	casos := []struct {
		nombre string
		email  string
		uid    string
		rol    string
	}{
		{"admin a un admin dado de baja", "old-admin@mail.com", oldAdminID, "admin"},
		{"admin a un owner dado de baja", "old-owner@mail.com", oldOwnerID, "owner"},
	}
	for _, c := range casos {
		w := httpkit.Do(env.Router, "POST", base, `{"email":"`+c.email+`","role":"professional"}`, adminTok)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", c.nombre, w.Code, w.Body.String())
		}
		if got := okBodyError(t, w.Body.Bytes()); got != "sin permiso" {
			t.Fatalf("%s: mensaje %q", c.nombre, got)
		}
		// Nada cambió: sigue de baja y con su rol guardado.
		row := memberRows(t, env, ownerTok, salonID)[c.uid]
		if row["active"] != false || row["role"] != c.rol {
			t.Fatalf("%s: el 403 no puede reactivar ni cambiar el rol: %v", c.nombre, row)
		}
	}

	// El dueño sí reactiva al admin dado de baja, con el rol pedido (201 como un
	// alta y la forma completa del miembro).
	w := httpkit.Do(env.Router, "POST", base, `{"email":"old-admin@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("el dueño reactiva al admin dado de baja: %d %s", w.Code, w.Body.String())
	}
	var added map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &added)
	memberShaped(t, "alta de una baja por el dueño", added)
	if added["id"] != oldAdminID || added["role"] != "professional" {
		t.Fatalf("la reactivación toma el rol pedido: %s", w.Body.String())
	}
}

// F2, puerta de la invitación: la administración tampoco puede emitir una
// invitación que le cambiaría el rol a un admin o a un owner dados de baja
// (403); el dueño sí, y el canje reactiva con el rol de la invitación.
func TestHTTP_invitation_admin_cannot_invite_deactivated_admin_or_owner(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	adminTok, adminID := env.Register(t, "admin@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	oldAdminTok, oldAdminID := env.Register(t, "old-admin@mail.com")
	bajarSiembra(env, domain.Membership{UserID: oldAdminID, SalonID: salonID, Role: domain.RoleAdmin})
	_, oldOwnerID := env.Register(t, "old-owner@mail.com")
	bajarSiembra(env, domain.Membership{UserID: oldOwnerID, SalonID: salonID, Role: domain.RoleOwner})

	url := "/api/v1/salons/" + salonID + "/invitations"
	for _, email := range []string{"old-admin@mail.com", "old-owner@mail.com"} {
		w := httpkit.Do(env.Router, "POST", url, `{"email":"`+email+`","role":"professional"}`, adminTok)
		if w.Code != http.StatusForbidden {
			t.Fatalf("admin invitando a una baja (%s): %d %s", email, w.Code, w.Body.String())
		}
		if got := okBodyError(t, w.Body.Bytes()); got != "sin permiso" {
			t.Fatalf("admin invitando a una baja (%s): mensaje %q", email, got)
		}
	}
	// Nada quedó emitido.
	w := httpkit.Do(env.Router, "GET", url, "", ownerTok)
	if w.Code != http.StatusOK || len(mustInvitations(t, w.Body.Bytes())) != 0 {
		t.Fatalf("una invitación rechazada no puede quedar guardada: %s", w.Body.String())
	}

	// El dueño sí invita al admin dado de baja (rol nuevo) y el canje de su
	// cuenta existente lo reactiva con el rol de la invitación.
	created := invite(t, env, ownerTok, salonID, "old-admin@mail.com", "reception")
	token := invitationToken(t, created.Link)
	if w := httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"`+token+`"}`, oldAdminTok); w.Code != http.StatusOK {
		t.Fatalf("canje de una baja invitada por el dueño: %d %s", w.Code, w.Body.String())
	}
	row := memberRows(t, env, ownerTok, salonID)[oldAdminID]
	if row["active"] != true || row["role"] != "reception" {
		t.Fatalf("el canje reactiva con el rol de la invitación: %v", row)
	}
}

// mustInvitations lee el listado de invitaciones pendientes (solo el largo).
func mustInvitations(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var out struct {
		Invitations []map[string]any `json:"invitations"`
	}
	httpkit.MustJSON(t, body, &out)
	return out.Invitations
}

// R3-001: toda respuesta con forma de miembro lleva `active: true` y
// `ended_at: null` cuando la persona está activa. El listado, el alta (nueva y
// de una baja), el PATCH de la marca, el PATCH del oficio y la reactivación
// pasan por la MISMA forma; el `PUT .../services` responde solo `service_ids`.
func TestHTTP_members_every_member_response_is_active(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, ownerID := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-forma")
	base := "/api/v1/salons/" + salonID + "/members"
	profTok, profID := env.Register(t, "profe@mail.com")

	// Alta de alguien ACTIVO: 201 con la forma completa.
	w := httpkit.Do(env.Router, "POST", base, `{"email":"profe@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var row map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &row)
	memberShaped(t, "POST /members (alta)", row)

	// PATCH de la marca (el dueño se enciende "atiende").
	w = httpkit.Do(env.Router, "PATCH", base+"/"+ownerID, `{"attends":true}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH attends: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &row)
	memberShaped(t, "PATCH /members (attends)", row)

	// PATCH del oficio.
	w = httpkit.Do(env.Router, "PATCH", base+"/"+profID, `{"trade":"Barbero"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH trade: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &row)
	memberShaped(t, "PATCH /members (trade)", row)
	if row["trade"] != "Barbero" {
		t.Fatalf("el PATCH devuelve el oficio nuevo: %v", row)
	}

	// PUT de servicios: NO es una respuesta de miembro (solo la lista).
	w = httpkit.Do(env.Router, "PUT", base+"/"+profID+"/services", `{"service_ids":[]}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT services: %d %s", w.Code, w.Body.String())
	}
	var solo struct {
		ServiceIDs []string `json:"service_ids"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &solo)
	if solo.ServiceIDs == nil {
		t.Fatalf("el PUT devuelve un array (aunque vacío): %s", w.Body.String())
	}

	// Reactivación: 200 con la forma completa y activo.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "POST", base+"/"+profID+"/reactivate", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("reactivar: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &row)
	memberShaped(t, "POST reactivate", row)

	// Y el alta de alguien dado de baja (la otra puerta) también.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("segunda baja: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "POST", base, `{"email":"profe@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta de una baja: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &row)
	memberShaped(t, "POST /members (reactivación)", row)

	// El listado: cada fila tiene las dos claves; las activas salen true+null y
	// la dada de baja sale false+string.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("tercera baja: %d %s", w.Code, w.Body.String())
	}
	rows := memberRows(t, env, ownerTok, salonID)
	for _, id := range []string{ownerID, profID} {
		if _, ok := rows[id]["ended_at"]; !ok {
			t.Fatalf("el listado tiene que llevar ended_at en %s: %v", id, rows[id])
		}
	}
	memberShaped(t, "GET /members (miembro activo)", rows[ownerID])
	if rows[profID]["active"] != false {
		t.Fatalf("la baja sale con active:false: %v", rows[profID])
	}
	if _, ok := rows[profID]["ended_at"].(string); !ok {
		t.Fatalf("la baja sale con ended_at como string: %v", rows[profID])
	}
	// El propio dueño sigue entrando (la baja no le tocó el acceso).
	if w := httpkit.Do(env.Router, "GET", base, "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("el dueño sigue en el equipo: %d %s", w.Code, w.Body.String())
	}
	_ = profTok
}
