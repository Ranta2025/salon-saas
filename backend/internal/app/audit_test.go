package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

type auditEnv struct {
	svc   AuditService
	mems  *fakeMemberships
	users *fakeUsers
	repo  *fakeAudit
	clock time.Time

	owner, admin, recep, prof, client, other, removed domain.Membership
}

func newAuditEnv(t *testing.T) *auditEnv {
	t.Helper()
	e := &auditEnv{
		mems: &fakeMemberships{}, users: &fakeUsers{}, repo: &fakeAudit{},
		clock: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	e.svc = AuditService{Events: e.repo, Users: e.users, Memberships: e.mems, Now: func() time.Time { return e.clock }}
	e.owner = miembro(e.mems, "owner", "s1", domain.RoleOwner)
	e.admin = miembro(e.mems, "admin", "s1", domain.RoleAdmin)
	e.recep = miembro(e.mems, "recep", "s1", domain.RoleReception)
	e.prof = miembro(e.mems, "prof", "s1", domain.RoleProfessional)
	e.client = miembro(e.mems, "client", "s1", domain.RoleClient)
	e.other = miembro(e.mems, "owner2", "s2", domain.RoleOwner)
	e.removed = miembro(e.mems, "removed", "s1", domain.RoleAdmin)
	at := e.clock.Add(-time.Hour)
	e.removed.EndedAt = &at
	e.mems.byKey["removed|s1"] = e.removed
	_ = e.users.Save(domain.User{ID: "owner", Name: "Olga Dueña", Email: "olga@mail.com"})
	return e
}

// seed escribe n eventos del salón, uno por minuto hacia atrás desde el reloj.
func (e *auditEnv) seed(salon string, n int, action string) {
	for i := 0; i < n; i++ {
		ev, err := domain.NewAuditEvent(salon, "owner", "Olga", action, fmt.Sprintf("e%03d", i), e.clock.Add(-time.Duration(i)*time.Minute))
		if err != nil {
			panic(err)
		}
		_ = e.repo.Append(ev)
	}
}

func isMsg(err error, kind error, text string) bool {
	return err != nil && errors.Is(err, kind) && err.Error() == text
}

// --- Record -----------------------------------------------------------------

func TestAuditRecord_snapshotsActorName(t *testing.T) {
	e := newAuditEnv(t)
	if err := e.svc.Record("s1", "owner", "member.update", "m-9"); err != nil {
		t.Fatal(err)
	}
	if len(e.repo.rows) != 1 {
		t.Fatalf("esperaba 1 evento, hay %d", len(e.repo.rows))
	}
	ev := e.repo.rows[0]
	if ev.ActorName != "Olga Dueña" || ev.ActorUserID != "owner" || ev.SalonID != "s1" ||
		ev.Action != "member.update" || ev.EntityID != "m-9" || !ev.CreatedAt.Equal(e.clock) || ev.ID == "" {
		t.Fatalf("evento inesperado: %+v", ev)
	}
	// El nombre es una FOTO: renombrar después no cambia lo ya guardado.
	_ = e.users.Save(domain.User{ID: "owner", Name: "Otro Nombre", Email: "olga@mail.com"})
	if e.repo.rows[0].ActorName != "Olga Dueña" {
		t.Fatal("el snapshot no debe cambiar")
	}
}

func TestAuditRecord_unknownActorRecordsEmptyName(t *testing.T) {
	e := newAuditEnv(t)
	if err := e.svc.Record("s1", "fantasma", "settings.update", ""); err != nil {
		t.Fatalf("un actor sin resolver no debe fallar: %v", err)
	}
	if len(e.repo.rows) != 1 || e.repo.rows[0].ActorName != "" || e.repo.rows[0].EntityID != "" {
		t.Fatalf("esperaba 1 evento con nombre vacío: %+v", e.repo.rows)
	}
	// Sin repositorio de usuarios tampoco falla.
	e.svc.Users = nil
	if err := e.svc.Record("s1", "owner", "settings.update", ""); err != nil || len(e.repo.rows) != 2 {
		t.Fatalf("sin Users debe registrar igual: %v %d", err, len(e.repo.rows))
	}
}

func TestAuditRecord_writerErrorIsReturned(t *testing.T) {
	e := newAuditEnv(t)
	e.repo.appendErr = errAuditDown
	if err := e.svc.Record("s1", "owner", "member.add", ""); !errors.Is(err, errAuditDown) {
		t.Fatalf("el error del repositorio debe volver al llamador: %v", err)
	}
}

func TestAuditRecord_invalidInputWritesNothing(t *testing.T) {
	e := newAuditEnv(t)
	for _, c := range []struct{ salon, actor, action, entity string }{
		{"", "owner", "a.b", ""},
		{"s1", "", "a.b", ""},
		{"s1", "owner", "", ""},
		{"s1", "owner", strings.Repeat("x", 61), ""},
		{"s1", "owner", "a.b", strings.Repeat("y", 101)},
	} {
		if err := e.svc.Record(c.salon, c.actor, c.action, c.entity); err == nil {
			t.Errorf("esperaba error para %+v", c)
		}
	}
	if len(e.repo.rows) != 0 || e.repo.appendCalls != 0 {
		t.Fatalf("una entrada inválida no escribe: %d filas, %d llamadas", len(e.repo.rows), e.repo.appendCalls)
	}
}

func TestAuditRecord_noWriterIsNoop(t *testing.T) {
	var s AuditService
	if s.Enabled() {
		t.Fatal("sin repositorio no está habilitado")
	}
	if err := s.Record("s1", "owner", "member.add", ""); err != nil {
		t.Fatalf("sin repositorio es un no-op: %v", err)
	}
	if !newAuditEnv(t).svc.Enabled() {
		t.Fatal("con repositorio está habilitado")
	}
}

// --- List: permisos primero ---------------------------------------------------

func TestAuditList_onlyOwnerAdmin_andBeforeAnything(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 3, "member.add")
	// Parámetros INVÁLIDOS a propósito: quien no puede ver recibe 403, no 400, y
	// la base no se toca.
	for name, m := range map[string]domain.Membership{
		"recepcion": e.recep, "profesional": e.prof, "clienta": e.client,
	} {
		_, err := e.svc.List(m, "s1", "999", "basura", "a\x00")
		if !errors.Is(err, domain.ErrForbidden) || err.Error() != "sin permiso" {
			t.Errorf("%s: esperaba 403 «sin permiso», obtuve %v", name, err)
		}
	}
	for name, m := range map[string]domain.Membership{"baja": e.removed, "otro salón": e.other} {
		_, err := e.svc.List(m, "s1", "999", "basura", "a\x00")
		if !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: esperaba 403, obtuve %v", name, err)
		}
	}
	if e.repo.listCalls != 0 {
		t.Fatalf("el permiso va antes de la base: %d lecturas", e.repo.listCalls)
	}
	for name, m := range map[string]domain.Membership{"dueña": e.owner, "admin": e.admin} {
		p, err := e.svc.List(m, "s1", "", "", "")
		if err != nil || len(p.Events) != 3 {
			t.Errorf("%s: esperaba 3 eventos, obtuve %d %v", name, len(p.Events), err)
		}
	}
	// RequireViewer es el mismo permiso SIN datos (el handler lo llama primero).
	if err := e.svc.RequireViewer(e.recep, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RequireViewer recepción: %v", err)
	}
	if err := e.svc.RequireViewer(e.admin, "s1"); err != nil {
		t.Fatalf("RequireViewer admin: %v", err)
	}
	if e.repo.listCalls != 2 {
		t.Fatalf("RequireViewer no debe leer la base: %d lecturas", e.repo.listCalls)
	}
}

// --- List: límite -------------------------------------------------------------

func TestAuditList_limitDefaultAndBounds(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 130, "sale.x")
	p, err := e.svc.List(e.owner, "s1", "", "", "")
	if err != nil || len(p.Events) != 50 || p.NextCursor == "" {
		t.Fatalf("por defecto 50 con cursor: %d %q %v", len(p.Events), p.NextCursor, err)
	}
	p, err = e.svc.List(e.owner, "s1", "100", "", "")
	if err != nil || len(p.Events) != 100 || p.NextCursor == "" {
		t.Fatalf("máximo 100: %d %v", len(p.Events), err)
	}
	p, err = e.svc.List(e.owner, "s1", "1", "", "")
	if err != nil || len(p.Events) != 1 || p.NextCursor == "" {
		t.Fatalf("mínimo 1: %d %v", len(p.Events), err)
	}
	calls := e.repo.listCalls
	for _, l := range []string{"0", "101", "-1", "abc", "1.5"} {
		_, err := e.svc.List(e.owner, "s1", l, "", "")
		if !isMsg(err, domain.ErrValidation, "El limite debe ser entre 1 y 100") {
			t.Errorf("limit=%q: %v", l, err)
		}
	}
	if e.repo.listCalls != calls {
		t.Fatal("un límite inválido no toca la base")
	}
}

// --- List: paginación ----------------------------------------------------------

func TestAuditList_threePagesNoGapsNoDuplicates(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 25, "sale.x")
	var seen []string
	cursor := ""
	var sizes []int
	for page := 0; page < 5; page++ {
		p, err := e.svc.List(e.owner, "s1", "10", cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(p.Events))
		for _, ev := range p.Events {
			seen = append(seen, ev.EntityID)
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if fmt.Sprint(sizes) != "[10 10 5]" {
		t.Fatalf("páginas %v, quería [10 10 5]", sizes)
	}
	if len(seen) != 25 {
		t.Fatalf("esperaba 25 eventos, vi %d", len(seen))
	}
	for i, id := range seen {
		if want := fmt.Sprintf("e%03d", i); id != want {
			t.Fatalf("posición %d: %s, quería %s (más nuevo primero, sin saltos ni repetidos)", i, id, want)
		}
	}
}

func TestAuditList_exactMultipleHasNoExtraPage(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 20, "sale.x")
	p, _ := e.svc.List(e.owner, "s1", "10", "", "")
	p, err := e.svc.List(e.owner, "s1", "10", p.NextCursor, "")
	if err != nil || len(p.Events) != 10 || p.NextCursor != "" {
		t.Fatalf("la última página exacta no lleva cursor: %d %q %v", len(p.Events), p.NextCursor, err)
	}
}

func TestAuditList_sameTimestampTiesBrokenByID(t *testing.T) {
	e := newAuditEnv(t)
	for i := 0; i < 7; i++ {
		_ = e.repo.Append(domain.AuditEvent{
			ID: fmt.Sprintf("id%d", i), SalonID: "s1", ActorUserID: "owner", Action: "a.b", CreatedAt: e.clock,
		})
	}
	var seen []string
	cursor := ""
	for i := 0; i < 10; i++ {
		p, err := e.svc.List(e.owner, "s1", "3", cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range p.Events {
			seen = append(seen, ev.ID)
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if fmt.Sprint(seen) != "[id6 id5 id4 id3 id2 id1 id0]" {
		t.Fatalf("empates por id: %v", seen)
	}
}

func TestAuditList_invalidCursor(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 3, "a.b")
	calls := e.repo.listCalls
	for _, c := range []string{"basura", "AAAA", "!!", strings.Repeat("A", 500)} {
		_, err := e.svc.List(e.owner, "s1", "", c, "")
		if !isMsg(err, domain.ErrValidation, "Cursor invalido") {
			t.Errorf("cursor %q: %v", c, err)
		}
	}
	if e.repo.listCalls != calls {
		t.Fatal("un cursor inválido no toca la base")
	}
}

// --- List: filtro y aislamiento ---------------------------------------------------

func TestAuditList_actionFilter(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 2, "member.add")
	e.seed("s1", 2, "member.update")
	e.seed("s1", 2, "memberx.add")
	e.seed("s1", 2, "payroll_run.pay")
	count := func(filter string) int {
		p, err := e.svc.List(e.owner, "s1", "", "", filter)
		if err != nil {
			t.Fatalf("filtro %q: %v", filter, err)
		}
		return len(p.Events)
	}
	if n := count(""); n != 8 {
		t.Errorf("sin filtro: %d", n)
	}
	if n := count("member"); n != 4 {
		t.Errorf("prefijo member (add+update, sin memberx): %d", n)
	}
	if n := count("member.add"); n != 2 {
		t.Errorf("acción completa: %d", n)
	}
	if n := count("mem"); n != 0 {
		t.Errorf("«mem» no es un límite de punto: %d", n)
	}
	if n := count("payroll_run"); n != 2 {
		t.Errorf("payroll_run: %d", n)
	}
	for _, bad := range []string{"a\x00b", "a\nb", "a\rb"} {
		_, err := e.svc.List(e.owner, "s1", "", "", bad)
		if !isMsg(err, domain.ErrValidation, "Accion invalida") {
			t.Errorf("filtro %q: %v", bad, err)
		}
	}
}

func TestAuditList_salonIsolation(t *testing.T) {
	e := newAuditEnv(t)
	e.seed("s1", 3, "member.add")
	e.seed("s2", 5, "member.add")
	p, err := e.svc.List(e.owner, "s1", "", "", "")
	if err != nil || len(p.Events) != 3 {
		t.Fatalf("s1 ve solo lo suyo: %d %v", len(p.Events), err)
	}
	for _, ev := range p.Events {
		if ev.SalonID != "s1" {
			t.Fatalf("fuga entre salones: %+v", ev)
		}
	}
	// La dueña de s2 no puede leer s1, ni con su propia membresía.
	if _, err := e.svc.List(e.other, "s1", "", "", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}
	// Un cursor de s2 usado en s1 no filtra eventos de s2.
	p2, _ := e.svc.List(domain.Membership{UserID: "owner2", SalonID: "s2", Role: domain.RoleOwner}, "s2", "2", "", "")
	p3, err := e.svc.List(e.owner, "s1", "", p2.NextCursor, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range p3.Events {
		if ev.SalonID != "s1" {
			t.Fatalf("fuga por cursor: %+v", ev)
		}
	}
}
