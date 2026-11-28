package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// SalonGallery: la galería pública del salón (F16, D3), tabla salon_photos. Es
// una tabla APARTE de client_photos: ninguna consulta de acá puede devolver una
// foto de clienta. Toda query va scopeada por salón (o por slug en la pública).
type SalonGallery struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción de Add (lock + conteo + insert). 0 = el
	// tope por defecto de txtimeout.go.
	TxTimeout time.Duration
}

const galleryColumns = `id, salon_id, storage_key, content_type, COALESCE(caption,''), created_at`

func scanSalonPhoto(scan func(...any) error) (domain.SalonPhoto, error) {
	var p domain.SalonPhoto
	if err := scan(&p.ID, &p.SalonID, &p.StorageKey, &p.ContentType, &p.Caption, &p.CreatedAt); err != nil {
		return domain.SalonPhoto{}, err
	}
	return p, nil
}

// Add inserta la foto en una transacción que toma un lock por salón y cuenta
// DENTRO del lock: dos subidas simultáneas con 11 fotos se serializan y la
// segunda ve 12 (409), así el tope no depende de un chequeo previo racy.
func (r SalonGallery) Add(p domain.SalonPhoto, max int) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "salon-gallery:"+p.SalonID); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM salon_photos WHERE salon_id=$1`, p.SalonID).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgGalleryFull)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO salon_photos(id, salon_id, storage_key, content_type, caption, created_at)
		 VALUES($1,$2,$3,$4,NULLIF($5,''),$6)`,
		p.ID, p.SalonID, p.StorageKey, p.ContentType, p.Caption, p.CreatedAt); err != nil {
		return pgstore.PGError(err)
	}
	return tx.Commit(ctx)
}

func (r SalonGallery) Count(salonID string) (int, error) {
	var n int
	err := r.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM salon_photos WHERE salon_id=$1`, salonID).Scan(&n)
	return n, err
}

// List trae la galería en orden de subida (la más vieja primero; el id desempata).
func (r SalonGallery) List(salonID string) ([]domain.SalonPhoto, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+galleryColumns+` FROM salon_photos WHERE salon_id=$1 ORDER BY created_at, id`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SalonPhoto, 0, domain.MaxSalonPhotos)
	for rows.Next() {
		p, err := scanSalonPhoto(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r SalonGallery) Find(salonID, id string) (domain.SalonPhoto, error) {
	p, err := scanSalonPhoto(r.Pool.QueryRow(context.Background(),
		`SELECT `+galleryColumns+` FROM salon_photos WHERE salon_id=$1 AND id=$2`, salonID, id).Scan)
	if err != nil {
		return domain.SalonPhoto{}, pgstore.NotFound(err)
	}
	return p, nil
}

// FindPublic resuelve por el slug del salón y el id: un id de otro salón o
// inexistente es ErrNotFound. Solo mira salon_photos.
func (r SalonGallery) FindPublic(slug, id string) (domain.SalonPhoto, error) {
	p, err := scanSalonPhoto(r.Pool.QueryRow(context.Background(),
		`SELECT p.id, p.salon_id, p.storage_key, p.content_type, COALESCE(p.caption,''), p.created_at
		 FROM salon_photos p JOIN salons s ON s.id = p.salon_id
		 WHERE s.slug=$1 AND p.id=$2`, slug, id).Scan)
	if err != nil {
		return domain.SalonPhoto{}, pgstore.NotFound(err)
	}
	return p, nil
}

func (r SalonGallery) SetCaption(salonID, id, caption string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE salon_photos SET caption=NULLIF($3,'') WHERE salon_id=$1 AND id=$2`, salonID, id, caption)
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r SalonGallery) Delete(salonID, id string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM salon_photos WHERE salon_id=$1 AND id=$2`, salonID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ ports.SalonGalleryRepository = SalonGallery{}
