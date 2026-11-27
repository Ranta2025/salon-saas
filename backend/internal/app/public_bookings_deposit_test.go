package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// fakeDepositCreator es el DepositCreator de la reserva pública (F11, D8): crea
// una sena con un porcentaje fijo, o falla si el test lo pide.
type fakeDepositCreator struct {
	rows     map[string]domain.BookingDeposit
	percent  int
	charges  int
	calls    int
	fail     error
	lastPric int
}

func newFakeDepositCreator(percent int) *fakeDepositCreator {
	return &fakeDepositCreator{rows: map[string]domain.BookingDeposit{}, percent: percent}
}

func (f *fakeDepositCreator) CreateForBooking(o domain.DepositOrder) (*domain.BookingDeposit, error) {
	f.calls++
	f.lastPric = o.PriceCents
	if f.fail != nil {
		return nil, f.fail
	}
	if d, ok := f.rows[o.BookingID]; ok {
		return &d, nil
	}
	amount := domain.DepositAmountCents(o.PriceCents, f.percent)
	if amount < domain.MinDepositAmountCents {
		return nil, nil
	}
	chargeID, err := domain.NewDepositChargeID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	d := domain.BookingDeposit{
		ID: domain.NewID(), SalonID: o.SalonID, BookingID: o.BookingID,
		AmountCents: amount, Percent: f.percent, Status: domain.DepositPending,
		ChargeID: chargeID, CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
		ClientOpID: o.ClientOpID,
	}
	f.rows[o.BookingID] = d
	f.charges++
	return &d, nil
}

var _ DepositCreator = (*fakeDepositCreator)(nil)

// depPublicEnv deja la reserva pública lista para cobrar seña: el creator
// cableado, APP_URL y un mailer que cuenta.
func depPublicEnv(t *testing.T, creator DepositCreator) (*publicEnv, *fakeMailer) {
	t.Helper()
	env := newPublicEnv(t)
	mailer := &fakeMailer{}
	env.book.Mailer = mailer
	env.book.AppURL = "http://localhost:8081"
	env.book.Deposits = creator
	return env, mailer
}

func depInput(env *publicEnv, opID string) PublicBookingInput {
	in := publicInput()
	in.ServiceID = env.corte.ID
	in.ClientEmail = "lia@mail.com"
	in.ClientOpID = opID
	return in
}

// D8: con la política activa y precio > 0 la reserva devuelve `payment`, retiene
// el cupo y NO manda la confirmación (sale al pagarse, D7).
func TestBookWithDeposit_creaLaSenaYSinCorreo(t *testing.T) {
	creator := newFakeDepositCreator(30)
	env, mailer := depPublicEnv(t, creator)

	b, payment, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-sena-1"))
	if err != nil {
		t.Fatalf("reservar con seña: %v", err)
	}
	if b.Status != domain.BookingBooked {
		t.Fatalf("el cupo se retiene con una reserva normal: %+v", b)
	}
	if payment == nil {
		t.Fatal("la respuesta tiene que traer payment")
	}
	if payment.AmountCents != domain.DepositAmountCents(env.corte.PriceCents, 30) || !payment.Simulated {
		t.Fatalf("payment: %+v", payment)
	}
	if payment.DepositID == "" || payment.ExpiresAt.IsZero() {
		t.Fatalf("payment incompleto: %+v", payment)
	}
	if want := "http://localhost:8081/pagar#"; len(payment.PayURL) <= len(want) || payment.PayURL[:len(want)] != want {
		t.Fatalf("pay_url: %q", payment.PayURL)
	}
	if creator.charges != 1 {
		t.Fatalf("cargos creados: %d", creator.charges)
	}
	if creator.lastPric != env.corte.PriceCents {
		t.Fatalf("el precio de la reserva: %d", creator.lastPric)
	}
	if n := mailer.count(); n != 0 {
		t.Fatalf("con seña no puede salir la confirmación todavía: %d", n)
	}
}

// D8: el mismo client_op_id devuelve el MISMO payment y un solo cargo.
func TestBookWithDeposit_reintentoMismoPayment(t *testing.T) {
	creator := newFakeDepositCreator(30)
	env, _ := depPublicEnv(t, creator)
	in := depInput(env, "op-sena-repetida")

	_, first, err := env.book.BookWithDeposit("navajas-oro", in)
	if err != nil || first == nil {
		t.Fatalf("primera reserva: %v (%+v)", err, first)
	}
	second, again, err := env.book.BookWithDeposit("navajas-oro", in)
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if again == nil || again.DepositID != first.DepositID || again.PayURL != first.PayURL {
		t.Fatalf("el reintento cambió el pago: %+v vs %+v", again, first)
	}
	if second.ID != in.ClientOpID && second.ID == "" {
		t.Fatalf("la reserva repetida: %+v", second)
	}
	if creator.charges != 1 {
		t.Fatalf("un solo cargo: %d", creator.charges)
	}
	if got := len(env.bookings.all); got != 1 {
		t.Fatalf("un solo turno: %d", got)
	}
}

// D8: sin seña (política apagada o precio 0) la reserva es la de hoy: sin
// `payment` y con la confirmación por correo.
func TestBookWithDeposit_sinSenaEsLaDeHoy(t *testing.T) {
	creator := newFakeDepositCreator(30) // el precio 0 hace que no haya seña
	env, mailer := depPublicEnv(t, creator)
	in := depInput(env, "op-sin-sena")

	// Un servicio de precio 0 que prof1 hace: no hay seña que cobrar.
	zero, err := domain.NewService("s1", "Gratis", 30, 0, 0)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	if err := env.services.Save(zero); err != nil {
		t.Fatalf("guardar servicio: %v", err)
	}
	if err := env.profs.SetServices("s1", "prof1", []string{zero.ID}); err != nil {
		t.Fatalf("asignar servicio: %v", err)
	}
	in.ServiceID = zero.ID
	in.ProfessionalID = "prof1"

	b, payment, err := env.book.BookWithDeposit("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar sin seña: %v", err)
	}
	if payment != nil {
		t.Fatalf("precio 0 no tiene payment: %+v", payment)
	}
	if b.ID == "" {
		t.Fatalf("reserva: %+v", b)
	}
	mailer.waitCount(t, 1)
}

// D8: si el cargo no se puede crear, la reserva se cancela por el camino
// existente y vuelve el error (el cliente recibe el 500 de siempre).
func TestBookWithDeposit_fallaElCargoCancelaLaReserva(t *testing.T) {
	creator := newFakeDepositCreator(30)
	creator.fail = errors.New("banco caído")
	env, mailer := depPublicEnv(t, creator)

	if _, payment, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-caida")); err == nil {
		t.Fatalf("el error del cargo tiene que volver (payment %+v)", payment)
	}
	if len(env.bookings.all) != 1 {
		t.Fatalf("el turno: %+v", env.bookings.all)
	}
	if status := env.bookings.all[0].Status; status != domain.BookingCancelled {
		t.Fatalf("el turno sin cargo se cancela por el camino existente: %q", status)
	}
	if n := mailer.count(); n != 0 {
		t.Fatalf("sin pago no hay confirmación: %d", n)
	}
}
