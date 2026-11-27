package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// --- Lista de espera (F7) ---

// WaitlistRepository: la lista de espera del salón (migración 030). Una entrada
// es un servicio + un día LOCAL + (profesional opcional) + contacto, y el token
// de la baja se guarda SOLO como SHA-256.
//
// Toda consulta va scopeada por salon_id: la entrada de otro tenant no se lista,
// no se encuentra y no se da de baja (ErrNotFound). Las fechas NULL son la
// verdad del estado (D3): no hay una columna `status` que pueda contradecirlas.
type WaitlistRepository interface {
	// FindActive resuelve la entrada ACTIVA idéntica (mismo email, servicio,
	// profesional y día): es la idempotencia del alta (D4). Sin fila es
	// ErrNotFound.
	FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error)
	// FindByTokenHash es la ÚNICA query sin salon_id: el hash del token es la
	// capability que resuelve la entrada antes de saber de qué salón es (quien
	// abre el enlace no tiene sesión). Sin fila es ErrNotFound.
	FindByTokenHash(tokenHash string) (domain.WaitlistEntry, error)
	// Leave setea left_at con un UPDATE condicional por token_hash. Repetirlo no
	// hace nada (la baja es idempotente, D5): no es un error.
	Leave(tokenHash string, now time.Time) error
	// ListUpcoming son las entradas vigentes del salón con día >= fromDay (día
	// LOCAL del salón), ordenadas por día y created_at (D8).
	ListUpcoming(salonID, fromDay string) ([]domain.WaitlistStaffEntry, error)
	// RemoveByID da de baja una entrada del salón (UPDATE condicional left_at
	// IS NULL). De otro salón o ya dada de baja: ErrNotFound.
	RemoveByID(salonID, id string, now time.Time) error
	// ListGroups son los grupos (salón, servicio, profesional, día) con al menos
	// una entrada RECLAMABLE ahora: sin baja, sin aviso, con intentos
	// disponibles, con el reclamo vencido y con el día >= hoy en la zona del
	// salón (D3/D6). Es lo que la tarea periódica recorre.
	ListGroups(now time.Time) ([]domain.WaitlistGroup, error)
	// MarkNotified cierra la entrada después de un envío exitoso: nunca se
	// vuelve a avisar (D6).
	MarkNotified(id string, now time.Time) error
}

// WaitlistTx es la vista TRANSACCIONAL del barrido (D6): el reclamo de los
// avisos de UN grupo corre dentro de una transacción con
// `pg_advisory_xact_lock(hashtext('waitlist-write:<salón>'))` y RECUENTA los
// avisos retenidos ahí adentro. Dos instancias del job (o dos ticks) no pueden
// avisar dos veces a la misma entrada: el lock serializa y el UPDATE ... FOR
// UPDATE SKIP LOCKED se lleva cada fila una sola vez.
type WaitlistTx interface {
	// LockWrites toma el advisory lock de escrituras de la lista del salón. Es
	// el único lock de la transacción y se libera solo al terminar.
	LockWrites(salonID string) error
	// FindActive, CountActiveByEmail, CountActiveBySalon, Create y MarkLeft son las
	// escrituras del ALTA (D4) atadas a la MISMA transacción que LockWrites: el
	// conteo de los topes y el insert no pueden intercalarse con otra alta del
	// salón. FindActive resuelve la entrada ACTIVA idéntica (mismo email,
	// servicio, profesional y día). fromDay es HOY en la zona del salón: un aviso
	// de un día pasado no ocupa cupo. Create devuelve ErrAlreadyExists si el
	// índice único parcial rechaza. MarkLeft cierra una entrada por id (la ya
	// avisada que se reemplaza al volver a anotarse).
	FindActive(salonID, serviceID, professionalID, day, email string) (domain.WaitlistEntry, error)
	CountActiveByEmail(salonID, email, fromDay string) (int, error)
	CountActiveBySalon(salonID, fromDay string) (int, error)
	Create(e domain.WaitlistEntry) error
	MarkLeft(id string, now time.Time) error
	// CountRetained cuenta los avisos RETENIDOS del grupo (D6): los enviados con
	// reclamo de las últimas 2 h y los que están EN VUELO (reclamo de los
	// últimos 5 minutos sin aviso). Un envío que falló no retiene nada: se
	// reintenta pasada la gracia.
	CountRetained(g domain.WaitlistGroup, now time.Time) (int, error)
	// Claim reclama hasta limit entradas del grupo, FIFO por (created_at, id),
	// con UN solo UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED):
	// setea notify_claimed_at = now y notify_attempts = notify_attempts + 1.
	Claim(g domain.WaitlistGroup, now time.Time, limit int) ([]domain.WaitlistEntry, error)
	// SetToken guarda el SHA-256 del token de la baja de UNA entrada reclamada.
	// La capability se emite en el momento del aviso (el token en claro solo
	// viaja en el email): una entrada que nunca se avisa no tiene enlace vivo.
	SetToken(id, tokenHash string) error
}

// WaitlistTxRunner abre la transacción del reclamo. Una sola conexión del pool
// para toda la operación.
type WaitlistTxRunner interface {
	InWaitlistTx(fn func(tx WaitlistTx) error) error
}
