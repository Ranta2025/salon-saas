// Casos de uso de los gastos (F3b, D3/D4/D5): permisos owner/admin, tenant
// isolation, el rango del listado, el PUT que REEMPLAZA y la frase del 404.
package app

import (
	"errors"
	"sort"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeExpenses es el repo de gastos con la MISMA semántica que pgstore: scope
// por salón (el de otro tenant no existe), orden descendente y tope del listado.
type fakeExpenses struct {
	byID map[string]domain.Expense
	err  error
	// lastList guarda los argumentos del último ListRange (rango y tope).
	lastList struct {
		salonID, from, to string
		limit             int
	}
	saves int
}

func newFakeExpenses() *fakeExpenses { return &fakeExpenses{byID: map[string]domain.Expense{}} }

func (f *fakeExpenses) Save(e domain.Expense) error {
	if f.err != nil {
		return f.err
	}
	f.saves++
	f.byID[e.ID] = e
	return nil
}

func (f *fakeExpenses) ListRange(salonID, from, to string, limit int) ([]domain.Expense, bool, error) {
	f.lastList.salonID, f.lastList.from, f.lastList.to, f.lastList.limit = salonID, from, to, limit
	if f.err != nil {
		return nil, false, f.err
	}
	out := []domain.Expense{}
	for _, e := range f.byID {
		if e.SalonID != salonID || e.SpentOn < from || e.SpentOn > to {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpentOn != out[j].SpentOn {
			return out[i].SpentOn > out[j].SpentOn
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (f *fakeExpenses) FindByID(salonID, id string) (domain.Expense, error) {
	if f.err != nil {
		return domain.Expense{}, f.err
	}
	e, ok := f.byID[id]
	if !ok || e.SalonID != salonID {
		return domain.Expense{}, domain.ErrNotFound
	}
	return e, nil
}

func (f *fakeExpenses) Update(e domain.Expense) error {
	if f.err != nil {
		return f.err
	}
	cur, ok := f.byID[e.ID]
	if !ok || cur.SalonID != e.SalonID {
		return domain.ErrNotFound
	}
	f.byID[e.ID] = e
	return nil
}

func (f *fakeExpenses) Delete(salonID, id string) error {
	if f.err != nil {
		return f.err
	}
	e, ok := f.byID[id]
	if !ok || e.SalonID != salonID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

var _ ports.ExpenseRepository = (*fakeExpenses)(nil)

// expenseNow es el «hoy» de estos tests: 20/3/2026 12:00 en CDMX.
var expenseNow = time.Date(2026, 3, 20, 18, 0, 0, 0, time.UTC)

type expenseEnv struct {
	repo     *fakeExpenses
	settings *fakeSettings
	svc      ExpenseService
	mems     *fakeMemberships
}

func newExpenseEnv() *expenseEnv {
	mems := &fakeMemberships{byKey: map[string]domain.Membership{}}
	settings := &fakeSettings{bySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "America/Mexico_City", OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30},
	}}
	for _, m := range []domain.Membership{
		{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner},
		{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin},
		{UserID: "u-recep", SalonID: "s1", Role: domain.RoleReception},
		{UserID: "u-pro", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		{UserID: "u-client", SalonID: "s1", Role: domain.RoleClient},
		{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner},
	} {
		mems.byKey[m.UserID+"|"+m.SalonID] = m
	}
	repo := newFakeExpenses()
	return &expenseEnv{
		repo: repo, settings: settings, mems: mems,
		svc: ExpenseService{
			Expenses: repo, Memberships: mems, Settings: settings,
			Now: func() time.Time { return expenseNow },
		},
	}
}

func (e *expenseEnv) actor(t *testing.T, userID string) domain.Membership {
	t.Helper()
	m, err := e.mems.Find(userID, "s1")
	if err != nil {
		t.Fatalf("actor %s: %v", userID, err)
	}
	return m
}

// D5: solo owner y administración, en las cuatro operaciones de gastos.
func TestExpenseService_permisos_por_rol(t *testing.T) {
	e := newExpenseEnv()
	fields := domain.ExpenseFields{Category: "rent", AmountCents: 100, SpentOn: "2026-03-19", Description: ""}
	for _, tc := range []struct {
		user string
		want bool
	}{
		{"u-owner", true},
		{"u-admin", true},
		{"u-recep", false},
		{"u-pro", false},
		{"u-client", false},
	} {
		actor := e.actor(t, tc.user)
		_, errCreate := e.svc.Create(actor, "s1", fields)
		_, _, errList := e.svc.List(actor, "s1", "2026-03-01", "2026-03-31")
		_, errUpdate := e.svc.Update(actor, "s1", "no-existe", fields)
		errDelete := e.svc.Delete(actor, "s1", "no-existe")
		if tc.want {
			if errCreate != nil || errList != nil {
				t.Fatalf("%s debería poder (crear %v, listar %v)", tc.user, errCreate, errList)
			}
			// Editar y borrar llegan hasta el 404 del gasto inexistente.
			if !errors.Is(errUpdate, domain.ErrNotFound) {
				t.Fatalf("%s: update = %v, esperaba 404 del gasto inexistente", tc.user, errUpdate)
			}
			if !errors.Is(errDelete, domain.ErrNotFound) {
				t.Fatalf("%s: delete = %v, esperaba 404 del gasto inexistente", tc.user, errDelete)
			}
			continue
		}
		for what, err := range map[string]error{
			"crear": errCreate, "listar": errList, "editar": errUpdate, "borrar": errDelete,
		} {
			if !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("%s no debería poder %s: %v", tc.user, what, err)
			}
		}
	}

	// Un actor de OTRO salón no puede tocar este (403), aunque sea dueño del suyo.
	ajeno := domain.Membership{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner}
	if _, err := e.svc.Create(ajeno, "s1", fields); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("un dueño de otro salón: %v", err)
	}
}

func TestExpenseService_create_list_update_delete(t *testing.T) {
	e := newExpenseEnv()
	owner := e.actor(t, "u-owner")

	created, err := e.svc.Create(owner, "s1", domain.ExpenseFields{
		Category: "supplies", AmountCents: 250000, SpentOn: "2026-03-19", Description: "  Tijeras  ",
	})
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	if created.ID == "" || created.SalonID != "s1" || created.Category != domain.ExpenseSupplies ||
		created.AmountCents != 250000 || created.SpentOn != "2026-03-19" ||
		created.Description != "Tijeras" || created.CreatedBy != "u-owner" {
		t.Fatalf("gasto creado = %+v", created)
	}

	list, truncated, err := e.svc.List(owner, "s1", "2026-03-01", "2026-03-31")
	if err != nil || truncated || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("listar = %+v %v %v", list, truncated, err)
	}

	updated, err := e.svc.Update(owner, "s1", created.ID, domain.ExpenseFields{
		Category: "rent", AmountCents: 1, SpentOn: "2026-03-01", Description: "",
	})
	if err != nil {
		t.Fatalf("editar: %v", err)
	}
	if updated.Category != domain.ExpenseRent || updated.AmountCents != 1 ||
		updated.SpentOn != "2026-03-01" || updated.Description != "" ||
		updated.CreatedBy != "u-owner" || updated.ID != created.ID {
		t.Fatalf("gasto editado = %+v", updated)
	}

	if err := e.svc.Delete(owner, "s1", created.ID); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	if err := e.svc.Delete(owner, "s1", created.ID); !errors.Is(err, domain.ErrNotFound) ||
		err.Error() != "Gasto no encontrado" {
		t.Fatalf("borrar dos veces = %v", err)
	}
	// La lista vacía es [] y no nil (el frontend no distingue null de []).
	list, _, err = e.svc.List(owner, "s1", "2026-03-01", "2026-03-31")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("lista vacía = %#v %v", list, err)
	}
}

// Un gasto de OTRO salón no se edita ni se borra (404 con la frase del
// contrato), y el listado nunca trae filas de otro tenant.
func TestExpenseService_aislamiento_de_tenant(t *testing.T) {
	e := newExpenseEnv()
	owner := e.actor(t, "u-owner")
	ajeno := domain.Expense{
		ID: "e-ajeno", SalonID: "s2", Category: domain.ExpenseRent,
		AmountCents: 500, SpentOn: "2026-03-10", Description: "", CreatedBy: "u-otro",
		CreatedAt: expenseNow,
	}
	e.repo.byID[ajeno.ID] = ajeno

	if _, err := e.svc.Update(owner, "s1", ajeno.ID, domain.ExpenseFields{
		Category: "rent", AmountCents: 1, SpentOn: "2026-03-10", Description: "",
	}); err == nil || err.Error() != "Gasto no encontrado" || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("editar el de otro salón = %v", err)
	}
	if err := e.svc.Delete(owner, "s1", ajeno.ID); err == nil || err.Error() != "Gasto no encontrado" {
		t.Fatalf("borrar el de otro salón = %v", err)
	}
	if _, ok := e.repo.byID[ajeno.ID]; !ok {
		t.Fatal("el gasto de otro salón NO se puede borrar")
	}
	list, _, err := e.svc.List(owner, "s1", "2026-03-01", "2026-03-31")
	if err != nil || len(list) != 0 {
		t.Fatalf("el listado trajo otro tenant: %+v %v", list, err)
	}
}

// Un 400 de validación no escribe NADA (ni en crear ni en editar).
func TestExpenseService_validacion_no_escribe(t *testing.T) {
	e := newExpenseEnv()
	owner := e.actor(t, "u-owner")
	cases := []struct {
		in   domain.ExpenseFields
		want string
	}{
		{domain.ExpenseFields{Category: "salary", AmountCents: 100, SpentOn: "2026-03-19"},
			"Categoría inválida: usá alquiler, insumos, servicios u otros"},
		{domain.ExpenseFields{Category: "rent", AmountCents: 0, SpentOn: "2026-03-19"},
			"El monto debe ser mayor a cero"},
		{domain.ExpenseFields{Category: "rent", AmountCents: 100, SpentOn: "2026-03-21"},
			"La fecha del gasto no puede ser futura"},
		{domain.ExpenseFields{Category: "rent", AmountCents: 100, SpentOn: "1999-12-31"},
			"La fecha del gasto es demasiado antigua"},
	}
	for _, tc := range cases {
		if _, err := e.svc.Create(owner, "s1", tc.in); err == nil || err.Error() != tc.want {
			t.Fatalf("crear %+v = %v", tc.in, err)
		}
	}
	if e.repo.saves != 0 {
		t.Fatalf("un 400 no puede escribir: hubo %d altas", e.repo.saves)
	}

	created, err := e.svc.Create(owner, "s1", domain.ExpenseFields{
		Category: "rent", AmountCents: 100, SpentOn: "2026-03-19", Description: "ok",
	})
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	if _, err := e.svc.Update(owner, "s1", created.ID, domain.ExpenseFields{
		Category: "rent", AmountCents: -5, SpentOn: "2026-03-19", Description: "x",
	}); err == nil || err.Error() != "El monto debe ser mayor a cero" {
		t.Fatalf("editar con monto inválido = %v", err)
	}
	if got := e.repo.byID[created.ID]; got.AmountCents != 100 || got.Description != "ok" {
		t.Fatalf("el 400 del PUT escribió: %+v", got)
	}
}

// El listado valida el rango (400 del contrato) y pide 500 filas + 1 al repo.
func TestExpenseService_listado_rango_y_tope(t *testing.T) {
	e := newExpenseEnv()
	owner := e.actor(t, "u-owner")
	if _, _, err := e.svc.List(owner, "s1", "", "2026-03-31"); err == nil ||
		err.Error() != "Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días" {
		t.Fatalf("rango incompleto = %v", err)
	}
	if _, _, err := e.svc.List(owner, "s1", "2026-03-31", "2026-03-01"); err == nil {
		t.Fatal("to < from debería ser 400")
	}
	if _, _, err := e.svc.List(owner, "s1", "2026-01-01", "2027-01-02"); err == nil {
		t.Fatal("367 días debería ser 400")
	}
	if _, _, err := e.svc.List(owner, "s1", "2026-01-01", "2027-01-01"); err != nil {
		t.Fatalf("366 días debería ser válido: %v", err)
	}
	if e.repo.lastList.limit != 500 || e.repo.lastList.salonID != "s1" ||
		e.repo.lastList.from != "2026-01-01" || e.repo.lastList.to != "2027-01-01" {
		t.Fatalf("argumentos del listado = %+v", e.repo.lastList)
	}

	// 501 filas -> 500 y truncated.
	for i := 0; i < 501; i++ {
		e.repo.byID[domain.NewID()] = domain.Expense{
			ID: domain.NewID(), SalonID: "s1", Category: domain.ExpenseRent,
			AmountCents: 1, SpentOn: "2026-03-10", CreatedAt: expenseNow,
		}
	}
	list, truncated, err := e.svc.List(owner, "s1", "2026-03-01", "2026-03-31")
	if err != nil || !truncated || len(list) != 500 {
		t.Fatalf("listado con más de 500: %d filas, truncated=%v, err=%v", len(list), truncated, err)
	}
}
