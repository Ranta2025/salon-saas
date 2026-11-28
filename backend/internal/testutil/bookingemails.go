package testutil

import (
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// SavedBookingEmail es una fila de la cola del email a clientes.
type SavedBookingEmail struct {
	SalonID   string
	BookingID string
	Email     string
}

// FakeBookingEmails es la cola del adaptador Postgres del email a clientes
// (con la misma semántica de "enviado no vuelve"). Es la copia exportada del
// fake de los tests de app.
type FakeBookingEmails struct {
	Mu      sync.Mutex
	Saved   []SavedBookingEmail
	SaveErr error
	Due     []ports.DueReminder
	Sent    map[string]bool
	MarkErr error
	Claims  int
}

func (f *FakeBookingEmails) Save(salonID, bookingID, email string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.SaveErr != nil {
		return f.SaveErr
	}
	f.Saved = append(f.Saved, SavedBookingEmail{SalonID: salonID, BookingID: bookingID, Email: email})
	return nil
}

func (f *FakeBookingEmails) ClaimDueReminders(now time.Time, limit int) ([]ports.DueReminder, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Claims++
	if f.Sent == nil {
		f.Sent = map[string]bool{}
	}
	var out []ports.DueReminder
	for _, d := range f.Due {
		if f.Sent[d.BookingID] {
			continue
		}
		out = append(out, d)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *FakeBookingEmails) MarkReminderSent(bookingID string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.MarkErr != nil {
		return f.MarkErr
	}
	if f.Sent == nil {
		f.Sent = map[string]bool{}
	}
	f.Sent[bookingID] = true
	return nil
}

func (f *FakeBookingEmails) IsSent(bookingID string) bool {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.Sent[bookingID]
}

func (f *FakeBookingEmails) SavedFor(bookingID string) (SavedBookingEmail, bool) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, s := range f.Saved {
		if s.BookingID == bookingID {
			return s, true
		}
	}
	return SavedBookingEmail{}, false
}

// FindEmail resuelve la dirección guardada del turno (ports.BookingEmailFinder):
// la vuelve a usar F2d3 para avisar que el turno cambió de profesional.
func (f *FakeBookingEmails) FindEmail(salonID, bookingID string) (string, error) {
	if saved, ok := f.SavedFor(bookingID); ok && saved.SalonID == salonID {
		return saved.Email, nil
	}
	return "", domain.ErrNotFound
}

var (
	_ ports.BookingEmailRepository = (*FakeBookingEmails)(nil)
	_ ports.BookingEmailFinder     = (*FakeBookingEmails)(nil)
)
