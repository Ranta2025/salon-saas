// Vínculo de la ficha con turnos y ventas (F5, D3): los casos de uso del nudo y
// del POS vinculan como best-effort. Viven en app porque prueban esos servicios.
package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/pos"
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

// D3-b: el turno del personal se vincula por teléfono como best-effort. Un
// fallo del UPDATE no tumba el turno y el replay no toca el vínculo.
func TestBooking_link_best_effort_y_replay(t *testing.T) {
	bookings := &fb{}
	mems := &fakeMemberships{}
	links := &fakeClientLinks{}
	svc := BookingService{Bookings: bookings, Memberships: mems, ClientLinks: links}
	rec := recepcionista(mems)
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	day := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)

	b, err := svc.CreateBooking(rec, "s1", "prof1", "Lía", "11 5555-1234", day, day.Add(time.Hour))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	if len(links.bookingByPhone) != 1 || links.bookingByPhone[0].BookingID != b.ID ||
		links.bookingByPhone[0].SalonID != "s1" {
		t.Fatalf("el turno debe pedir el vínculo por teléfono: %+v", links.bookingByPhone)
	}

	// Falla el UPDATE de vínculo: el turno entra igual y no hay error.
	links.bookingErr = errors.New("db caída")
	if _, err := svc.CreateBooking(rec, "s1", "prof1", "Ana", "556", day.Add(2*time.Hour), day.Add(3*time.Hour)); err != nil {
		t.Fatalf("un fallo del vínculo no debe tumbar el turno: %v", err)
	}

	// Replay por client_op_id: devuelve el original y NO vuelve a vincular.
	links.bookingErr = nil
	antes := len(links.bookingByPhone)
	if _, err := svc.CreateBookingWithOp(rec, "s1", "prof1", "Eva", "557", day.Add(4*time.Hour), day.Add(5*time.Hour), "op-b"); err != nil {
		t.Fatalf("turno con op: %v", err)
	}
	original, err := svc.CreateBookingWithOp(rec, "s1", "prof1", "Eva", "557", day.Add(4*time.Hour), day.Add(5*time.Hour), "op-b")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if original.ID == "" || len(links.bookingByPhone) != antes+1 {
		t.Fatalf("el replay no debe volver a vincular: %+v", links.bookingByPhone)
	}
}

// D3-c: el POS acepta client_id opcional. Una ficha de otro salón es 400 y no
// guarda la venta; sin client_id no hay vínculo. Un fallo del UPDATE no tumba la
// venta y el replay no toca el vínculo.
func TestSale_client_id_y_best_effort(t *testing.T) {
	sales := &testutil.FakeSales{}
	services := &testutil.FakeServices{}
	service, _ := domain.NewService("s1", "Corte", 45, 1000, 10)
	_ = services.Save(service)
	mems := &fakeMemberships{}
	miembro(mems, "prof1", "s1", domain.RoleProfessional)
	owner := actorOf(mems, "owner", "s1", domain.RoleOwner)
	clients := &testutil.FakeClients{}
	c := clientFixture(t, clients, "s1", "Lía Fernández", "555")
	ajena := clientFixture(t, clients, "s2", "Ajena", "556")
	links := &fakeClientLinks{}
	svc := pos.SaleService{
		Sales: sales, Services: services, Memberships: mems,
		Clients: clients, ClientLinks: links,
	}
	items := []domain.SaleItem{{ServiceID: service.ID, Qty: 1, UnitPriceCents: 1000}}

	// Sin client_id: se comporta como siempre y no vincula nada.
	if _, err := svc.RegisterSaleWithOp(owner, "s1", "prof1", "Lía", items, domain.MethodCash, "op-0"); err != nil {
		t.Fatalf("venta sin ficha: %v", err)
	}
	if len(links.sales) != 0 {
		t.Fatalf("sin client_id no debe vincular: %+v", links.sales)
	}

	// client_name vacío se completa con el nombre de la ficha.
	sale, err := svc.RegisterSaleForClient(owner, "s1", "prof1", "", c.ID, items, domain.MethodCard, "op-1")
	if err != nil {
		t.Fatalf("venta con ficha: %v", err)
	}
	if sale.ClientName != "Lía Fernández" {
		t.Fatalf("el nombre debe salir de la ficha: %q", sale.ClientName)
	}
	if len(links.sales) != 1 || links.sales[0].SaleID != sale.ID || links.sales[0].ClientID != c.ID {
		t.Fatalf("la venta debe vincular la ficha: %+v", links.sales)
	}

	// Ficha de otro salón: 400 del contrato y la venta NO se guarda.
	antes := len(sales.All)
	_, err = svc.RegisterSaleForClient(owner, "s1", "prof1", "X", ajena.ID, items, domain.MethodCash, "")
	if err == nil || err.Error() != domain.MsgSaleClientInvalid {
		t.Fatalf("ficha de otro salón: %v", err)
	}
	if len(sales.All) != antes {
		t.Fatalf("una ficha inválida no debe guardar la venta")
	}

	// Un fallo del UPDATE de vínculo no tumba la venta.
	links.saleErr = errors.New("db caída")
	if _, err := svc.RegisterSaleForClient(owner, "s1", "prof1", "", c.ID, items, domain.MethodCash, ""); err != nil {
		t.Fatalf("un fallo del vínculo no debe tumbar la venta: %v", err)
	}

	// Replay por client_op_id: devuelve la venta original sin volver a vincular.
	links.saleErr = nil
	vinculos := len(links.sales)
	replay, err := svc.RegisterSaleForClient(owner, "s1", "prof1", "", c.ID, items, domain.MethodCard, "op-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ID != sale.ID || len(links.sales) != vinculos {
		t.Fatalf("el replay no debe tocar el vínculo: %+v", links.sales)
	}
}

// D3-a: la reserva pública vincula el turno a la ficha que ya crea o reusa.
func TestPublicBooking_vincula_la_ficha(t *testing.T) {
	env := newPublicEnv(t)
	links := &fakeClientLinks{}
	book := env.book
	book.ClientLinks = links

	in := publicInput()
	in.ServiceID = env.corte.ID
	in.ProfessionalID = "prof1"
	b, err := book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	if len(env.clients.Clients) != 1 {
		t.Fatalf("la reserva debe dejar la ficha: %+v", env.clients.Clients)
	}
	if len(links.bookingByID) != 1 || links.bookingByID[0].BookingID != b.ID ||
		links.bookingByID[0].ClientID != env.clients.Clients[0].ID || links.bookingByID[0].SalonID != "s1" {
		t.Fatalf("vínculo de la reserva pública: %+v", links.bookingByID)
	}

	// Falla el vínculo: la reserva entra igual.
	book.ClientLinks = &fakeClientLinks{bookingErr: errors.New("db caída")}
	in.StartsAt = in.StartsAt.Add(2 * time.Hour)
	if _, err := book.Book("navajas-oro", in); err != nil {
		t.Fatalf("un fallo del vínculo no debe tumbar la reserva pública: %v", err)
	}
}

var _ = strings.TrimSpace

// actorOf guarda la membresía y devuelve la que ve el caso de uso.
func actorOf(m *fakeMemberships, userID, salonID string, role domain.Role) domain.Membership {
	_ = m.Save(domain.Membership{UserID: userID, SalonID: salonID, Role: role})
	got, _ := m.Find(userID, salonID)
	return got
}

func clientFixture(t *testing.T, f *testutil.FakeClients, salonID, name, phone string) domain.Client {
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
