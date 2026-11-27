package app

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Dobles compartidos por los casos de uso que todavía viven en este paquete:
// el bus de eventos y el notificador. Salieron de notifications_test.go cuando
// los avisos pasaron a internal/notifications (los _test.go no se importan
// entre paquetes).
// fakeEvents guarda lo publicado: los casos de uso no saben que hay websockets.
type fakeEvents struct{ all []ports.Event }

func (f *fakeEvents) Publish(ev ports.Event) error { f.all = append(f.all, ev); return nil }

// typed cuenta los eventos de un tipo y devuelve el último.
func (f *fakeEvents) typed(t string) (ports.Event, int) {
	var last ports.Event
	n := 0
	for _, ev := range f.all {
		if ev.Type == t {
			last, n = ev, n+1
		}
	}
	return last, n
}

var _ ports.EventPublisher = (*fakeEvents)(nil)

// fakeNotifier registra los avisos que los casos de uso piden crear.
type fakeNotifier struct {
	calls  int
	bodies []string
}

func (f *fakeNotifier) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	f.calls++
	f.bodies = append(f.bodies, body)
	return domain.NewNotification(salonID, userID, title, body, kind)
}

// lastBody es el texto del último aviso: los tests de la hora local miran el
// texto que ve la persona, no el evento.
func (f *fakeNotifier) lastBody() string {
	if len(f.bodies) == 0 {
		return ""
	}
	return f.bodies[len(f.bodies)-1]
}

var _ Notifier = (*fakeNotifier)(nil)
