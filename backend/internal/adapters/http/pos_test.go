package httpapi_test

import (
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
	"time"
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

func TestHTTP_sales(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "pos")
	base := "/api/v1/salons/" + salonID + "/sales"
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)

	httpkit.SeedProfessional(env, salonID, "prof1")

	// El cliente manda su propio total: debe ser ignorado y recalculado.
	body := `{"professional_id":"prof1","client_name":"Lía","method":"cash",` +
		`"total":1,"total_cents":1,` +
		`"items":[{"service_id":"` + svcID + `","qty":2,"unit_price_cents":150000}]}`
	w := httpkit.Do(env.Router, "POST", base, body, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("registrar venta: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		SaleID string `json:"sale_id"`
		Total  int    `json:"total"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out.SaleID == "" || out.Total != 300000 {
		t.Fatalf("total del servidor = 300000, dio %s", w.Body.String())
	}

	// Ventas inválidas: 400 (sin ítems, qty 0, método desconocido, json roto).
	for _, bad := range []string{
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[]}`,
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"` + svcID + `","qty":0,"unit_price_cents":150000}]}`,
		`{"professional_id":"prof1","client_name":"Lía","method":"bitcoin","items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`,
		`{"professional_id":"prof1","client_name":"","method":"cash","items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`,
		`no-json`,
	} {
		if w := httpkit.Do(env.Router, "POST", base, bad, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("venta inválida %s debe dar 400, dio %d %s", bad, w.Code, w.Body.String())
		}
	}
	// Servicio inexistente: 400 (no 500 ni venta huérfana).
	if w := httpkit.Do(env.Router, "POST", base, `{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"no-existe","qty":1,"unit_price_cents":1}]}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("servicio inexistente debe dar 400, dio %d %s", w.Code, w.Body.String())
	}

	// Permisos: no-miembro 403, profesional 403.
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "POST", base, body, strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	profTok, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	if w := httpkit.Do(env.Router, "POST", base, body, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional vendiendo debe dar 403, dio %d %s", w.Code, w.Body.String())
	}

	// Listado: trae la venta con sus ítems y el total en centavos.
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar ventas: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Sales []struct {
			ID             string `json:"id"`
			SalonID        string `json:"salon_id"`
			ProfessionalID string `json:"professional_id"`
			ClientName     string `json:"client_name"`
			TotalCents     int    `json:"total_cents"`
			Method         string `json:"method"`
			CreatedAt      string `json:"created_at"`
			Items          []struct {
				ServiceID      string `json:"service_id"`
				Qty            int    `json:"qty"`
				UnitPriceCents int    `json:"unit_price_cents"`
			} `json:"items"`
		} `json:"sales"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Sales) != 1 {
		t.Fatalf("esperaba 1 venta, dio %s", w.Body.String())
	}
	s := list.Sales[0]
	if s.ID != out.SaleID || s.SalonID != salonID || s.ProfessionalID != "prof1" || s.ClientName != "Lía" ||
		s.TotalCents != 300000 || s.Method != "cash" || s.CreatedAt == "" {
		t.Fatalf("venta mal serializada: %s", w.Body.String())
	}
	if len(s.Items) != 1 || s.Items[0].ServiceID != svcID || s.Items[0].Qty != 2 || s.Items[0].UnitPriceCents != 150000 {
		t.Fatalf("ítems mal serializados: %s", w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// Ventana sin ventas: [].
	w = httpkit.Do(env.Router, "GET", base+"?from=2020-01-01T00:00:00Z&to=2020-01-02T00:00:00Z", "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sales":[]`) {
		t.Fatalf("ventana vacía debe dar []: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTP_cash_closes(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "caja")
	base := "/api/v1/salons/" + salonID + "/cash-closes"
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	httpkit.SeedProfessional(env, salonID, "prof1")

	// Una venta en efectivo de 300000 deja eso en el cajón.
	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/sales",
		`{"professional_id":"prof1","client_name":"Lía","method":"cash","items":[{"service_id":"`+svcID+`","qty":2,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta previa: %d %s", w.Code, w.Body.String())
	}

	// Recepción no cierra caja (solo owner/admin): 403.
	recepTok, recepID := env.Register(t, "recep@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: recepID, SalonID: salonID, Role: domain.RoleReception})
	if w := httpkit.Do(env.Router, "POST", base, `{"counted_cents":300000,"note":"turno mañana"}`, recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción cerrando debe dar 403, dio %d %s", w.Code, w.Body.String())
	}

	// Cierre con faltante: 201 {close_id, expected_cash}.
	w = httpkit.Do(env.Router, "POST", base, `{"counted_cents":298000,"note":"faltó vuelto"}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("cerrar caja: %d %s", w.Code, w.Body.String())
	}
	var closed struct {
		CloseID      string `json:"close_id"`
		ExpectedCash int    `json:"expected_cash"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &closed)
	if closed.CloseID == "" || closed.ExpectedCash != 300000 {
		t.Fatalf("esperaba expected_cash=300000, dio %s", w.Body.String())
	}

	// Contado negativo: 400.
	if w := httpkit.Do(env.Router, "POST", base, `{"counted_cents":-1}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("contado negativo debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// JSON roto: 400.
	if w := httpkit.Do(env.Router, "POST", base, `{"counted_cents":"dos"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("json roto debe dar 400, dio %d %s", w.Code, w.Body.String())
	}
	// Cerrar dos veces seguidas: el segundo turno arranca sin efectivo previo.
	w = httpkit.Do(env.Router, "POST", base, `{"counted_cents":0}`, ownerTok)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"expected_cash":0`) {
		t.Fatalf("segundo cierre debe arrancar en 0: %d %s", w.Code, w.Body.String())
	}

	// Listado con la diferencia contable.
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("listar cierres: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Closes []struct {
			ID              string `json:"id"`
			SalonID         string `json:"salon_id"`
			OpenedAt        string `json:"opened_at"`
			ClosedAt        string `json:"closed_at"`
			SystemCashCents int    `json:"system_cash_cents"`
			CountedCents    int    `json:"counted_cents"`
			DifferenceCents int    `json:"difference_cents"`
			Note            string `json:"note"`
		} `json:"closes"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Closes) != 2 {
		t.Fatalf("esperaba 2 cierres, dio %s", w.Body.String())
	}
	var sawShortfall bool
	for _, c := range list.Closes {
		if c.SalonID != salonID || c.OpenedAt == "" || c.ClosedAt == "" {
			t.Fatalf("cierre mal serializado: %s", w.Body.String())
		}
		if c.SystemCashCents == 300000 && c.CountedCents == 298000 && c.DifferenceCents == -2000 && c.Note == "faltó vuelto" {
			sawShortfall = true
		}
	}
	if !sawShortfall {
		t.Fatalf("falta el cierre con faltante -2000: %s", w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
}

func TestHTTP_dashboard(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "panel")
	base := "/api/v1/salons/" + salonID + "/dashboard"
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)

	// Un profesional en el salón = 720 minutos de capacidad del día.
	_, profID := env.Register(t, "profe@mail.com")
	_ = env.Mems.Save(domain.Membership{UserID: profID, SalonID: salonID, Role: domain.RoleProfessional})
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	env.Bookings.All = append(env.Bookings.All,
		domain.Booking{SalonID: salonID, ProfessionalID: profID, StartsAt: dayStart.Add(9 * time.Hour), EndsAt: dayStart.Add(11 * time.Hour), Status: domain.BookingBooked},
		domain.Booking{SalonID: salonID, ProfessionalID: profID, StartsAt: dayStart.Add(15 * time.Hour), EndsAt: dayStart.Add(16 * time.Hour), Status: domain.BookingCancelled},
	)
	env.Sales.Top = []domain.TopService{{ServiceID: svcID, Name: "Corte", Qty: 2, RevenueCents: 300000}}

	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/sales",
		`{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","items":[{"service_id":"`+svcID+`","qty":2,"unit_price_cents":150000}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta previa: %d %s", w.Code, w.Body.String())
	}

	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var d struct {
		RevenueToday      int     `json:"revenue_today"`
		AppointmentsToday int     `json:"appointments_today"`
		OccupancyRate     float64 `json:"occupancy_rate"`
		TopServices       []struct {
			ServiceID    string `json:"service_id"`
			Name         string `json:"name"`
			Qty          int    `json:"qty"`
			RevenueCents int    `json:"revenue_cents"`
		} `json:"top_services"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &d)
	if d.RevenueToday != 300000 {
		t.Fatalf("revenue_today = 300000, dio %d", d.RevenueToday)
	}
	if d.AppointmentsToday != 1 {
		t.Fatalf("appointments_today = 1 (la cancelada no cuenta), dio %d", d.AppointmentsToday)
	}
	// 120 min de 720 de un profesional = 0.1667
	if d.OccupancyRate != 0.1667 {
		t.Fatalf("occupancy_rate = 120/720, dio %v", d.OccupancyRate)
	}
	if len(d.TopServices) != 1 || d.TopServices[0].ServiceID != svcID || d.TopServices[0].Qty != 2 || d.TopServices[0].RevenueCents != 300000 {
		t.Fatalf("top_services: %s", w.Body.String())
	}

	// No-miembro: 403. Sin token: 401.
	strangerTok, _ := env.Register(t, "stranger@mail.com")
	if w := httpkit.Do(env.Router, "GET", base, "", strangerTok); w.Code != http.StatusForbidden {
		t.Fatalf("no-miembro debe dar 403, dio %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", base, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token debe dar 401, dio %d", w.Code)
	}
	// La agenda de hoy se lee con el mismo calendario: sin token 401, con
	// token de miembro 200 (regresión del adaptador de reservas intacta).
	day := dayStart.Format(time.RFC3339)
	if w := httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/bookings?from="+day+"&to="+dayStart.Add(24*time.Hour).Format(time.RFC3339), "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("bookings sigue funcionando: %d %s", w.Code, w.Body.String())
	}
}

// TestHTTP_sale_replay: reintento offline con el mismo client_op_id.
// El segundo POST debe devolver 201 con el MISMO sale_id (no 409, no duplicado).
func TestHTTP_sale_replay(t *testing.T) {
	env := httpkit.NewPOSEnv()
	ownerTok, _ := env.Register(t, "owner@mail.com")
	salonID := env.Salon(t, ownerTok, "replay")
	base := "/api/v1/salons/" + salonID + "/sales"
	svcID := env.SeedService(t, ownerTok, salonID, "Corte", 150000)
	httpkit.SeedProfessional(env, salonID, "prof1")

	body := `{"professional_id":"prof1","client_name":"Lía","method":"cash","client_op_id":"op-abc",` +
		`"items":[{"service_id":"` + svcID + `","qty":1,"unit_price_cents":150000}]}`
	w1 := httpkit.Do(env.Router, "POST", base, body, ownerTok)
	w2 := httpkit.Do(env.Router, "POST", base, body, ownerTok)
	if w1.Code != http.StatusCreated || w2.Code != http.StatusCreated {
		t.Fatalf("reintento debe dar 201/201, dio %d/%d (%s | %s)", w1.Code, w2.Code, w1.Body.String(), w2.Body.String())
	}
	var o1, o2 struct {
		SaleID string `json:"sale_id"`
	}
	httpkit.MustJSON(t, w1.Body.Bytes(), &o1)
	httpkit.MustJSON(t, w2.Body.Bytes(), &o2)
	if o1.SaleID == "" || o1.SaleID != o2.SaleID {
		t.Fatalf("el replay debe devolver el mismo sale_id: %q vs %q", o1.SaleID, o2.SaleID)
	}
	w := httpkit.Do(env.Router, "GET", base, "", ownerTok)
	var list struct {
		Sales []struct {
			ID string `json:"id"`
		} `json:"sales"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if len(list.Sales) != 1 {
		t.Fatalf("debe existir una sola venta, hay %d", len(list.Sales))
	}
}
