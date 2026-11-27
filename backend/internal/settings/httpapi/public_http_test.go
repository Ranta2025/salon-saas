package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
)

func publicGet(t *testing.T, w *f16World, slug string) map[string]any {
	t.Helper()
	r := httpkit.Do(w.Env.Router, "GET", "/api/v1/public/salons/"+slug, "", "")
	if r.Code != http.StatusOK {
		t.Fatalf("público %s: %d %s", slug, r.Code, r.Body.String())
	}
	var out map[string]any
	httpkit.MustJSON(t, r.Body.Bytes(), &out)
	return out
}

// listOf lee una clave del payload como lista (falla el test, sin panic, si no lo es).
func listOf(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	l, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s debe ser una lista: %v", key, m[key])
	}
	return l
}

// D4: la forma EXACTA del payload público: todo lo anterior sigue y solo se
// agrega (salon.*, services[].description, team, gallery, packages).
func TestPublicPayload_exactShape(t *testing.T) {
	w := newF16World(t)
	svcID := w.Env.SeedService(t, w.Owner, w.Salon, "Corte", 15000)
	// El dueño no atiende por defecto: Ana (profesional) hace el servicio.
	_ = w.Env.Professionals.SetServices(w.Salon, w.ProfID, []string{svcID})
	httpkit.Do(w.Env.Router, "PUT", w.servicesPath()+"/"+svcID, `{"name":"Corte","duration_min":45,"price_cents":15000,"commission_pct":20,"description":"Corte clásico"}`, w.Owner)
	httpkit.Do(w.Env.Router, "PUT", w.profilePath(), fullProfileBody, w.Owner)
	p := w.addPhoto(t, w.Salon, w.Owner)
	httpkit.Do(w.Env.Router, "PUT", w.galleryPath()+"/"+p.ID, `{"caption":"El local"}`, w.Owner)
	ninety := 90
	pkg, err := domain.NewServicePackage(w.Salon, "Pack 5", svcID, 5, 60000, &ninety)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Packages.Create(pkg)

	got := publicGet(t, w, "f16-uno")
	if strings.Join(httpkit.KeysOf(got), ",") != "gallery,hours,loyalty,packages,salon,services,team" {
		t.Fatalf("claves de primer nivel: %v", httpkit.KeysOf(got))
	}
	salon := got["salon"].(map[string]any)
	if strings.Join(httpkit.KeysOf(salon), ",") != "address,description,email,id,instagram,name,phone,slug,timezone" {
		t.Fatalf("claves de salon: %v", httpkit.KeysOf(salon))
	}
	if salon["id"] != w.Salon || salon["slug"] != "f16-uno" || salon["name"] != "Navajas de Oro" || salon["description"] != "Barbería clásica" ||
		salon["address"] != "Calle 1 #23" || salon["phone"] != "+52 55" || salon["email"] != "hola@navajas.com" ||
		salon["instagram"] != "navajas.oro" || salon["timezone"] != domain.DefaultSalonSettings("x").Timezone {
		t.Fatalf("salon: %v", salon)
	}
	services := listOf(t, got, "services")
	if len(services) != 1 {
		t.Fatalf("servicios: %v", services)
	}
	svc := services[0].(map[string]any)
	if strings.Join(httpkit.KeysOf(svc), ",") != "description,duration_min,id,name,price_cents" ||
		svc["id"] != svcID || svc["name"] != "Corte" || svc["duration_min"].(float64) != 45 || svc["price_cents"].(float64) != 15000 || svc["description"] != "Corte clásico" {
		t.Fatalf("servicio: %v", svc)
	}
	// Equipo: SOLO name y trade (ni id, ni email, ni rol, ni pagos).
	// El dueño del salón nuevo atiende (S-F2a-4) y la profesional también.
	team := listOf(t, got, "team")
	if len(team) != 2 {
		t.Fatalf("equipo: %v", team)
	}
	for _, m := range team {
		if strings.Join(httpkit.KeysOf(m.(map[string]any)), ",") != "name,trade" {
			t.Fatalf("el equipo solo expone name y trade: %v", httpkit.KeysOf(m.(map[string]any)))
		}
	}
	gallery := listOf(t, got, "gallery")
	g := gallery[0].(map[string]any)
	if len(gallery) != 1 || strings.Join(httpkit.KeysOf(g), ",") != "caption,id,url" || g["id"] != p.ID ||
		g["url"] != "/api/v1/public/salons/f16-uno/gallery/"+p.ID || g["caption"] != "El local" {
		t.Fatalf("galería: %v", gallery)
	}
	hours := listOf(t, got, "hours")
	if len(hours) != 7 {
		t.Fatalf("hours como hoy: %v", hours)
	}
	packages := listOf(t, got, "packages")
	if len(packages) != 1 {
		t.Fatalf("packages: %v", packages)
	}
	pk := packages[0].(map[string]any)
	if pk["id"] != pkg.ID || pk["name"] != "Pack 5" || pk["service_name"] != "Corte" || pk["sessions"].(float64) != 5 ||
		pk["price_cents"].(float64) != 60000 || pk["validity_days"].(float64) != 90 {
		t.Fatalf("paquete: %v", pk)
	}
}

// D4: perfil sin cargar -> nulls; secciones vacías son listas vacías (no null).
func TestPublicPayload_emptySections(t *testing.T) {
	w := newF16World(t)
	// El dueño de f16-dos deja de atender: nadie del equipo.
	for k, m := range w.Env.Mems.ByKey {
		if m.SalonID == w.other {
			m.Attends = false
			w.Env.Mems.ByKey[k] = m
		}
	}
	got := publicGet(t, w, "f16-dos")
	salon := got["salon"].(map[string]any)
	if salon["description"] != nil || salon["address"] != nil || salon["phone"] != nil || salon["email"] != nil || salon["instagram"] != nil {
		t.Fatalf("nulls: %v", salon)
	}
	for _, k := range []string{"services", "team", "gallery", "packages"} {
		if list, ok := got[k].([]any); !ok || len(list) != 0 {
			t.Fatalf("%s debe ser []: %v", k, got[k])
		}
	}
}

// D4: un servicio que nadie hace no se lista; uno inactivo tampoco; los
// profesionales sin datos sensibles; los paquetes inactivos fuera.
func TestPublicPayload_filtersTeamAndPackages(t *testing.T) {
	w := newF16World(t)
	hecho := w.Env.SeedService(t, w.Owner, w.Salon, "Hecho", 1000)
	nadie := w.Env.SeedService(t, w.Owner, w.Salon, "Nadie lo hace", 2000)
	viejo := w.Env.SeedService(t, w.Owner, w.Salon, "Viejo", 3000)
	_ = w.Env.Professionals.SetServices(w.Salon, w.ProfID, []string{hecho, viejo})
	w.Env.Professionals.ByService = map[string][]string{w.Salon + "|" + nadie: {}}
	httpkit.Do(w.Env.Router, "PUT", w.servicesPath()+"/"+viejo, `{"name":"Viejo","duration_min":45,"price_cents":3000,"commission_pct":20,"active":false}`, w.Owner)
	// Recepción (rol que no atiende) y un dueño sin la marca no son el equipo.
	got := publicGet(t, w, "f16-uno")
	names := []string{}
	for _, s := range listOf(t, got, "services") {
		names = append(names, s.(map[string]any)["name"].(string))
	}
	if len(names) != 1 || names[0] != "Hecho" {
		t.Fatalf("carta: %v", names)
	}
	// Equipo: el dueño (atiende) y la profesional; recepción y administración
	// que no atienden no aparecen.
	if team := listOf(t, got, "team"); len(team) != 2 {
		t.Fatalf("equipo (solo quien atiende): %v", team)
	}
	// Inactivo, de otro salón y de servicio inactivo: fuera de packages.
	mk := func(salon, svc, name string, active bool) {
		p, err := domain.NewServicePackage(salon, name, svc, 3, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Active = active
		_ = w.Packages.Create(p)
	}
	mk(w.Salon, hecho, "Pack vigente", true)
	mk(w.Salon, hecho, "Pack apagado", false)
	mk(w.Salon, viejo, "Pack de servicio inactivo", true)
	mk(w.other, hecho, "Pack ajeno", true)
	got = publicGet(t, w, "f16-uno")
	pk := listOf(t, got, "packages")
	if len(pk) != 1 {
		t.Fatalf("paquetes: %v", pk)
	}
	one := pk[0].(map[string]any)
	if strings.Join(httpkit.KeysOf(one), ",") != "id,name,price_cents,service_name,sessions,validity_days" ||
		one["name"] != "Pack vigente" || one["service_name"] != "Hecho" || one["sessions"].(float64) != 3 || one["price_cents"].(float64) != 100 || one["validity_days"] != nil {
		t.Fatalf("paquete: %v", one)
	}
}
