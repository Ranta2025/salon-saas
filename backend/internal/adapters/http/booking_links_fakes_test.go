package httpapi

import (
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// F1: fake del enlace firmado para los tests de contrato HTTP. La concurrencia
// real (16 pedidos con el mismo token) se prueba contra Postgres en los IT: acá
// alcanza con la semántica del reclamo (UPDATE condicional) y del rollback.
type mlinks struct {
	mu       sync.Mutex
	bookings *mb
	links    []domain.BookingLink
	locks    []string
}

func (f *mlinks) Save(l domain.BookingLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, l)
	return nil
}

func (f *mlinks) FindByTokenHash(tokenHash string) (domain.BookingLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		if l.TokenHash == tokenHash {
			return l, nil
		}
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

func (f *mlinks) rows() []domain.BookingLink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.BookingLink(nil), f.links...)
}

func (f *mlinks) InBookingLinkTx(fn func(tx ports.BookingLinkTx) error) error {
	staged := &mlinkTx{parent: f, claimed: map[string]bool{}, updated: map[string]domain.Booking{}}
	if err := fn(staged); err != nil {
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

var _ ports.BookingLinkRepository = (*mlinks)(nil)
var _ ports.BookingLinkTxRunner = (*mlinks)(nil)

type mlinkTx struct {
	parent  *mlinks
	claimed map[string]bool
	updated map[string]domain.Booking
	saved   []domain.BookingLink
	now     time.Time
}

func (t *mlinkTx) FindLink(tokenHash string) (domain.BookingLink, error) {
	return t.parent.FindByTokenHash(tokenHash)
}

func (t *mlinkTx) LockWrites(salonID string) error {
	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()
	t.parent.locks = append(t.parent.locks, "booking-write:"+salonID)
	return nil
}

func (t *mlinkTx) ClaimLink(id string, now time.Time) (domain.BookingLink, error) {
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

func (t *mlinkTx) SaveLink(l domain.BookingLink) error { t.saved = append(t.saved, l); return nil }

func (t *mlinkTx) FindBooking(salonID, id string) (domain.Booking, error) {
	if b, ok := t.updated[id]; ok && b.SalonID == salonID {
		return b, nil
	}
	return t.parent.bookings.FindByID(salonID, id)
}

func (t *mlinkTx) UpdateBooking(b domain.Booking) error {
	t.updated[b.ID] = b
	return nil
}
