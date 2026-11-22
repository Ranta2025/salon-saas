package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// resourceUnitBody es una unidad tal como la devuelve la API.
type resourceUnitBody struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// resourceTypeBody es un tipo con sus unidades y cuántos servicios lo usan.
type resourceTypeBody struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Units    []resourceUnitBody `json:"units"`
	Services int                `json:"services"`
}

func listResources(t *testing.T, env *posEnv, salonID, token string) []resourceTypeBody {
	t.Helper()
	w := doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/resources", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("listar equipamiento: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Types []resourceTypeBody `json:"types"`
	}
	mustJSON(t, w.Body.Bytes(), &out)
	return out.Types
}

// S-F2e-3: alta de tipo con unidades, listado, renombrar, agregar unidades,
// desactivar y borrar. Los mensajes del servidor viajan verbatim.
func TestHTTP_resources_lifecycle(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo")
	base := "/api/v1/salons/" + salonID + "/resources"

	if got := listResources(t, env, salonID, ownerTok); len(got) != 0 {
		t.Fatalf("sin equipamiento: %+v", got)
	}

	w := doReq(env.r, "POST", base, `{"name":"  Puesto de peluquería ","count":3}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear tipo: %d %s", w.Code, w.Body.String())
	}
	var created resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &created)
	if created.Name != "Puesto de peluquería" || len(created.Units) != 3 {
		t.Fatalf("tipo creado: %+v", created)
	}
	if created.Units[0].Name != "Puesto de peluquería 1" || !created.Units[0].Active {
		t.Fatalf("unidad automática: %+v", created.Units[0])
	}

	// Duplicado sin importar mayúsculas: 400 con el mensaje del contrato.
	w = doReq(env.r, "POST", base, `{"name":"puesto de PELUQUERÍA","count":1}`, ownerTok)
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"Ya existe un equipo con ese nombre"}` {
		t.Fatalf("duplicado: %d %s", w.Code, w.Body.String())
	}

	// Agregar unidades: 201 con las nuevas, con el siguiente número libre.
	w = doReq(env.r, "POST", base+"/"+created.ID+"/units", `{"count":2}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("agregar unidades: %d %s", w.Code, w.Body.String())
	}
	var added struct {
		Units []resourceUnitBody `json:"units"`
	}
	mustJSON(t, w.Body.Bytes(), &added)
	if len(added.Units) != 2 || added.Units[0].Name != "Puesto de peluquería 4" ||
		added.Units[1].Name != "Puesto de peluquería 5" {
		t.Fatalf("unidades nuevas: %+v", added.Units)
	}

	// Renombrar: 200 con el tipo.
	w = doReq(env.r, "PATCH", base+"/"+created.ID, `{"name":"Puesto"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("renombrar: %d %s", w.Code, w.Body.String())
	}
	var renamed resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &renamed)
	if renamed.Name != "Puesto" {
		t.Fatalf("renombrado: %+v", renamed)
	}

	// Desactivar: 200 con future_bookings informativo.
	w = doReq(env.r, "PATCH", "/api/v1/salons/"+salonID+"/resource-units/"+created.Units[0].ID,
		`{"active":false}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("desactivar unidad: %d %s", w.Code, w.Body.String())
	}
	var off struct {
		ID             string `json:"id"`
		Active         bool   `json:"active"`
		FutureBookings int    `json:"future_bookings"`
	}
	mustJSON(t, w.Body.Bytes(), &off)
	if off.Active || off.FutureBookings != 0 || off.ID != created.Units[0].ID {
		t.Fatalf("unidad desactivada: %+v", off)
	}
	types := listResources(t, env, salonID, ownerTok)
	if got := types[0].Units[0].Active; got {
		t.Fatalf("el listado muestra la unidad desactivada: %+v", types[0].Units[0])
	}

	// Borrar: 204 (ningún servicio ni turno lo usa).
	w = doReq(env.r, "DELETE", base+"/"+created.ID, "", ownerTok)
	if w.Code != http.StatusNoContent {
		t.Fatalf("borrar tipo: %d %s", w.Code, w.Body.String())
	}
	if got := listResources(t, env, salonID, ownerTok); len(got) != 0 {
		t.Fatalf("el tipo borrado no se lista: %+v", got)
	}
}

// S-F2e-3: permisos. Leer: cualquier miembro. Escribir: solo dueño/administración.
func TestHTTP_resources_permissions(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-permisos")
	profTok, profID := env.register(t, "profe@mail.com")
	seedProfessional(env, salonID, profID)
	recepTok, recepID := env.register(t, "recep@mail.com")
	_ = env.mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})
	base := "/api/v1/salons/" + salonID + "/resources"

	w := doReq(env.r, "POST", base, `{"name":"Puesto","count":1}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", w.Code, w.Body.String())
	}
	var created resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &created)

	for _, tok := range []string{profTok, recepTok} {
		if w := doReq(env.r, "POST", base, `{"name":"Otra","count":1}`, tok); w.Code != http.StatusForbidden {
			t.Fatalf("no dueño no crea: %d %s", w.Code, w.Body.String())
		}
		if w := doReq(env.r, "PATCH", base+"/"+created.ID, `{"name":"Hack"}`, tok); w.Code != http.StatusForbidden {
			t.Fatalf("no dueño no renombra: %d %s", w.Code, w.Body.String())
		}
		if w := doReq(env.r, "DELETE", base+"/"+created.ID, "", tok); w.Code != http.StatusForbidden {
			t.Fatalf("no dueño no borra: %d %s", w.Code, w.Body.String())
		}
	}
	// Profesional y recepción SÍ leen.
	for _, tok := range []string{profTok, recepTok} {
		if w := doReq(env.r, "GET", base, "", tok); w.Code != http.StatusOK {
			t.Fatalf("leer equipamiento: %d %s", w.Code, w.Body.String())
		}
	}
	if w := doReq(env.r, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
}

// S-F2e-3: topes y cantidad con los mensajes exactos.
func TestHTTP_resources_limits(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-limites")
	base := "/api/v1/salons/" + salonID + "/resources"

	for _, body := range []string{`{"name":"Puesto","count":0}`, `{"name":"Puesto","count":51}`} {
		w := doReq(env.r, "POST", base, body, ownerTok)
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"La cantidad de unidades debe estar entre 1 y 50"}` {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}

	// 20 tipos entran; el 21 es 400.
	ids := make([]string, 0, 20)
	for i := 0; i < domain.MaxResourceTypes; i++ {
		w := doReq(env.r, "POST", base, `{"name":"Tipo `+itoa(i)+`","count":1}`, ownerTok)
		if w.Code != http.StatusCreated {
			t.Fatalf("tipo %d: %d %s", i, w.Code, w.Body.String())
		}
		var out resourceTypeBody
		mustJSON(t, w.Body.Bytes(), &out)
		ids = append(ids, out.ID)
	}
	w := doReq(env.r, "POST", base, `{"name":"Uno más","count":1}`, ownerTok)
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"Máximo 20 tipos de equipo"}` {
		t.Fatalf("21º tipo: %d %s", w.Code, w.Body.String())
	}

	// 50 unidades por tipo: llenar uno y pedir la 51.
	tipo := ids[0]
	if w := doReq(env.r, "POST", base+"/"+tipo+"/units", `{"count":49}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("49 unidades: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "POST", base+"/"+tipo+"/units", `{"count":1}`, ownerTok)
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"Máximo 50 unidades por tipo"}` {
		t.Fatalf("51ª unidad: %d %s", w.Code, w.Body.String())
	}
}

// S-F2e-3: no se borra un tipo que un servicio usa o que tiene una unidad con
// turnos; el mensaje es el del contrato. Otro salón no existe (404).
func TestHTTP_resources_delete_refused(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-borrar")
	base := "/api/v1/salons/" + salonID + "/resources"

	w := doReq(env.r, "POST", base, `{"name":"Sillón","count":2}`, ownerTok)
	var created resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &created)

	// Un servicio que usa el tipo frena el borrado.
	w = doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Corte","duration_min":30,"price_cents":100000,"resource_type_id":"`+created.ID+`"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("servicio con equipo: %d %s", w.Code, w.Body.String())
	}
	w = doReq(env.r, "DELETE", base+"/"+created.ID, "", ownerTok)
	if w.Code != http.StatusConflict ||
		w.Body.String() != `{"error":"Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo"}` {
		t.Fatalf("borrar con servicio: %d %s", w.Code, w.Body.String())
	}

	// Un tipo sin servicios pero con un turno agendado tampoco se borra.
	w = doReq(env.r, "POST", base, `{"name":"Lavacabezas","count":1}`, ownerTok)
	var lavado resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &lavado)
	seedBookingWithUnit(env, salonID, lavado.Units[0].ID)
	w = doReq(env.r, "DELETE", base+"/"+lavado.ID, "", ownerTok)
	if w.Code != http.StatusConflict ||
		w.Body.String() != `{"error":"Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo"}` {
		t.Fatalf("borrar con turnos: %d %s", w.Code, w.Body.String())
	}

	// Scope por salón: la DUEÑA de este salón también es dueña de otro, y desde
	// ese otro salón los ids de acá no existen (404, no un borrado silencioso).
	otherSalon := env.salon(t, ownerTok, "equipo-otro")
	for _, req := range []struct {
		method, path, body string
	}{
		{"PATCH", "/api/v1/salons/" + otherSalon + "/resources/" + created.ID, `{"name":"Hack"}`},
		{"POST", "/api/v1/salons/" + otherSalon + "/resources/" + created.ID + "/units", `{"count":1}`},
		{"DELETE", "/api/v1/salons/" + otherSalon + "/resources/" + created.ID, ""},
		{"PATCH", "/api/v1/salons/" + otherSalon + "/resource-units/" + created.Units[0].ID, `{"name":"Hack"}`},
	} {
		w := doReq(env.r, req.method, req.path, req.body, ownerTok)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s desde otro salón: %d %s", req.method, req.path, w.Code, w.Body.String())
		}
	}
	if types := listResources(t, env, otherSalon, ownerTok); len(types) != 0 {
		t.Fatalf("el equipamiento de otro salón no se lista: %+v", types)
	}
	// Y desde este salón, el equipo del otro tampoco.
	w = doReq(env.r, "POST", base, `{"name":"Ajeno"}`, ownerTok)
	var ajeno resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &ajeno)
	w = doReq(env.r, "POST", "/api/v1/salons/"+otherSalon+"/resources/"+ajeno.ID+"/units", `{"count":1}`, ownerTok)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unidad de otro salón: %d %s", w.Code, w.Body.String())
	}
}

// S-F2e-3: el servicio lleva su tipo de equipo y lo devuelve en el JSON; un
// tipo de otro salón es 400 "Equipo inválido".
func TestHTTP_service_resource_type(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-servicios")
	base := "/api/v1/salons/" + salonID + "/resources"

	w := doReq(env.r, "POST", base, `{"name":"Puesto","count":1}`, ownerTok)
	var created resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &created)

	w = doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Corte","duration_min":30,"price_cents":100000,"resource_type_id":"`+created.ID+`"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("servicio con equipo: %d %s", w.Code, w.Body.String())
	}
	var svc struct {
		ID             string `json:"id"`
		ResourceTypeID string `json:"resource_type_id"`
	}
	mustJSON(t, w.Body.Bytes(), &svc)
	if svc.ResourceTypeID != created.ID {
		t.Fatalf("el JSON del servicio lleva su equipo: %s", w.Body.String())
	}

	// El listado del catálogo también.
	w = doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/services", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar servicios: %d", w.Code)
	}
	if want := `"resource_type_id":"` + created.ID + `"`; !strings.Contains(w.Body.String(), want) {
		t.Fatalf("el listado no trae el equipo: %s", w.Body.String())
	}

	// Equipo de otro salón: 400 con el mensaje exacto.
	otherTok, _ := env.register(t, "otro@mail.com")
	otherSalon := env.salon(t, otherTok, "equipo-otro-2")
	w = doReq(env.r, "POST", "/api/v1/salons/"+otherSalon+"/resources", `{"name":"Silla","count":1}`, otherTok)
	var ajeno resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &ajeno)

	w = doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Color","duration_min":30,"price_cents":100000,"resource_type_id":"`+ajeno.ID+`"}`, ownerTok)
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"Equipo inválido"}` {
		t.Fatalf("equipo de otro salón: %d %s", w.Code, w.Body.String())
	}

	// PUT puede quitar el equipo (null) sin perder el servicio.
	w = doReq(env.r, "PUT", "/api/v1/salons/"+salonID+"/services/"+svc.ID,
		`{"name":"Corte","duration_min":30,"price_cents":100000,"commission_pct":0,"active":true,"resource_type_id":null}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("quitar equipo: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"resource_type_id":"`) {
		t.Fatalf("el equipo quedó sin asignar: %s", w.Body.String())
	}
}

// S-F2e-3: los turnos del personal devuelven su unidad (o null).
func TestHTTP_booking_resource_unit_json(t *testing.T) {
	env := newPOSEnv()
	ownerTok, _ := env.register(t, "owner@mail.com")
	salonID := env.salon(t, ownerTok, "equipo-turnos")
	seedProfessional(env, salonID, "prof1")

	w := doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/resources", `{"name":"Puesto","count":1}`, ownerTok)
	var created resourceTypeBody
	mustJSON(t, w.Body.Bytes(), &created)

	// Un turno del calendario, sin unidad: resource_unit_id null.
	w = doReq(env.r, "POST", "/api/v1/salons/"+salonID+"/bookings",
		`{"professional_id":"prof1","client_name":"Ana","starts_at":"2026-12-02T10:00:00Z","ends_at":"2026-12-02T11:00:00Z"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("turno: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"resource_unit_id":null`) {
		t.Fatalf("turno sin unidad: %s", w.Body.String())
	}

	// Uno con unidad: el JSON la trae.
	seedBookingWithUnit(env, salonID, created.Units[0].ID)
	w = doReq(env.r, "GET", "/api/v1/salons/"+salonID+"/bookings?from=2026-12-02T00:00:00Z&to=2026-12-03T00:00:00Z", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("agenda: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"resource_unit_id":"`+created.Units[0].ID+`"`) {
		t.Fatalf("la agenda no trae la unidad: %s", w.Body.String())
	}
}

// seedBookingWithUnit deja en el fake un turno reservado que ocupa esa unidad.
func seedBookingWithUnit(env *posEnv, salonID, unitID string) {
	start := time.Date(2026, 12, 2, 14, 0, 0, 0, time.UTC)
	b, err := domain.NewBooking(salonID, "prof-unit", "Clienta", "", start, start.Add(time.Hour))
	if err != nil {
		return
	}
	b.ResourceUnitID = unitID
	_ = env.bookings.Save(b)
}
