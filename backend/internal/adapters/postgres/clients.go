package pgstore

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
)

// Clients: fichas de clientes y sus fotos. Toda query va scopeada por salon_id
// (y por client_id cuando aplica): el id de otro tenant no existe.
type Clients struct{ Pool *pgxpool.Pool }

const clientColumns = `id,salon_id,name,phone,email,notes,created_at`

func scanClient(scan func(...interface{}) error) (domain.Client, error) {
	var c domain.Client
	if err := scan(&c.ID, &c.SalonID, &c.Name, &c.Phone, &c.Email, &c.Notes, &c.CreatedAt); err != nil {
		return domain.Client{}, err
	}
	c.CreatedAt = c.CreatedAt.UTC()
	return c, nil
}

func (r Clients) Save(c domain.Client) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO clients(id,salon_id,name,phone,email,notes,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, phone=EXCLUDED.phone,
		   email=EXCLUDED.email, notes=EXCLUDED.notes`,
		c.ID, c.SalonID, c.Name, c.Phone, c.Email, c.Notes, c.CreatedAt)
	return pgError(err)
}

// FindByPhone resuelve la ficha por teléfono normalizado (solo dígitos, la
// misma regla que domain.NormalizePhone) DENTRO del salón. La columna generada
// phone_norm es la que garantiza que "11 5555-1234" y "1155551234" sean la
// misma persona; la ficha sigue mostrando el teléfono como se escribió.
func (r Clients) FindByPhone(salonID, phone string) (domain.Client, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+clientColumns+` FROM clients
		 WHERE salon_id=$1 AND phone_norm=$2
		 ORDER BY created_at, id LIMIT 1`, salonID, domain.NormalizePhone(phone))
	c, err := scanClient(row.Scan)
	if err != nil {
		return domain.Client{}, notFound(err)
	}
	return c, nil
}

// FindByName es el respaldo cuando la reserva pública no trae teléfono: nombre
// exacto dentro del salón (sin esto, cada reserva anónima crearía una ficha).
func (r Clients) FindByName(salonID, name string) (domain.Client, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+clientColumns+` FROM clients
		 WHERE salon_id=$1 AND name=$2
		 ORDER BY created_at, id LIMIT 1`, salonID, name)
	c, err := scanClient(row.Scan)
	if err != nil {
		return domain.Client{}, notFound(err)
	}
	return c, nil
}

func (r Clients) FindByID(salonID, id string) (domain.Client, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+clientColumns+` FROM clients WHERE salon_id=$1 AND id=$2`, salonID, id)
	c, err := scanClient(row.Scan)
	if err != nil {
		return domain.Client{}, notFound(err)
	}
	return c, nil
}

func (r Clients) List(salonID string) ([]domain.Client, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+clientColumns+` FROM clients WHERE salon_id=$1 ORDER BY name`, salonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Client
	for rows.Next() {
		c, err := scanClient(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const photoColumns = `id,salon_id,client_id,path,width,height,bytes,content_type,created_at`

func scanPhoto(scan func(...interface{}) error) (domain.ClientPhoto, error) {
	var p domain.ClientPhoto
	if err := scan(&p.ID, &p.SalonID, &p.ClientID, &p.Path, &p.Width, &p.Height,
		&p.Bytes, &p.ContentType, &p.CreatedAt); err != nil {
		return domain.ClientPhoto{}, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

// SavePhoto registra la foto ya guardada en el storage. La FK compuesta
// (client_id, salon_id) rechaza cualquier cruce de salón: 23503 -> ErrValidation.
func (r Clients) SavePhoto(p domain.ClientPhoto) error {
	_, err := r.Pool.Exec(context.Background(),
		`INSERT INTO client_photos(id,salon_id,client_id,path,width,height,bytes,content_type,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (id) DO NOTHING`,
		p.ID, p.SalonID, p.ClientID, p.Path, p.Width, p.Height, p.Bytes, p.ContentType, p.CreatedAt)
	return pgError(err)
}

func (r Clients) ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+photoColumns+` FROM client_photos
		 WHERE salon_id=$1 AND client_id=$2 ORDER BY created_at DESC, id DESC`, salonID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ClientPhoto
	for rows.Next() {
		p, err := scanPhoto(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r Clients) FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+photoColumns+` FROM client_photos
		 WHERE salon_id=$1 AND client_id=$2 AND id=$3`, salonID, clientID, id)
	p, err := scanPhoto(row.Scan)
	if err != nil {
		return domain.ClientPhoto{}, notFound(err)
	}
	return p, nil
}

// PhotoByID resuelve solo por id: lo usa GET /api/v1/photos/:pid, donde el id
// aleatorio ES la capability que el navegador usa en <img src> (no puede mandar
// el header Authorization en una imagen).
func (r Clients) PhotoByID(id string) (domain.ClientPhoto, error) {
	row := r.Pool.QueryRow(context.Background(),
		`SELECT `+photoColumns+` FROM client_photos WHERE id=$1`, id)
	p, err := scanPhoto(row.Scan)
	if err != nil {
		return domain.ClientPhoto{}, notFound(err)
	}
	return p, nil
}

func (r Clients) DeletePhoto(salonID, clientID, id string) error {
	tag, err := r.Pool.Exec(context.Background(),
		`DELETE FROM client_photos WHERE salon_id=$1 AND client_id=$2 AND id=$3`,
		salonID, clientID, id)
	if err != nil {
		return pgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
