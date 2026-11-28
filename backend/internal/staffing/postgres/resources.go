package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Resources: tipos y unidades de equipo del salón (F2e). Toda query va scopeada
// por salon_id: el equipo de otro tenant no existe acá (ErrNotFound).
type Resources struct{ Pool *pgxpool.Pool }

const (
	resourceTypeColumns = `id,salon_id,name,created_at`
	resourceUnitColumns = `id,salon_id,type_id,name,active`
)

func scanResourceType(scan func(...interface{}) error) (domain.ResourceType, error) {
	var t domain.ResourceType
	if err := scan(&t.ID, &t.SalonID, &t.Name, &t.CreatedAt); err != nil {
		return domain.ResourceType{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	return t, nil
}

func scanResourceUnit(scan func(...interface{}) error) (domain.ResourceUnit, error) {
	var u domain.ResourceUnit
	if err := scan(&u.ID, &u.SalonID, &u.TypeID, &u.Name, &u.Active); err != nil {
		return domain.ResourceUnit{}, err
	}
	return u, nil
}

// CreateType crea el tipo y sus unidades en UNA transacción: un alta con
// `count` no puede quedar a medias (S-F2e-3).
func (r Resources) CreateType(t domain.ResourceType, units []domain.ResourceUnit) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO resource_types(id,salon_id,name,created_at) VALUES($1,$2,$3,$4)`,
		t.ID, t.SalonID, t.Name, t.CreatedAt); err != nil {
		return pgstore.PGError(err)
	}
	for _, u := range units {
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_units(id,salon_id,type_id,name,active) VALUES($1,$2,$3,$4,$5)`,
			u.ID, u.SalonID, u.TypeID, u.Name, u.Active); err != nil {
			return pgstore.PGError(err)
		}
	}
	return pgstore.PGError(tx.Commit(ctx))
}

// SaveType actualiza el nombre del tipo. El WHERE con salon_id es la
// autorización: un id de otro tenant no afecta filas (ErrNotFound).
func (r Resources) SaveType(t domain.ResourceType) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE resource_types SET name=$3 WHERE salon_id=$1 AND id=$2`,
		t.SalonID, t.ID, t.Name)
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteType borra el tipo y sus unidades en UNA transacción. La FK de
// services.resource_type_id y la de bookings.resource_unit_id son RESTRICT: si
// algo lo usa, el rollback deja todo como estaba y el error sube como
// ErrAlreadyExists (el caso de uso responde el mensaje del contrato).
func (r Resources) DeleteType(salonID, id string) error {
	ctx := context.Background()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	// Las unidades con turnos frenan el borrado (RESTRICT en bookings).
	if _, err := tx.Exec(ctx,
		`DELETE FROM resource_units WHERE salon_id=$1 AND type_id=$2`, salonID, id); err != nil {
		return foreignInUse(err)
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM resource_types WHERE salon_id=$1 AND id=$2`, salonID, id)
	if err != nil {
		return foreignInUse(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return pgstore.PGError(tx.Commit(ctx))
}

func (r Resources) FindType(salonID, id string) (domain.ResourceType, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+resourceTypeColumns+` FROM resource_types WHERE salon_id=$1 AND id=$2`, salonID, id)
	t, err := scanResourceType(row.Scan)
	if err != nil {
		return domain.ResourceType{}, pgstore.NotFound(err)
	}
	return t, nil
}

func (r Resources) ListTypes(salonID string) ([]domain.ResourceType, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+resourceTypeColumns+` FROM resource_types WHERE salon_id=$1 ORDER BY name, id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ResourceType, 0, 8)
	for rows.Next() {
		t, err := scanResourceType(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveUnit inserta o actualiza una unidad (upsert por id).
func (r Resources) SaveUnit(u domain.ResourceUnit) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO resource_units(id,salon_id,type_id,name,active)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, active=EXCLUDED.active
		 WHERE resource_units.salon_id = EXCLUDED.salon_id`,
		u.ID, u.SalonID, u.TypeID, u.Name, u.Active)
	return pgstore.PGError(err)
}

func (r Resources) FindUnit(salonID, id string) (domain.ResourceUnit, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+resourceUnitColumns+` FROM resource_units WHERE salon_id=$1 AND id=$2`, salonID, id)
	u, err := scanResourceUnit(row.Scan)
	if err != nil {
		return domain.ResourceUnit{}, pgstore.NotFound(err)
	}
	return u, nil
}

func (r Resources) ListUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	return r.units(salonID, typeID, false)
}

func (r Resources) ActiveUnits(salonID, typeID string) ([]domain.ResourceUnit, error) {
	return r.units(salonID, typeID, true)
}

func (r Resources) units(salonID, typeID string, activeOnly bool) ([]domain.ResourceUnit, error) {
	q := `SELECT ` + resourceUnitColumns + ` FROM resource_units WHERE salon_id=$1 AND type_id=$2`
	if activeOnly {
		q += ` AND active`
	}
	q += ` ORDER BY name, id`
	rows, err := r.Pool.Query(context.Background(), q, salonID, typeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ResourceUnit, 0, 8)
	for rows.Next() {
		u, err := scanResourceUnit(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnitsWithBookings cuenta unidades del tipo con algún turno reservado: es lo
// que impide borrar el tipo (S-F2e-3).
func (r Resources) UnitsWithBookings(salonID, typeID string) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT count(DISTINCT u.id)
		 FROM resource_units u
		 JOIN bookings b ON b.salon_id = u.salon_id AND b.resource_unit_id = u.id
		 WHERE u.salon_id=$1 AND u.type_id=$2 AND b.status='booked'`,
		salonID, typeID).Scan(&n)
	return n, err
}

// UnitFutureBookings cuenta los turnos reservados que empiezan después de now y
// usan la unidad: el aviso "N turnos futuros usan este puesto".
func (r Resources) UnitFutureBookings(salonID, unitID string, now time.Time) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings
		 WHERE salon_id=$1 AND resource_unit_id=$2 AND status='booked' AND starts_at > $3`,
		salonID, unitID, now).Scan(&n)
	return n, err
}

// OccupiedUnits trae los intervalos tomados por turnos reservados con unidad en
// [from,to). excludeBookingID deja fuera al turno que se está reprogramando.
func (r Resources) OccupiedUnits(salonID string, from, to time.Time, excludeBookingID string) ([]domain.UnitOccupancy, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT resource_unit_id, starts_at, ends_at FROM bookings
		 WHERE salon_id=$1 AND status='booked' AND resource_unit_id IS NOT NULL
		   AND id <> $4
		   AND tstzrange(starts_at, ends_at) && tstzrange($2, $3)
		 ORDER BY starts_at`,
		salonID, from, to, excludeBookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.UnitOccupancy, 0, 16)
	for rows.Next() {
		var o domain.UnitOccupancy
		if err := rows.Scan(&o.UnitID, &o.StartsAt, &o.EndsAt); err != nil {
			return nil, err
		}
		o.StartsAt, o.EndsAt = o.StartsAt.UTC(), o.EndsAt.UTC()
		out = append(out, o)
	}
	return out, rows.Err()
}

// foreignInUse traduce "otra tabla todavía lo referencia" a "está en uso" (409),
// no al 400 genérico de una referencia inexistente: 23001 (RESTRICT) lo levanta
// la FK de services/units, 23503 (NO ACTION comprobado al final) el resto.
func foreignInUse(err error) error {
	var pg *pgconn.PgError
	if err != nil && errors.As(err, &pg) && (pg.Code == "23001" || pg.Code == "23503") {
		return domain.ErrAlreadyExists
	}
	return pgstore.PGError(err)
}

var _ ports.ResourceRepository = Resources{}
