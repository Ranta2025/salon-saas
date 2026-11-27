package httpkit

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"sync"
	"time"
)

// F1: fake del enlace firmado para los tests de contrato HTTP. La concurrencia
// real (16 pedidos con el mismo token) se prueba contra Postgres en los IT: acá
// alcanza con la semántica del reclamo (UPDATE condicional) y del rollback.
type Links struct {
	Mu       sync.Mutex
	Bookings *Bookings
	Links    []domain.BookingLink
	Locks    []string
}

func (f *Links) Save(l domain.BookingLink) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Links = append(f.Links, l)
	return nil
}

func (f *Links) FindByTokenHash(tokenHash string) (domain.BookingLink, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, l := range f.Links {
		if l.TokenHash == tokenHash {
			return l, nil
		}
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

func (f *Links) Rows() []domain.BookingLink {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return append([]domain.BookingLink(nil), f.Links...)
}

func (f *Links) InBookingLinkTx(fn func(tx ports.BookingLinkTx) error) error {
	staged := &LinkTx{Parent: f, Claimed: map[string]bool{}, Updated: map[string]domain.Booking{}}
	if err := fn(staged); err != nil {
		return err
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for id := range staged.Claimed {
		for i := range f.Links {
			if f.Links[i].ID == id {
				usedAt := staged.Now
				f.Links[i].UsedAt = &usedAt
			}
		}
	}
	f.Links = append(f.Links, staged.Saved...)
	bookings := make([]domain.Booking, 0, len(staged.Updated))
	for _, b := range staged.Updated {
		bookings = append(bookings, b)
	}
	for _, b := range bookings {
		if err := f.Bookings.Update(b); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.BookingLinkRepository = (*Links)(nil)
var _ ports.BookingLinkTxRunner = (*Links)(nil)

type LinkTx struct {
	Parent  *Links
	Claimed map[string]bool
	Updated map[string]domain.Booking
	Saved   []domain.BookingLink
	Now     time.Time
}

func (t *LinkTx) FindLink(tokenHash string) (domain.BookingLink, error) {
	return t.Parent.FindByTokenHash(tokenHash)
}

func (t *LinkTx) LockWrites(salonID string) error {
	t.Parent.Mu.Lock()
	defer t.Parent.Mu.Unlock()
	t.Parent.Locks = append(t.Parent.Locks, "booking-write:"+salonID)
	return nil
}

func (t *LinkTx) ClaimLink(id string, now time.Time) (domain.BookingLink, error) {
	t.Parent.Mu.Lock()
	defer t.Parent.Mu.Unlock()
	for _, l := range t.Parent.Links {
		if l.ID != id {
			continue
		}
		if l.Used() || t.Claimed[id] {
			return domain.BookingLink{}, domain.ErrNotFound
		}
		t.Claimed[id] = true
		t.Now = now
		return l, nil
	}
	return domain.BookingLink{}, domain.ErrNotFound
}

func (t *LinkTx) SaveLink(l domain.BookingLink) error { t.Saved = append(t.Saved, l); return nil }

func (t *LinkTx) FindBooking(salonID, id string) (domain.Booking, error) {
	if b, ok := t.Updated[id]; ok && b.SalonID == salonID {
		return b, nil
	}
	return t.Parent.Bookings.FindByID(salonID, id)
}

func (t *LinkTx) UpdateBooking(b domain.Booking) error {
	t.Updated[b.ID] = b
	return nil
}
