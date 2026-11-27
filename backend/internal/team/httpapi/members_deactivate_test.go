package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
)

// S-F2g2-4, mitad HTTP: `DELETE /salons/:id/members/:uid` da de BAJA (marca
// ended_at) en vez de borrar la fila, el listado dice quién sigue en el equipo,
// y `POST .../reactivate` lo vuelve a sumar con su rol, su marca y su oficio.

// seedMembership escribe una membresía en el fake SIN pasar por Save (el upsert
// del alta deja la fila activa): sirve para sembrar una baja o un segundo owner.
func seedMembership(env *httpkit.Env, m domain.Membership) {
	if env.Mems.ByKey == nil {
		env.Mems.ByKey = map[string]domain.Membership{}
	}
	env.Mems.ByKey[m.UserID+"|"+m.SalonID] = m
}

// memberRows trae GET /members como mapas: así se puede afirmar que una clave
// EXISTE (un campo ausente y uno null no se distinguen con un struct).
func memberRows(t *testing.T, env *httpkit.Env, token, salonID string) map[string]map[string]any {
	t.Helper()
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/members", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("listar miembros: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Members []map[string]any `json:"members"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	rows := map[string]map[string]any{}
	for _, m := range out.Members {
		id, _ := m["id"].(string)
		rows[id] = m
	}
	return rows
}

// okBodyError lee {"error": ...} de una respuesta de error.
func okBodyError(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	httpkit.MustJSON(t, body, &out)
	return out.Error
}

// La baja: 204, la fila NO se borra (sigue en el listado con active=false y
// ended_at RFC3339 UTC), la persona pierde el acceso en su próximo pedido y sus
// invitaciones pendientes quedan revocadas. Un turno pasado y uno futuro
// CANCELADO no frenan la baja.
func TestHTTP_members_deactivate(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-baja")
	base := "/api/v1/salons/" + salonID + "/members"
	profTok, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)
	now := time.Now().UTC()

	env.Bookings.All = append(env.Bookings.All,
		domain.Booking{
			ID: "b-pasado", SalonID: salonID, ProfessionalID: profID, ClientName: "Lía",
			StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Status: domain.BookingBooked,
		},
		domain.Booking{
			ID: "b-cancelado", SalonID: salonID, ProfessionalID: profID, ClientName: "Bea",
			StartsAt: now.Add(48 * time.Hour), EndsAt: now.Add(49 * time.Hour), Status: domain.BookingCancelled,
		},
	)
	// Una invitación pendiente emitida por ella: la baja la revoca en el MISMO
	// commit (hallazgo de F2g1).
	env.Invitations.Rows["inv-baja"] = domain.Invitation{
		ID: "inv-baja", SalonID: salonID, Email: "nueva@mail.com", Role: domain.RoleProfessional,
		TokenHash: strings.Repeat("a", 64), ExpiresAt: now.Add(24 * time.Hour), CreatedBy: profID,
	}

	// Antes de la baja: activo y ended_at null (la clave está, con null).
	rows := memberRows(t, env, ownerTok, salonID)
	if v, ok := rows[profID]["active"]; !ok || v != true {
		t.Fatalf("un miembro activo tiene que traer active:true: %v", rows[profID])
	}
	if v, ok := rows[profID]["ended_at"]; !ok || v != nil {
		t.Fatalf("un miembro activo tiene que traer ended_at:null: %v", rows[profID])
	}

	// La baja: 204 y sin body.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("dar de baja: %d %q", w.Code, w.Body.String())
	}

	// La fila sigue en el equipo, marcada como dada de baja con su fecha.
	rows = memberRows(t, env, ownerTok, salonID)
	row, ok := rows[profID]
	if !ok {
		t.Fatalf("la persona dada de baja tiene que seguir en el listado: %v", rows)
	}
	if v, _ := row["active"]; v != false {
		t.Fatalf("después de la baja active tiene que ser false: %v", row)
	}
	raw, isStr := row["ended_at"].(string)
	if !isStr {
		t.Fatalf("después de la baja ended_at tiene que ser un string RFC3339: %v", row)
	}
	endedAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("ended_at no es RFC3339 (%q): %v", raw, err)
	}
	if endedAt.Location() != time.UTC || endedAt.Sub(now) > time.Minute || now.Sub(endedAt) > time.Minute {
		t.Fatalf("ended_at tiene que ser UTC y de ahora (%q)", raw)
	}
	// Rol y marca se conservan: la baja no reescribe la membresía.
	if row["role"] != "professional" || row["attends"] != true {
		t.Fatalf("la baja conserva rol y attends: %v", row)
	}

	// Acceso inmediato: la persona dada de baja recibe 403 en cualquier ruta del
	// salón (su JWT sigue siendo válido, la membresía activa ya no está).
	if w := httpkit.Do(env.Router, "GET", base, "", profTok); w.Code != http.StatusForbidden {
		t.Fatalf("dado de baja listando: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/staff", "", profTok); w.Code != http.StatusForbidden {
		t.Fatalf("dado de baja en /staff: %d %s", w.Code, w.Body.String())
	}
	// Y deja de aparecer en el select de profesional de la agenda.
	var staff struct {
		Staff []struct {
			ID string `json:"id"`
		} `json:"staff"`
	}
	w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/staff", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("staff: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &staff)
	for _, s := range staff.Staff {
		if s.ID == profID {
			t.Fatalf("quien fue dado de baja no atiende más: %s", w.Body.String())
		}
	}

	// Sus invitaciones pendientes quedaron revocadas (el token viejo ya no sirve).
	if inv := env.Invitations.Rows["inv-baja"]; inv.RevokedAt == nil {
		t.Fatalf("la baja tiene que revocar las invitaciones pendientes que emitió: %+v", inv)
	}

	// Dar de baja dos veces: para esta ruta ya no está.
	w = httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok)
	if w.Code != http.StatusNotFound || okBodyError(t, w.Body.Bytes()) != "no encontrado" {
		t.Fatalf("baja dos veces: %d %s", w.Code, w.Body.String())
	}
}

// S-F2g2-4: con turnos FUTUROS no cancelados la baja es 409 con la oración
// exacta (singular y plural) y NO se hace nada.
func TestHTTP_members_deactivate_turnos_pendientes(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-baja-turnos")
	base := "/api/v1/salons/" + salonID + "/members"
	now := time.Now().UTC()

	unoTok, unoID := env.Register(t, "uno@mail.com")
	httpkit.SeedProfessional(env, salonID, unoID)
	env.Bookings.All = append(env.Bookings.All, domain.Booking{
		ID: "b-uno", SalonID: salonID, ProfessionalID: unoID, ClientName: "Lía",
		StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(25 * time.Hour), Status: domain.BookingBooked,
	})
	// Segundo con dos turnos futuros: el mensaje va en plural.
	_, dosID := env.Register(t, "dos@mail.com")
	httpkit.SeedProfessional(env, salonID, dosID)
	for _, id := range []string{"b-dos-1", "b-dos-2"} {
		env.Bookings.All = append(env.Bookings.All, domain.Booking{
			ID: id, SalonID: salonID, ProfessionalID: dosID, ClientName: "Cata",
			StartsAt: now.Add(72 * time.Hour), EndsAt: now.Add(73 * time.Hour), Status: domain.BookingBooked,
		})
	}

	casos := []struct {
		nombre  string
		uid     string
		mensaje string
	}{
		{"un turno", unoID, "Tiene 1 turno pendiente: reasignalo o cancelalo antes de darla de baja"},
		{"dos turnos", dosID, "Tiene 2 turnos pendientes: reasignalos o cancelalos antes de darla de baja"},
	}
	for _, c := range casos {
		w := httpkit.Do(env.Router, "DELETE", base+"/"+c.uid, "", ownerTok)
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: %d %s", c.nombre, w.Code, w.Body.String())
		}
		if got := okBodyError(t, w.Body.Bytes()); got != c.mensaje {
			t.Fatalf("%s: mensaje %q, esperaba %q", c.nombre, got, c.mensaje)
		}
	}
	// El 409 revierte todo: los dos siguen activos y con su acceso intacto.
	rows := memberRows(t, env, ownerTok, salonID)
	for _, uid := range []string{unoID, dosID} {
		if rows[uid]["active"] != true || rows[uid]["ended_at"] != nil {
			t.Fatalf("un 409 no puede cambiar nada: %v", rows[uid])
		}
	}
	if w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/staff", "", unoTok); w.Code != http.StatusOK {
		t.Fatalf("el rechazo no puede sacar el acceso: %d %s", w.Code, w.Body.String())
	}
}

// La autorización de la baja es el `manageActor` de siempre (solo owner/admin
// del salón) con la regla de rol de la baja: el dueño da de baja a cualquiera,
// incluido otro dueño; la administración solo a profesional, recepción o cliente
// (un admin no baja al dueño ni a otro admin, igual que su invitación de F2g1);
// el último owner ACTIVO es 400 y recepción/profesional no dan de baja a nadie.
// El aislamiento por salón sigue igual.
func TestHTTP_members_deactivate_permisos(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, ownerID := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-baja-permisos")
	base := "/api/v1/salons/" + salonID + "/members"

	adminTok, adminID := env.Register(t, "admin@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	profTok, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)
	recepTok, recepID := env.Register(t, "recep@mail.com")
	recepM, _ := domain.NewMembership(recepID, salonID, domain.RoleReception)
	_ = env.Mems.Save(recepM)
	// Un segundo dueño y un segundo admin directos en la base (la API no reparte
	// owner): así la regla del último owner no tapa la de rol.
	_, owner2ID := env.Register(t, "owner2@mail.com")
	seedMembership(env, domain.Membership{UserID: owner2ID, SalonID: salonID, Role: domain.RoleOwner})
	_, admin2ID := env.Register(t, "admin2@mail.com")
	seedMembership(env, domain.Membership{UserID: admin2ID, SalonID: salonID, Role: domain.RoleAdmin})

	// 403: la administración no baja al dueño ni a otro admin; quien no
	// administra no baja a nadie.
	casos := []struct {
		nombre string
		uid    string
		tok    string
	}{
		{"admin al dueño", ownerID, adminTok},
		{"admin a otro admin", admin2ID, adminTok},
		{"profesional a la administración", adminID, profTok},
		{"recepción a la administración", adminID, recepTok},
	}
	for _, c := range casos {
		w := httpkit.Do(env.Router, "DELETE", base+"/"+c.uid, "", c.tok)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", c.nombre, w.Code, w.Body.String())
		}
		if got := okBodyError(t, w.Body.Bytes()); got != "sin permiso" {
			t.Fatalf("%s: mensaje %q", c.nombre, got)
		}
	}

	// El dueño SÍ da de baja a otro dueño: con dos activos ninguno es "el
	// último", así que es 204 y la fila queda con active:false.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+owner2ID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("dueño dando de baja a otro dueño: %d %s", w.Code, w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[owner2ID]["active"] != false {
		t.Fatalf("el otro dueño quedó activo: %v", rows[owner2ID])
	}

	// Con el otro dueño ya de baja, el que queda sí es el último owner activo:
	// 400 con el mensaje de siempre.
	w := httpkit.Do(env.Router, "DELETE", base+"/"+ownerID, "", ownerTok)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("último owner: %d %s", w.Code, w.Body.String())
	}
	if got := okBodyError(t, w.Body.Bytes()); got != "No se puede quitar al último dueño" {
		t.Fatalf("último owner: mensaje %q", got)
	}

	// Aislamiento de tenant: el miembro de otro salón no existe acá (404) y el
	// owner de otro salón no toca este equipo (403).
	otherTok, otherID := env.Register(t, "otro@mail.com")
	otherSalon := env.Salon(t, otherTok, "equipo-baja-otro")
	if w := httpkit.Do(env.Router, "DELETE", "/api/v1/salons/"+otherSalon+"/members/"+profID, "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+otherID, "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("alguien ajeno a este salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+recepID, "", otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("owner de otro salón: %d %s", w.Code, w.Body.String())
	}

	// La administración SÍ da de baja a un profesional (204).
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", adminTok); w.Code != http.StatusNoContent {
		t.Fatalf("admin da de baja a un profesional: %d %s", w.Code, w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[profID]["active"] != false {
		t.Fatalf("el profesional quedó activo: %v", rows[profID])
	}
}

// El camino de hoy sigue igual: el dueño da de baja a alguien SIN turnos
// pendientes y la respuesta es 204.
func TestHTTP_members_delete_sin_turnos_pendientes_204(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-baja-simple")
	base := "/api/v1/salons/" + salonID + "/members"
	_, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)

	w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("DELETE del dueño: %d %q", w.Code, w.Body.String())
	}
}

// Reactivar: 200 con el miembro (rol, attends, oficio y servicios anteriores),
// 409 si ya está activo, 404 si es de otro salón o no existe, 403 sin permiso.
func TestHTTP_members_reactivate(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-reactivar")
	base := "/api/v1/salons/" + salonID + "/members"
	profTok, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)

	// Oficio y servicios de antes de la baja: los dos se conservan.
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Corte","duration_min":30,"price_cents":150000,"commission_pct":20}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("servicio: %d %s", w.Code, w.Body.String())
	}
	var svc struct {
		ID string `json:"id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &svc)
	if w := httpkit.Do(env.Router, "PATCH", base+"/"+profID, `{"trade":"Barbero"}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("oficio: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "PUT", base+"/"+profID+"/services", `{"service_ids":["`+svc.ID+`"]}`, ownerTok); w.Code != http.StatusOK {
		t.Fatalf("servicios: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}

	// Reactivar: 200 con el miembro completo.
	w = httpkit.Do(env.Router, "POST", base+"/"+profID+"/reactivate", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("reactivar: %d %s", w.Code, w.Body.String())
	}
	var m map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &m)
	if m["id"] != profID || m["role"] != "professional" || m["attends"] != true || m["active"] != true {
		t.Fatalf("miembro reactivado: %s", w.Body.String())
	}
	if v, ok := m["ended_at"]; !ok || v != nil {
		t.Fatalf("reactivado: ended_at tiene que volver a null: %s", w.Body.String())
	}
	if m["trade"] != "Barbero" {
		t.Fatalf("la reactivación conserva el oficio: %s", w.Body.String())
	}
	ids, _ := m["service_ids"].([]any)
	if len(ids) != 1 || ids[0] != svc.ID {
		t.Fatalf("la reactivación conserva los servicios: %s", w.Body.String())
	}
	// Y vuelve a tener acceso al salón.
	if w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/staff", "", profTok); w.Code != http.StatusOK {
		t.Fatalf("/staff después de reactivar: %d %s", w.Code, w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[profID]["active"] != true {
		t.Fatalf("la lista no lo ve activo: %v", rows[profID])
	}

	// Ya activo: 409 con la oración del contrato.
	w = httpkit.Do(env.Router, "POST", base+"/"+profID+"/reactivate", "", ownerTok)
	if w.Code != http.StatusConflict || okBodyError(t, w.Body.Bytes()) != "Esa persona ya está en el equipo" {
		t.Fatalf("reactivar dos veces: %d %s", w.Code, w.Body.String())
	}
	// Inexistente: 404.
	if w := httpkit.Do(env.Router, "POST", base+"/no-existe/reactivate", "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("reactivar inexistente: %d %s", w.Code, w.Body.String())
	}

	// Sin permiso: un profesional no reactiva a nadie y un admin no reactiva al
	// dueño (ni a otro admin).
	adminTok, adminID := env.Register(t, "admin@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: adminID, SalonID: salonID, Role: domain.RoleAdmin})
	at := time.Now().UTC().Add(-time.Hour)
	_, oldOwnerID := env.Register(t, "old-owner@mail.com")
	seedMembership(env, domain.Membership{
		UserID: oldOwnerID, SalonID: salonID, Role: domain.RoleOwner, EndedAt: &at,
	})
	if w := httpkit.Do(env.Router, "POST", base+"/"+adminID+"/reactivate", "", profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional reactivando: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "POST", base+"/"+oldOwnerID+"/reactivate", "", adminTok); w.Code != http.StatusForbidden {
		t.Fatalf("admin reactivando al dueño: %d %s", w.Code, w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[oldOwnerID]["active"] != false {
		t.Fatalf("el 403 no puede reactivar: %v", rows[oldOwnerID])
	}
	// Con la misma regla al revés, el dueño SÍ reactiva a otro dueño.
	if w := httpkit.Do(env.Router, "POST", base+"/"+oldOwnerID+"/reactivate", "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("dueño reactivando a otro dueño: %d %s", w.Code, w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[oldOwnerID]["active"] != true {
		t.Fatalf("el otro dueño tiene que quedar activo: %v", rows[oldOwnerID])
	}

	// Aislamiento: el owner de otro salón no reactiva acá y un miembro de otro
	// salón no existe para esta ruta.
	otherTok, otherID := env.Register(t, "otro@mail.com")
	otherSalon := env.Salon(t, otherTok, "equipo-reactivar-otro")
	if w := httpkit.Do(env.Router, "POST", base+"/"+otherID+"/reactivate", "", ownerTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro ajeno a este salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+otherSalon+"/members/"+profID+"/reactivate", "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("miembro de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "POST", base+"/"+oldOwnerID+"/reactivate", "", otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("owner de otro salón: %d %s", w.Code, w.Body.String())
	}
}

// S-F2g2-4: aceptar una invitación (canje autenticado de una cuenta existente)
// de alguien dado de baja también lo reactiva, con el rol de la invitación.
func TestHTTP_invitation_accept_reactiva_baja(t *testing.T) {
	env, ownerTok, salonID := invitationEnv(t)
	invitedTok, invitedID := env.Register(t, "invitada@mail.com")
	httpkit.SeedProfessional(env, salonID, invitedID)
	if w := httpkit.Do(env.Router, "DELETE", "/api/v1/salons/"+salonID+"/members/"+invitedID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}

	// Se le puede invitar igual: para la invitación una baja no es equipo.
	created := invite(t, env, ownerTok, salonID, "invitada@mail.com", "reception")
	token := invitationToken(t, created.Link)
	if w := httpkit.Do(env.Router, "POST", "/api/v1/invitations/accept", `{"token":"`+token+`"}`, invitedTok); w.Code != http.StatusOK {
		t.Fatalf("canje de alguien dado de baja: %d %s", w.Code, w.Body.String())
	}
	rows := memberRows(t, env, ownerTok, salonID)
	row := rows[invitedID]
	if row["active"] != true || row["role"] != "reception" {
		t.Fatalf("el canje reactiva con el rol de la invitación: %v", row)
	}
}

// S-F2g2-4: agregar por email a alguien dado de baja lo REACTIVA (201 como un
// alta, con el rol pedido) en vez de responder "ya es parte del equipo".
func TestHTTP_members_add_reactivates(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "equipo-alta-reactivar")
	base := "/api/v1/salons/" + salonID + "/members"
	_, profID := env.Register(t, "profe@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)

	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("baja: %d %s", w.Code, w.Body.String())
	}

	w := httpkit.Do(env.Router, "POST", base, `{"email":"profe@mail.com","role":"professional"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("el alta de alguien dado de baja tiene que ser 201: %d %s", w.Code, w.Body.String())
	}
	var added map[string]any
	httpkit.MustJSON(t, w.Body.Bytes(), &added)
	if added["id"] != profID || added["role"] != "professional" || added["active"] != true || added["ended_at"] != nil {
		t.Fatalf("miembro reactivado por el alta: %s", w.Body.String())
	}
	if rows := memberRows(t, env, ownerTok, salonID); rows[profID]["active"] != true {
		t.Fatalf("la lista no lo ve activo: %v", rows[profID])
	}

	// Con otro rol: la reactivación también toma el rol pedido.
	if w := httpkit.Do(env.Router, "DELETE", base+"/"+profID, "", ownerTok); w.Code != http.StatusNoContent {
		t.Fatalf("segunda baja: %d %s", w.Code, w.Body.String())
	}
	w = httpkit.Do(env.Router, "POST", base, `{"email":"profe@mail.com","role":"reception"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta con otro rol: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &added)
	if added["role"] != "reception" || added["active"] != true {
		t.Fatalf("la reactivación toma el rol pedido: %s", w.Body.String())
	}
}
