package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/ports"
)

// Fakes del email a clientes: la cola del adaptador Postgres (con la misma
// semántica de "enviado no vuelve") y un sender que registra lo enviado.

type savedBookingEmail struct {
	SalonID   string
	BookingID string
	Email     string
}

type fakeBookingEmails struct {
	mu      sync.Mutex
	saved   []savedBookingEmail
	saveErr error
	due     []ports.DueReminder
	sent    map[string]bool
	markErr error
	claims  int
}

func (f *fakeBookingEmails) Save(salonID, bookingID, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, savedBookingEmail{SalonID: salonID, BookingID: bookingID, Email: email})
	return nil
}

func (f *fakeBookingEmails) ClaimDueReminders(now time.Time, limit int) ([]ports.DueReminder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.sent == nil {
		f.sent = map[string]bool{}
	}
	var out []ports.DueReminder
	for _, d := range f.due {
		if f.sent[d.BookingID] {
			continue
		}
		out = append(out, d)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeBookingEmails) MarkReminderSent(bookingID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	if f.sent == nil {
		f.sent = map[string]bool{}
	}
	f.sent[bookingID] = true
	return nil
}

func (f *fakeBookingEmails) isSent(bookingID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[bookingID]
}

func (f *fakeBookingEmails) savedFor(bookingID string) (savedBookingEmail, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.saved {
		if s.BookingID == bookingID {
			return s, true
		}
	}
	return savedBookingEmail{}, false
}

// fakeMailer registra los mensajes. Con release no nulo, Send se queda
// bloqueado hasta que el test lo libere: prueba que la reserva no espera.
type fakeMailer struct {
	mu      sync.Mutex
	sent    []ports.Message
	err     error
	errFor  map[string]error
	started chan struct{}
	release chan struct{}
}

func (f *fakeMailer) Send(ctx context.Context, msg ports.Message) error {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errFor[msg.To]; ok {
		return err
	}
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeMailer) messages() []ports.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.Message(nil), f.sent...)
}

// waitCount espera (hasta 2 s) a que se hayan enviado n mensajes: la
// confirmación viaja en goroutine y el test no puede asumir el instante.
func (f *fakeMailer) waitCount(t *testing.T, n int) []ports.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs := f.messages()
		if len(msgs) >= n {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperaba %d mensajes enviados, hubo %d", n, len(msgs))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
