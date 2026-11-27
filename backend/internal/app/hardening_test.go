package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/auth"
	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
	"salon-saas/internal/settings"
	"salon-saas/internal/team"
	"salon-saas/internal/testutil"
)

// B-06: la clave se valida ANTES de hashear (8..72 bytes).
func TestAuth_register_valida_clave(t *testing.T) {
	svc := auth.AuthService{Users: &fakeUsers{}, Hasher: fakeHasher{}, Tokens: fakeTokens{}}
	for _, bad := range []string{"corta", strings.Repeat("x", domain.MaxPasswordBytes+1)} {
		if _, err := svc.Register("Ana", "ana@mail.com", bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("clave de %d bytes debe dar ErrValidation, dio %v", len(bad), err)
		}
	}
	if _, err := svc.Register("Ana", "ana@mail.com", "unaClaveLarga"); err != nil {
		t.Fatalf("clave válida rechazada: %v", err)
	}
}

// B-05: el email se normaliza antes de buscar, así el duplicado con mayúsculas
// se detecta y el login funciona sin importar cómo se tipeó.
func TestAuth_email_normalizado(t *testing.T) {
	users := &fakeUsers{}
	svc := auth.AuthService{Users: users, Hasher: fakeHasher{}, Tokens: fakeTokens{}}
	if _, err := svc.Register("Ana", "  Ana@Mail.COM ", "unaClaveLarga"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := svc.Register("Otra", "ANA@mail.com", "otraClaveLarga"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicado con mayúsculas debe dar ErrAlreadyExists, dio %v", err)
	}
	if _, err := svc.Login(" Ana@mail.com", "unaClaveLarga"); err != nil {
		t.Fatalf("login con email sin normalizar: %v", err)
	}
}

// B-05: si el usuario no existe igual se gasta el trabajo de bcrypt.
func TestAuth_login_usuario_inexistente_gasta_tiempo(t *testing.T) {
	h := &countingHasher{}
	svc := auth.AuthService{Users: &fakeUsers{}, Hasher: h, Tokens: fakeTokens{}}
	if _, err := svc.Login("nadie@mail.com", "unaClaveLarga"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("esperaba ErrUnauthorized, dio %v", err)
	}
	if h.dummyCalls != 1 {
		t.Fatalf("debe comparar contra el hash falso una vez, dio %d", h.dummyCalls)
	}
}

// B-04: el precio NO lo pone el cliente, lo pone el catálogo. Y un servicio
// inactivo no se vende (ya no está en la carta).
func TestSaleService_precio_del_catalogo_y_servicio_activo(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	mems := &fakeMemberships{}
	svc := pos.SaleService{Sales: sales, Services: services, Memberships: mems}
	catalogo(t, services, "s1")
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	corte := svcIDByName(t, services, "s1", "Corte")

	// El cliente manda 1 centavo por un corte de 150000: se cobra el catálogo.
	sale, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 2, UnitPriceCents: 1},
	}, domain.MethodCash)
	if err != nil {
		t.Fatalf("registrar: %v", err)
	}
	if sale.TotalCents != 300000 {
		t.Fatalf("total con precio de catálogo = 300000, dio %d", sale.TotalCents)
	}
	for _, it := range sale.Items {
		if it.UnitPriceCents != 150000 {
			t.Fatalf("el ítem debe guardar el precio del catálogo, dio %d", it.UnitPriceCents)
		}
	}

	// Un precio absurdo en el pedido se rechaza aunque el catálogo mande: el
	// pedido se valida tal como llegó (B-03).
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: corte, Qty: 1, UnitPriceCents: 2_000_000_000},
	}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("precio de 2e9 debe dar ErrValidation, dio %v", err)
	}

	// Servicio inactivo: 400, no se vende.
	inactivo, _ := domain.NewService("s1", "Keratina", 90, 500000, 10)
	inactivo, _ = inactivo.WithDetails(inactivo.Name, inactivo.DurationMin, inactivo.PriceCents, inactivo.CommissionPct, false)
	if err := services.Save(inactivo); err != nil {
		t.Fatalf("catálogo: %v", err)
	}
	if _, err := svc.RegisterSale(recep, "s1", "prof1", "Lía", []domain.SaleItem{
		{ServiceID: inactivo.ID, Qty: 1, UnitPriceCents: 500000},
	}, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("servicio inactivo debe dar ErrValidation, dio %v", err)
	}
}

// B-12: el professional_id tiene que ser alguien que atiende EN ESE salón.
func TestSaleService_profesional_debe_ser_del_salon(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	mems := &fakeMemberships{}
	svc := pos.SaleService{Sales: sales, Services: services, Memberships: mems}
	catalogo(t, services, "s1")
	recep := miembro(mems, "u-recep", "s1", domain.RoleReception)
	corte := svcIDByName(t, services, "s1", "Corte")
	items := []domain.SaleItem{{ServiceID: corte, Qty: 1, UnitPriceCents: 150000}}

	// Inexistente en el salón.
	if _, err := svc.RegisterSale(recep, "s1", "fantasma", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional inexistente debe dar ErrValidation, dio %v", err)
	}
	// Miembro del salón pero con rol que no atiende (recepción).
	miembro(mems, "u-otra-recep", "s1", domain.RoleReception)
	if _, err := svc.RegisterSale(recep, "s1", "u-otra-recep", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("recepción no puede ser el profesional, dio %v", err)
	}
	// Profesional de OTRO salón: tampoco.
	miembro(mems, "u-prof2", "s2", domain.RoleProfessional)
	if _, err := svc.RegisterSale(recep, "s1", "u-prof2", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional de otro salón debe dar ErrValidation, dio %v", err)
	}
	// Los tres roles que atienden sí pasan (F2a: el dueño y la administración
	// con la marca de "atiende clientes", como hasta ahora).
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleProfessional} {
		uid := "u-" + string(role)
		_ = mems.Save(domain.Membership{UserID: uid, SalonID: "s1", Role: role, Attends: true})
		if _, err := svc.RegisterSale(recep, "s1", uid, "Lía", items, domain.MethodCash); err != nil {
			t.Fatalf("%s debe poder ser el profesional de la venta: %v", role, err)
		}
	}
	// F2a: el dueño que no atiende ya no es el profesional de una venta.
	_ = mems.Save(domain.Membership{UserID: "u-duenio-escritorio", SalonID: "s1", Role: domain.RoleOwner})
	if _, err := svc.RegisterSale(recep, "s1", "u-duenio-escritorio", "Lía", items, domain.MethodCash); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("el dueño sin attends no atiende: %v", err)
	}
}

func TestBookingService_profesional_debe_ser_del_salon(t *testing.T) {
	mems := &fakeMemberships{}
	svc := bkg.BookingService{Bookings: &fb{}, Memberships: mems}
	rec := miembro(mems, "u-recep", "s1", domain.RoleReception)
	start := time.Now().UTC().Add(24 * time.Hour)

	if _, err := svc.CreateBooking(rec, "s1", "fantasma", "Lía", "", start, start.Add(time.Hour)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("profesional inexistente debe dar ErrValidation, dio %v", err)
	}
	miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	if _, err := svc.CreateBooking(rec, "s1", "u-prof", "Lía", "", start, start.Add(time.Hour)); err != nil {
		t.Fatalf("profesional del salón debe poder agendarse: %v", err)
	}
}

// B-09: el cierre corre en la transacción con lock del salón; sin Tx no hay
// cierre (producción siempre lo configura, los fakes de test pueden no hacerlo).
func TestCashCloseService_usa_la_transaccion_con_lock(t *testing.T) {
	sales := &testutil.FakeSales{}
	mems := &fakeMemberships{}
	closes := &testutil.FakeCashCloses{}
	tx := &fakeSalonTx{closes: closes, sales: sales}
	svc := pos.CashCloseService{CashCloses: closes, Sales: sales, Memberships: mems, Tx: tx}
	owner := miembro(mems, "u-owner", "s1", domain.RoleOwner)
	prof := miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	_ = prof

	sales.All = append(sales.All, domain.Sale{
		ID: "v1", SalonID: "s1", TotalCents: 300000, Method: domain.MethodCash,
		CreatedAt: time.Now().UTC(),
	})
	c, err := svc.CloseCash(owner, "s1", 300000, "")
	if err != nil {
		t.Fatalf("cerrar caja: %v", err)
	}
	if tx.locks != 1 || tx.lastSalonID != "s1" {
		t.Fatalf("el cierre debe tomar el lock del salón una vez: %+v", tx)
	}
	if c.SystemCashCents != 300000 {
		t.Fatalf("expected cash = 300000, dio %d", c.SystemCashCents)
	}
	if len(closes.All) != 1 {
		t.Fatalf("debe quedar un cierre, hay %d", len(closes.All))
	}
}

// --- Endpoints nuevos: /me, /staff, /public/salons/:slug ---

func TestMeService(t *testing.T) {
	users := &fakeUsers{}
	salons := &fakeSalons{}
	mems := &fakeMemberships{}
	svc := auth.MeService{Users: users, Salons: salons, Members: mems}

	u := domain.User{ID: "u1", Name: "Ana", Email: "ana@mail.com"}
	_ = users.Save(u)
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "navajas", Name: "Navajas de Oro"})
	_ = mems.Save(domain.Membership{UserID: "u1", SalonID: "s1", Role: domain.RoleOwner})

	me, err := svc.Me("u1")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if me.User.ID != "u1" || me.User.Email != "ana@mail.com" {
		t.Fatalf("usuario: %+v", me.User)
	}
	if len(me.Salons) != 1 || me.Salons[0].Slug != "navajas" || me.Salons[0].Role != domain.RoleOwner {
		t.Fatalf("salones: %+v", me.Salons)
	}
	// Usuario inexistente (token viejo): 401, no un 500.
	if _, err := svc.Me("u-fantasma"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("usuario inexistente debe dar ErrUnauthorized, dio %v", err)
	}
}

func TestStaffService(t *testing.T) {
	staff := &fakeStaffLister{BySalon: map[string][]domain.StaffMember{
		"s1": {
			{ID: "u-owner", Name: "Ana", Role: domain.RoleOwner, Attends: true},
			{ID: "u-prof", Name: "Bruno", Role: domain.RoleProfessional},
			// F2a: la administración sin la marca no atiende y no se lista.
			{ID: "u-admin", Name: "Carla", Role: domain.RoleAdmin},
			{ID: "u-recep", Name: "Dani", Role: domain.RoleReception, Attends: true},
		},
	}}
	mems := &fakeMemberships{}
	svc := team.StaffService{Staff: staff, Memberships: mems}
	_ = mems.Save(domain.Membership{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception})

	list, err := svc.List(domain.Membership{UserID: "u-recep", SalonID: "s1"}, "s1")
	if err != nil {
		t.Fatalf("staff: %v", err)
	}
	if len(list) != 2 || list[0].ID != "u-owner" || list[1].ID != "u-prof" {
		t.Fatalf("esperaba dueño (con attends) y profesional, dio %+v", list)
	}
	// No-miembro: 403 (no se filtra el equipo de otro salón).
	if _, err := svc.List(domain.Membership{UserID: "u-ajeno", SalonID: "s2"}, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no-miembro debe dar ErrForbidden, dio %v", err)
	}
}

func TestPublicSalonService(t *testing.T) {
	salons := &fakeSalons{}
	services := &testutil.FakeServices{}
	svc := settings.PublicSalonService{Salons: salons, Services: services}
	_ = salons.Save(domain.Salon{ID: "s1", Slug: "navajas", Name: "Navajas de Oro"})
	activo, _ := domain.NewService("s1", "Corte", 45, 150000, 20)
	_ = services.Save(activo)
	inactivo, _ := domain.NewService("s1", "Viejo", 30, 1000, 0)
	inactivo, _ = inactivo.WithDetails(inactivo.Name, inactivo.DurationMin, inactivo.PriceCents, inactivo.CommissionPct, false)
	_ = services.Save(inactivo)

	got, err := svc.Public("navajas")
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if got.Salon.Name != "Navajas de Oro" || len(got.Services) != 1 || got.Services[0].Name != "Corte" {
		t.Fatalf("landing: %+v", got)
	}
	if _, err := svc.Public("no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("slug inexistente debe dar ErrNotFound, dio %v", err)
	}
}
