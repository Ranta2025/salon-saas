package app

import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeNotifications imita a pgstore.Notifications: scoping por salón, avisos
// para todo el salón (user_id "") y ErrNotFound cuando el aviso no es del
// salón/usuario que pregunta.
type fakeNotifications struct {
	all  []domain.Notification
	last int // último limit recibido en List
}

func (f *fakeNotifications) Save(n domain.Notification) error {
	f.all = append(f.all, n)
	return nil
}

func (f *fakeNotifications) visible(n domain.Notification, salonID, userID string) bool {
	return n.SalonID == salonID && (n.UserID == "" || n.UserID == userID)
}

func (f *fakeNotifications) List(salonID, userID string, limit int) ([]domain.Notification, error) {
	f.last = limit
	var out []domain.Notification
	for i := len(f.all) - 1; i >= 0; i-- { // más nuevas primero
		if f.visible(f.all[i], salonID, userID) {
			out = append(out, f.all[i])
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeNotifications) MarkRead(salonID, userID, id string) error {
	for i := range f.all {
		if f.all[i].ID == id && f.visible(f.all[i], salonID, userID) {
			f.all[i].MarkRead(time.Now().UTC())
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeNotifications) UnreadCount(salonID, userID string) (int, error) {
	n := 0
	for _, x := range f.all {
		if f.visible(x, salonID, userID) && !x.Read() {
			n++
		}
	}
	return n, nil
}

var _ ports.NotificationRepository = (*fakeNotifications)(nil)

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

func notificationRecep(m *fakeMemberships, salonID string) domain.Membership {
	return miembro(m, "u-recep", salonID, domain.RoleReception)
}

func TestNotificationService_create_persiste_y_publica_notify(t *testing.T) {
	reps := &fakeNotifications{}
	events := &fakeEvents{}
	svc := NotificationService{Notifications: reps, Salons: &fakeMemberships{}, Events: events}

	n, err := svc.Create("s1", "", "Cierre de caja", "faltante de $200", domain.KindCash)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(reps.all) != 1 || reps.all[0].ID != n.ID {
		t.Fatalf("no persistió el aviso: %+v", reps.all)
	}
	if n.Read() {
		t.Fatal("un aviso nuevo nace no leído")
	}
	ev, count := events.typed("notify")
	if count != 1 {
		t.Fatalf("esperaba 1 evento notify, dio %d (%+v)", count, events.all)
	}
	if ev.SalonID != "s1" || ev.TS.IsZero() {
		t.Fatalf("evento notify sin salón/ts: %+v", ev)
	}
	payload := NotificationPayload(n)
	if payload["id"] != n.ID || payload["title"] != "Cierre de caja" || payload["read"] != false {
		t.Fatalf("payload del evento: %+v", payload)
	}
}

func TestNotificationService_create_invalido_no_persiste(t *testing.T) {
	reps := &fakeNotifications{}
	events := &fakeEvents{}
	svc := NotificationService{Notifications: reps, Salons: &fakeMemberships{}, Events: events}

	if _, err := svc.Create("s1", "", "  ", "", domain.KindInfo); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("título vacío debe dar ErrValidation, dio %v", err)
	}
	if len(reps.all) != 0 || len(events.all) != 0 {
		t.Fatalf("un aviso inválido no debe persistirse ni publicarse: %+v / %+v", reps.all, events.all)
	}
}

// Sin publisher (tests, herramientas) crear un aviso no puede romper.
func TestNotificationService_create_sin_publisher(t *testing.T) {
	reps := &fakeNotifications{}
	svc := NotificationService{Notifications: reps, Salons: &fakeMemberships{}}
	if _, err := svc.Create("s1", "", "Aviso", "", domain.KindInfo); err != nil {
		t.Fatalf("sin publisher debe funcionar igual: %v", err)
	}
}

func TestNotificationService_list_scoped_por_salon(t *testing.T) {
	reps := &fakeNotifications{}
	mems := &fakeMemberships{}
	svc := NotificationService{Notifications: reps, Salons: mems}
	recep := notificationRecep(mems, "s1")
	_ = miembro(mems, "u-otro", "s2", domain.RoleOwner)

	// Un aviso para todo s1, uno para la recepcionista de s1 y uno de otro salón.
	wide, _ := domain.NewNotification("s1", "", "Para todos", "", domain.KindInfo)
	own, _ := domain.NewNotification("s1", "u-recep", "Para vos", "", domain.KindBooking)
	foreign, _ := domain.NewNotification("s2", "", "Otro salón", "", domain.KindInfo)
	for _, n := range []domain.Notification{wide, own, foreign} {
		if err := reps.Save(n); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	list, err := svc.List(recep, "s1", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba los 2 avisos de s1 (salón + propios), dio %d: %+v", len(list), list)
	}
	if reps.last != defaultNotificationLimit {
		t.Fatalf("limit 0 debe usar el default %d, dio %d", defaultNotificationLimit, reps.last)
	}
	if _, err := svc.List(recep, "s1", 10000); err != nil {
		t.Fatalf("limit grande: %v", err)
	}
	if reps.last != maxNotificationLimit {
		t.Fatalf("limit grande debe recortarse a %d, dio %d", maxNotificationLimit, reps.last)
	}
	// Un miembro de otro salón no puede leer s1 (403), ni siquiera el owner.
	ajeno := miembro(mems, "u-otro", "s2", domain.RoleOwner)
	if _, err := svc.List(ajeno, "s1", 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón debe dar Forbidden, dio %v", err)
	}
}

func TestNotificationService_markread_solo_del_salon(t *testing.T) {
	reps := &fakeNotifications{}
	mems := &fakeMemberships{}
	svc := NotificationService{Notifications: reps, Salons: mems}
	recep := notificationRecep(mems, "s1")
	_ = miembro(mems, "u-otro", "s2", domain.RoleOwner)

	wide, _ := domain.NewNotification("s1", "", "Para todos", "", domain.KindInfo)
	own, _ := domain.NewNotification("s1", "u-recep", "Para vos", "", domain.KindBooking)
	foreign, _ := domain.NewNotification("s2", "", "Otro salón", "", domain.KindInfo)
	for _, n := range []domain.Notification{wide, own, foreign} {
		_ = reps.Save(n)
	}

	if err := svc.MarkRead(recep, wide.ID); err != nil {
		t.Fatalf("marcar un aviso de todo el salón: %v", err)
	}
	if err := svc.MarkRead(recep, own.ID); err != nil {
		t.Fatalf("marcar un aviso propio: %v", err)
	}
	// Idempotente: marcar de nuevo no es un error.
	if err := svc.MarkRead(recep, own.ID); err != nil {
		t.Fatalf("marcar dos veces: %v", err)
	}
	// Aviso de otro salón: no existe para esta persona (404, sin filtrar).
	if err := svc.MarkRead(recep, foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("aviso de otro salón debe dar NotFound, dio %v", err)
	}
	// Aviso dirigido a otra persona del mismo salón: tampoco.
	_ = miembro(mems, "u-prof", "s1", domain.RoleProfessional)
	personal, _ := domain.NewNotification("s1", "u-prof", "Solo para el profe", "", domain.KindInfo)
	_ = reps.Save(personal)
	if err := svc.MarkRead(recep, personal.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("aviso de otra persona debe dar NotFound, dio %v", err)
	}
	// Sin usuario autenticado: Unauthorized.
	if err := svc.MarkRead(domain.Membership{}, wide.ID); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("sin usuario debe dar Unauthorized, dio %v", err)
	}
}

func TestNotificationService_unread_count(t *testing.T) {
	reps := &fakeNotifications{}
	mems := &fakeMemberships{}
	svc := NotificationService{Notifications: reps, Salons: mems}
	recep := notificationRecep(mems, "s1")
	for _, title := range []string{"uno", "dos"} {
		n, _ := domain.NewNotification("s1", "", title, "", domain.KindInfo)
		_ = reps.Save(n)
	}
	read, _ := domain.NewNotification("s1", "", "leído", "", domain.KindInfo)
	read.MarkRead(time.Now().UTC())
	_ = reps.Save(read)

	n, err := svc.UnreadCount(recep, "s1")
	if err != nil || n != 2 {
		t.Fatalf("no leídas: %d %v (esperaba 2)", n, err)
	}
	ajeno := miembro(mems, "u-otro", "s2", domain.RoleOwner)
	if _, err := svc.UnreadCount(ajeno, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón debe dar Forbidden, dio %v", err)
	}
}
