// Package kernel son las reglas TRANSVERSALES de los casos de uso: quién puede
// hacer qué, la configuración del salón con sus valores de fábrica, los avisos
// best-effort y los eventos del salón.
//
// Vive en un paquete compartido porque lo usan todas las funcionalidades: con el
// código ordenado por funcionalidad (`internal/<feature>/`) cada módulo importa
// el núcleo, nunca al revés. No conoce HTTP ni SQL: solo domain y ports.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// --- Permisos: quién puede hacer qué ---------------------------------------

// PuedeVender es el permiso del POS: el dinero del salón no es de la persona.
// Owner/admin y recepción venden; la profesional NO (solo ve su agenda).
// Se mantiene explícito y en un solo lugar para que el día que cambie, cambie
// una función.
func PuedeVender(m domain.Membership) bool {
	return m.CanManage() || m.Role == domain.RoleReception
}

// PuedeVerPlata: el dinero (ventas, cierres de caja, revenue del panel) es
// información sensible del negocio. Solo la ve quien administra o recepción.
// Un profesional o un cliente del salón ven la agenda y el catálogo, no la
// facturación.
func PuedeVerPlata(m domain.Membership) bool {
	return m.CanManage() || m.Role == domain.RoleReception
}

// ManageActor valida que el actor sea owner/admin del salón contra la base (no
// confía en el rol del token): mira la membership ACTIVA. Cualquier otro caso es
// 403.
func ManageActor(memberships ports.MembershipRepository, actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	if !m.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// --- Configuración del salón -------------------------------------------------

// LoadSalonSettings lee la configuración del salón aplicando los valores de
// fábrica cuando no hay fila (un salón recién creado se comporta igual que uno
// configurado). Sin repositorio (tests, herramientas) también es de fábrica.
func LoadSalonSettings(repo ports.SalonSettingsRepository, salonID string) (domain.SalonSettings, error) {
	if repo == nil {
		return domain.DefaultSalonSettings(salonID), nil
	}
	settings, err := repo.Settings(salonID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DefaultSalonSettings(salonID), nil
	}
	if err != nil {
		return domain.SalonSettings{}, err
	}
	return settings, nil
}

// SalonLocation resuelve la zona horaria del salón con los defaults de fábrica
// cuando no hay fila de configuración. Es la MISMA lectura que usa la nómina y
// el reporte de ganancia: no hay una segunda regla de «hoy».
func SalonLocation(repo ports.SalonSettingsRepository, salonID string) (*time.Location, error) {
	settings, err := LoadSalonSettings(repo, salonID)
	if err != nil {
		return nil, err
	}
	return settings.Location()
}

// --- Equipo ---------------------------------------------------------------------

// EnsureProfessional valida a quien atiende una venta, una reserva o un uso de
// premio: un id que no existe en el salón es un error de validación (400), no un
// 404: no se confirma ni se niega la existencia de usuarios de otros salones.
func EnsureProfessional(memberships ports.MembershipRepository, salonID, professionalID string) error {
	professionalID = strings.TrimSpace(professionalID)
	if professionalID == "" {
		return fmt.Errorf("%w: el profesional es obligatorio", domain.ErrValidation)
	}
	m, err := memberships.Find(professionalID, salonID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: el profesional no pertenece al salón", domain.ErrValidation)
		}
		return err
	}
	// Quién atiende lo decide el dominio (profesional siempre; dueño/administración
	// solo con la marca attends): el rol solo ya no alcanza (F2a).
	if !m.Bookable() {
		return fmt.Errorf("%w: el profesional elegido no atiende en el salón", domain.ErrValidation)
	}
	return nil
}

// --- Clientes ------------------------------------------------------------------

// ClientError traduce la falta de la ficha al mensaje del contrato F5: una ficha
// de otro salón es «Ficha no encontrada» (404), nunca un 500 ni un 403 que
// confirme que existe.
func ClientError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NewMessage(domain.ErrNotFound, domain.MsgClientNotFound)
	}
	return err
}

// --- Eventos y avisos ----------------------------------------------------------

// Notifier es lo que necesitan los casos de uso para dejar un aviso persistido.
// Lo implementa NotificationService; nil significa "este entorno no avisa".
type Notifier interface {
	Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error)
}

// Publish manda el evento al bus del salón. Best-effort a propósito: si el
// broadcast falla, la venta ya está cobrada y no se puede deshacer por un
// websocket caído (el error del canal no es el error del negocio).
func Publish(events ports.EventPublisher, ev ports.Event) {
	if events == nil {
		return
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	_ = events.Publish(ev)
}

// Notify deja el aviso persistido, si hay quién lo haga. Mismo criterio que
// Publish: un aviso que no se pudo guardar no tumba la operación de negocio.
func Notify(n Notifier, salonID, userID, title, body string, kind domain.NotificationKind) {
	if n == nil {
		return
	}
	_, _ = n.Create(salonID, userID, title, body, kind)
}

// AuditRecorder es lo MÍNIMO que necesitan otros casos de uso (2FA) para dejar un
// evento de auditoría: lo implementa el módulo audit. Con la interfaz acá, el
// módulo audit puede vivir aparte sin que el resto dependa de él.
type AuditRecorder interface {
	// Enabled dice si hay registro configurado (sin repositorio no hay auditoría).
	Enabled() bool
	// Record deja el evento; un error del registro no tumba la operación.
	Record(salonID, actorUserID, action, entityID string) error
}

// --- Enlaces y fechas ---------------------------------------------------------

// DefaultAppURL es la base de los enlaces que van en un correo cuando APP_URL no
// está configurada (cmd/api siempre la define; esto es el respaldo para los
// tests y para un caso de uso usado desde otro binario).
const DefaultAppURL = "http://localhost:8081"

// DateLayout es el formato del DÍA LOCAL del salón ("YYYY-MM-DD"): sin hora y
// sin zona, porque el día de una cita es del calendario del salón.
const DateLayout = "2006-01-02"

// --- Ventanas de tiempo y textos de aviso ---------------------------------------

// LocalMinutes es el minuto del día LOCAL (09:00 = 540) de un instante, en la
// zona del salón. Sirve para comparar un turno contra las ventanas del día.
func LocalMinutes(t time.Time, loc *time.Location) int {
	local := t.In(loc)
	return local.Hour()*60 + local.Minute()
}

// FormatSalonTime pasa un instante al texto de un aviso en la hora LOCAL del
// salón y nombra la zona: la hora UTC no la entiende nadie del mostrador.
//
// Es solo texto: el evento del websocket sigue llevando el instante en UTC
// RFC3339, que es el valor que las pantallas formatean por su cuenta.
func FormatSalonTime(settings domain.SalonSettings, t time.Time) string {
	loc, err := settings.Location()
	if err != nil {
		// Configuración ilegible: se dice UTC explícitamente en vez de mostrar
		// una hora sin referencia.
		return t.UTC().Format("02/01 15:04") + " (UTC)"
	}
	return t.In(loc).Format("02/01 15:04") + " (" + loc.String() + ")"
}

// SalonTimeLabel arma ese texto con la configuración del salón. Si la
// configuración no se puede leer, el aviso sale igual (en UTC): un aviso que se
// pierde por una fila de settings es peor que una hora sin zona.
func SalonTimeLabel(repo ports.SalonSettingsRepository, salonID string, t time.Time) string {
	settings, err := LoadSalonSettings(repo, salonID)
	if err != nil {
		settings = domain.SalonSettings{SalonID: salonID, Timezone: "UTC"}
	}
	return FormatSalonTime(settings, t)
}

// FormatCents pasa centavos a un texto para MOSTRAR ($1.500,00). El dinero sigue
// siendo int en centavos en todo el sistema: esto es solo el texto de un aviso,
// nunca un valor que se vuelva a leer para calcular.
func FormatCents(cents int) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d,%02d", sign, cents/100, cents%100)
}

// --- Días del calendario ---------------------------------------------------

// OccupancyDayMinutes es la jornada de referencia para la ocupación del panel.
const OccupancyDayMinutes = 12 * 60

// StartOfUTCDay es la medianoche UTC del día de t (el calendario trabaja en UTC).
func StartOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// EndOfUTCDay es la medianoche UTC del día siguiente.
func EndOfUTCDay(t time.Time) time.Time { return StartOfUTCDay(t).Add(24 * time.Hour) }

// StartOfSalonDay es la medianoche local de la fecha de t en la zona del salón.
// Si loc es nil, cae al día UTC (compatibilidad y tests sin configuración).
func StartOfSalonDay(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		return StartOfUTCDay(t)
	}
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// --- Trabajos asíncronos -------------------------------------------------------

// AsyncJobTimeout es el techo de un trabajo asíncrono (buscar, emitir, enviar).
const AsyncJobTimeout = 15 * time.Second

// AsyncRunner corre un trabajo FUERA del pedido. En producción es GoAsync (una
// goroutine con timeout y recover); los tests inyectan uno que registra y
// difiere, o uno que corre en línea.
type AsyncRunner func(job func(ctx context.Context))

// GoAsync es el runner de producción: una goroutine por trabajo, con su propio
// timeout y con recover (un pánico del adaptador no puede tumbar el proceso desde
// una goroutine suelta). Es el mismo patrón de la confirmación de reserva.
func GoAsync(timeout time.Duration) AsyncRunner {
	if timeout <= 0 {
		timeout = AsyncJobTimeout
	}
	return func(job func(ctx context.Context)) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("cuenta: pánico en un trabajo asíncrono: %v", r)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			job(ctx)
		}()
	}
}

// RunAsync usa el runner inyectado o, si no hay, el de producción.
func RunAsync(run AsyncRunner, job func(ctx context.Context)) {
	if run == nil {
		run = GoAsync(AsyncJobTimeout)
	}
	run(job)
}
