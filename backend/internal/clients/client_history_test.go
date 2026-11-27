// F5: ficha editable, historial por rol (el dinero solo quien puede verlo) y el
// vínculo de turnos/ventas como best-effort. Todo con fakes: acá se prueba la
// REGLA, no SQL.
package clients

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// fakeClientLinks registra los vínculos pedidos y puede fallar como una DB caída.
type fakeClientLinks struct {
	bookingByID    []struct{ SalonID, BookingID, ClientID string }
	bookingByPhone []struct{ SalonID, BookingID string }
	sales          []struct{ SalonID, SaleID, ClientID string }
	bookingErr     error
	saleErr        error
}

func (f *fakeClientLinks) LinkBookingByID(salonID, bookingID, clientID string) error {
	if f.bookingErr != nil {
		return f.bookingErr
	}
	f.bookingByID = append(f.bookingByID, struct{ SalonID, BookingID, ClientID string }{salonID, bookingID, clientID})
	return nil
}

func (f *fakeClientLinks) LinkBookingByPhone(salonID, bookingID string) error {
	if f.bookingErr != nil {
		return f.bookingErr
	}
	f.bookingByPhone = append(f.bookingByPhone, struct{ SalonID, BookingID string }{salonID, bookingID})
	return nil
}

func (f *fakeClientLinks) LinkSaleByID(salonID, saleID, clientID string) error {
	if f.saleErr != nil {
		return f.saleErr
	}
	f.sales = append(f.sales, struct{ SalonID, SaleID, ClientID string }{salonID, saleID, clientID})
	return nil
}

// SaleClientID devuelve la ficha del último vínculo guardado de esa venta.
func (f *fakeClientLinks) SaleClientID(salonID, saleID string) (string, error) {
	for i := len(f.sales) - 1; i >= 0; i-- {
		if f.sales[i].SalonID == salonID && f.sales[i].SaleID == saleID {
			return f.sales[i].ClientID, nil
		}
	}
	return "", nil
}

// fakeClientHistory devuelve una foto fija y recuerda con qué argumentos se pidió.
type fakeClientHistory struct {
	data     domain.ClientHistoryData
	err      error
	calls    int
	salonID  string
	clientID string
	now      time.Time
	limit    int
}

func (f *fakeClientHistory) History(salonID, clientID string, now time.Time, limit int) (domain.ClientHistoryData, error) {
	f.calls++
	f.salonID, f.clientID, f.now, f.limit = salonID, clientID, now, limit
	if f.err != nil {
		return domain.ClientHistoryData{}, f.err
	}
	return f.data, nil
}

var _ = strings.TrimSpace

// actorOf guarda la membresía y devuelve la que ve el caso de uso.
func actorOf(m *testutil.FakeMemberships, userID, salonID string, role domain.Role) domain.Membership {
	_ = m.Save(domain.Membership{UserID: userID, SalonID: salonID, Role: role})
	got, _ := m.Find(userID, salonID)
	return got
}

func clientFixture(t *testing.T, f *fakeClients, salonID, name, phone string) domain.Client {
	t.Helper()
	c, err := domain.NewClient(salonID, name, phone, "", "")
	if err != nil {
		t.Fatalf("ficha: %v", err)
	}
	if err := f.Save(c); err != nil {
		t.Fatalf("guardar ficha: %v", err)
	}
	return c
}

// D5: GET lo ve cualquier miembro activo; PUT solo owner/admin/recepción. Otro
// salón es 403 (actor) o 404 «Ficha no encontrada» (ficha).
func TestClientService_get_y_put_por_rol(t *testing.T) {
	clients := &fakeClients{}
	mems := &testutil.FakeMemberships{}
	c := clientFixture(t, clients, "s1", "Lía Fernández", "11 5555-1234")
	ajena := clientFixture(t, clients, "s2", "Otra", "555")
	svc := ClientService{Clients: clients, Memberships: mems}

	roles := map[string]domain.Role{
		"owner": domain.RoleOwner, "admin": domain.RoleAdmin,
		"recep": domain.RoleReception, "prof": domain.RoleProfessional, "cli": domain.RoleClient,
	}
	for user, role := range roles {
		actor := actorOf(mems, user, "s1", role)
		got, err := svc.Get(actor, "s1", c.ID)
		if err != nil || got.ID != c.ID {
			t.Fatalf("%s no puede leer la ficha: %+v %v", user, got, err)
		}
	}
	// El actor de otro salón no entra ni con el id correcto.
	ajeno := actorOf(mems, "ajeno", "s2", domain.RoleOwner)
	if _, err := svc.Get(ajeno, "s1", c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("actor de otro salón: %v", err)
	}
	// Una ficha de otro salón no existe para este salón.
	if _, err := svc.Get(actorOf(mems, "owner", "s1", domain.RoleOwner), "s1", ajena.ID); err == nil ||
		err.Error() != domain.MsgClientNotFound {
		t.Fatalf("ficha de otro salón: %v", err)
	}

	// PUT: profesional y cliente 403; no escriben nada.
	for _, user := range []string{"prof", "cli"} {
		actor, _ := mems.Find(user, "s1")
		if _, err := svc.Update(actor, "s1", c.ID, "Otra", "555", "", "", "x"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no debe editar: %v", user, err)
		}
	}
	if clients.clients[0].Name != c.Name || clients.clients[0].Allergies != "" {
		t.Fatalf("un 403 no debe escribir: %+v", clients.clients[0])
	}
	// recepción/owner/admin sí, y las alergias quedan guardadas.
	for _, user := range []string{"recep", "admin", "owner"} {
		actor, _ := mems.Find(user, "s1")
		up, err := svc.Update(actor, "s1", c.ID, "Lía G.", "11 4444-0000", "LIA@mail.com", "nota", "nickel")
		if err != nil {
			t.Fatalf("%s edita: %v", user, err)
		}
		if up.Email != "lia@mail.com" || up.Allergies != "nickel" {
			t.Fatalf("%s: update incompleto %+v", user, up)
		}
	}
	stored, _ := clients.FindByID("s1", c.ID)
	if stored.Name != "Lía G." || stored.Allergies != "nickel" || stored.Email != "lia@mail.com" {
		t.Fatalf("no persistió: %+v", stored)
	}
	// Un PUT inválido no escribe NADA (ni el resto de los campos).
	owner, _ := mems.Find("owner", "s1")
	if _, err := svc.Update(owner, "s1", c.ID, "Cambio", "555", "", "", strings.Repeat("a", 501)); err == nil {
		t.Fatal("501 caracteres de alergias debe fallar")
	}
	if _, err := svc.Update(owner, "s1", c.ID, "Cambio", "555", "", "", "a\x00b"); err == nil ||
		err.Error() != domain.MsgClientInvalidChars {
		t.Fatalf("NUL: %v", err)
	}
	stored, _ = clients.FindByID("s1", c.ID)
	if stored.Name != "Lía G." || stored.Allergies != "nickel" {
		t.Fatalf("un 400 no debe escribir: %+v", stored)
	}
	// Ficha inexistente: 404 del contrato.
	if _, err := svc.Update(owner, "s1", "nope", "Lía", "555", "", "", ""); err == nil ||
		err.Error() != domain.MsgClientNotFound {
		t.Fatalf("PUT inexistente: %v", err)
	}
}

// D5: POST acepta alergias opcionales; el alta sin alergias las deja en "".
func TestClientService_create_con_alergias(t *testing.T) {
	clients := &fakeClients{}
	mems := &testutil.FakeMemberships{}
	svc := ClientService{Clients: clients, Memberships: mems}
	owner := actorOf(mems, "owner", "s1", domain.RoleOwner)

	c, err := svc.CreateWithAllergies(owner, "s1", "Lía", "555", "", "", "nickel")
	if err != nil || c.Allergies != "nickel" {
		t.Fatalf("alta con alergias: %+v %v", c, err)
	}
	base, err := svc.Create(owner, "s1", "Ana", "556", "", "")
	if err != nil || base.Allergies != "" {
		t.Fatalf("alta sin alergias: %+v %v", base, err)
	}
	prof := actorOf(mems, "prof", "s1", domain.RoleProfessional)
	if _, err := svc.CreateWithAllergies(prof, "s1", "X", "", "", "", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional no crea fichas: %v", err)
	}
	if _, err := svc.CreateWithAllergies(owner, "s1", "X", "", "", "", strings.Repeat("a", 501)); err == nil ||
		err.Error() != domain.MsgClientAllergiesTooLong {
		t.Fatalf("alergias largas en el alta: %v", err)
	}
}

// D6: cualquier miembro ve visitas y turnos; el dinero solo quien puede verlo.
func TestClientHistoryService_roles_y_dinero(t *testing.T) {
	clients := &fakeClients{}
	mems := &testutil.FakeMemberships{}
	c := clientFixture(t, clients, "s1", "Lía", "555")
	lastDone := time.Date(2026, 3, 8, 23, 59, 59, 0, time.UTC)
	next := time.Date(2026, 3, 20, 16, 0, 0, 0, time.UTC)
	history := &fakeClientHistory{data: domain.ClientHistoryData{
		Visits: 3, NoShows: 1, Cancelled: 2, Upcoming: 1,
		LastDoneAt: &lastDone, NextBookedAt: &next,
		Bookings: []domain.ClientHistoryBooking{{ID: "b1"}},
		Spend:    domain.ClientSpend{SalesCount: 2, TotalCents: 4500},
		Sales:    []domain.ClientHistorySale{{ID: "sa1", TotalCents: 4500}},
	}}
	now := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)
	settings := &testutil.FakeSettings{BySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/Mexico_City"},
	}}
	svc := ClientHistoryService{
		Clients: clients, Source: history, Memberships: mems, Settings: settings,
		Now: func() time.Time { return now },
	}

	prof := actorOf(mems, "prof", "s1", domain.RoleProfessional)
	view, err := svc.History(prof, "s1", c.ID)
	if err != nil {
		t.Fatalf("profesional ve el historial: %v", err)
	}
	if view.Spend != nil || view.Sales != nil {
		t.Fatalf("el profesional NO debe ver el dinero: %+v", view)
	}
	if len(view.Bookings) != 1 || view.Summary.Visits != 3 || view.Summary.Timezone != "America/Mexico_City" {
		t.Fatalf("visitas/turnos del profesional: %+v", view)
	}
	// La última visita es el día LOCAL del salón (23:59:59Z del 8 son las 17:59
	// del 8 en México).
	if view.Summary.LastVisit != "2026-03-08" {
		t.Fatalf("last_visit: %q", view.Summary.LastVisit)
	}
	if history.limit != ClientHistoryLimit || !history.now.Equal(now) || history.clientID != c.ID {
		t.Fatalf("argumentos de la lectura: %+v", history)
	}

	// owner/admin/recepción ven el dinero; el rol cliente no.
	for _, user := range []string{"owner", "admin", "recep"} {
		actor := actorOf(mems, user, "s1", map[string]domain.Role{
			"owner": domain.RoleOwner, "admin": domain.RoleAdmin, "recep": domain.RoleReception,
		}[user])
		v, err := svc.History(actor, "s1", c.ID)
		if err != nil {
			t.Fatalf("%s: %v", user, err)
		}
		if v.Spend == nil || v.Spend.TotalCents != 4500 || len(v.Sales) != 1 {
			t.Fatalf("%s debe ver el dinero: %+v", user, v)
		}
	}
	cli := actorOf(mems, "cli", "s1", domain.RoleClient)
	if v, err := svc.History(cli, "s1", c.ID); err != nil || v.Spend != nil {
		t.Fatalf("rol cliente ve visitas sin dinero: %+v %v", v, err)
	}

	// Aislamiento: otra ficha/otro salón no existen.
	ajena := clientFixture(t, clients, "s2", "Otra", "556")
	if _, err := svc.History(prof, "s1", ajena.ID); err == nil || err.Error() != domain.MsgClientNotFound {
		t.Fatalf("ficha de otro salón en el historial: %v", err)
	}
	if _, err := svc.History(actorOf(mems, "ajeno", "s2", domain.RoleOwner), "s1", c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("actor de otro salón en el historial: %v", err)
	}
}
