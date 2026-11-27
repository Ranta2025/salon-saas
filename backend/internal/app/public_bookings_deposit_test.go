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

// atomicCancelBookings adds the OPTIONAL adapter capability the service uses to
// cancel a booking AND free its idempotency key in ONE step when the deposit
// charge could not be created (F11 native review R4-RETRY-CANCELLED and
// R4-COMPENSATION). It wraps the shared fake `fb`.
type atomicCancelBookings struct{ *fb }

func (r *atomicCancelBookings) CancelBookingReleasingClientOp(salonID, bookingID string) error {
	for i := range r.fb.all {
		b := r.fb.all[i]
		if b.SalonID == salonID && b.ID == bookingID && b.Status == domain.BookingBooked {
			b.Status = domain.BookingCancelled
			b.ClientOpID = ""
			r.fb.all[i] = b
		}
	}
	return nil
}

// A retry with the SAME client_op_id after a failed charge must not get the
// cancelled booking back as a 201: it is a fresh attempt that holds the slot again
// and returns its payment.
func TestBookWithDeposit_reintentoTrasFallaDelCargoEsUnaReservaNueva(t *testing.T) {
	creator := newFakeDepositCreator(30)
	creator.fail = errors.New("banco caído")
	env, mailer := depPublicEnv(t, creator)
	env.book.Bookings = &atomicCancelBookings{fb: env.bookings}

	if _, _, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-retry")); err == nil {
		t.Fatal("el primer intento debía fallar por el cargo")
	}
	first := env.bookings.all[0]
	if first.Status != domain.BookingCancelled || first.ClientOpID != "" {
		t.Fatalf("el turno sin cargo se cancela y suelta su clave: %+v", first)
	}

	creator.fail = nil
	b, payment, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-retry"))
	if err != nil {
		t.Fatalf("el reintento debe poder reservar de nuevo: %v", err)
	}
	if b.ID == first.ID || b.Status != domain.BookingBooked {
		t.Fatalf("tiene que ser un turno NUEVO y agendado, no el cancelado: %+v", b)
	}
	if payment == nil || payment.AmountCents <= 0 {
		t.Fatalf("el reintento devuelve su seña: %+v", payment)
	}
	if n := mailer.count(); n != 0 {
		t.Fatalf("la confirmación sigue esperando al pago: %d", n)
	}
	// Un reintento más de la MISMA clave ya devuelve el turno nuevo y la misma seña.
	again, payment2, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-retry"))
	if err != nil || again.ID != b.ID || payment2 == nil || payment2.DepositID != payment.DepositID {
		t.Fatalf("el segundo reintento repite el mismo turno y la misma seña: %+v %+v %v", again, payment2, err)
	}
}

// failingUpdateBookings makes the compensation fail (the database is down for the
// cancel): the booking stays `booked` with no deposit.
type failingUpdateBookings struct{ *fb }

func (r *failingUpdateBookings) Update(domain.Booking) error {
	return errors.New("la base no responde")
}

// F11 native review R4-COMPENSATION: if the charge creation AND the compensation
// both fail, the booking stays booked with no deposit. The retry with the same key
// must create the deposit now (idempotent by booking), never return a free booking.
func TestBookWithDeposit_reintentoSanaUnaReservaSinSena(t *testing.T) {
	creator := newFakeDepositCreator(30)
	creator.fail = errors.New("banco caído")
	env, mailer := depPublicEnv(t, creator)
	env.book.Bookings = &failingUpdateBookings{fb: env.bookings}

	if _, _, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-sana")); err == nil {
		t.Fatal("el primer intento debía fallar por el cargo")
	}
	first := env.bookings.all[0]
	if first.Status != domain.BookingBooked {
		t.Fatalf("precondición: la compensación falló y el turno sigue agendado: %q", first.Status)
	}

	creator.fail = nil
	b, payment, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-sana"))
	if err != nil {
		t.Fatalf("el reintento: %v", err)
	}
	if b.ID != first.ID {
		t.Fatalf("es la MISMA reserva: %s != %s", b.ID, first.ID)
	}
	if payment == nil || payment.AmountCents <= 0 {
		t.Fatalf("una reserva agendada sin seña no se devuelve gratis: %+v", payment)
	}
	if n := mailer.count(); n != 0 {
		t.Fatalf("la confirmación espera al pago: %d", n)
	}
}

// F11 verifier V2 advisory 2: replaying the key of a CANCELLED booking (expired hold
// or cancelled in the panel) must not hand out a payment link for a dead deposit.
func TestBookWithDeposit_reintentoDeUnTurnoCanceladoNoDevuelveSena(t *testing.T) {
	creator := newFakeDepositCreator(30)
	env, _ := depPublicEnv(t, creator)

	first, payment, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-cancelado"))
	if err != nil || payment == nil {
		t.Fatalf("precondición: reserva con seña: %v %v", payment, err)
	}
	for i := range env.bookings.all {
		if env.bookings.all[i].ID == first.ID {
			env.bookings.all[i].Status = domain.BookingCancelled
		}
	}
	again, payment2, err := env.book.BookWithDeposit("navajas-oro", depInput(env, "op-cancelado"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.Status != domain.BookingCancelled {
		t.Fatalf("el replay devuelve el turno tal como quedó: %q", again.Status)
	}
	if payment2 != nil {
		t.Fatalf("un turno cancelado no ofrece pago: %+v", payment2)
	}
}
