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
	"salon-saas/internal/kernel"
	"salon-saas/internal/ports"
)

// Notifier es el aviso persistido que piden los casos de uso (vive en el
// núcleo: kernel.Notifier). Alias para no tocar a quien ya lo nombra acá.
type Notifier = kernel.Notifier

// publish manda el evento al bus del salón. Best-effort a propósito: si el
// broadcast falla, la venta ya está cobrada y no se puede deshacer por un
// websocket caído (el error del canal no es el error del negocio).

// notify deja el aviso persistido, si hay quién lo haga. Mismo criterio que
// publish: un aviso que no se pudo guardar no tumba la operación de negocio.

// FormatSalonTime pasa un instante al texto de un aviso en la hora LOCAL del
// salón y nombra la zona. Sin esto, un turno de las 11:00 en CDMX se lee "17:00"
// en la bandeja (E-04): la hora UTC no la entiende nadie del mostrador.
//
// Es solo texto: el evento del websocket sigue llevando starts_at en UTC
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

// salonTimeLabel arma ese texto con la configuración del salón. Si la
// configuración no se puede leer, el aviso sale igual (en UTC): un aviso que se
// pierde por una fila de settings es peor que una hora con la zona a la vista.
func salonTimeLabel(repo ports.SalonSettingsRepository, salonID string, t time.Time) string {
	settings, err := kernel.LoadSalonSettings(repo, salonID)
	if err != nil {
		settings = domain.SalonSettings{SalonID: salonID, Timezone: "UTC"}
	}
	return FormatSalonTime(settings, t)
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
