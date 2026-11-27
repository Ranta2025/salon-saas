package ports

import "time"

// ===== F13: reseñas posvisita (inicio) =====
//
// Los tipos viven acá (y no en `domain`) porque el módulo `internal/reviews` es
// nuevo y su contrato de cableado lo comparten tres paquetes: el caso de uso, su
// adaptador de Postgres y la vidriera pública (`internal/settings`), que recibe
// la sección `reviews` por un puerto OPCIONAL para no cambiar nada del resto del
// payload.

// ReviewSettings es la configuración de reseñas de un salón (D6). Saved=false es
// «nunca guardada»: la API responde enabled:false y delay_hours:24, y el barrido
// no pide ninguna reseña.
type ReviewSettings struct {
	SalonID    string
	Enabled    bool
	DelayHours int
	Saved      bool
}

// Review es una reseña YA publicada (D2). AuthorName es la FOTO del nombre
// público (D5: «Lía G.») y ServiceName el servicio del turno, tomados al crear
// la reseña: cambiar el servicio después no reescribe el historial.
type Review struct {
	ID          string
	SalonID     string
	BookingID   string
	Rating      int
	Comment     string
	AuthorName  string
	ServiceName string
	Hidden      bool
	CreatedAt   time.Time
}

// ReviewCursor es la posición de una página del panel: el último renglón ya
// devuelto. El orden es (created_at DESC, id DESC), así que la página siguiente
// son los renglones ESTRICTAMENTE anteriores a (CreatedAt, ID).
type ReviewCursor struct {
	CreatedAt time.Time
	ID        string
}

// ReviewQuery es una página del panel YA validada (límite 1..100 y cursor).
type ReviewQuery struct {
	Limit  int
	Before *ReviewCursor
}

// ReviewLink es lo que ve quien abre el enlace del correo (D4). Nunca lleva el
// correo, el teléfono ni el turno.
type ReviewLink struct {
	SalonName   string
	ServiceName string
	StartsAt    time.Time
	Timezone    string
}

// ReviewSubmit es el canje del enlace: el hash del token y lo que escribió la
// persona. El comentario llega YA validado y recortado por el caso de uso.
type ReviewSubmit struct {
	TokenHash string
	Rating    int
	Comment   string
	Now       time.Time
}

// PublicReviews es la sección `reviews` de la vidriera pública (D7): el promedio
// con una decimal y el total de las VISIBLES, y como máximo los 6 renglones más
// recientes. Count = 0 significa «no se muestra la sección».
type PublicReviews struct {
	Average float64
	Count   int
	Items   []PublicReview
}

// PublicReview es un renglón público de una reseña.
type PublicReview struct {
	Rating  int
	Comment string
	Author  string
	// Date es el día LOCAL del salón de la reseña ("YYYY-MM-DD").
	Date string
}

// DueReview es un turno atendido que ya puede pedir su reseña (D3). Trae todo lo
// que la plantilla del correo necesita: el envío no vuelve a consultar la base.
type DueReview struct {
	BookingID   string
	SalonID     string
	SalonName   string
	ServiceName string
	ClientName  string
	Email       string
	StartsAt    time.Time
	Timezone    string
}

// ReviewClaim es el pedido que el barrido quiere crear (o reutilizar) y cuyo
// envío quiere reclamar. TokenHash es el SHA-256 del token NUEVO: el token en
// claro nunca llega a la base.
type ReviewClaim struct {
	ID        string
	SalonID   string
	BookingID string
	TokenHash string
	Now       time.Time
	ExpiresAt time.Time
}

// ReviewRepository es la lectura/escritura de las reseñas y su configuración.
// Toda consulta va scopeada por salon_id.
type ReviewRepository interface {
	// Settings lee la configuración del salón. ErrNotFound = nunca se guardó.
	Settings(salonID string) (ReviewSettings, error)
	// SaveSettings guarda (upsert) la configuración.
	SaveSettings(s ReviewSettings) error
	// List devuelve una página del panel del salón, del más nuevo al más viejo.
	List(salonID string, q ReviewQuery) ([]Review, error)
	// SetHidden oculta o muestra UNA reseña del salón. Idempotente; ErrNotFound
	// si la reseña es de otro salón o no existe.
	SetHidden(salonID, reviewID string, hidden bool) error
	// Visible arma la sección pública: promedio y total de las VISIBLES y los
	// 6 renglones más recientes. El promedio llega SIN redondear (lo redondea el
	// caso de uso, que es donde vive la regla).
	Visible(salonID string) (PublicReviews, error)
	// Submit canjea el token y crea la reseña en UNA transacción: un token
	// desconocido, vencido o ya usado es ErrNotFound (uniforme) y el segundo de
	// dos POST simultáneos no escribe nada.
	Submit(req ReviewSubmit) (Review, error)
}

// ClaimLease es el ARRIENDO del reclamo del envío (D3): un proceso que reclamó un
// pedido y no lo entregó (murió, o el SMTP se colgó) lo suelta pasado este plazo y
// otro tick lo reintenta. Es mayor que cualquier envío sano y mucho menor que el
// vencimiento del enlace (30 días). Vive acá y no en el adaptador porque el
// barrido y sus dobles tienen que medir el MISMO plazo que la SQL.
const ClaimLease = 15 * time.Minute

// ReviewRequestRepository son los pedidos de reseña: el barrido que crea y
// reclama el envío (D3) y el enlace público (D4).
type ReviewRequestRepository interface {
	// Link resuelve un token vigente y sin usar. Unknown/expired/used =
	// ErrNotFound, el MISMO error para los tres.
	Link(tokenHash string, now time.Time) (ReviewLink, error)
	// Due devuelve los turnos atendidos de salones con reseñas activas que ya
	// pasaron su demora, no son más viejos que 14 días y NO tienen un pedido
	// entregado ni usado. Un pedido sin entregar vuelve a salir cuando su
	// arriendo ya venció (ClaimLease): es el reintento de un correo que falló O de
	// un proceso que se cayó entre el reclamo y el envío.
	Due(now time.Time, limit int) ([]DueReview, error)
	// Claim crea el pedido del turno o, si ya existe y es reclamable, le rota el
	// token y RENUEVA el arriendo (sent_at); devuelve el id del pedido y si este
	// proceso ganó. claimed=false = otro barredor lo tiene arrendado, ya se
	// entregó o ya se usó. Dos barredores simultáneos: exactamente UNO gana.
	Claim(req ReviewClaim) (requestID string, claimed bool, err error)
	// Unclaim devuelve el arriendo cuando el envío falló: sent_at vuelve a NULL y
	// el próximo tick lo reintenta sin esperar el vencimiento. Solo deshace SU
	// arriendo (sent_at = sentAt).
	Unclaim(requestID string, sentAt time.Time) error
	// MarkDelivered sella la ENTREGA después de que el correo salió bien. Es
	// condicional al arriendo que este proceso tiene (sent_at = sentAt): si otro
	// barredor ya lo reclamó, no toca nada. Sin esto el pedido vuelve a salir al
	// vencer el arriendo (a lo sumo un segundo correo, nunca cero).
	MarkDelivered(requestID string, sentAt time.Time) error
}

// PublicReviewReader es lo ÚNICO que la vidriera pública necesita de las
// reseñas (D7). Es un puerto de una sola función para que la sección sea
// aditiva: sin él, el payload público es el de hoy.
type PublicReviewReader interface {
	PublicSummary(salonID string) (PublicReviews, error)
}

// ===== F13: reseñas posvisita (fin) =====
