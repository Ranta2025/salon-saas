package bookings_test

import (
	"salon-saas/internal/testutil"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// F1: fakes del enlace firmado y de su transacción.
//
// La transacción del fake NO bloquea (los tests de concurrencia real corren
// contra Postgres en los IT): lo que sí reproduce es la semántica que importa
// para el contrato:
//   - reclamar es un UPDATE condicional: el segundo reclamo del mismo enlace ve
//     0 filas y recibe ErrNotFound;
//   - si la función devuelve error, NADA queda escrito (rollback): ni el enlace
//     usado ni el turno movido ni el token nuevo.
type fakeBookingLinks struct {
	mu sync.Mutex
	// bookings es la agenda donde vive el turno del enlace.
	bookings *testutil.FakeBookings
	links    []domain.BookingLink
	// saveErr/findErr simulan la DB.
	saveErr error
	findErr error
	// rejectUpdate marca turnos cuyo Update falla como la EXCLUDE de Postgres
	// (23P01 -> ErrAlreadyExists): el hueco se lo llevó otro.
	rejectUpdate map[string]bool
	// locks registra los locks pedidos dentro de la transacción: tiene que ser
	// exactamente uno, el de la escritura del salón (`booking-write:<salón>`).
	locks []string
	// rollbacks cuenta las transacciones que terminaron en error.
	rollbacks int
}

func (f *fakeBookingLinks) Save(l domain.BookingLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.links = append(f.links, l)
	return nil
}

func (f *fakeBookingLinks) FindByTokenHash(tokenHash string) (domain.BookingLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return domain.BookingLink{}, f.findErr
	}
	for _, l := range f.links {
		if l.TokenHash == tokenHash {
			return l, nil
		}
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

// rows devuelve una copia de los enlaces guardados (lo único que la DB tiene del
// token es su hash).
func (f *fakeBookingLinks) rows() []domain.BookingLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.BookingLink(nil), f.links...)
}

func (f *fakeBookingLinks) lockLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.locks...)
}

// InBookingLinkTx abre la "transacción": acumula los cambios y los aplica solo
// si fn termina bien.
func (f *fakeBookingLinks) InBookingLinkTx(fn func(tx ports.BookingLinkTx) error) error {
	staged := &fakeBookingLinkTx{parent: f, claimed: map[string]bool{}, updated: map[string]domain.Booking{}}
	if err := fn(staged); err != nil {
		f.mu.Lock()
		f.rollbacks++
		f.mu.Unlock()
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range staged.claimed {
		for i := range f.links {
			if f.links[i].ID == id {
				usedAt := staged.now
				f.links[i].UsedAt = &usedAt
			}
		}
	}
	f.links = append(f.links, staged.saved...)
	bookings := make([]domain.Booking, 0, len(staged.updated))
	for _, b := range staged.updated {
		bookings = append(bookings, b)
	}
	for _, b := range bookings {
		if err := f.bookings.Update(b); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.BookingLinkRepository = (*fakeBookingLinks)(nil)
var _ ports.BookingLinkTxRunner = (*fakeBookingLinks)(nil)

// fakeBookingLinkTx es la vista transaccional del fake.
type fakeBookingLinkTx struct {
	parent  *fakeBookingLinks
	claimed map[string]bool
	updated map[string]domain.Booking
	saved   []domain.BookingLink
	now     time.Time
}

func (t *fakeBookingLinkTx) FindLink(tokenHash string) (domain.BookingLink, error) {
	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()
	if t.parent.findErr != nil {
		return domain.BookingLink{}, t.parent.findErr
	}
	for _, l := range t.parent.links {
		if l.TokenHash == tokenHash {
			return l, nil
		}
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

// LockWrites apunta el lock pedido. El fake no bloquea; el caso de uso tiene que
// pedir EXACTAMENTE `booking-write:<salón>` y una sola vez.
func (t *fakeBookingLinkTx) LockWrites(salonID string) error {
	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()
	t.parent.locks = append(t.parent.locks, "booking-write:"+salonID)
	return nil
}

func (t *fakeBookingLinkTx) ClaimLink(id string, now time.Time) (domain.BookingLink, error) {
	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()
	for _, l := range t.parent.links {
		if l.ID != id {
			continue
		}
		if l.Used() || t.claimed[id] {
			return domain.BookingLink{}, domain.ErrNotFound
		}
		t.claimed[id] = true
		t.now = now
		return l, nil
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

func (t *fakeBookingLinkTx) SaveLink(l domain.BookingLink) error {
	if t.parent.saveErr != nil {
		return t.parent.saveErr
	}
	t.saved = append(t.saved, l)
	return nil
}

func (t *fakeBookingLinkTx) FindBooking(salonID, id string) (domain.Booking, error) {
	if b, ok := t.updated[id]; ok && b.SalonID == salonID {
		return b, nil
	}
	return t.parent.bookings.FindByID(salonID, id)
}

func (t *fakeBookingLinkTx) UpdateBooking(b domain.Booking) error {
	if t.parent.rejectUpdate[b.ID] {
		return domain.ErrAlreadyExists
	}
	t.updated[b.ID] = b
	return nil
}

// fakeLinkNotifier registra el aviso interno COMPLETO (título, cuerpo y tipo)
// para comprobar que cancelar o reprogramar por enlace deja exactamente el mismo
// aviso que bkg.BookingService.Update (D8).
type fakeLinkNotifier struct {
	titles []string
	bodies []string
	kinds  []domain.NotificationKind
}

func (f *fakeLinkNotifier) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	f.kinds = append(f.kinds, kind)
	return domain.NewNotification(salonID, userID, title, body, kind)
}

var _ kernel.Notifier = (*fakeLinkNotifier)(nil)
