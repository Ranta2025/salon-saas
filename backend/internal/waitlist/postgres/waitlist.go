package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgstore "salon-saas/internal/adapters/postgres"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Waitlist es el adaptador de la lista de espera (F7, migración 030).
//
// Garantías duras que viven acá y no en el caso de uso:
//   - toda lectura y toda escritura del personal va scopeada por salon_id: la
//     entrada de otro tenant no se lista, no se encuentra y no se da de baja;
//   - el alta choca con el índice único PARCIAL (dos pedidos idénticos
//     simultáneos): el que pierde recibe ErrAlreadyExists y el caso de uso lo
//     trata como idempotente;
//   - el reclamo del barrido es UN solo UPDATE ... FOR UPDATE SKIP LOCKED dentro
//     de una transacción con `pg_advisory_xact_lock(hashtext('waitlist-write:<salón>'))`,
//     y el recuento de retenidos va DENTRO del lock: dos instancias del job (o
//     dos ticks) nunca avisan dos veces a la misma entrada;
//   - las fechas son las de Postgres (now()) para el reclamo y las que pasa el
//     caso de uso para el resto, así los tests pueden fijar el reloj.
type Waitlist struct {
	Pool *pgxpool.Pool
	// TxTimeout acota la transacción del reclamo: sin tope, una transacción
	// trabada (esperando el lock de un salón ocupado) deja el tick del job
	// esperando para siempre con una conexión del pool tomada.
	TxTimeout time.Duration
}

// waitlistColumns es la lista única de columnas de la entrada. Las tres fechas
// NULL se escanean en punteros: su ausencia es el estado (D3).
const waitlistColumns = `id,salon_id,service_id,coalesce(professional_id,''),day,name,email,coalesce(phone,''),
	coalesce(token_hash,''),created_at,notify_claimed_at,notify_attempts,notified_at,left_at`

// waitlistColumnsAliased es la misma lista con el alias de la tabla: la necesita
// el listado del personal, que hace JOIN con services y users (un `id` suelto
// sería ambiguo).
const waitlistColumnsAliased = `e.id,e.salon_id,e.service_id,coalesce(e.professional_id,''),e.day,e.name,e.email,coalesce(e.phone,''),
	coalesce(e.token_hash,''),e.created_at,e.notify_claimed_at,e.notify_attempts,e.notified_at,e.left_at`

func scanWaitlistEntry(scan func(...any) error) (domain.WaitlistEntry, error) {
	var e domain.WaitlistEntry
	var day time.Time
	if err := scan(&e.ID, &e.SalonID, &e.ServiceID, &e.ProfessionalID, &day, &e.Name, &e.Email, &e.Phone,
		&e.TokenHash, &e.CreatedAt, &e.NotifyClaimedAt, &e.NotifyAttempts, &e.NotifiedAt, &e.LeftAt); err != nil {
		return domain.WaitlistEntry{}, err
	}
	// El día es una fecha del calendario local del salón: se guarda y se lee
	// como "YYYY-MM-DD", sin hora ni zona.
	e.Day = day.Format("2006-01-02")
	e.CreatedAt = e.CreatedAt.UTC()
	if e.NotifyClaimedAt != nil {
		claimed := e.NotifyClaimedAt.UTC()
		e.NotifyClaimedAt = &claimed
	}
	if e.NotifiedAt != nil {
		notified := e.NotifiedAt.UTC()
		e.NotifiedAt = &notified
	}
	if e.LeftAt != nil {
		left := e.LeftAt.UTC()
		e.LeftAt = &left
	}
	return e, nil
}

// FindActive resuelve la entrada ACTIVA idéntica: es la idempotencia del alta
// (D4). La comparación del email es sin mayúsculas, igual que el índice único.
func (r Waitlist) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	entry, err := scanWaitlistEntry(r.Pool.QueryRow(context.Background(),
		`SELECT `+waitlistColumns+` FROM waitlist_entries
		 WHERE salon_id=$1 AND service_id=$2 AND coalesce(professional_id,'')=$3
		   AND day=$4::date AND lower(email)=lower($5) AND left_at IS NULL`,
		salonID, serviceID, professionalID, day, email).Scan)
	if err != nil {
		return domain.WaitlistEntry{}, pgstore.NotFound(err)
	}
	return entry, nil
}

// FindByTokenHash es la ÚNICA query sin salon_id: el hash del token es la
// capability que resuelve la entrada antes de saber de qué salón es (quien abre
// el enlace no tiene sesión). Sin fila es ErrNotFound.
func (r Waitlist) FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error) {
	entry, err := scanWaitlistEntry(r.Pool.QueryRow(context.Background(),
		`SELECT `+waitlistColumns+` FROM waitlist_entries WHERE token_hash=$1`, tokenHash).Scan)
	if err != nil {
		return domain.WaitlistEntry{}, pgstore.NotFound(err)
	}
	return entry, nil
}

// Leave setea left_at con un UPDATE condicional. Repetir la baja no afecta
// filas y NO es un error: la baja es idempotente (D5).
func (r Waitlist) Leave(tokenHash string, now time.Time) error {
	_, err := r.Pool.Exec(context.Background(),
		`UPDATE waitlist_entries SET left_at=$2 WHERE token_hash=$1 AND left_at IS NULL`,
		tokenHash, now.UTC())
	return pgstore.PGError(err)
}

// ListUpcoming es la pantalla del personal (D8): entradas vigentes del salón con
// día >= fromDay, ordenadas por día y alta, con el nombre del servicio y el de
// la profesional (JOIN; si el servicio o la persona ya no están, vacío).
func (r Waitlist) ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT `+waitlistColumnsAliased+`, coalesce(sv.name,''), coalesce(u.name,'')
		   FROM waitlist_entries e
		   LEFT JOIN services sv ON sv.id = e.service_id AND sv.salon_id = e.salon_id
		   LEFT JOIN users u ON u.id = e.professional_id
		  WHERE e.salon_id=$1 AND e.left_at IS NULL AND e.day >= $2::date
		  ORDER BY e.day, e.created_at, e.id`,
		salonID, fromDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WaitlistStaffEntry
	for rows.Next() {
		var row domain.WaitlistStaffEntry
		entry, err := scanWaitlistEntry(func(dest ...any) error {
			return rows.Scan(append(dest, &row.ServiceName, &row.ProfessionalName)...)
		})
		if err != nil {
			return nil, err
		}
		row.WaitlistEntry = entry
		out = append(out, row)
	}
	return out, rows.Err()
}

// RemoveByID da de baja una entrada desde el panel: UPDATE condicional con el
// scope del salón. De otro salón o ya dada de baja es ErrNotFound (404).
func (r Waitlist) RemoveByID(salonID, id string, now time.Time) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE waitlist_entries SET left_at=$3
		 WHERE id=$1 AND salon_id=$2 AND left_at IS NULL`, id, salonID, now.UTC())
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListGroups son los grupos con al menos una entrada RECLAMABLE ahora (D6): sin
// baja, sin aviso, con intentos disponibles, con el reclamo vencido (la gracia
// de 5 minutos) y con un día que todavía no pasó en la ZONA DEL SALÓN (el día
// local de `salons.timezone`, nunca UTC).
func (r Waitlist) ListGroups(now time.Time) ([]domain.WaitlistGroup, error) {
	rows, err := r.Pool.Query(context.Background(),
		`SELECT DISTINCT e.salon_id, s.slug, s.name, e.service_id, coalesce(sv.name,''),
		        coalesce(e.professional_id,''), e.day
		   FROM waitlist_entries e
		   JOIN salons s ON s.id = e.salon_id
		   LEFT JOIN services sv ON sv.id = e.service_id AND sv.salon_id = e.salon_id
		  WHERE e.left_at IS NULL
		    AND e.notified_at IS NULL
		    AND e.notify_attempts < 3
		    AND (e.notify_claimed_at IS NULL OR e.notify_claimed_at < $1::timestamptz - interval '5 minutes')
		    AND e.day >= (($1::timestamptz AT TIME ZONE s.timezone)::date)
		  ORDER BY e.salon_id, e.service_id, coalesce(e.professional_id,''), e.day`,
		now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WaitlistGroup
	for rows.Next() {
		var g domain.WaitlistGroup
		var day time.Time
		if err := rows.Scan(&g.SalonID, &g.SalonSlug, &g.SalonName, &g.ServiceID, &g.ServiceName,
			&g.ProfessionalID, &day); err != nil {
			return nil, err
		}
		g.Day = day.Format("2006-01-02")
		out = append(out, g)
	}
	return out, rows.Err()
}

// MarkNotified cierra la entrada después de un envío exitoso: no se vuelve a
// avisar nunca (D6).
func (r Waitlist) MarkNotified(id string, now time.Time) error {
	tag, err := r.Pool.Exec(context.Background(),
		`UPDATE waitlist_entries SET notified_at=$2 WHERE id=$1`, id, now.UTC())
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ ports.WaitlistRepository = Waitlist{}

// --- Transacción del barrido (D6) ------------------------------------------

// InWaitlistTx abre la transacción del reclamo y commitea SOLO si fn termina
// bien. Una sola conexión del pool para toda la operación.
func (r Waitlist) InWaitlistTx(fn func(tx ports.WaitlistTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgstore.TxTimeout(r.TxTimeout))
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer pgstore.RollbackTx(tx) // no-op si ya commiteó
	if err := fn(waitlistTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type waitlistTx struct {
	tx  pgx.Tx
	ctx context.Context
}

// LockWrites toma el advisory lock de ESCRITURAS de la lista del salón. Es el
// único lock de la transacción y se libera solo al terminar.
func (t waitlistTx) LockWrites(salonID string) error {
	_, err := t.tx.Exec(t.ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "waitlist-write:"+salonID)
	return err
}

// FindActive resuelve la entrada ACTIVA idéntica DENTRO de la transacción del
// alta (ya con LockWrites tomado): idempotencia del alta (D4). El email se
// compara sin mayúsculas, igual que el índice único.
func (t waitlistTx) FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error) {
	entry, err := scanWaitlistEntry(t.tx.QueryRow(t.ctx,
		`SELECT `+waitlistColumns+` FROM waitlist_entries
		 WHERE salon_id=$1 AND service_id=$2 AND coalesce(professional_id,'')=$3
		   AND day=$4::date AND lower(email)=lower($5) AND left_at IS NULL`,
		salonID, serviceID, professionalID, day, email).Scan)
	if err != nil {
		return domain.WaitlistEntry{}, pgstore.NotFound(err)
	}
	return entry, nil
}

// CountActiveByEmail cuenta los avisos activos de ese email en el salón con día
// >= fromDay (tope de 3, D4). fromDay es hoy en la zona del salón: un aviso de un
// día pasado no ocupa cupo (revisión nativa R4-001). Corre bajo el lock del alta.
func (t waitlistTx) CountActiveByEmail(salonID, email, fromDay string) (int, error) {
	var n int
	err := t.tx.QueryRow(t.ctx,
		`SELECT count(*) FROM waitlist_entries
		 WHERE salon_id=$1 AND lower(email)=lower($2) AND left_at IS NULL
		   AND day >= $3::date`,
		salonID, email, fromDay).Scan(&n)
	return n, err
}

// CountActiveBySalon cuenta los avisos activos del salón con día >= fromDay (tope
// de 500, D4); mismo criterio de fromDay. Corre bajo el lock del alta.
func (t waitlistTx) CountActiveBySalon(salonID, fromDay string) (int, error) {
	var n int
	err := t.tx.QueryRow(t.ctx,
		`SELECT count(*) FROM waitlist_entries
		 WHERE salon_id=$1 AND left_at IS NULL AND day >= $2::date`,
		salonID, fromDay).Scan(&n)
	return n, err
}

// Create inserta la entrada. El token nace NULL: la capability de la baja se
// emite con el aviso, así un alta que nunca se anuncia no deja un enlace vivo.
// Si el índice único parcial rechaza devuelve ErrAlreadyExists.
func (t waitlistTx) Create(e domain.WaitlistEntry) error {
	_, err := t.tx.Exec(t.ctx,
		`INSERT INTO waitlist_entries(id,salon_id,service_id,professional_id,day,name,email,phone,
		   token_hash,created_at,notify_attempts)
		 VALUES($1,$2,$3,NULLIF($4,''),$5::date,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,0)`,
		e.ID, e.SalonID, e.ServiceID, e.ProfessionalID, e.Day, e.Name, e.Email, e.Phone,
		e.TokenHash, e.CreatedAt.UTC())
	return pgstore.PGError(err)
}

// MarkLeft cierra una entrada por id (UPDATE condicional): la ya avisada que se
// reemplaza cuando la persona vuelve a anotarse (D6). Sin fila activa es
// ErrNotFound.
func (t waitlistTx) MarkLeft(id string, now time.Time) error {
	tag, err := t.tx.Exec(t.ctx,
		`UPDATE waitlist_entries SET left_at=$2 WHERE id=$1 AND left_at IS NULL`, id, now.UTC())
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountRetained cuenta los avisos RETENIDOS del grupo (D6): los ENVIADOS con
// reclamo de las últimas 2 horas y los que están EN VUELO (reclamo de los
// últimos 5 minutos sin aviso). Un envío que falló no retiene nada: pasada la
// gracia se reintenta (tope de 3 intentos). Se llama SIEMPRE dentro del lock.
const countRetainedSQL = `
SELECT count(*) FROM waitlist_entries
 WHERE salon_id=$1 AND service_id=$2 AND coalesce(professional_id,'')=$3
   AND day=$4::date AND left_at IS NULL
   AND ((notified_at IS NOT NULL AND notify_claimed_at > $5::timestamptz - interval '2 hours')
     OR (notified_at IS NULL AND notify_claimed_at > $5::timestamptz - interval '5 minutes'))`

func (t waitlistTx) CountRetained(g domain.WaitlistGroup, now time.Time) (int, error) {
	var n int
	err := t.tx.QueryRow(t.ctx, countRetainedSQL,
		g.SalonID, g.ServiceID, g.ProfessionalID, g.Day, now.UTC()).Scan(&n)
	return n, err
}

// claimSQL es UN solo statement: elige FIFO y reclama. Sin check-then-update,
// así dos instancias (o dos ticks) no pueden llevarse la misma entrada: el
// segundo UPDATE no ve filas.
const claimSQL = `
WITH picked AS (
  SELECT e.id
    FROM waitlist_entries e
    JOIN salons s ON s.id = e.salon_id
   WHERE e.salon_id=$1 AND e.service_id=$2 AND coalesce(e.professional_id,'')=$3
     AND e.day=$4::date
     AND e.left_at IS NULL AND e.notified_at IS NULL
     AND e.notify_attempts < 3
     AND (e.notify_claimed_at IS NULL OR e.notify_claimed_at < $5::timestamptz - interval '5 minutes')
     AND e.day >= (($5::timestamptz AT TIME ZONE s.timezone)::date)
   ORDER BY e.created_at, e.id
   LIMIT $6
   FOR UPDATE OF e SKIP LOCKED
)
UPDATE waitlist_entries e
   SET notify_claimed_at = $5,
       notify_attempts   = e.notify_attempts + 1
  FROM picked
 WHERE e.id = picked.id
RETURNING ` + waitlistColumnsAliased

// Claim reclama hasta limit entradas del grupo y las devuelve ya reclamadas (con
// notify_attempts incrementado).
func (t waitlistTx) Claim(g domain.WaitlistGroup, now time.Time, limit int) ([]domain.WaitlistEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := t.tx.Query(t.ctx, claimSQL,
		g.SalonID, g.ServiceID, g.ProfessionalID, g.Day, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WaitlistEntry
	for rows.Next() {
		entry, err := scanWaitlistEntry(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// SetToken guarda el SHA-256 del token de la baja de una entrada reclamada. El
// token en claro solo viaja en el email; en la DB queda su hash (D5).
func (t waitlistTx) SetToken(id, tokenHash string) error {
	tag, err := t.tx.Exec(t.ctx,
		`UPDATE waitlist_entries SET token_hash=$2 WHERE id=$1`, id, tokenHash)
	if err != nil {
		return pgstore.PGError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var (
	_ ports.WaitlistRepository = Waitlist{}
	_ ports.WaitlistTxRunner   = Waitlist{}
	_ ports.WaitlistTx         = waitlistTx{}
)
