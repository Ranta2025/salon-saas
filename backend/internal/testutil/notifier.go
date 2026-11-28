package testutil

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/kernel"
)

// FakeNotifier registra los avisos que los casos de uso piden crear. Es la
// copia exportada del fake de los tests de app (los _test.go no se importan
// entre paquetes).
type FakeNotifier struct {
	Calls  int
	Bodies []string
}

func (f *FakeNotifier) Create(salonID, userID, title, body string, kind domain.NotificationKind) (domain.Notification, error) {
	f.Calls++
	f.Bodies = append(f.Bodies, body)
	return domain.NewNotification(salonID, userID, title, body, kind)
}

// LastBody es el texto del último aviso: los tests de la hora local miran el
// texto que ve la persona, no el evento.
func (f *FakeNotifier) LastBody() string {
	if len(f.Bodies) == 0 {
		return ""
	}
	return f.Bodies[len(f.Bodies)-1]
}

var _ kernel.Notifier = (*FakeNotifier)(nil)
