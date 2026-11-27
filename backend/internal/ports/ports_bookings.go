package ports

import (
	"context"
	"salon-saas/internal/domain"
	"time"
)

// --- Enlaces firmados de un turno (F1) ---
//
// El enlace de cancelar/reprogramar es una capability: la fila guarda SOLO el
// SHA-256 del token (nunca el token en claro) y `FindByTokenHash` es la única
// lectura del sistema que no va scopeada por salón, porque el token resuelve el
// turno antes de saber de qué salón es (quien abre el enlace no tiene sesión).
type BookingLinkRepository interface {
	// Save inserta un token nuevo. El UNIQUE de token_hash es la garantía de
	// que un token no se repite (no hay upsert: cada email lleva el suyo).
	Save(l domain.BookingLink) error
	// FindByTokenHash resuelve el enlace por su hash. Sin fila es ErrNotFound
	// (el caso de uso responde el 404 genérico, sin oráculo).
	FindByTokenHash(tokenHash string) (domain.BookingLink, error)
}

// BookingLinkTx es la vista TRANSACCIONAL del enlace (D2/D4): reclamar el
// token, validar y cambiar el turno commitean juntos o no pasan. El lock es el
// MISMO `booking-write:<salón>` de la agenda y se toma dentro del caso de uso,
// después de resolver el enlace (que es lo único que dice de qué salón es).
type BookingLinkTx interface {
	// FindLink resuelve el enlace por su hash SIN reclamarlo: es lo que dice de
	// qué salón es para poder tomar el lock.
	FindLink(tokenHash string) (domain.BookingLink, error)
	// LockWrites toma el advisory lock de escrituras del salón
	// (`booking-write:<salón>`), el mismo de Book y Update. Es el ÚNICO lock de
	// esta transacción.
	LockWrites(salonID string) error
	// ClaimLink marca el enlace como usado (UPDATE condicional used_at IS NULL)
	// y devuelve la fila reclamada. Sin filas (ya usado, o reclamado por otro
	// pedido simultáneo) es ErrNotFound.
	ClaimLink(id string, now time.Time) (domain.BookingLink, error)
	// SaveLink emite el token nuevo de una reprogramación dentro de la MISMA
	// transacción del cambio: si el cambio no commitea, el token no existe.
	SaveLink(l domain.BookingLink) error
	// FindBooking resuelve el turno scopeado por el salón del enlace (el turno
	// de otro tenant no existe).
	FindBooking(salonID, id string) (domain.Booking, error)
	// UpdateBooking persiste el cambio del turno. La EXCLUDE de Postgres sigue
	// siendo el árbitro del solape (23P01 -> ErrAlreadyExists -> 409).
	UpdateBooking(b domain.Booking) error
}

// BookingLinkTxRunner abre la transacción del enlace: una sola conexión del
// pool y, adentro, un solo advisory lock (el de la agenda del salón).
type BookingLinkTxRunner interface {
	InBookingLinkTx(fn func(tx BookingLinkTx) error) error
}

// --- Reservas ---

type BookingRepository interface {
	Save(b domain.Booking) error
	// FindByClientOpID busca una reserva ya registrada por su operation id.
	FindByClientOpID(salonID, clientOpID string) (domain.Booking, error)
	// FindByID resuelve un turno scopeado por salón: el id de otro tenant no
	// existe (404), que es la autorización del PATCH.
	FindByID(salonID, id string) (domain.Booking, error)
	// Update persiste los cambios de un turno existente (estado y horario).
	// La EXCLUDE de la DB sigue siendo el árbitro final del solape.
	Update(b domain.Booking) error
	// FindOverlapping trae activas del profesional que pisan [start,end).
	// El caso de uso la usa como filtro amable; la EXCLUDE de la DB decide.
	FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error)
	// ListRange trae todo lo del salón en [from,to) para el calendario.
	ListRange(salonID string, from, to time.Time) ([]domain.Booking, error)
}

// --- Email a clientes ---

// Message es un correo ya armado (texto plano + HTML alternativos). El caso de
// uso decide el contenido y el adaptador solo habla SMTP.
//
// To y Subject son headers: el adaptador rechaza CR/LF en ellos (header
// injection). Text y HTML son cuerpos completos, no fragmentos.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// MailSender es el puerto de salida hacia un servidor de correo. El adaptador
// de cmd/api puede ser un no-op cuando SMTP_HOST está vacío (entorno sin
// email): el caso de uso no tiene que saberlo.
type MailSender interface {
	Send(ctx context.Context, msg Message) error
}

// DueReminder es un recordatorio ya reclamado: trae todo lo que la plantilla
// necesita (salón, reserva y email), así el envío no vuelve a consultar la DB.
type DueReminder struct {
	BookingID  string
	SalonID    string
	SalonName  string
	ClientName string
	Email      string
	StartsAt   time.Time
}

// BookingEmailRepository: el email de un turno reservado por la web y la cola
// de recordatorios. Vive en su propia tabla para no tocar las SELECT de
// reservas (y para que un turno sin email no ocupe una fila de bookings).
type BookingEmailRepository interface {
	// Save guarda el email del turno. Un turno ya guardado no se pisa.
	Save(salonID, bookingID, email string) error
	// ClaimDueReminders reclama ATÓMICAMENTE hasta limit recordatorios que
	// vencen en [now, now+24h) y todavía no se enviaron ni agotaron intentos.
	// Lo reclamado no lo vuelve a tomar otro proceso (5 minutos de gracia).
	ClaimDueReminders(now time.Time, limit int) ([]DueReminder, error)
	// MarkReminderSent cierra el recordatorio del turno: no se reintenta más.
	MarkReminderSent(bookingID string) error
}
