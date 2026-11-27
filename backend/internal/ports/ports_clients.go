package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// --- Clientes y adjuntos ---

// ClientRepository: fichas de clientes del salón y sus fotos.
// Toda query va scopeada por salon_id: el id de otro tenant no existe acá.
type ClientRepository interface {
	Save(c domain.Client) error
	FindByID(salonID, id string) (domain.Client, error)
	List(salonID string) ([]domain.Client, error)
	// FindByPhone busca la ficha por teléfono NORMALIZADO (domain.NormalizePhone)
	// dentro del salón. La usa la reserva pública para no crear una ficha nueva
	// cada vez que la misma persona reserva.
	FindByPhone(salonID, phone string) (domain.Client, error)
	// FindByName busca la ficha por nombre exacto dentro del salón: es el
	// respaldo de la reserva pública cuando la persona no dejó teléfono.
	FindByName(salonID, name string) (domain.Client, error)
	SavePhoto(p domain.ClientPhoto) error
	ListPhotos(salonID, clientID string) ([]domain.ClientPhoto, error)
	DeletePhoto(salonID, clientID, id string) error
	// FindPhoto resuelve una foto con el scope completo (salón + cliente).
	FindPhoto(salonID, clientID, id string) (domain.ClientPhoto, error)
	// PhotoByID resuelve por id solo: lo usa el streaming /photos/:pid, donde
	// el id aleatorio es la capability que el navegador usa en <img src>.
	PhotoByID(id string) (domain.ClientPhoto, error)
}

// PhotoStorage guarda los binarios fuera de la DB. El adaptador puede ser el
// filesystem local o S3/MinIO (se elige por S3_ENDPOINT en el composition
// root); el puerto es el mismo.
type PhotoStorage interface {
	// Put guarda data bajo key y devuelve la ruta estable a persistir en la DB.
	Put(key string, data []byte, contentType string) (storedPath string, err error)
	Get(path string) ([]byte, error)
	Delete(path string) error
}

// PhotoURLSigner firma la URL de una foto con vencimiento (HMAC). El navegador
// usa la url tal cual en <img src> y no puede mandar Authorization; la firma es
// la capability temporal (24 h).
type PhotoURLSigner interface {
	// Sign devuelve la firma (texto seguro para URL) de photoID con ese vencimiento.
	Sign(photoID string, expiresUnix int64) string
	// Verify dice si la firma es válida y no venció.
	Verify(photoID string, expiresUnix int64, sig string) bool
}

// ImageProcessor normaliza la subida: devuelve la variante que se persiste
// (WebP, lado mayor acotado) y sus dimensiones reales. Es un puerto porque el
// día que haya que usar otra herramienta, cambia el adaptador y nada más.
type ImageProcessor interface {
	ToWebP(data []byte) (out []byte, width, height int, err error)
}

// --- Historial de la clienta y vínculos por id (F5 del contrato Fase 6) ---

// ClientHistoryRepository: la LECTURA del historial (D6). Todo sale de UNA
// transacción REPEATABLE READ de solo lectura con tope de 25 s en el adaptador,
// así la foto es una sola: un turno que se marca hecho a mitad de camino no
// puede entrar en los conteos y no en la lista.
//
// `now` lo decide el caso de uso (los próximos son los `booked` con starts_at >
// now) y `limit` acota CADA lista (más reciente primero). Las queries van
// scopeadas por salon_id Y client_id: el historial de otra ficha no existe.
type ClientHistoryRepository interface {
	History(salonID, clientID string, now time.Time, limit int) (domain.ClientHistoryData, error)
}

// ClientLinkRepository: el UPDATE aparte que vincula un turno o una venta con la
// ficha (D1/D3). NO es parte del INSERT: es best-effort, un fallo se loguea y
// nunca tumba el turno ni la venta.
//
// Todas las escrituras van scopeadas por salon_id: la ficha de otro salón no se
// vincula (el EXISTS de la query es la garantía dura, no solo el caso de uso).
type ClientLinkRepository interface {
	// LinkBookingByID vincula el turno con ESA ficha (misma ficha que ya creó o
	// reusó la reserva pública).
	LinkBookingByID(salonID, bookingID, clientID string) error
	// LinkBookingByPhone vincula el turno del personal con la ÚNICA ficha del
	// salón cuyo teléfono normalizado coincide: sin coincidencia o con dos
	// candidatas no toca nada (nunca crea una ficha, D3-b).
	LinkBookingByPhone(salonID, bookingID string) error
	// LinkSaleByID vincula la venta del POS con la ficha elegida a mano (D3-c).
	LinkSaleByID(salonID, saleID, clientID string) error
	// SaleClientID devuelve el vínculo REAL de la venta ("" si no tiene): la
	// respuesta de POST /sales dice lo que quedó guardado y no lo que se pidió
	// (R4-001: un UPDATE caído o un replay no deben afirmar un vínculo falso).
	SaleClientID(salonID, saleID string) (string, error)
}
