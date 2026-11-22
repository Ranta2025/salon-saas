// Package pgstore implementa ports.* con Postgres/pgx.
// Traduce pgx.ErrNoRows <-> domain.ErrNotFound: el resto del sistema
// nunca ve pgx, solo errores de dominio (Puertos y Adaptadores).
package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// querier es lo que comparten *pgxpool.Pool y pgx.Tx: las queries se escriben UNA
// sola vez y corren igual fuera (la agenda, la caja) y dentro de una transacción
// (el cierre de caja con lock y el "Reasignar todo" de F2d3).
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

type Users struct{ Pool *pgxpool.Pool }

func (r Users) Save(u domain.User) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO users(id,name,email,pass_hash,created_at)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`,
		u.ID, u.Name, u.Email, u.PasswordHash, u.CreatedAt)
	// Email duplicado (23505) es un 409, no un 500: el índice único decide.
	return pgError(err)
}

// FindByID resuelve el usuario del token (GET /me).
func (r Users) FindByID(id string) (domain.User, error) {
	var u domain.User
	err := r.Pool.QueryRow(context.Background(),
		`SELECT id,name,email,pass_hash,created_at FROM users WHERE id=$1`, id,
	).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return domain.User{}, notFound(err)
	}
	return u, nil
}

func (r Users) FindByEmail(email string) (domain.User, error) {
	var u domain.User
	err := r.Pool.QueryRow(context.Background(),
		`SELECT id,name,email,pass_hash,created_at FROM users WHERE email=$1`, email,
	).Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return domain.User{}, notFound(err)
	}
	return u, nil
}

type Salons struct{ Pool *pgxpool.Pool }

func (r Salons) Save(s domain.Salon) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO salons(id,slug,name,created_at) VALUES($1,$2,$3,$4)
		 ON CONFLICT (id) DO NOTHING`,
		s.ID, s.Slug, s.Name, s.CreatedAt)
	return err
}

func (r Salons) FindBySlug(slug string) (domain.Salon, error) {
	var s domain.Salon
	err := r.Pool.QueryRow(context.Background(),
		`SELECT id,slug,name,created_at FROM salons WHERE slug=$1`, slug,
	).Scan(&s.ID, &s.Slug, &s.Name, &s.CreatedAt)
	if err != nil {
		return domain.Salon{}, notFound(err)
	}
	return s, nil
}

// FindByID resuelve el salón de una membership (GET /me).
func (r Salons) FindByID(id string) (domain.Salon, error) {
	var s domain.Salon
	err := r.Pool.QueryRow(context.Background(),
		`SELECT id,slug,name,created_at FROM salons WHERE id=$1`, id,
	).Scan(&s.ID, &s.Slug, &s.Name, &s.CreatedAt)
	if err != nil {
		return domain.Salon{}, notFound(err)
	}
	return s, nil
}

// Settings lee la configuración operativa del salón (ports.SalonSettingsRepository).
// Una fila ausente es ErrNotFound: el caso de uso decide el default.
func (r Salons) Settings(salonID string) (domain.SalonSettings, error) {
	var s domain.SalonSettings
	err := r.Pool.QueryRow(context.Background(),
		`SELECT id, timezone, open_time, close_time, slot_step_min FROM salons WHERE id=$1`, salonID,
	).Scan(&s.SalonID, &s.Timezone, &s.OpenTime, &s.CloseTime, &s.SlotStepMin)
	if err != nil {
		return domain.SalonSettings{}, notFound(err)
	}
	return s, nil
}

// SaveSettings actualiza la configuración. El UPDATE es la autorización: si el
// salón no existe no se afecta ninguna fila (ErrNotFound, nunca un upsert).
func (r Salons) SaveSettings(s domain.SalonSettings) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE salons SET timezone=$2, open_time=$3, close_time=$4, slot_step_min=$5 WHERE id=$1`,
		s.SalonID, s.Timezone, s.OpenTime, s.CloseTime, s.SlotStepMin)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type Memberships struct{ Pool *pgxpool.Pool }

func (r Memberships) Save(m domain.Membership) error {
	// attends viaja en el upsert: la membresía es una sola fila (un rol por
	// persona y salón) y cambiar el rol recalcula la marca por defecto
	// (professional atiende, el resto no) en vez de arrastrar la vieja.
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO memberships(user_id,salon_id,role,attends,trade) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (user_id,salon_id) DO UPDATE SET role=EXCLUDED.role, attends=EXCLUDED.attends, trade=EXCLUDED.trade`,
		m.UserID, m.SalonID, string(m.Role), m.Attends, m.Trade)
	return err
}

func (r Memberships) Find(userID, salonID string) (domain.Membership, error) {
	var role, trade string
	var attends bool
	err := r.Pool.QueryRow(context.Background(),
		`SELECT role, attends, trade FROM memberships WHERE user_id=$1 AND salon_id=$2`, userID, salonID,
	).Scan(&role, &attends, &trade)
	if err != nil {
		return domain.Membership{}, notFound(err)
	}
	m, err := domain.NewMembership(userID, salonID, domain.Role(role))
	if err != nil {
		return domain.Membership{}, err
	}
	m.Attends = attends
	m.Trade = trade
	return m, nil
}

// ListByUser trae los salones de un usuario con su rol. Lo usan las features
// que solo tienen el JWT: rooms del websocket y "marcar notificación leída".
func (r Memberships) ListByUser(userID string) ([]domain.Membership, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT salon_id, role, attends, trade FROM memberships WHERE user_id=$1 ORDER BY salon_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Membership
	for rows.Next() {
		var salonID, role, trade string
		var attends bool
		if err := rows.Scan(&salonID, &role, &attends, &trade); err != nil {
			return nil, err
		}
		m, err := domain.NewMembership(userID, salonID, domain.Role(role))
		if err != nil {
			return nil, err
		}
		m.Attends = attends
		m.Trade = trade
		out = append(out, m)
	}
	return out, rows.Err()
}
