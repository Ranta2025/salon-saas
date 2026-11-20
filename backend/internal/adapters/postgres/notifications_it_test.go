// Notificaciones contra Postgres real (SALON_IT=1). Verifica el scoping por
// salón y el "aviso para todo el salón" (user_id NULL) contra la DB de verdad.
// Corre con: SALON_IT=1 go test ./internal/adapters/postgres/ -run TestNotifications -v
package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func seedTenant(t *testing.T, salonID, userID string) {
	t.Helper()
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO users(id,name,email,pass_hash) VALUES($1,'Usuario IT',$2,'x')
		 ON CONFLICT (id) DO NOTHING`, userID, userID+"@test.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO salons(id,slug,name) VALUES($1,$1,'Salón IT') ON CONFLICT (id) DO NOTHING`, salonID)
	if err != nil {
		t.Fatalf("seed salon: %v", err)
	}
}

func TestNotifications_scope_y_lectura(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()

	seedTenant(t, "s-notif", "u-notif")
	seedTenant(t, "s-notif2", "u-notif2")
	_, _ = pool.Exec(ctx, `DELETE FROM notifications WHERE salon_id IN ('s-notif','s-notif2')`)

	repo := Notifications{Pool: pool}
	newNotif := func(salonID, userID, title string) domain.Notification {
		t.Helper()
		n, err := domain.NewNotification(salonID, userID, title, "cuerpo", domain.KindInfo)
		if err != nil {
			t.Fatalf("dominio: %v", err)
		}
		return n
	}
	wide := newNotif("s-notif", "", "Para todo el salón")    // user_id NULL en la DB
	own := newNotif("s-notif", "u-notif", "Para mí")         // dirigido
	other := newNotif("s-notif", "u-notif2", "Para el otro") // otra persona, mismo salón
	foreign := newNotif("s-notif2", "", "Otro salón")        // otro tenant
	for _, n := range []domain.Notification{wide, own, other, foreign} {
		if err := repo.Save(n); err != nil {
			t.Fatalf("save %q: %v", n.Title, err)
		}
	}

	// List: lo del salón + lo propio, nada del otro salón ni de otra persona.
	list, err := repo.List("s-notif", "u-notif", 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("esperaba 2 avisos visibles, dio %d: %+v", len(list), list)
	}
	for _, n := range list {
		if n.SalonID != "s-notif" {
			t.Fatalf("se coló un aviso de otro salón: %+v", n)
		}
		if n.UserID != "" && n.UserID != "u-notif" {
			t.Fatalf("se coló un aviso de otra persona: %+v", n)
		}
		if n.Read() || n.ReadAt != nil {
			t.Fatalf("los avisos recién creados van no leídos: %+v", n)
		}
	}
	// El limit se respeta.
	if one, err := repo.List("s-notif", "u-notif", 1); err != nil || len(one) != 1 {
		t.Fatalf("limit 1: %d %v", len(one), err)
	}

	// UnreadCount cuenta solo lo visible.
	if n, err := repo.UnreadCount("s-notif", "u-notif"); err != nil || n != 2 {
		t.Fatalf("no leídas: %d %v (esperaba 2)", n, err)
	}

	// MarkRead del aviso del salón (user_id NULL) y del propio.
	if err := repo.MarkRead("s-notif", "u-notif", wide.ID); err != nil {
		t.Fatalf("markread del aviso del salón: %v", err)
	}
	if err := repo.MarkRead("s-notif", "u-notif", own.ID); err != nil {
		t.Fatalf("markread del propio: %v", err)
	}
	if err := repo.MarkRead("s-notif", "u-notif", own.ID); err != nil {
		t.Fatalf("marcar dos veces debe ser idempotente: %v", err)
	}
	if n, err := repo.UnreadCount("s-notif", "u-notif"); err != nil || n != 0 {
		t.Fatalf("después de leer: %d %v (esperaba 0)", n, err)
	}
	// La marca quedó persistida.
	list, _ = repo.List("s-notif", "u-notif", 50)
	for _, n := range list {
		if !n.Read() || n.ReadAt == nil {
			t.Fatalf("la marca no se persistió: %+v", n)
		}
		if !n.CreatedAt.Before(time.Now().Add(time.Minute)) {
			t.Fatalf("created_at raro: %v", n.CreatedAt)
		}
	}

	// Aviso de otra persona del mismo salón: no existe para mí.
	if err := repo.MarkRead("s-notif", "u-notif", other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("aviso de otra persona debe dar NotFound, dio %v", err)
	}
	// Aviso de otro salón: tampoco (ni siquiera se puede marcar por id).
	if err := repo.MarkRead("s-notif", "u-notif", foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("aviso de otro salón debe dar NotFound, dio %v", err)
	}
	// Id inexistente.
	if err := repo.MarkRead("s-notif", "u-notif", "no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("id inexistente debe dar NotFound, dio %v", err)
	}
}

// El índice CHECK de kind y la FK del salón son la última barrera.
func TestNotifications_db_rechaza_kind_y_salon_invalidos(t *testing.T) {
	pool := itPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := Notifications{Pool: pool}

	bad := domain.Notification{ID: domain.NewID(), SalonID: "s-notif", Title: "x", Kind: "push"}
	if err := repo.Save(bad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("kind inválido debe dar ErrValidation, dio %v", err)
	}
	noSalon := domain.Notification{ID: domain.NewID(), SalonID: "no-existe", Title: "x", Kind: domain.KindInfo}
	if err := repo.Save(noSalon); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("salón inexistente debe dar ErrValidation, dio %v", err)
	}
	_ = ctx
}
