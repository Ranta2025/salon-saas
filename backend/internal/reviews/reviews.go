// Package reviews: reseñas posvisita (F13 del contrato de la fase 6,
// decisiones D1..D7).
//
//   - Solo reseñas REALES (D1): una por turno ATENDIDO (`done`) de una clienta
//     con correo. Nunca se escriben desde el panel ni se importan. El módulo LEE
//     `bookings` por su propio adaptador de solo lectura y no toca reservas, POS,
//     caja, nómina ni la lógica de turnos.
//   - El enlace es un token de 32 bytes base64url del que se guarda SOLO el
//     SHA-256 (como F15), vence a los 30 días y sirve UNA vez (D4). Token
//     desconocido, vencido o usado son el MISMO 404 uniforme.
//   - Privacidad (D5): la reseña pública muestra nombre de pila + inicial del
//     apellido («Lía G.»), nunca correo, teléfono ni turno. El comentario se
//     muestra como TEXTO PLANO.
//   - Panel (D6): ver la lista y la configuración es de owner/admin/recepción;
//     guardar la configuración y ocultar/mostrar una reseña, solo owner/admin. El
//     permiso va ANTES de leer parámetros o cuerpo y los errores son 403 aunque
//     lo demás esté mal. La reseña se puede OCULTAR, nunca editar ni borrar.
//   - Lo que ve el cliente coincide con el panel (D7): una reseña oculta
//     desaparece de la web y del promedio, que se calcula sobre TODAS las
//     visibles.
//   - El pedido sale de un trabajo periódico (D3): turnos atendidos de salones con
//     reseñas activas, con correo, terminados entre `delay_hours` y 14 días atrás
//     y sin pedido enviado. UNIQUE(booking_id) lo hace idempotente y `sent_at` es
//     el reclamo del envío: si el correo falla se deshace el reclamo y el próximo
//     tick lo reintenta, nunca dos correos.
//
// Toda consulta del panel va scopeada por salon_id: la reseña de otro salón es
// 404 y no se escribe nada.
package reviews

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// Límites y valores del contrato.
const (
	// MaxCommentRunes es el tope del comentario en CARACTERES (D4): 500 runas,
	// no 500 bytes (un emoji cuenta uno).
	MaxCommentRunes = 500
	// MaxAuthorNameLen y MaxServiceNameLen son los CHECK de la migración 038 y lo
	// que se recorta del turno antes de guardar el snapshot (D2).
	MaxAuthorNameLen  = 120
	MaxServiceNameLen = 120
	// Demora del pedido en horas (D3): 1..72. Sin configurar, 24 (D6).
	MinDelayHours     = 1
	MaxDelayHours     = 72
	DefaultDelayHours = 24
	// Página del panel (D6): 20 por defecto, 1..100.
	DefaultLimit = 20
	MaxLimit     = 100
	// RequestTTL es la vigencia del enlace (D4): 30 días.
	RequestTTL = 30 * 24 * time.Hour
	// MaxDueAge es la antigüedad máxima de un turno para pedir reseña (D3): 14 días.
	MaxDueAge = 14 * 24 * time.Hour
	// DueBatch es el tamaño de página de cada tick del barrido.
	DueBatch = 50
	// MaxIDLen acota un id de ruta/URL antes de tocar la base.
	MaxIDLen = 100

	// tokenBytes son los 32 bytes de crypto/rand del token, como en F15.
	tokenBytes = 32
	// TokenLen es el largo de 32 bytes en base64url sin relleno.
	TokenLen = 43
	// maxCursorLen acota el cursor antes de decodificarlo: uno legítimo mide ~60.
	maxCursorLen = 300
)

// Textos de la API. El 404 del enlace es UNO solo (D4): desconocido, vencido y
// usado dicen lo mismo.
const (
	MsgLinkInvalid    = "Enlace no válido"
	MsgRatingInvalid  = "La calificación debe ser entre 1 y 5"
	MsgCommentTooLong = "El comentario no puede superar los 500 caracteres"
	MsgCommentInvalid = "El comentario tiene caracteres no válidos"
	MsgReviewNotFound = "Reseña no encontrada"
	// Los mismos textos de la auditoría para el límite y el cursor: es el mismo
	// cursor opaco y el mismo tope de página.
	MsgLimitRange    = "El limite debe ser entre 1 y 100"
	MsgCursorInvalid = "Cursor invalido"
	// D6: faltan claves del PUT de la configuración y rango de la demora.
	MsgSettingsMissing = "Faltan datos de las reseñas: activadas y demora"
	MsgDelayRange      = "La demora debe ser entre 1 y 72 horas"
)

// errLinkInvalid es el 404 uniforme del enlace (D4).
func errLinkInvalid() error { return domain.NewMessage(domain.ErrNotFound, MsgLinkInvalid) }

// Service es el caso de uso de las reseñas.
type Service struct {
	Reviews     ports.ReviewRepository
	Requests    ports.ReviewRequestRepository
	Memberships ports.MembershipRepository
	// Mailer nil = este entorno no manda correo: el barrido no reclama nada (no
	// se pide una reseña que nunca va a llegar).
	Mailer ports.MailSender
	// AppURL es la base del enlace del correo: "<APP_URL>/resena#<token>".
	AppURL string
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ReviewPage es una página del panel: los renglones y el cursor de la siguiente
// ("" = no hay más).
type ReviewPage struct {
	Items      []ports.Review
	NextCursor string
}

// --- Reglas puras (D4/D5/D6) ---------------------------------------------------

// ValidateRating exige un entero 1..5 (D4).
func ValidateRating(rating int) error {
	if rating < MinRating || rating > MaxRating {
		return domain.NewMessage(domain.ErrValidation, MsgRatingInvalid)
	}
	return nil
}

// MinRating y MaxRating son las estrellas del contrato (D4).
const (
	MinRating = 1
	MaxRating = 5
)

// ValidateComment valida y recorta el comentario (D4).
//
// Decisión: se rechazan el NUL, los controles C0 (menos "\n" y "\t", que son
// texto normal de un comentario), el DEL (0x7F), los controles C1 (0x80-0x9F) y
// los dos separadores de línea Unicode (U+2028/U+2029). Un carácter de control o
// un UTF-8 inválido llegaría a Postgres como un 500 (o guardaría basura): es un
// 400 con el texto de arriba. Después se recorta (espacios de los extremos) y se
// cuentan RUNAS: 500 entran, 501 no.
func ValidateComment(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", domain.NewMessage(domain.ErrValidation, MsgCommentInvalid)
	}
	for _, r := range raw {
		control := (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
		if control || r == '\u2028' || r == '\u2029' {
			return "", domain.NewMessage(domain.ErrValidation, MsgCommentInvalid)
		}
	}
	out := strings.TrimSpace(raw)
	if utf8.RuneCountInString(out) > MaxCommentRunes {
		return "", domain.NewMessage(domain.ErrValidation, MsgCommentTooLong)
	}
	return out, nil
}

// ValidateDelayHours valida la demora de la configuración (D6): 1..72 horas.
func ValidateDelayHours(hours int) error {
	if hours < MinDelayHours || hours > MaxDelayHours {
		return domain.NewMessage(domain.ErrValidation, MsgDelayRange)
	}
	return nil
}

// AuthorName aplica la regla de privacidad de D5: nombre de pila + inicial del
// apellido («Lía G.»). Nada más del turno: ni el apellido completo, ni el correo,
// ni el teléfono. Un nombre de una sola palabra sale tal cual y un nombre vacío
// sale vacío (la web no inventa un autor).
func AuthorName(clientName string) string {
	fields := strings.Fields(cleanText(clientName, MaxAuthorNameLen))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fields[0]
	}
	initial, _ := utf8.DecodeRuneInString(fields[1])
	if initial == utf8.RuneError {
		return fields[0]
	}
	return fields[0] + " " + string(initial) + "."
}

// ServiceName es el snapshot del servicio del turno (vacío si el turno no tenía).
func ServiceName(name string) string { return cleanText(name, MaxServiceNameLen) }

// cleanText quita los caracteres de control, normaliza el UTF-8 inválido y
// recorta a `max` runas: es lo que se guarda de un dato que viene de otra
// persona.
func cleanText(value string, max int) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if r := []rune(value); len(r) > max {
		value = string(r[:max])
	}
	return value
}

// CleanID dice si un id de la ruta es usable: no vacío, sin NUL, UTF-8 válido y
// de largo razonable. Un id que no puede existir es un recurso que no existe (la
// lección de F16: un NUL en un TEXT de Postgres es un 500).
func CleanID(id string) bool {
	return id != "" && len(id) <= MaxIDLen && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

// ParseQuery valida los parámetros del panel (D6): límite 1..100 (20 por
// defecto) y el cursor opaco. `before` y `limit` llegan como texto (vacío = no se
// pasó). El orden de las reglas es: límite y después cursor.
func ParseQuery(limit, before string) (ports.ReviewQuery, error) {
	q := ports.ReviewQuery{Limit: DefaultLimit}
	if limit != "" {
		n, ok := parseLimit(limit)
		if !ok {
			return ports.ReviewQuery{}, domain.NewMessage(domain.ErrValidation, MsgLimitRange)
		}
		q.Limit = n
	}
	if before != "" {
		c, err := DecodeCursor(before)
		if err != nil {
			return ports.ReviewQuery{}, err
		}
		q.Before = &c
	}
	return q, nil
}

// parseLimit acepta solo dígitos ASCII (sin signo ni espacios) de 1 a 100.
func parseLimit(s string) (int, bool) {
	if len(s) > 4 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > MaxLimit {
		return 0, false
	}
	return n, true
}

// EncodeCursor arma el cursor opaco del panel: base64url sin relleno de
// «<created_at RFC3339Nano UTC>|<id>». Es el MISMO formato del registro de
// auditoría (D6: «cursor opaco como la auditoría»).
func EncodeCursor(c ports.ReviewCursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor valida un cursor: solo acepta la forma que produce EncodeCursor.
// Todo lo demás es 400 con el texto del contrato.
func DecodeCursor(s string) (ports.ReviewCursor, error) {
	invalid := domain.NewMessage(domain.ErrValidation, MsgCursorInvalid)
	if s == "" || len(s) > maxCursorLen {
		return ports.ReviewCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ports.ReviewCursor{}, invalid
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !CleanID(id) || strings.TrimSpace(id) != id {
		return ports.ReviewCursor{}, invalid
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ports.ReviewCursor{}, invalid
	}
	c := ports.ReviewCursor{CreatedAt: at.UTC(), ID: id}
	if EncodeCursor(c) != s {
		return ports.ReviewCursor{}, invalid
	}
	return c, nil
}

// --- Token del enlace (D4) -----------------------------------------------------

// NewToken genera el token del enlace: 32 bytes de crypto/rand en base64url sin
// relleno (43 caracteres), igual que los tokens de F15.
func NewToken() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reseñas: generar el token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashToken es la ÚNICA forma de guardar/buscar el token: SHA-256 en hex (64),
// el mismo hash de los tokens de F15. El token en claro nunca se guarda.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidTokenShape dice si el valor PUEDE ser un token: exactamente 43 caracteres
// del alfabeto base64url. Se chequea ANTES de consultar la base, así un valor
// basura (vacío, largo, con NUL, con relleno) nunca llega a una query ni puede
// provocar un 500.
func ValidTokenShape(token string) bool {
	if len(token) != TokenLen {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// ReviewLinkURL arma el enlace del correo: <APP_URL>/resena#<token>. El token va
// en el FRAGMENTO (lo que sigue al "#" no se manda al servidor), como los otros
// enlaces con token del proyecto.
func ReviewLinkURL(appURL, token string) string {
	base := strings.TrimSpace(appURL)
	if base == "" {
		base = kernel.DefaultAppURL
	}
	return strings.TrimRight(base, "/") + "/resena#" + token
}

// --- Panel: lista y configuración (D6) -----------------------------------------

// RequireViewer exige membership ACTIVA y uno de los roles que ven el panel de
// reseñas: owner/admin/recepción (la misma regla del dinero y de la ficha de la
// clienta). Profesional, clienta y baja reciben 403.
func (s Service) RequireViewer(actor domain.Membership, salonID string) error {
	m, err := s.member(actor, salonID)
	if err != nil {
		return err
	}
	if !kernel.PuedeVender(m) {
		return domain.ErrForbidden
	}
	return nil
}

// RequireManager exige membership ACTIVA y owner/admin. El handler lo llama ANTES
// de leer el cuerpo: quien no puede guardar recibe 403 aunque el cuerpo esté mal.
func (s Service) RequireManager(actor domain.Membership, salonID string) error {
	_, err := s.manager(actor, salonID)
	return err
}

// List devuelve una página del panel, del más nuevo al más viejo. El permiso va
// ANTES de mirar los parámetros (403 aunque el límite esté mal). Pide limit+1
// filas para saber si hay otra página sin un COUNT.
func (s Service) List(actor domain.Membership, salonID, limit, before string) (ReviewPage, error) {
	if err := s.RequireViewer(actor, salonID); err != nil {
		return ReviewPage{}, err
	}
	q, err := ParseQuery(limit, before)
	if err != nil {
		return ReviewPage{}, err
	}
	page := q.Limit
	q.Limit = page + 1
	rows, err := s.Reviews.List(salonID, q)
	if err != nil {
		return ReviewPage{}, err
	}
	out := ReviewPage{Items: []ports.Review{}}
	if len(rows) > page {
		rows = rows[:page]
		last := rows[len(rows)-1]
		out.NextCursor = EncodeCursor(ports.ReviewCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	out.Items = append(out.Items, rows...)
	return out, nil
}

// Settings lee la configuración del salón. Nunca guardada = enabled:false y la
// demora por defecto (D6), sin fila y sin error.
func (s Service) Settings(actor domain.Membership, salonID string) (ports.ReviewSettings, error) {
	if err := s.RequireViewer(actor, salonID); err != nil {
		return ports.ReviewSettings{}, err
	}
	return s.settings(salonID)
}

// settings lee la configuración sin permisos: es el valor de fábrica del salón.
func (s Service) settings(salonID string) (ports.ReviewSettings, error) {
	st, err := s.Reviews.Settings(salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ports.ReviewSettings{SalonID: salonID, Enabled: false, DelayHours: DefaultDelayHours}, nil
		}
		return ports.ReviewSettings{}, err
	}
	st.Saved = true
	return st, nil
}

// SaveSettings REEMPLAZA la configuración (el handler exige las DOS claves). Solo
// owner/admin y la demora se valida antes de escribir.
func (s Service) SaveSettings(actor domain.Membership, salonID string, enabled bool, delayHours int) (ports.ReviewSettings, error) {
	if _, err := s.manager(actor, salonID); err != nil {
		return ports.ReviewSettings{}, err
	}
	if err := ValidateDelayHours(delayHours); err != nil {
		return ports.ReviewSettings{}, err
	}
	st := ports.ReviewSettings{SalonID: salonID, Enabled: enabled, DelayHours: delayHours, Saved: true}
	if err := s.Reviews.SaveSettings(st); err != nil {
		return ports.ReviewSettings{}, err
	}
	return st, nil
}

// Hide oculta o muestra UNA reseña del salón. Solo owner/admin; el id se valida
// antes de la base (un id que no puede existir es «Reseña no encontrada», nunca
// un 500). Idempotente: ocultar dos veces responde lo mismo.
func (s Service) Hide(actor domain.Membership, salonID, reviewID string, hidden bool) error {
	if err := s.RequireManager(actor, salonID); err != nil {
		return err
	}
	if !CleanID(reviewID) {
		return domain.NewMessage(domain.ErrNotFound, MsgReviewNotFound)
	}
	if err := s.Reviews.SetHidden(salonID, reviewID, hidden); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewMessage(domain.ErrNotFound, MsgReviewNotFound)
		}
		return err
	}
	return nil
}

// --- Enlace público y canje (D4) -----------------------------------------------

// Link resuelve el enlace de una reseña: el salón, el servicio y el turno. Un
// token con forma inválida NO consulta la base (mismo 404 uniforme).
func (s Service) Link(token string) (ports.ReviewLink, error) {
	if !ValidTokenShape(token) {
		return ports.ReviewLink{}, errLinkInvalid()
	}
	link, err := s.Requests.Link(HashToken(token), s.now())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ports.ReviewLink{}, errLinkInvalid()
		}
		return ports.ReviewLink{}, err
	}
	return link, nil
}

// Submit canjea el enlace y crea la reseña. El orden importa: primero se valida
// lo que escribió la persona (un comentario de 501 caracteres o un rating 0 NO
// consumen el enlace) y recién después se canjea, en UNA transacción del
// adaptador (segundo POST simultáneo = 404 uniforme y una sola reseña).
func (s Service) Submit(token string, rating int, comment string) (ports.Review, error) {
	if !ValidTokenShape(token) {
		return ports.Review{}, errLinkInvalid()
	}
	if err := ValidateRating(rating); err != nil {
		return ports.Review{}, err
	}
	clean, err := ValidateComment(comment)
	if err != nil {
		return ports.Review{}, err
	}
	r, err := s.Reviews.Submit(ports.ReviewSubmit{
		TokenHash: HashToken(token), Rating: rating, Comment: clean, Now: s.now(),
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ports.Review{}, errLinkInvalid()
		}
		return ports.Review{}, err
	}
	return r, nil
}

// --- Vidriera pública (D7) ------------------------------------------------------

// PublicSummary arma la sección `reviews` de la web pública: el promedio con UNA
// decimal (redondeado acá, sobre TODAS las visibles), el total y los 6 renglones
// más recientes. Sin reseñas visibles Count = 0 y la sección no se muestra. Un
// fallo de la consulta se propaga con el error: quien llama decide degradar.
func (s Service) PublicSummary(salonID string) (ports.PublicReviews, error) {
	sum, err := s.Reviews.Visible(salonID)
	if err != nil {
		return ports.PublicReviews{}, err
	}
	if sum.Count < 0 {
		sum.Count = 0
	}
	if sum.Count == 0 {
		return ports.PublicReviews{Items: []ports.PublicReview{}}, nil
	}
	// Una decimal, sin floats raros: 4.555 -> 4.6; el frontend redondea igual.
	sum.Average = math.Round(sum.Average*10) / 10
	if sum.Average < MinRating {
		sum.Average = MinRating
	}
	if sum.Average > MaxRating {
		sum.Average = MaxRating
	}
	if sum.Items == nil {
		sum.Items = []ports.PublicReview{}
	}
	if len(sum.Items) > MaxPublicReviews {
		sum.Items = sum.Items[:MaxPublicReviews]
	}
	return sum, nil
}

// MaxPublicReviews son las reseñas que muestra la landing (D7): las 6 más nuevas.
const MaxPublicReviews = 6

// --- Pedido periódico (D3) -------------------------------------------------------

// RunOnce es UNA pasada del barrido: busca los turnos atendidos que ya pueden
// pedir reseña, crea el pedido y manda el correo. Devuelve cuántos se enviaron y
// cuántos fallaron (el tick los reintenta).
//
// El reclamo es un ARRIENDO (D3), no un candado eterno: dos barredores (dos
// réplicas, o un tick que se solapa) pueden ver el MISMO turno; el UNIQUE por
// turno y la condición de «reclamable» del adaptador dejan que uno solo mande el
// correo. Después del envío bueno se sella la ENTREGA (MarkDelivered). Si el
// envío falla se deshace el arriendo (Unclaim) y el próximo tick lo reintenta con
// un token nuevo. Si el proceso muere entre el reclamo y el envío, el arriendo
// vence a los 15 minutos y otro tick lo reintenta: como solo se guarda el hash,
// el token del intento muerto no se puede reenviar. El resultado es
// AL-MENOS-UNA-VEZ: en el peor caso salen dos correos con tokens distintos (solo
// sirve el del ÚLTIMO: el reintento rota el token) y el UNIQUE(booking_id) de `reviews` sigue dejando UNA sola
// reseña. Un pedido entregado no vuelve a salir nunca.
func (s Service) RunOnce(ctx context.Context, now time.Time) (sent, failed int) {
	if s.Mailer == nil || s.Requests == nil {
		return 0, 0
	}
	now = now.UTC()
	due, err := s.Requests.Due(now, DueBatch)
	if err != nil {
		return 0, 1
	}
	for _, d := range due {
		if ctx.Err() != nil {
			break
		}
		if err := s.requestOne(ctx, d, now); err != nil {
			failed++
			continue
		}
		sent++
	}
	return sent, failed
}

// requestOne crea/reutiliza el pedido, reclama el envío y manda UN correo.
func (s Service) requestOne(ctx context.Context, d ports.DueReview, now time.Time) error {
	token, err := NewToken()
	if err != nil {
		return err
	}
	requestID, claimed, err := s.Requests.Claim(ports.ReviewClaim{
		ID:        domain.NewID(),
		SalonID:   d.SalonID,
		BookingID: d.BookingID,
		TokenHash: HashToken(token),
		Now:       now,
		ExpiresAt: now.Add(RequestTTL),
	})
	if err != nil {
		return err
	}
	if !claimed {
		// Otro barredor ya lo tiene o ya se envió: este tick no manda nada.
		return nil
	}
	if s.Mailer == nil {
		return nil
	}
	if err := s.Mailer.Send(ctx, reviewRequestMail(d, ReviewLinkURL(s.AppURL, token))); err != nil {
		// El correo no salió: se deshace el arriendo y el próximo tick reintenta.
		_ = s.Requests.Unclaim(requestID, now)
		return err
	}
	// El correo salió: se sella la entrega para que este turno no vuelva a salir.
	// Si este UPDATE falla, el correo YA se mandó y el arriendo vence en 15
	// minutos: a lo sumo saldrá un segundo correo con otro token (solo sirve el último
	// enlace y la reseña sigue siendo una sola). Se cuenta como fallo para que
	// quede en el log.
	return s.Requests.MarkDelivered(requestID, now)
}

// --- Auxiliares -------------------------------------------------------------------

// member valida que el actor sea miembro ACTIVO del salón (no confía en el rol
// que traiga el token).
func (s Service) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// manager exige membership activa y permiso de configuración del salón.
func (s Service) manager(actor domain.Membership, salonID string) (domain.Membership, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return domain.Membership{}, err
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}
