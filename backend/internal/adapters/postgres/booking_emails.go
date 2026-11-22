package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// BookingEmails: email de contacto de un turno y la cola de recordatorios.
// Toda query va scopeada por salon_id (salvo MarkReminderSent, que cierra un
// recordatorio ya reclamado por su id de turno).
type BookingEmails struct{ Pool *pgxpool.Pool }

// Save registra el email del turno. Un turno ya registrado NO se pisa: la
// confirmación se manda una sola vez y la dirección de la reserva original es
// la que vale (un reintento con otro email no cambia el destino).
func (r BookingEmails) Save(salonID, bookingID, email string) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO booking_emails(booking_id,salon_id,email)
		 VALUES($1,$2,$3)
		 ON CONFLICT (booking_id) DO NOTHING`,
		bookingID, salonID, email)
	return pgError(err)
}

// dueRemindersSQL es UN solo statement: reclama y devuelve los recordatorios
// vencidos. No hay check-then-update, así que dos procesos (o dos ticks) no
// pueden enviar el mismo recordatorio: el UPDATE ... WHERE ... FOR UPDATE SKIP
// LOCKED se serializa en la DB y el segundo no ve nada.
//
// Reglas del vencimiento:
//   - turno 'booked', en el futuro y a menos de 24 h;
//   - reserva creada con al menos 24 h de anticipación: una reserva hecha para
//     dentro de dos horas ya recibió su confirmación y no lleva recordatorio;
//   - no enviado, menos de 3 intentos y sin un reclamo vivo (5 minutos).
const dueRemindersSQL = `
WITH due AS (
  UPDATE booking_emails e
     SET reminder_attempts  = e.reminder_attempts + 1,
         reminder_claimed_at = now()
   WHERE e.booking_id IN (
     SELECT e2.booking_id
       FROM booking_emails e2
       JOIN bookings b ON b.id = e2.booking_id AND b.salon_id = e2.salon_id
       JOIN salons s ON s.id = e2.salon_id
      WHERE e2.reminder_sent_at IS NULL
        AND e2.reminder_attempts < 3
        AND (e2.reminder_claimed_at IS NULL OR e2.reminder_claimed_at < now() - interval '5 minutes')
        AND b.status = 'booked'
        AND b.starts_at > $1
        AND b.starts_at <= $1 + interval '24 hours'
        AND b.created_at <= b.starts_at - interval '24 hours'
      ORDER BY b.starts_at
      LIMIT $2
      FOR UPDATE OF e2 SKIP LOCKED
   )
   RETURNING e.booking_id, e.salon_id, e.email
)
SELECT d.booking_id, d.salon_id, s.name, b.client_name, d.email, b.starts_at
  FROM due d
  JOIN bookings b ON b.id = d.booking_id AND b.salon_id = d.salon_id
  JOIN salons s ON s.id = d.salon_id
 ORDER BY b.starts_at`

// ClaimDueReminders reclama atómicamente los recordatorios que vencen en
// [now, now+24h). El salón y la reserva se leen por el mismo scope con el que
// se reclamó: una fila cruzada (salon_id de otro tenant) no se devuelve.
func (r BookingEmails) ClaimDueReminders(now time.Time, limit int) ([]ports.DueReminder, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := r.Pool.Query(context.Background(), dueRemindersSQL, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.DueReminder
	for rows.Next() {
		var d ports.DueReminder
		if err := rows.Scan(&d.BookingID, &d.SalonID, &d.SalonName, &d.ClientName, &d.Email, &d.StartsAt); err != nil {
			return nil, err
		}
		d.StartsAt = d.StartsAt.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkReminderSent cierra el recordatorio del turno. Solo se llama después de
// un envío exitoso: si esto falla, el recordatorio vuelve tras la gracia (se
// prefiere un recordatorio repetido a uno perdido).
func (r BookingEmails) MarkReminderSent(bookingID string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE booking_emails SET reminder_sent_at = now() WHERE booking_id = $1`, bookingID)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ ports.BookingEmailRepository = BookingEmails{}

// FindEmail implementa ports.BookingEmailFinder: la dirección con la que F2d3
// avisa que el turno cambió de profesional (o se canceló). La query va scopeada
// por salon_id: el turno de otro tenant no existe acá. Sin fila es ErrNotFound
// (el turno se agendó sin email: no hay a quién escribir).
func (r BookingEmails) FindEmail(salonID, bookingID string) (string, error) {
	var email string
	err := r.Pool.QueryRow(context.Background(),
		`SELECT email FROM booking_emails
		 WHERE salon_id=$1 AND booking_id=$2`, salonID, bookingID).Scan(&email)
	if err != nil {
		return "", notFound(err)
	}
	return email, nil
}

var _ ports.BookingEmailFinder = BookingEmails{}
