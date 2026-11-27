package httpkit

import (
	"context"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"strings"
	"sync"
	"testing"
	"time"
)

// BookingTx: la transacción del "Reasignar todo" sobre la agenda fake, con la
// semántica de Postgres (rollback a la foto y EXCLUDE en el UPDATE).
type BookingTx struct{ Bookings *Bookings }

type BookingTxView struct{ Parent *BookingTx }

func (v BookingTxView) Save(b domain.Booking) error { return v.Parent.Bookings.Save(b) }

func (v BookingTxView) FindByClientOpID(salonID, op string) (domain.Booking, error) {
	return v.Parent.Bookings.FindByClientOpID(salonID, op)
}

func (v BookingTxView) FindByID(salonID, id string) (domain.Booking, error) {
	return v.Parent.Bookings.FindByID(salonID, id)
}

func (v BookingTxView) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	return v.Parent.Bookings.FindOverlapping(salonID, professionalID, start, end)
}

func (v BookingTxView) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	return v.Parent.Bookings.ListRange(salonID, from, to)
}

func (v BookingTxView) Update(b domain.Booking) error {
	busy, err := v.Parent.Bookings.FindOverlapping(b.SalonID, b.ProfessionalID, b.StartsAt, b.EndsAt)
	if err != nil {
		return err
	}
	for _, other := range busy {
		if other.ID != b.ID {
			return domain.ErrAlreadyExists
		}
	}
	return v.Parent.Bookings.Update(b)
}

func (t *BookingTx) InSalonTx(salonID string, fn func(ports.BookingTx) error) error {
	snapshot := append([]domain.Booking(nil), t.Bookings.All...)
	if err := fn(BookingTxView{Parent: t}); err != nil {
		t.Bookings.All = snapshot
		return err
	}
	return nil
}

// Mailer / memails: lo mínimo del puerto de email para verificar el contrato
// HTTP de la reserva pública (el adaptador SMTP tiene sus propios tests). El
// mutex es obligatorio: la confirmación viaja en goroutine y `go test -race` la
// ve.
type Mailer struct {
	Mu   sync.Mutex
	Sent []ports.Message
}

func (f *Mailer) Send(_ context.Context, msg ports.Message) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Sent = append(f.Sent, msg)
	return nil
}

func (f *Mailer) Count() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return len(f.Sent)
}

func (f *Mailer) WaitCount(t *testing.T, n int) []ports.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.Mu.Lock()
		sent := append([]ports.Message(nil), f.Sent...)
		f.Mu.Unlock()
		if len(sent) >= n {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperaba %d correos, hubo %d", n, len(sent))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type BookingEmails struct {
	Mu    sync.Mutex
	Saved []string // "salon|booking|email"
}

func (f *BookingEmails) Save(salonID, bookingID, email string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Saved = append(f.Saved, salonID+"|"+bookingID+"|"+email)
	return nil
}

func (f *BookingEmails) ClaimDueReminders(time.Time, int) ([]ports.DueReminder, error) {
	return nil, nil
}

func (f *BookingEmails) MarkReminderSent(string) error { return nil }

// FindEmail: la lectura que usa F2d3 para avisar un cambio de turno.
func (f *BookingEmails) FindEmail(salonID, bookingID string) (string, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	prefix := salonID + "|" + bookingID + "|"
	for _, saved := range f.Saved {
		if strings.HasPrefix(saved, prefix) {
			return strings.TrimPrefix(saved, prefix), nil
		}
	}
	return "", domain.ErrNotFound
}

func (f *BookingEmails) SavedEmails() []string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return append([]string(nil), f.Saved...)
}

// Weekly: horario semanal del salón y de cada persona en memoria, con la misma
// semántica que pgstore (reemplazo total, scope por salón).
type Weekly struct {
	SalonDays map[string][]domain.WeekdayHours
	MemberSeg map[string][]domain.HoursSegment // "salonID|userID"
}

func (f *Weekly) SalonHours(salonID string) ([]domain.WeekdayHours, error) {
	return append([]domain.WeekdayHours{}, f.SalonDays[salonID]...), nil
}

func (f *Weekly) SaveSalonHours(salonID string, days []domain.WeekdayHours) error {
	if f.SalonDays == nil {
		f.SalonDays = map[string][]domain.WeekdayHours{}
	}
	f.SalonDays[salonID] = append([]domain.WeekdayHours{}, days...)
	return nil
}

func (f *Weekly) MemberHours(salonID, userID string) ([]domain.HoursSegment, error) {
	return append([]domain.HoursSegment{}, f.MemberSeg[salonID+"|"+userID]...), nil
}

func (f *Weekly) SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error {
	if f.MemberSeg == nil {
		f.MemberSeg = map[string][]domain.HoursSegment{}
	}
	f.MemberSeg[salonID+"|"+userID] = append([]domain.HoursSegment{}, segments...)
	return nil
}

func (f *Weekly) AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error) {
	prefix := salonID + "|"
	out := map[string][]domain.HoursSegment{}
	for key, segs := range f.MemberSeg {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = append([]domain.HoursSegment{}, segs...)
		}
	}
	return out, nil
}

type Notifications struct{ All []domain.Notification }

func (f *Notifications) Save(n domain.Notification) error { f.All = append(f.All, n); return nil }

func (f *Notifications) visible(n domain.Notification, salonID, userID string) bool {
	return n.SalonID == salonID && (n.UserID == "" || n.UserID == userID)
}

func (f *Notifications) List(salonID, userID string, limit int) ([]domain.Notification, error) {
	var out []domain.Notification
	for i := len(f.All) - 1; i >= 0; i-- {
		if f.visible(f.All[i], salonID, userID) {
			out = append(out, f.All[i])
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (f *Notifications) MarkRead(salonID, userID, id string) error {
	for i := range f.All {
		if f.All[i].ID == id && f.visible(f.All[i], salonID, userID) {
			f.All[i].MarkRead(time.Now().UTC())
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Notifications) UnreadCount(salonID, userID string) (int, error) {
	n := 0
	for _, x := range f.All {
		if f.visible(x, salonID, userID) && !x.Read() {
			n++
		}
	}
	return n, nil
}

type Clients struct {
	Clients []domain.Client
	Photos  []domain.ClientPhoto
}

func (f *Clients) Save(c domain.Client) error { f.Clients = append(f.Clients, c); return nil }

// FindByPhone / FindByName: la reserva pública busca la ficha del visitante
// antes de crear una nueva (teléfono normalizado / nombre exacto).
func (f *Clients) FindByPhone(salonID, phone string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && domain.NormalizePhone(c.Phone) == phone {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *Clients) FindByName(salonID, name string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && c.Name == name {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *Clients) FindByID(salonID, id string) (domain.Client, error) {
	for _, c := range f.Clients {
		if c.SalonID == salonID && c.ID == id {
			return c, nil
		}
	}
	return domain.Client{}, domain.ErrNotFound
}

func (f *Clients) List(salonID string) ([]domain.Client, error) {
	var out []domain.Client
	for _, c := range f.Clients {
		if c.SalonID == salonID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *Clients) SavePhoto(p domain.ClientPhoto) error { f.Photos = append(f.Photos, p); return nil }

func (f *Clients) ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error) {
	var out []domain.ClientPhoto
	for _, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *Clients) DeletePhoto(salonID, clientID, id string) error {
	for i, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			f.Photos = append(f.Photos[:i], f.Photos[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *Clients) FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error) {
	for _, p := range f.Photos {
		if p.SalonID == salonID && p.ClientID == clientID && p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

func (f *Clients) PhotoByID(id string) (domain.ClientPhoto, error) {
	for _, p := range f.Photos {
		if p.ID == id {
			return p, nil
		}
	}
	return domain.ClientPhoto{}, domain.ErrNotFound
}

type Storage struct{ Files map[string][]byte }

func (f *Storage) Put(key string, data []byte, contentType string) (string, error) {
	if f.Files == nil {
		f.Files = map[string][]byte{}
	}
	f.Files[key] = data
	return key, nil
}

func (f *Storage) Get(path string) ([]byte, error) {
	if data, ok := f.Files[path]; ok {
		return data, nil
	}
	return nil, domain.ErrNotFound
}

func (f *Storage) Delete(path string) error { delete(f.Files, path); return nil }

// Images: "convierte" a un webp de mentira con dimensiones fijas.
type Images struct{}

func (Images) ToWebP(data []byte) ([]byte, int, int, error) {
	return []byte("RIFF....WEBP-falso"), 1280, 960, nil
}

type Events struct{ All []ports.Event }

func (f *Events) Publish(ev ports.Event) error { f.All = append(f.All, ev); return nil }

// TimeOff: ausencias en memoria con la misma semántica que pgstore (scope por
// salón, solape contra el período).
type TimeOff struct{ All []domain.TimeOff }

func (f *TimeOff) Save(t domain.TimeOff) error { f.All = append(f.All, t); return nil }

func (f *TimeOff) ListRange(salonID string, from, to time.Time) ([]domain.TimeOff, error) {
	out := []domain.TimeOff{}
	for _, t := range f.All {
		if t.SalonID == salonID && t.Overlaps(from, to) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *TimeOff) FindByID(salonID, id string) (domain.TimeOff, error) {
	for _, t := range f.All {
		if t.SalonID == salonID && t.ID == id {
			return t, nil
		}
	}
	return domain.TimeOff{}, domain.ErrNotFound
}

func (f *TimeOff) Delete(salonID, id string) error {
	for i, t := range f.All {
		if t.SalonID == salonID && t.ID == id {
			f.All = append(f.All[:i], f.All[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}
