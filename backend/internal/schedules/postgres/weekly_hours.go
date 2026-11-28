package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// WeeklyHours: horario semanal del salón y de cada profesional (F2d1).
//
// Sin filas = comportamiento de siempre (la ventana única de /settings todos
// los días y la profesional sigue al salón): por eso leer no devuelve
// ErrNotFound, devuelve vacío. Todo va scopeado por salon_id.
type WeeklyHours struct{ Pool *pgxpool.Pool }

// SalonHours trae las ventanas por día del salón, ordenadas por día ISO.
func (r WeeklyHours) SalonHours(salonID string) ([]domain.WeekdayHours, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT weekday, open_time, close_time FROM salon_weekly_hours
		 WHERE salon_id=$1
		 ORDER BY weekday`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.WeekdayHours, 0, domain.LastWeekday)
	for rows.Next() {
		var d domain.WeekdayHours
		if err := rows.Scan(&d.Weekday, &d.Open, &d.Close); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveSalonHours REEMPLAZA el horario del salón dentro de una transacción: o
// queda el horario nuevo entero o queda el anterior, nunca media semana. Una
// lista vacía borra las filas y el salón vuelve a la ventana única.
func (r WeeklyHours) SaveSalonHours(salonID string, days []domain.WeekdayHours) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM salon_weekly_hours WHERE salon_id=$1`, salonID); err != nil {
		return err
	}
	for _, d := range days {
		if _, err := tx.Exec(ctx,
			`INSERT INTO salon_weekly_hours(salon_id,weekday,open_time,close_time)
			 VALUES($1,$2,$3,$4)`, salonID, d.Weekday, d.Open, d.Close); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MemberHours trae los tramos del miembro, ordenados por día y hora de inicio.
func (r WeeklyHours) MemberHours(salonID, userID string) ([]domain.HoursSegment, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT weekday, start_time, end_time FROM professional_hours
		 WHERE salon_id=$1 AND user_id=$2
		 ORDER BY weekday, start_time`, salonID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.HoursSegment, 0, 8)
	for rows.Next() {
		var s domain.HoursSegment
		if err := rows.Scan(&s.Weekday, &s.Start, &s.End); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SaveMemberHours REEMPLAZA los tramos del miembro dentro de una transacción.
// Un tramo inválido rompe el CHECK y el rollback deja la lista anterior intacta
// (la atomicidad no la da el orden del código). Un miembro inexistente en ESE
// salón rompe la FK: el horario de otro tenant no se escribe.
func (r WeeklyHours) SaveMemberHours(salonID, userID string, segments []domain.HoursSegment) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM professional_hours WHERE salon_id=$1 AND user_id=$2`,
		salonID, userID); err != nil {
		return err
	}
	for _, s := range segments {
		if _, err := tx.Exec(ctx,
			`INSERT INTO professional_hours(salon_id,user_id,weekday,start_time,end_time)
			 VALUES($1,$2,$3,$4,$5)`, salonID, userID, s.Weekday, s.Start, s.End); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AllMemberHours trae los tramos de todo el salón indexados por persona: la
// disponibilidad y la reserva pública lo piden una vez por día.
func (r WeeklyHours) AllMemberHours(salonID string) (map[string][]domain.HoursSegment, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT user_id, weekday, start_time, end_time FROM professional_hours
		 WHERE salon_id=$1
		 ORDER BY user_id, weekday, start_time`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]domain.HoursSegment, 8)
	for rows.Next() {
		var userID string
		var s domain.HoursSegment
		if err := rows.Scan(&userID, &s.Weekday, &s.Start, &s.End); err != nil {
			return nil, err
		}
		out[userID] = append(out[userID], s)
	}
	return out, rows.Err()
}

var _ ports.WeeklyHoursRepository = WeeklyHours{}
