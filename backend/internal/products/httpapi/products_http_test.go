package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"salon-saas/internal/domain"
	httpkit "salon-saas/internal/testutil/httpkit"
	"strings"
	"testing"
)

// apiProduct es la forma JSON del producto del contrato F6.
type apiProduct struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	PriceCents        int    `json:"price_cents"`
	Stock             int    `json:"stock"`
	LowStockThreshold int    `json:"low_stock_threshold"`
	Active            bool   `json:"active"`
	Low               bool   `json:"low"`
	CreatedAt         string `json:"created_at"`
}

func decodeProduct(t *testing.T, body []byte) apiProduct {
	t.Helper()
	var p apiProduct
	httpkit.MustJSON(t, body, &p)
	return p
}

// productsSetup arma un salón con owner y devuelve (env, token, salonID).
func productsSetup(t *testing.T) (*httpkit.Env, *httpkit.Products, string, string) {
	t.Helper()
	env, inv := httpkit.NewProductsEnv()
	ownerTok, _ := env.Register(t, "owner-prod@mail.com")
	salonID := env.Salon(t, ownerTok, "productos")
	return env, inv, ownerTok, salonID
}

func productBase(salonID string) string { return "/api/v1/salons/" + salonID + "/products" }

func createProductHTTP(t *testing.T, env *httpkit.Env, token, salonID, body string) apiProduct {
	t.Helper()
	w := httpkit.Do(env.Router, "POST", productBase(salonID), body, token)
	if w.Code != http.StatusCreated {
		t.Fatalf("crear producto: %d %s", w.Code, w.Body.String())
	}
	return decodeProduct(t, w.Body.Bytes())
}

// D7: listar productos y ver el stock = owner/admin/recepción; crear, editar,
// ajustar y ver movimientos = SOLO owner/admin; profesional y cliente 403; sin
// token 401; otro salón 403. Rol por rol en las cinco rutas.
func TestHTTP_products_roles(t *testing.T) {
	env, _, ownerTok, salonID := productsSetup(t)
	adminTok, adminID := env.Register(t, "admin-prod@mail.com")
	recepTok, recepID := env.Register(t, "recep-prod@mail.com")
	profTok, profID := env.Register(t, "prof-prod@mail.com")
	clientTok, clientID := env.Register(t, "client-prod@mail.com")
	httpkit.SeedRole(t, env, salonID, adminID, domain.RoleAdmin)
	httpkit.SeedRole(t, env, salonID, recepID, domain.RoleReception)
	httpkit.SeedRole(t, env, salonID, profID, domain.RoleProfessional)
	httpkit.SeedRole(t, env, salonID, clientID, domain.RoleClient)

	created := createProductHTTP(t, env, ownerTok, salonID, `{"name":"Shampoo","price_cents":50000,"stock":5,"low_stock_threshold":2}`)
	base := productBase(salonID) + "/" + created.ID
	adjust := base + "/adjust"
	movements := base + "/movements"

	// Leer: owner, admin y recepción.
	for _, tok := range []string{ownerTok, adminTok, recepTok} {
		if w := httpkit.Do(env.Router, "GET", productBase(salonID), "", tok); w.Code != http.StatusOK {
			t.Fatalf("listar con rol válido: %d %s", w.Code, w.Body.String())
		}
	}
	// Profesional y cliente: 403 en TODO (listar incluido).
	for _, tok := range []string{profTok, clientTok} {
		for _, req := range []struct{ method, path, body string }{
			{"GET", productBase(salonID), ""},
			{"POST", productBase(salonID), `{"name":"X","price_cents":1}`},
			{"PUT", base, `{"name":"X","price_cents":1,"low_stock_threshold":0,"active":true}`},
			{"POST", adjust, `{"delta":1,"reason":"restock"}`},
			{"GET", movements, ""},
		} {
			if w := httpkit.Do(env.Router, req.method, req.path, req.body, tok); w.Code != http.StatusForbidden {
				t.Fatalf("%s %s debe dar 403, dio %d %s", req.method, req.path, w.Code, w.Body.String())
			}
		}
	}
	// Recepción: lista pero NO escribe ni ve movimientos.
	if w := httpkit.Do(env.Router, "POST", productBase(salonID), `{"name":"Cera","price_cents":1}`, recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción no crea: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "PUT", base, `{"name":"Cera","price_cents":1,"low_stock_threshold":0,"active":true}`, recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción no edita: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "POST", adjust, `{"delta":1,"reason":"restock"}`, recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción no ajusta: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "GET", movements, "", recepTok); w.Code != http.StatusForbidden {
		t.Fatalf("recepción no ve movimientos: %d", w.Code)
	}
	// Admin SÍ escribe.
	if w := httpkit.Do(env.Router, "POST", productBase(salonID), `{"name":"Cera admin","price_cents":30000}`, adminTok); w.Code != http.StatusCreated {
		t.Fatalf("admin crea: %d %s", w.Code, w.Body.String())
	}
	// Sin token: 401 en todas.
	for _, req := range []struct{ method, path, body string }{
		{"GET", productBase(salonID), ""},
		{"POST", productBase(salonID), `{"name":"X","price_cents":1}`},
		{"PUT", base, `{}`},
		{"POST", adjust, `{}`},
		{"GET", movements, ""},
	} {
		if w := httpkit.Do(env.Router, req.method, req.path, req.body, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s sin token debe dar 401, dio %d", req.method, req.path, w.Code)
		}
	}
	// Otro salón: no es miembro -> 403; y su propio salón no ve el producto ajeno -> 404.
	otherTok, _ := env.Register(t, "otro-prod@mail.com")
	otherSalon := env.Salon(t, otherTok, "otro-productos")
	if w := httpkit.Do(env.Router, "GET", productBase(salonID), "", otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("otro salón listando: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "PUT", base, `{"name":"X","price_cents":1,"low_stock_threshold":0,"active":true}`, otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("otro salón editando: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "PUT", productBase(otherSalon)+"/"+created.ID, `{"name":"X","price_cents":1,"low_stock_threshold":0,"active":true}`, otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("producto de otro salón: %d %s", w.Code, w.Body.String())
	}
	if w := httpkit.Do(env.Router, "GET", productBase(otherSalon)+"/"+created.ID+"/movements", "", otherTok); w.Code != http.StatusNotFound {
		t.Fatalf("movimientos de producto de otro salón: %d", w.Code)
	}
}

// Validaciones exactas del contrato: textos, NUL, tipos de JSON, PUT completo,
// nombre activo único y el 400 no escribe nada.
func TestHTTP_products_validations(t *testing.T) {
	env, _, ownerTok, salonID := productsSetup(t)
	base := productBase(salonID)

	// Un 400 no escribe nada: después del error la lista sigue vacía.
	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"name":"","price_cents":100}`, domain.MsgProductNameRequired},
		{`{"name":"` + fmt.Sprintf("%0121d", 0) + `","price_cents":100}`, domain.MsgProductNameTooLong},
		{`{"name":"Sha\u0000mpoo","price_cents":100}`, domain.MsgProductInvalidChars},
		{`{"name":"X","price_cents":-1}`, domain.MsgProductPriceRange},
		{`{"name":"X","price_cents":10000001}`, domain.MsgProductPriceRange},
		{`{"name":"X","price_cents":1,"stock":-1}`, domain.MsgProductStockRange},
		{`{"name":"X","price_cents":1,"low_stock_threshold":1000001}`, domain.MsgProductStockRange},
	} {
		w := httpkit.Do(env.Router, "POST", base, tc.body, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s debe dar 400, dio %d %s", tc.body, w.Code, w.Body.String())
		}
		var out struct{ Error string }
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out.Error != tc.want {
			t.Fatalf("mensaje = %q, quería %q", out.Error, tc.want)
		}
	}
	if w := httpkit.Do(env.Router, "GET", base, "", ownerTok); w.Code != http.StatusOK {
		t.Fatalf("listar: %d", w.Code)
	} else {
		var out struct{ Products []apiProduct }
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if len(out.Products) != 0 {
			t.Fatalf("un 400 no escribe nada: %+v", out.Products)
		}
	}

	// JSON de tipo equivocado: 400, nunca 500.
	for _, body := range []string{
		`{"name":123,"price_cents":1}`,
		`{"name":"X","price_cents":"mucho"}`,
		`{"name":"X","price_cents":1,"stock":"dos"}`,
		`{`,
	} {
		if w := httpkit.Do(env.Router, "POST", base, body, ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("JSON %s debe dar 400, dio %d %s", body, w.Code, w.Body.String())
		}
	}

	product := createProductHTTP(t, env, ownerTok, salonID, `{"name":"Shampoo","price_cents":50000,"stock":5,"low_stock_threshold":2}`)
	if product.Stock != 5 || product.LowStockThreshold != 2 || !product.Active || product.CreatedAt == "" {
		t.Fatalf("producto creado incompleto: %+v", product)
	}
	// Nombre activo duplicado (sin importar mayúsculas): 409 exacto.
	w := httpkit.Do(env.Router, "POST", base, `{"name":"shampoo","price_cents":1}`, ownerTok)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicado: %d", w.Code)
	}
	var dup struct{ Error string }
	httpkit.MustJSON(t, w.Body.Bytes(), &dup)
	if dup.Error != domain.MsgProductNameTaken {
		t.Fatalf("mensaje duplicado: %q", dup.Error)
	}

	// PUT incompleto: 400 con el texto del contrato y NO escribe nada.
	for _, body := range []string{
		`{"name":"Nuevo","price_cents":9,"low_stock_threshold":1}`,
		`{"name":"Nuevo","price_cents":9,"active":true}`,
		`{"price_cents":9,"low_stock_threshold":1,"active":true}`,
		`{"name":null,"price_cents":9,"low_stock_threshold":1,"active":true}`,
	} {
		w := httpkit.Do(env.Router, "PUT", base+"/"+product.ID, body, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("PUT incompleto %s: %d", body, w.Code)
		}
		var out struct{ Error string }
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out.Error != domain.MsgProductMissingFields {
			t.Fatalf("mensaje de PUT incompleto: %q", out.Error)
		}
	}
	var list struct{ Products []apiProduct }
	w = httpkit.Do(env.Router, "GET", base, "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if list.Products[0].Name != "Shampoo" || list.Products[0].PriceCents != 50000 {
		t.Fatalf("PUT incompleto escribió: %+v", list.Products[0])
	}

	// PUT completo: reemplaza los cuatro campos y NO toca el stock.
	w = httpkit.Do(env.Router, "PUT", base+"/"+product.ID, `{"name":"Shampoo premium","price_cents":60000,"low_stock_threshold":3,"active":true}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	up := decodeProduct(t, w.Body.Bytes())
	if up.Name != "Shampoo premium" || up.PriceCents != 60000 || up.LowStockThreshold != 3 || up.Stock != 5 {
		t.Fatalf("PUT: %+v", up)
	}
	// NUL en el PUT también es 400 exacto; tipo equivocado, 400.
	w = httpkit.Do(env.Router, "PUT", base+"/"+product.ID, "{\"name\":\"a\\u0000b\",\"price_cents\":1,\"low_stock_threshold\":0,\"active\":true}", ownerTok)
	var nul struct{ Error string }
	httpkit.MustJSON(t, w.Body.Bytes(), &nul)
	if w.Code != http.StatusBadRequest || nul.Error != domain.MsgProductInvalidChars {
		t.Fatalf("NUL en PUT: %d %q", w.Code, nul.Error)
	}
	if w := httpkit.Do(env.Router, "PUT", base+"/"+product.ID, `{"name":"X","price_cents":1,"low_stock_threshold":0,"active":"sí"}`, ownerTok); w.Code != http.StatusBadRequest {
		t.Fatalf("tipo equivocado en PUT: %d", w.Code)
	}
	// Producto inactivo: 200 y `low` deja de aplicar.
	w = httpkit.Do(env.Router, "PUT", base+"/"+product.ID, `{"name":"Shampoo premium","price_cents":60000,"low_stock_threshold":3,"active":false}`, ownerTok)
	off := decodeProduct(t, w.Body.Bytes())
	if w.Code != http.StatusOK || off.Active || off.Low {
		t.Fatalf("desactivar: %d %+v", w.Code, off)
	}
}

// Ajuste de stock: motivo, signo, cero, negativo, nota y el libro de
// movimientos con `limit` 1..100 (default 50).
func TestHTTP_products_adjust_movements(t *testing.T) {
	env, _, ownerTok, salonID := productsSetup(t)
	base := productBase(salonID)
	product := createProductHTTP(t, env, ownerTok, salonID, `{"name":"Shampoo","price_cents":10000,"stock":6,"low_stock_threshold":5}`)
	adjust := base + "/" + product.ID + "/adjust"
	movements := base + "/" + product.ID + "/movements"

	for _, tc := range []struct {
		body string
		code int
		want string
	}{
		{`{"delta":0,"reason":"restock"}`, http.StatusBadRequest, domain.MsgProductAdjustZero},
		{`{"delta":1,"reason":"initial"}`, http.StatusBadRequest, domain.MsgProductAdjustReason},
		{`{"delta":1,"reason":"sale"}`, http.StatusBadRequest, domain.MsgProductAdjustReason},
		{`{"delta":1,"reason":"otro"}`, http.StatusBadRequest, domain.MsgProductAdjustReason},
		{`{"delta":-1,"reason":"restock"}`, http.StatusBadRequest, domain.MsgProductAdjustSign},
		{`{"delta":1,"reason":"loss"}`, http.StatusBadRequest, domain.MsgProductAdjustSign},
		{`{"delta":-50,"reason":"loss"}`, http.StatusConflict, domain.MsgProductNegativeStock},
	} {
		w := httpkit.Do(env.Router, "POST", adjust, tc.body, ownerTok)
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
		var out struct{ Error string }
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out.Error != tc.want {
			t.Fatalf("%s: mensaje %q, quería %q", tc.body, out.Error, tc.want)
		}
	}
	// Nada de lo rechazado movió el stock.
	var list struct{ Products []apiProduct }
	w := httpkit.Do(env.Router, "GET", base, "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &list)
	if list.Products[0].Stock != 6 {
		t.Fatalf("un ajuste inválido no cambia el stock: %+v", list.Products[0])
	}
	// Ajuste válido: stock +2 y `low` se recalcula.
	w = httpkit.Do(env.Router, "POST", adjust, `{"delta":2,"reason":"restock","note":"compra"}`, ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("restock: %d %s", w.Code, w.Body.String())
	}
	if p := decodeProduct(t, w.Body.Bytes()); p.Stock != 8 || p.Low {
		t.Fatalf("restock: %+v", p)
	}
	// Cruzar el umbral hacia abajo deja el aviso «Stock bajo» una vez.
	w = httpkit.Do(env.Router, "POST", adjust, `{"delta":-3,"reason":"loss","note":"rotura"}`, ownerTok)
	if p := decodeProduct(t, w.Body.Bytes()); p.Stock != 5 || !p.Low {
		t.Fatalf("stock bajo: %+v", p)
	}
	var notifs []domain.Notification
	for _, n := range env.Notifications.All {
		if n.Title == "Stock bajo" {
			notifs = append(notifs, n)
		}
	}
	if len(notifs) != 1 {
		t.Fatalf("aviso de stock bajo una vez: %d", len(notifs))
	}

	// Movimientos: más reciente primero, con limit y errores de limit.
	w = httpkit.Do(env.Router, "GET", movements, "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("movimientos: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Movements []struct {
			ID        string  `json:"id"`
			Delta     int     `json:"delta"`
			Reason    string  `json:"reason"`
			Note      string  `json:"note"`
			SaleID    *string `json:"sale_id"`
			CreatedAt string  `json:"created_at"`
		} `json:"movements"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if len(out.Movements) != 3 {
		t.Fatalf("movimientos = 3 (initial + 2 ajustes), dio %d", len(out.Movements))
	}
	if out.Movements[0].Delta != -2 && out.Movements[0].Delta != -3 {
		// el último ajuste fue -3
		t.Fatalf("más reciente primero: %+v", out.Movements[0])
	}
	if out.Movements[0].Delta != -3 || out.Movements[0].Reason != "loss" || out.Movements[0].Note != "rotura" ||
		out.Movements[0].SaleID != nil || out.Movements[0].CreatedAt == "" {
		t.Fatalf("forma del movimiento: %+v", out.Movements[0])
	}
	w = httpkit.Do(env.Router, "GET", movements+"?limit=1", "", ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || len(out.Movements) != 1 {
		t.Fatalf("limit=1: %d %d", w.Code, len(out.Movements))
	}
	for _, limit := range []string{"0", "101", "abc"} {
		if w := httpkit.Do(env.Router, "GET", movements+"?limit="+limit, "", ownerTok); w.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s debe dar 400, dio %d %s", limit, w.Code, w.Body.String())
		}
	}
}

// POST /sales con `products` (D5/D7): productos solos, mixta, repetidos, stock
// insuficiente, producto inválido y permisos. La respuesta conserva la forma.
func TestHTTP_sales_with_products(t *testing.T) {
	env, inv, ownerTok, salonID := productsSetup(t)
	recepTok, recepID := env.Register(t, "recep-venta@mail.com")
	profTok, profID := env.Register(t, "prof-venta@mail.com")
	clientTok, clientID := env.Register(t, "client-venta@mail.com")
	httpkit.SeedRole(t, env, salonID, recepID, domain.RoleReception)
	httpkit.SeedProfessional(env, salonID, profID)
	httpkit.SeedRole(t, env, salonID, clientID, domain.RoleClient)

	svc := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, ownerTok)
	if svc.Code != http.StatusCreated {
		t.Fatalf("servicio: %d %s", svc.Code, svc.Body.String())
	}
	var created struct{ ID string }
	httpkit.MustJSON(t, svc.Body.Bytes(), &created)
	shampoo := createProductHTTP(t, env, ownerTok, salonID, `{"name":"Shampoo","price_cents":50000,"stock":10}`)
	cera := createProductHTTP(t, env, ownerTok, salonID, `{"name":"Cera","price_cents":30000,"stock":1}`)
	_ = cera
	salesURL := "/api/v1/salons/" + salonID + "/sales"

	type saleResponse struct {
		SaleID   string `json:"sale_id"`
		Total    int    `json:"total"`
		ClientID string `json:"client_id"`
	}

	// Venta SOLO de productos: 201 con el total del servidor.
	w := httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","products":[{"product_id":"`+shampoo.ID+`","qty":2}]}`, recepTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta de productos: %d %s", w.Code, w.Body.String())
	}
	var sale saleResponse
	httpkit.MustJSON(t, w.Body.Bytes(), &sale)
	if sale.Total != 100000 || sale.SaleID == "" || sale.ClientID != "" {
		t.Fatalf("respuesta de la venta: %+v", sale)
	}
	if got, _ := inv.FindByID(salonID, shampoo.ID); got.Stock != 8 {
		t.Fatalf("stock descontado: %d", got.Stock)
	}

	// Venta MIXTA: total = servicios + productos.
	w = httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"card","items":[{"service_id":"`+created.ID+`","qty":1}],"products":[{"product_id":"`+shampoo.ID+`","qty":1}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta mixta: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &sale)
	if sale.Total != 200000 {
		t.Fatalf("total mixto = 200000, dio %d", sale.Total)
	}

	// Repetidos: se suman en una línea (2 + 1 = 3) y bajan el stock una vez.
	w = httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","products":[{"product_id":"`+shampoo.ID+`","qty":2},{"product_id":"`+shampoo.ID+`","qty":1}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("repetidos: %d %s", w.Code, w.Body.String())
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &sale)
	if sale.Total != 150000 {
		t.Fatalf("total repetidos = 150000, dio %d", sale.Total)
	}
	if got, _ := inv.FindByID(salonID, shampoo.ID); got.Stock != 4 {
		t.Fatalf("stock tras las ventas: %d", got.Stock)
	}

	// Con 4 unidades en stock, 5 dan 409 verbatim y el stock no cambia.
	before, _ := inv.FindByID(salonID, shampoo.ID)
	w = httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","products":[{"product_id":"`+shampoo.ID+`","qty":5}]}`, ownerTok)
	if w.Code != http.StatusConflict {
		t.Fatalf("sin stock: %d %s", w.Code, w.Body.String())
	}
	var errOut struct{ Error string }
	httpkit.MustJSON(t, w.Body.Bytes(), &errOut)
	if errOut.Error != domain.NoStockMessage("Shampoo") {
		t.Fatalf("mensaje de stock: %q", errOut.Error)
	}
	if after, _ := inv.FindByID(salonID, shampoo.ID); after.Stock != before.Stock {
		t.Fatalf("el stock cambió con la venta rechazada: %d -> %d", before.Stock, after.Stock)
	}

	// Un precio enviado se IGNORA: el total sale del catálogo.
	w = httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","products":[{"product_id":"`+shampoo.ID+`","qty":1,"unit_price_cents":1}]}`, ownerTok)
	httpkit.MustJSON(t, w.Body.Bytes(), &sale)
	if w.Code != http.StatusCreated || sale.Total != 50000 {
		t.Fatalf("precio manipulado: %d total %d", w.Code, sale.Total)
	}

	// Producto de otro salón / inactivo: 400 «Producto inválido» y nada guardado.
	otherTok, _ := env.Register(t, "otro-venta@mail.com")
	otherSalon := env.Salon(t, otherTok, "otro-venta")
	foreign := createProductHTTP(t, env, otherTok, otherSalon, `{"name":"Ajeno","price_cents":1000,"stock":5}`)
	for _, pid := range []string{foreign.ID, "no-existe"} {
		w = httpkit.Do(env.Router, "POST", salesURL, `{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","products":[{"product_id":"`+pid+`","qty":1}]}`, ownerTok)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("producto %q: %d %s", pid, w.Code, w.Body.String())
		}
		var out struct{ Error string }
		httpkit.MustJSON(t, w.Body.Bytes(), &out)
		if out.Error != domain.MsgProductInvalid {
			t.Fatalf("mensaje de producto inválido: %q", out.Error)
		}
	}

	// Permisos: recepción vende; profesional y cliente no; sin token 401; otro
	// salón 403.
	body := `{"professional_id":"` + profID + `","client_name":"Lía","method":"cash","products":[{"product_id":"` + shampoo.ID + `","qty":1}]}`
	if w := httpkit.Do(env.Router, "POST", salesURL, body, profTok); w.Code != http.StatusForbidden {
		t.Fatalf("profesional no vende: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "POST", salesURL, body, clientTok); w.Code != http.StatusForbidden {
		t.Fatalf("cliente no vende: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "POST", salesURL, body, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: %d", w.Code)
	}
	if w := httpkit.Do(env.Router, "POST", salesURL, body, otherTok); w.Code != http.StatusForbidden {
		t.Fatalf("otro salón: %d", w.Code)
	}
}

// PRESERVE: una venta SIN `products` responde exactamente igual que antes.
func TestHTTP_sales_without_products_unchanged(t *testing.T) {
	env, inv, ownerTok, salonID := productsSetup(t)
	_, profID := env.Register(t, "prof-clasica@mail.com")
	httpkit.SeedProfessional(env, salonID, profID)
	svc := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/services",
		`{"name":"Corte","duration_min":45,"price_cents":150000,"commission_pct":20}`, ownerTok)
	var created struct{ ID string }
	httpkit.MustJSON(t, svc.Body.Bytes(), &created)

	w := httpkit.Do(env.Router, "POST", "/api/v1/salons/"+salonID+"/sales",
		`{"professional_id":"`+profID+`","client_name":"Lía","method":"cash","items":[{"service_id":"`+created.ID+`","qty":2}]}`, ownerTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("venta clásica: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		SaleID   string `json:"sale_id"`
		Total    int    `json:"total"`
		ClientID string `json:"client_id"`
	}
	httpkit.MustJSON(t, w.Body.Bytes(), &out)
	if out.Total != 300000 || out.SaleID == "" {
		t.Fatalf("venta clásica: %+v", out)
	}
	if len(inv.Movements) != 0 {
		t.Fatalf("una venta sin productos no toca el stock: %+v", inv.Movements)
	}
	// GET /sales sigue devolviendo la venta con sus ítems.
	w = httpkit.Do(env.Router, "GET", "/api/v1/salons/"+salonID+"/sales", "", ownerTok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /sales: %d", w.Code)
	}
	var list struct {
		Sales []struct {
			ID         string `json:"id"`
			TotalCents int    `json:"total_cents"`
			Items      []any  `json:"items"`
		} `json:"sales"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("json de ventas: %v", err)
	}
	if len(list.Sales) != 1 || list.Sales[0].TotalCents != 300000 || len(list.Sales[0].Items) != 1 {
		t.Fatalf("GET /sales: %+v", list.Sales)
	}
}

// Verificador independiente (F6): un POST sin precio, o con `price_cents: null`,
// creaba un producto GRATIS en silencio. Un campo obligatorio no se rellena por
// defecto (misma lección que el PUT de F3.2): 400 y no se escribe nada. Un precio
// 0 explícito sí es válido (un producto regalo existe).
func TestHTTP_products_create_exige_el_precio(t *testing.T) {
	env, _, ownerTok, salonID := productsSetup(t)
	base := productBase(salonID)

	for _, body := range []string{
		`{"name":"Sin precio"}`,
		`{"name":"Precio nulo","price_cents":null}`,
		`{"name":"Precio nulo con stock","price_cents":null,"stock":3}`,
	} {
		w := httpkit.Do(env.Router, "POST", base, body, ownerTok)
		if w.Code != http.StatusBadRequest || httpkit.OkErr(t, w.Body.Bytes()) != "Faltan datos del producto: nombre y precio" {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	// No se escribió nada.
	w := httpkit.Do(env.Router, "GET", base, "", ownerTok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"products":[]`) {
		t.Fatalf("un 400 no debía crear nada: %d %s", w.Code, w.Body.String())
	}
	// El precio 0 explícito sigue siendo válido.
	if w := httpkit.Do(env.Router, "POST", base, `{"name":"Regalo","price_cents":0}`, ownerTok); w.Code != http.StatusCreated {
		t.Fatalf("precio 0 explícito: %d %s", w.Code, w.Body.String())
	}
}
