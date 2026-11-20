// Helpers de eventos y avisos compartidos por los casos de uso.
//
// Patrón Observer sin acoplar: la venta, la reserva y el cierre avisan "pasó
// esto en este salón" y no saben si atrás hay websockets, Redis o nadie.
// Ambos ganchos son OPCIONALES (nil = noop) para que los tests y las
// herramientas puedan usar los casos de uso sin infraestructura.
package app

import (
	"fmt"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Notifier es lo que necesitan los casos de uso para dejar un aviso persistido.
// Lo implementa NotificationService; nil significa "este entorno no avisa".
type Notifier interface {
	Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error)
}

// publish manda el evento al bus del salón. Best-effort a propósito: si el
// broadcast falla, la venta ya está cobrada y no se puede deshacer por un
// websocket caído (el error del canal no es el error del negocio).
func publish(events ports.EventPublisher, ev ports.Event) {
	if events == nil {
		return
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	_ = events.Publish(ev)
}

// notify deja el aviso persistido, si hay quién lo haga. Mismo criterio que
// publish: un aviso que no se pudo guardar no tumba la operación de negocio.
func notify(n Notifier, salonID, userID, title, body string, kind domain.NotificationKind) {
	if n == nil {
		return
	}
	_, _ = n.Create(salonID, userID, title, body, kind)
}

// FormatCents pasa centavos a un texto para MOSTRAR ($1.500,00). El dinero
// sigue siendo int en centavos en todo el sistema: esto es solo el texto de un
// aviso, nunca un valor que se vuelva a leer para calcular.
func FormatCents(cents int) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d,%02d", sign, cents/100, cents%100)
}
