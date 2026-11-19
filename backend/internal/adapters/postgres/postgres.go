// Package pgstore implementa ports.* con Postgres/pgx.
// Traduce pgx.ErrNoRows <-> domain.ErrNotFound: el resto del sistema
// nunca ve pgx, solo errores de dominio (Puertos y Adaptadores).
package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

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
	return err
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

type Memberships struct{ Pool *pgxpool.Pool }

func (r Memberships) Save(m domain.Membership) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO memberships(user_id,salon_id,role) VALUES($1,$2,$3)
		 ON CONFLICT (user_id,salon_id) DO UPDATE SET role=EXCLUDED.role`,
		m.UserID, m.SalonID, string(m.Role))
	return err
}

func (r Memberships) Find(userID, salonID string) (domain.Membership, error) {
	var role string
	err := r.Pool.QueryRow(context.Background(),
		`SELECT role FROM memberships WHERE user_id=$1 AND salon_id=$2`, userID, salonID,
	).Scan(&role)
	if err != nil {
		return domain.Membership{}, notFound(err)
	}
	return domain.NewMembership(userID, salonID, domain.Role(role))
}
