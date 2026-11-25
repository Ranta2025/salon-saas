package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// loyEnv arma un salón s1 (zona de Auckland, lejos de UTC) con todos los roles,
// un servicio de regalo, una clienta y un reloj fijo.
type loyEnv struct {
	svc      LoyaltyService
	mems     *fakeMemberships
	services *fakeServices
	clients  *fakeClients
	programs *fakeLoyaltyPrograms
	rewards  *fakeLoyaltyRewards
	clock    time.Time

	owner, admin, recep, prof, client, other, removed domain.Membership
	corte, tinte                                      domain.Service
	lia, otherClient                                  domain.Client
}

func newLoyEnv(t *testing.T) *loyEnv {
	t.Helper()
	e := &loyEnv{
		mems:     &fakeMemberships{},
		services: &fakeServices{},
		clients:  &fakeClients{},
		// 11 de marzo 00:30 en Auckland (NZDT +13) = 10 de marzo 11:30 UTC.
		clock: time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC),
	}
	settings := &fakeSettings{bySalon: map[string]domain.SalonSettings{
		"s1": {SalonID: "s1", Timezone: "Pacific/Auckland", OpenTime: "09:00", CloseTime: "18:00", SlotStepMin: 30},
	}}
	e.programs = &fakeLoyaltyPrograms{services: e.services}
	e.rewards = &fakeLoyaltyRewards{programs: e.programs, visits: map[string]int{}}
	e.svc = LoyaltyService{
		Programs: e.programs, Rewards: e.rewards, Services: e.services, Clients: e.clients,
		Memberships: e.mems, Settings: settings,
		Now: func() time.Time { return e.clock },
	}
	e.owner = miembro(e.mems, "owner", "s1", domain.RoleOwner)
	e.admin = miembro(e.mems, "admin", "s1", domain.RoleAdmin)
	e.recep = miembro(e.mems, "recep", "s1", domain.RoleReception)
	_ = e.mems.Save(domain.Membership{UserID: "prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	e.prof, _ = e.mems.Find("prof", "s1")
	e.client = miembro(e.mems, "client", "s1", domain.RoleClient)
	e.other = miembro(e.mems, "owner2", "s2", domain.RoleOwner)
	e.removed = miembro(e.mems, "removed", "s1", domain.RoleAdmin)
	at := e.clock.Add(-time.Hour)
	e.removed.EndedAt = &at
	e.mems.byKey["removed|s1"] = e.removed

	e.corte = e.newService(t, "s1", "Corte")
	e.tinte = e.newService(t, "s1", "Tinte")
	e.lia = e.newClient(t, "s1", "Lía Pérez")
	e.otherClient = e.newClient(t, "s2", "Clienta Ajena")
	return e
}

func (e *loyEnv) newService(t *testing.T, salonID, name string) domain.Service {
	t.Helper()
	s, err := domain.NewService(salonID, name, 45, 15000, 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.services.Save(s)
	return s
}

func (e *loyEnv) newClient(t *testing.T, salonID, name string) domain.Client {
	t.Helper()
	c, err := domain.NewClient(salonID, name, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.clients.Save(c)
	return c
}

// saveProgram guarda un programa activo de N visitas con el corte de regalo.
func (e *loyEnv) saveProgram(t *testing.T, enabled bool, visits int, validity *int) domain.LoyaltyProgram {
	t.Helper()
	p, err := e.svc.SaveProgram(e.owner, "s1", enabled, visits, e.corte.ID, validity)
	if err != nil {
		t.Fatalf("guardar programa: %v", err)
	}
	return p
}

func (e *loyEnv) setVisits(clientID string, n int) { e.rewards.visits[loyKey("s1", clientID)] = n }

// nothingWritten comprueba que un rechazo no escribió NADA.
func (e *loyEnv) nothingWritten(t *testing.T, what string) {
	t.Helper()
	if e.programs.saveCalls != 0 || len(e.programs.rows) != 0 {
		t.Fatalf("%s escribió el programa", what)
	}
}

func (e *loyEnv) grantOne(t *testing.T, op string) domain.LoyaltyReward {
	t.Helper()
	r, err := e.svc.Grant(e.recep, "s1", e.lia.ID, op)
	if err != nil {
		t.Fatalf("entregar: %v", err)
	}
	return r
}

// --- D4: programa ----------------------------------------------------------

// Cualquier miembro ACTIVO lee el programa; la baja y los de otro salón, no.
func TestLoyaltyService_program_readableByAnyActiveMember(t *testing.T) {
	e := newLoyEnv(t)
	// Nunca guardado: Saved=false (el handler lo muestra enabled:false y null).
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep, e.prof, e.client} {
		p, err := e.svc.Program(actor, "s1")
		if err != nil || p.Saved || p.Enabled {
			t.Fatalf("%s lee el programa sin guardar: %+v %v", actor.Role, p, err)
		}
	}
	e.saveProgram(t, true, 5, nil)
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep, e.prof, e.client} {
		p, err := e.svc.Program(actor, "s1")
		if err != nil || !p.Saved || !p.Enabled || p.VisitsRequired != 5 || p.RewardServiceName != "Corte" {
			t.Fatalf("%s lee el programa: %+v %v", actor.Role, p, err)
		}
	}
	for name, actor := range map[string]domain.Membership{"baja": e.removed, "otro salón": e.other} {
		if _, err := e.svc.Program(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s lee el programa: %v, quería 403", name, err)
		}
	}
}

// D4: solo owner/admin guardan; el resto recibe 403 y NO se escribe nada.
func TestLoyaltyService_saveProgram_permissions(t *testing.T) {
	e := newLoyEnv(t)
	for _, actor := range []domain.Membership{e.recep, e.prof, e.client, e.removed, e.other} {
		if _, err := e.svc.SaveProgram(actor, "s1", true, 5, e.corte.ID, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s guarda: %v, quería 403", actor.Role, err)
		}
		if err := e.svc.RequireManager(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s RequireManager: %v, quería 403", actor.Role, err)
		}
	}
	e.nothingWritten(t, "un rol sin permiso")
	for _, actor := range []domain.Membership{e.owner, e.admin} {
		if err := e.svc.RequireManager(actor, "s1"); err != nil {
			t.Fatalf("%s RequireManager: %v", actor.Role, err)
		}
		if _, err := e.svc.SaveProgram(actor, "s1", true, 5, e.corte.ID, nil); err != nil {
			t.Fatalf("%s guarda: %v", actor.Role, err)
		}
	}
}

// D4: textos EXACTOS y nada escrito ante un dato inválido.
func TestLoyaltyService_saveProgram_validation(t *testing.T) {
	e := newLoyEnv(t)
	inactive, err := e.tinte.WithDetails("Tinte", 45, 15000, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.services.Save(inactive)
	foreign := e.newService(t, "s2", "Ajeno")
	zero, big := 0, 3651

	wantMsg(t, saveErr(e, 1, e.corte.ID, nil), domain.ErrValidation, "Las visitas necesarias deben ser entre 2 y 50")
	wantMsg(t, saveErr(e, 51, e.corte.ID, nil), domain.ErrValidation, "Las visitas necesarias deben ser entre 2 y 50")
	wantMsg(t, saveErr(e, 5, "no-existe", nil), domain.ErrValidation, "Servicio inválido")
	wantMsg(t, saveErr(e, 5, inactive.ID, nil), domain.ErrValidation, "Servicio inválido")
	wantMsg(t, saveErr(e, 5, foreign.ID, nil), domain.ErrValidation, "Servicio inválido")
	wantMsg(t, saveErr(e, 5, "a\x00b", nil), domain.ErrValidation, "Servicio inválido")
	wantMsg(t, saveErr(e, 5, "a\xffb", nil), domain.ErrValidation, "Servicio inválido")
	wantMsg(t, saveErr(e, 5, e.corte.ID, &zero), domain.ErrValidation, "La vigencia debe ser entre 1 y 3650 días")
	wantMsg(t, saveErr(e, 5, e.corte.ID, &big), domain.ErrValidation, "La vigencia debe ser entre 1 y 3650 días")
	e.nothingWritten(t, "un dato inválido")

	// Los límites exactos sí pasan; con el programa APAGADO también se valida.
	one, max := 1, 3650
	for _, c := range []struct {
		visits int
		days   *int
	}{{2, &one}, {50, &max}, {5, nil}} {
		if _, err := e.svc.SaveProgram(e.owner, "s1", false, c.visits, e.corte.ID, c.days); err != nil {
			t.Fatalf("visitas=%d vigencia=%v: %v", c.visits, c.days, err)
		}
	}
	wantMsg(t, saveErrEnabled(e, false, 1), domain.ErrValidation, "Las visitas necesarias deben ser entre 2 y 50")
}

func saveErr(e *loyEnv, visits int, serviceID string, days *int) error {
	_, err := e.svc.SaveProgram(e.owner, "s1", true, visits, serviceID, days)
	return err
}

func saveErrEnabled(e *loyEnv, enabled bool, visits int) error {
	_, err := e.svc.SaveProgram(e.owner, "s1", enabled, visits, e.corte.ID, nil)
	return err
}

// D2/D4: el primer guardado fija counting_from = ahora y los siguientes lo
// conservan aunque cambien los demás datos.
func TestLoyaltyService_saveProgram_countingFromIsSetOnce(t *testing.T) {
	e := newLoyEnv(t)
	first := e.saveProgram(t, true, 5, nil)
	if !first.CountingFrom.Equal(e.clock) {
		t.Fatalf("counting_from = %v, quería %v", first.CountingFrom, e.clock)
	}
	e.clock = e.clock.Add(48 * time.Hour)
	days := 30
	second, err := e.svc.SaveProgram(e.admin, "s1", false, 8, e.tinte.ID, &days)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CountingFrom.Equal(first.CountingFrom) {
		t.Fatalf("counting_from cambió al reguardar: %v -> %v", first.CountingFrom, second.CountingFrom)
	}
	if second.Enabled || second.VisitsRequired != 8 || second.RewardServiceName != "Tinte" || second.ValidityDays == nil || *second.ValidityDays != 30 {
		t.Fatalf("el segundo guardado no reemplazó los datos: %+v", second)
	}
	// La vigencia se puede quitar con null explícito.
	third, err := e.svc.SaveProgram(e.owner, "s1", true, 8, e.tinte.ID, nil)
	if err != nil || third.ValidityDays != nil || !third.CountingFrom.Equal(first.CountingFrom) {
		t.Fatalf("quitar la vigencia: %+v %v", third, err)
	}
}

// --- D5: ver a una clienta --------------------------------------------------

func TestLoyaltyService_clientStatus_permissions(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 5, nil)
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		if _, err := e.svc.ClientStatus(actor, "s1", e.lia.ID); err != nil {
			t.Fatalf("%s ve a la clienta: %v", actor.Role, err)
		}
	}
	for _, actor := range []domain.Membership{e.prof, e.client, e.removed, e.other} {
		if _, err := e.svc.ClientStatus(actor, "s1", e.lia.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s ve a la clienta: %v, quería 403", actor.Role, err)
		}
	}
}

func TestLoyaltyService_clientStatus_unknownClient(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 5, nil)
	for _, id := range []string{"no-existe", e.otherClient.ID, "", "a\x00b", "a\xffb"} {
		_, err := e.svc.ClientStatus(e.recep, "s1", id)
		wantMsg(t, err, domain.ErrNotFound, "Ficha no encontrada")
	}
}

// D5: las cuentas, la forma y que las visitas se cuentan desde counting_from.
func TestLoyaltyService_clientStatus_numbers(t *testing.T) {
	e := newLoyEnv(t)
	prog := e.saveProgram(t, true, 5, nil)
	e.setVisits(e.lia.ID, 12)
	// Un vale ya entregado (el 1) y un segundo entregable.
	first := e.grantOne(t, "")
	st, err := e.svc.ClientStatus(e.recep, "s1", e.lia.ID)
	if err != nil {
		t.Fatal(err)
	}
	pr := st.Progress
	if pr.Visits != 12 || pr.VisitsRequired != 5 || pr.Earned != 2 || pr.Granted != 1 || pr.Available != 1 || pr.NextIn != 3 {
		t.Fatalf("progreso: %+v", pr)
	}
	if !st.Program.Enabled || !st.Program.Saved {
		t.Fatalf("programa: %+v", st.Program)
	}
	if len(st.Rewards) != 1 || st.Rewards[0].ID != first.ID || st.Rewards[0].State != "available" {
		t.Fatalf("vales: %+v", st.Rewards)
	}
	// Las visitas se pidieron desde counting_from.
	last := e.rewards.since[len(e.rewards.since)-1]
	if !last.Equal(prog.CountingFrom) {
		t.Fatalf("visitas desde %v, quería counting_from %v", last, prog.CountingFrom)
	}
	// Con el programa APAGADO la ficha sigue mostrando los vales ya entregados.
	e.saveProgram(t, false, 5, nil)
	st, err = e.svc.ClientStatus(e.recep, "s1", e.lia.ID)
	if err != nil || st.Program.Enabled || len(st.Rewards) != 1 {
		t.Fatalf("apagado: %+v %v", st, err)
	}
}

func TestLoyaltyService_clientStatus_neverSaved(t *testing.T) {
	e := newLoyEnv(t)
	e.setVisits(e.lia.ID, 40)
	st, err := e.svc.ClientStatus(e.recep, "s1", e.lia.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Program.Saved || st.Program.Enabled || st.Progress.Visits != 0 || st.Progress.Available != 0 || len(st.Rewards) != 0 {
		t.Fatalf("sin programa: %+v", st)
	}
	if len(e.rewards.since) != 0 {
		t.Fatalf("sin programa no se cuentan visitas (no hay counting_from)")
	}
}

// El estado del vale lo calcula el caso de uso con SU reloj: usado, vencido y disponible.
func TestLoyaltyService_clientStatus_rewardStates(t *testing.T) {
	e := newLoyEnv(t)
	days := 1
	e.saveProgram(t, true, 2, &days)
	e.setVisits(e.lia.ID, 6)
	a := e.grantOne(t, "")
	b := e.grantOne(t, "")
	_ = e.grantOne(t, "")
	if _, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, a.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Dos días después los no usados vencieron.
	e.clock = e.clock.Add(72 * time.Hour)
	st, err := e.svc.ClientStatus(e.recep, "s1", e.lia.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, r := range st.Rewards {
		got[r.RewardNumber] = r.State
	}
	if got[1] != "redeemed" || got[2] != "expired" || got[3] != "expired" || len(got) != 3 {
		t.Fatalf("estados: %v (b=%d)", got, b.RewardNumber)
	}
	// Más nuevos primero.
	if st.Rewards[0].RewardNumber != 3 {
		t.Fatalf("orden: %+v", st.Rewards)
	}
}

// --- D6: entregar -----------------------------------------------------------

func TestLoyaltyService_grant_permissions(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 5, nil)
	e.setVisits(e.lia.ID, 50)
	for _, actor := range []domain.Membership{e.prof, e.client, e.removed, e.other} {
		if _, err := e.svc.Grant(actor, "s1", e.lia.ID, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s entrega: %v, quería 403", actor.Role, err)
		}
		if err := e.svc.RequireSeller(actor, "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s RequireSeller: %v, quería 403", actor.Role, err)
		}
	}
	if len(e.rewards.rewards) != 0 {
		t.Fatalf("un rol sin permiso entregó un vale")
	}
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		if err := e.svc.RequireSeller(actor, "s1"); err != nil {
			t.Fatalf("%s RequireSeller: %v", actor.Role, err)
		}
	}
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep} {
		if _, err := e.svc.Grant(actor, "s1", e.lia.ID, ""); err != nil {
			t.Fatalf("%s entrega: %v", actor.Role, err)
		}
	}
}

func TestLoyaltyService_grant_rules(t *testing.T) {
	e := newLoyEnv(t)
	// Nunca guardado y apagado: 409 «no activada», aunque haya visitas.
	e.setVisits(e.lia.ID, 50)
	_, err := e.svc.Grant(e.recep, "s1", e.lia.ID, "")
	wantMsg(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")
	e.saveProgram(t, false, 5, nil)
	_, err = e.svc.Grant(e.recep, "s1", e.lia.ID, "")
	wantMsg(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")

	// Encendido pero sin visitas suficientes.
	e.saveProgram(t, true, 5, nil)
	e.setVisits(e.lia.ID, 4)
	_, err = e.svc.Grant(e.recep, "s1", e.lia.ID, "")
	wantMsg(t, err, domain.ErrAlreadyExists, "No hay premios disponibles")

	// Con UN solo hito disponible: el primero entrega y el segundo recibe 409.
	e.setVisits(e.lia.ID, 5)
	r := e.grantOne(t, "")
	if r.RewardNumber != 1 || r.ServiceID != e.corte.ID || r.ServiceName != "Corte" || r.ClientID != e.lia.ID || r.State != "available" {
		t.Fatalf("vale: %+v", r)
	}
	_, err = e.svc.Grant(e.recep, "s1", e.lia.ID, "")
	wantMsg(t, err, domain.ErrAlreadyExists, "No hay premios disponibles")
	if len(e.rewards.rewards) != 1 {
		t.Fatalf("hay %d vales, quería 1", len(e.rewards.rewards))
	}

	// Al llegar a 10 visitas toca el número 2.
	e.setVisits(e.lia.ID, 10)
	if r2 := e.grantOne(t, ""); r2.RewardNumber != 2 {
		t.Fatalf("segundo vale: %+v", r2)
	}
}

// D6: clienta de otro salón o inexistente => 404 con el texto de clientas.
func TestLoyaltyService_grant_unknownClient(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 2, nil)
	e.setVisits(e.otherClient.ID, 50)
	for _, id := range []string{"no-existe", e.otherClient.ID, "a\x00b", "a\xffb"} {
		_, err := e.svc.Grant(e.recep, "s1", id, "")
		wantMsg(t, err, domain.ErrNotFound, "Ficha no encontrada")
	}
	if len(e.rewards.rewards) != 0 {
		t.Fatal("un rechazo entregó un vale")
	}
}

// D6: el vencimiento es el fin del día LOCAL (Auckland) de hoy + vigencia, y el
// servicio y su nombre son una FOTO: renombrar el servicio no cambia el vale.
func TestLoyaltyService_grant_expiryAndSnapshot(t *testing.T) {
	e := newLoyEnv(t)
	days := 1
	e.saveProgram(t, true, 2, &days)
	e.setVisits(e.lia.ID, 2)
	r := e.grantOne(t, "")
	auckland, _ := time.LoadLocation("Pacific/Auckland")
	// Hoy en Auckland es el 11 de marzo; +1 día => fin del día 12.
	want := time.Date(2026, 3, 12, 23, 59, 59, 999_999_000, auckland).UTC()
	if r.ExpiresAt == nil || !r.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, quería %v", r.ExpiresAt, want)
	}
	renamed, _ := e.corte.WithDetails("Corte premium", 45, 15000, 10, true)
	_ = e.services.Save(renamed)
	st, err := e.svc.ClientStatus(e.recep, "s1", e.lia.ID)
	if err != nil || st.Rewards[0].ServiceName != "Corte" || st.Rewards[0].ServiceID != e.corte.ID {
		t.Fatalf("la foto del servicio cambió: %+v %v", st.Rewards, err)
	}
	// Sin vigencia el vale no vence.
	e.saveProgram(t, true, 2, nil)
	e.setVisits(e.lia.ID, 4)
	if r2 := e.grantOne(t, ""); r2.ExpiresAt != nil {
		t.Fatalf("sin vigencia no vence: %v", r2.ExpiresAt)
	}
}

// D6: el repetido por client_op_id devuelve el MISMO vale sin entregar otro; el
// mismo id para otra clienta es 409; un id con NUL es 400.
func TestLoyaltyService_grant_idempotentByOp(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 2, nil)
	e.setVisits(e.lia.ID, 20) // 10 premios disponibles: el repetido NO debe entregar otro
	first := e.grantOne(t, "op-1")
	again := e.grantOne(t, " op-1 ")
	if again.ID != first.ID || again.RewardNumber != 1 || len(e.rewards.rewards) != 1 {
		t.Fatalf("el repetido entregó otro: %+v (hay %d)", again, len(e.rewards.rewards))
	}
	other := e.newClient(t, "s1", "Mora")
	e.setVisits(other.ID, 20)
	_, err := e.svc.Grant(e.recep, "s1", other.ID, "op-1")
	wantMsg(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
	if len(e.rewards.rewards) != 1 {
		t.Fatalf("el op id reusado entregó un vale")
	}
	_, err = e.svc.Grant(e.recep, "s1", e.lia.ID, "a\x00b")
	wantMsg(t, err, domain.ErrValidation, "El identificador de la operación no es válido")
	_, err = e.svc.Grant(e.recep, "s1", e.lia.ID, strings.Repeat("x", 201))
	wantMsg(t, err, domain.ErrValidation, "El identificador de la operación no es válido")
}

// --- D7: usar ---------------------------------------------------------------

func (e *loyEnv) withReward(t *testing.T, validity *int) domain.LoyaltyReward {
	t.Helper()
	e.saveProgram(t, true, 2, validity)
	e.setVisits(e.lia.ID, e.rewards.visits[loyKey("s1", e.lia.ID)]+2)
	return e.grantOne(t, "")
}

func TestLoyaltyService_redeem_permissions(t *testing.T) {
	e := newLoyEnv(t)
	r := e.withReward(t, nil)
	for _, actor := range []domain.Membership{e.prof, e.client, e.removed, e.other} {
		if _, err := e.svc.Redeem(actor, "s1", e.lia.ID, r.ID, "", "", ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s usa: %v, quería 403", actor.Role, err)
		}
	}
	if e.rewards.rewards[0].RedeemedAt != nil {
		t.Fatal("un rol sin permiso usó el vale")
	}
	got, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "", "", "")
	if err != nil || got.State != "redeemed" || got.RedeemedBy != "recep" || got.RedeemedAt == nil {
		t.Fatalf("recepción usa: %+v %v", got, err)
	}
}

func TestLoyaltyService_redeem_rules(t *testing.T) {
	e := newLoyEnv(t)
	days := 1
	r := e.withReward(t, &days)

	// Inexistente, de otra clienta/salón o con id sucio: 404.
	for _, id := range []string{"no-existe", "", "a\x00b", "a\xffb"} {
		_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, id, "", "", "")
		wantMsg(t, err, domain.ErrNotFound, "Premio no encontrado")
	}
	mora := e.newClient(t, "s1", "Mora")
	_, err := e.svc.Redeem(e.recep, "s1", mora.ID, r.ID, "", "", "")
	wantMsg(t, err, domain.ErrNotFound, "Premio no encontrado")
	_, err = e.svc.Redeem(e.recep, "s1", e.otherClient.ID, r.ID, "", "", "")
	wantMsg(t, err, domain.ErrNotFound, "Ficha no encontrada")
	_, err = e.svc.Redeem(e.recep, "s1", e.lia.ID, "x\xff", "", "", "")
	wantMsg(t, err, domain.ErrNotFound, "Premio no encontrado")
	if e.rewards.rewards[0].RedeemedAt != nil {
		t.Fatal("un rechazo usó el vale")
	}

	// Uso correcto con profesional (atiende) y nota; la nota se recorta.
	got, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "prof", "  Cumple  ", "")
	if err != nil {
		t.Fatalf("usar: %v", err)
	}
	if got.RedeemedProfessionalID != "prof" || got.RedeemNote != "Cumple" || got.RedeemedBy != "recep" ||
		got.RedeemedAt == nil || !got.RedeemedAt.Equal(e.clock) || got.State != "redeemed" {
		t.Fatalf("vale usado: %+v", got)
	}
	// Ya usado.
	_, err = e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "", "", "")
	wantMsg(t, err, domain.ErrAlreadyExists, "Ese premio ya se usó")
}

func TestLoyaltyService_redeem_expired(t *testing.T) {
	e := newLoyEnv(t)
	days := 1
	e.saveProgram(t, true, 2, &days)
	e.setVisits(e.lia.ID, 4)
	a, b := e.grantOne(t, ""), e.grantOne(t, "")
	exp := *a.ExpiresAt
	// Un microsegundo antes de expires_at el vale todavía vale...
	e.clock = exp.Add(-time.Microsecond)
	if _, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, a.ID, "", "", ""); err != nil {
		t.Fatalf("antes de vencer: %v", err)
	}
	// ... y EXACTO en expires_at ya venció (la misma regla del UPDATE).
	e.clock = exp
	_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, b.ID, "", "", "")
	wantMsg(t, err, domain.ErrAlreadyExists, "El premio venció")
	if e.rewards.rewards[1].RedeemedAt != nil {
		t.Fatal("el vale vencido se usó")
	}
}

func TestLoyaltyService_redeem_professionalAndNote(t *testing.T) {
	e := newLoyEnv(t)
	r := e.withReward(t, nil)
	foreign := miembro(e.mems, "prof2", "s2", domain.RoleProfessional)
	_ = foreign
	for _, pid := range []string{"nadie", "prof2", "recep", "a\x00b"} {
		_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, pid, "", "")
		if err == nil || !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("profesional %q: %v, quería 400", pid, err)
		}
	}
	for _, note := range []string{"a\nb", "a\x00b", strings.Repeat("x", 201)} {
		_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "", note, "")
		if err == nil || !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("nota %q: %v, quería 400", note, err)
		}
	}
	if e.rewards.rewards[0].RedeemedAt != nil {
		t.Fatal("un rechazo usó el vale")
	}
}

// D7: el programa apagado no usa vales, pero los entregados se conservan.
func TestLoyaltyService_redeem_disabledProgram(t *testing.T) {
	e := newLoyEnv(t)
	r := e.withReward(t, nil)
	e.saveProgram(t, false, 2, nil)
	_, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "", "", "")
	wantMsg(t, err, domain.ErrAlreadyExists, "La fidelidad no está activada")
	if len(e.rewards.rewards) != 1 || e.rewards.rewards[0].RedeemedAt != nil {
		t.Fatal("el vale no se conservó intacto")
	}
	// Al volver a encenderlo el vale sigue ahí y se puede usar.
	e.saveProgram(t, true, 2, nil)
	if _, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, r.ID, "", "", ""); err != nil {
		t.Fatalf("usar tras reactivar: %v", err)
	}
}

// D7: el repetido por client_op_id devuelve el estado actual SIN usar otra vez.
func TestLoyaltyService_redeem_idempotentByOp(t *testing.T) {
	e := newLoyEnv(t)
	e.saveProgram(t, true, 2, nil)
	e.setVisits(e.lia.ID, 4)
	a, b := e.grantOne(t, ""), e.grantOne(t, "")
	first, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, a.ID, "", "", "op-1")
	if err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(time.Hour)
	again, err := e.svc.Redeem(e.recep, "s1", e.lia.ID, a.ID, "", "", " op-1 ")
	if err != nil || again.ID != first.ID || again.RedeemedAt == nil || !again.RedeemedAt.Equal(first.RedeemedAt.UTC()) || again.State != "redeemed" {
		t.Fatalf("el repetido: %+v %v", again, err)
	}
	// El mismo id para OTRO vale es 409 y no usa el vale.
	_, err = e.svc.Redeem(e.recep, "s1", e.lia.ID, b.ID, "", "", "op-1")
	wantMsg(t, err, domain.ErrAlreadyExists, "Esa operación ya se usó en otro pedido")
	if e.rewards.rewards[1].RedeemedAt != nil {
		t.Fatal("el op id reusado usó otro vale")
	}
	_, err = e.svc.Redeem(e.recep, "s1", e.lia.ID, b.ID, "", "", "a\x00b")
	wantMsg(t, err, domain.ErrValidation, "El identificador de la operación no es válido")
}
