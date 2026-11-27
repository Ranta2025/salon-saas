package app

// F2d3: cubrir una ausencia reasignando los turnos a otra profesional.
// Se prueba el caso de uso completo con fakes (sin DB ni HTTP): quién puede
// cubrir, qué mensaje recibe el salón, el todo-o-nada de "Reasignar todo", el
// aviso interno y el correo a la clienta, y que cancelar registre el motivo.
import (
	"errors"
	"strings"
	"testing"
	"time"

	bkg "salon-saas/internal/bookings"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/schedules"
	"salon-saas/internal/testutil"
)

// --- fakes nuevos de la capa app ---

// fakeBookingTx es la transacción del "Reasignar todo" sobre el fake de turnos,
// con la MISMA semántica que Postgres: si fn falla no queda nada escrito
// (rollback = volver a la foto) y el UPDATE respeta la EXCLUDE (dos turnos
// reservados del mismo profesional no se solapan).
type fakeBookingTx struct {
	bookings  *fb
	txs       int
	lastSalon string
}

type fakeBookingTxView struct{ parent *fakeBookingTx }

func (v fakeBookingTxView) Save(b domain.Booking) error { return v.parent.bookings.Save(b) }
func (v fakeBookingTxView) FindByClientOpID(salonID, op string) (domain.Booking, error) {
	return v.parent.bookings.FindByClientOpID(salonID, op)
}
func (v fakeBookingTxView) FindByID(salonID, id string) (domain.Booking, error) {
	return v.parent.bookings.FindByID(salonID, id)
}
func (v fakeBookingTxView) FindOverlapping(salonID, professionalID string, start, end time.Time) ([]domain.Booking, error) {
	return v.parent.bookings.FindOverlapping(salonID, professionalID, start, end)
}
func (v fakeBookingTxView) ListRange(salonID string, from, to time.Time) ([]domain.Booking, error) {
	return v.parent.bookings.ListRange(salonID, from, to)
}

// Update simula la EXCLUDE de la tabla bookings (002): el segundo turno del
// mismo profesional en el mismo horario no entra.
func (v fakeBookingTxView) Update(b domain.Booking) error {
	busy, err := v.parent.bookings.FindOverlapping(b.SalonID, b.ProfessionalID, b.StartsAt, b.EndsAt)
	if err != nil {
		return err
	}
	for _, other := range busy {
		if other.ID != b.ID {
			return domain.ErrAlreadyExists
		}
	}
	return v.parent.bookings.Update(b)
}

func (t *fakeBookingTx) InSalonTx(salonID string, fn func(ports.BookingTx) error) error {
	t.txs++
	t.lastSalon = salonID
	snapshot := append([]domain.Booking(nil), t.bookings.All...)
	if err := fn(fakeBookingTxView{parent: t}); err != nil {
		t.bookings.All = snapshot // rollback
		return err
	}
	return nil
}

var _ ports.BookingTxRunner = (*fakeBookingTx)(nil)

type coverEnv struct {
	svc      schedules.CoverageService
	bookings *fb
	timeOff  *testutil.FakeTimeOff
	hours    *testutil.FakeWeeklyHours
	profs    *testutil.FakeProfessionalServices
	staff    *testutil.FakeStaffLister
	services *testutil.FakeServices
	mems     *fakeMemberships
	notifier *fakeNotifier
	events   *fakeEvents
	mailer   *fakeMailer
	emails   *fakeBookingEmails
	tx       *fakeBookingTx

	owner     domain.Membership
	admin     domain.Membership
	reception domain.Membership
	ana       domain.Membership
	bea       domain.Membership
	duena     domain.Membership
	otro      domain.Membership // otra persona del salón s2
	ownerS2   domain.Membership // dueña de OTRO salón (tenant isolation)
}

func newCoverEnv(t *testing.T) *coverEnv {
	t.Helper()
	salons := &fakeSalons{BySlug: map[string]domain.Salon{
		"navajas-oro": {ID: "s1", Slug: "navajas-oro", Name: "Navajas de Oro"},
	}}
	settings := &fakeSettings{BySalon: map[string]domain.SalonSettings{"s1": {
		SalonID: "s1", Timezone: "America/Mexico_City",
		OpenTime: "09:00", CloseTime: "19:00", SlotStepMin: 30,
	}}}
	services := &testutil.FakeServices{}
	corte, err := domain.NewService("s1", "Corte", 45, 150000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	_ = services.Save(corte)
	keratina, err := domain.NewService("s1", "Keratina", 60, 400000, 20)
	if err != nil {
		t.Fatalf("servicio: %v", err)
	}
	_ = services.Save(keratina)

	staff := &testutil.FakeStaffLister{BySalon: map[string][]domain.StaffMember{"s1": {
		{ID: "ana", Name: "Ana", Role: domain.RoleProfessional, Attends: true},
		{ID: "bea", Name: "Bea", Role: domain.RoleProfessional, Attends: true},
		{ID: "duena", Name: "Dueña", Role: domain.RoleOwner, Attends: true},
		{ID: "recep", Name: "Recep", Role: domain.RoleReception},
	}}}
	profs := &testutil.FakeProfessionalServices{
		Staff: staff,
		ByService: map[string][]string{
			"s1|" + corte.ID:    {"ana", "bea", "duena"},
			"s1|" + keratina.ID: {"ana", "bea"},
		},
	}
	mems := &fakeMemberships{}
	env := &coverEnv{
		bookings:  &fb{},
		timeOff:   &testutil.FakeTimeOff{},
		hours:     &testutil.FakeWeeklyHours{MemberSeg: map[string][]domain.HoursSegment{}},
		profs:     profs,
		staff:     staff,
		services:  services,
		mems:      mems,
		notifier:  &fakeNotifier{},
		events:    &fakeEvents{},
		mailer:    &fakeMailer{},
		emails:    &fakeBookingEmails{},
		owner:     domain.Membership{UserID: "u-own", SalonID: "s1", Role: domain.RoleOwner, Attends: true},
		admin:     domain.Membership{UserID: "u-adm", SalonID: "s1", Role: domain.RoleAdmin},
		reception: domain.Membership{UserID: "recep", SalonID: "s1", Role: domain.RoleReception},
		ana:       domain.Membership{UserID: "ana", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		bea:       domain.Membership{UserID: "bea", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		duena:     domain.Membership{UserID: "duena", SalonID: "s1", Role: domain.RoleOwner, Attends: true},
		otro:      domain.Membership{UserID: "u-otro", SalonID: "s2", Role: domain.RoleProfessional, Attends: true},
		ownerS2:   domain.Membership{UserID: "u-own2", SalonID: "s2", Role: domain.RoleOwner, Attends: true},
	}
	for _, m := range []domain.Membership{env.owner, env.admin, env.reception, env.ana, env.bea, env.duena, env.otro, env.ownerS2} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	env.tx = &fakeBookingTx{bookings: env.bookings}
	env.svc = schedules.CoverageService{
		Bookings: env.bookings, Memberships: mems, Staff: staff, Professionals: profs,
		Hours: env.hours, TimeOff: env.timeOff, Services: services, Settings: settings,
		Salons: salons, Tx: env.tx, Events: env.events, Notifier: env.notifier,
		Mailer: env.mailer, Emails: env.emails,
	}
	return env
}

// localAt arma un instante de la zona del salón (America/Mexico_City) del
// 2026-12-01, un martes.
func localAt(t *testing.T, hour, minute int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	return time.Date(2026, 12, 1, hour, minute, 0, 0, loc)
}

// seedBooking deja un turno reservado del salón de prueba.
func seedBooking(t *testing.T, env *coverEnv, id, userID string, start time.Time, minutes int, serviceID string) domain.Booking {
	t.Helper()
	b, err := domain.NewBooking("s1", userID, "Lía", "555", start, start.Add(time.Duration(minutes)*time.Minute))
	if err != nil {
		t.Fatalf("turno %s: %v", id, err)
	}
	b.ID = id
	b.ServiceID = serviceID
	if err := env.bookings.Save(b); err != nil {
		t.Fatalf("guardar turno: %v", err)
	}
	return b
}

// seedAbsence deja la ausencia de la profesional (F2d2) sin pasar por el alta.
func seedAbsence(t *testing.T, env *coverEnv, id, userID string, start, end time.Time) domain.TimeOff {
	t.Helper()
	off := domain.TimeOff{
		ID: id, SalonID: "s1", UserID: userID, StartsAt: start.UTC(), EndsAt: end.UTC(),
		Reason: "Vacaciones", CreatedAt: time.Now().UTC(),
	}
	env.timeOff.All = append(env.timeOff.All, off)
	return off
}

func corteID(t *testing.T, env *coverEnv) string {
	t.Helper()
	list, err := env.services.List("s1", false)
	if err != nil {
		t.Fatalf("servicios: %v", err)
	}
	for _, s := range list {
		if s.Name == "Corte" {
			return s.ID
		}
	}
	t.Fatal("falta el servicio Corte")
	return ""
}

func keratinaID(t *testing.T, env *coverEnv) string {
	t.Helper()
	list, _ := env.services.List("s1", false)
	for _, s := range list {
		if s.Name == "Keratina" {
			return s.ID
		}
	}
	t.Fatal("falta el servicio Keratina")
	return ""
}

// conflictOf extrae el error con el detalle de la negativa todo-o-nada.
func conflictOf(t *testing.T, err error) *schedules.CoverageConflict {
	t.Helper()
	var c *schedules.CoverageConflict
	if !errors.As(err, &c) {
		t.Fatalf("esperaba un *schedules.CoverageConflict, dio %v", err)
	}
	return c
}

// candidateReason devuelve el motivo con el que la lista marca a una candidata
// ("" = puede tomar el turno).
func candidateReason(b schedules.CoverageBooking, professionalID string) string {
	for _, c := range b.Candidates {
		if c.ProfessionalID == professionalID {
			return c.Reason
		}
	}
	return "no está en la lista"
}

// S-F2d3-2: GET /salons/:id/absences. Para cada persona ausente, sus turnos
// reservados que se pisan, con las candidatas y el motivo por el que no sirven.
func TestCoverage_absences_lists_bookings_and_candidates(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, corte)
	// Bea ya tiene un turno a las 11: no puede cubrir ESE turno.
	seedBooking(t, env, "b-bea", "bea", localAt(t, 11, 0), 45, corte)

	list, err := env.svc.Absences(env.owner, "s1", localAt(t, 0, 0), localAt(t, 23, 0))
	if err != nil {
		t.Fatalf("ausencias: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("esperaba una ausencia, dio %d", len(list))
	}
	got := list[0]
	if got.TimeOff.ID != "off-1" || got.UserName != "Ana" || got.TimeOff.Reason != "Vacaciones" {
		t.Fatalf("ausencia: %+v", got)
	}
	if len(got.Bookings) != 2 {
		t.Fatalf("turnos de la ausencia: %d", len(got.Bookings))
	}
	first := got.Bookings[0]
	if first.Booking.ID != "b-10" || first.ServiceName != "Corte" {
		t.Fatalf("primer turno: %+v", first)
	}
	// Ana (ausente) y recepción (no atiende) quedan marcadas; Bea sirve.
	if motivo := candidateReason(first, "ana"); motivo != domain.MsgCoverNotScheduled {
		t.Fatalf("motivo de la ausente: %q", motivo)
	}
	if motivo := candidateReason(first, "recep"); motivo != domain.MsgCoverNotBookable {
		t.Fatalf("motivo de recepción: %q", motivo)
	}
	if motivo := candidateReason(first, "bea"); motivo != "" {
		t.Fatalf("Bea puede cubrir las 10:00: %q", motivo)
	}
	// A las 11:00 Bea ya tiene turno: el mismo 409 del POST, con el detalle.
	second := got.Bookings[1]
	if motivo := candidateReason(second, "bea"); motivo != "Ya tiene un turno a esa hora" {
		t.Fatalf("motivo de la ocupada: %q", motivo)
	}
}

// S-F2d3-2: el motivo es privado: solo dueño/administración lee las ausencias
// por acá, y el salón de la ruta es el único que se mira (tenant isolation).
func TestCoverage_absences_requires_manage_and_scopes_salon(t *testing.T) {
	env := newCoverEnv(t)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	if err := env.timeOff.Save(domain.TimeOff{
		ID: "off-s2", SalonID: "s2", UserID: "u-otro",
		StartsAt: localAt(t, 10, 0), EndsAt: localAt(t, 13, 0), Reason: "privado",
	}); err != nil {
		t.Fatalf("ausencia de otro salón: %v", err)
	}

	for _, actor := range []domain.Membership{env.reception, env.ana} {
		if _, err := env.svc.Absences(actor, "s1", localAt(t, 0, 0), localAt(t, 23, 0)); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s no administra: %v", actor.Role, err)
		}
	}
	// Un actor que no es de este salón tampoco.
	ajeno := domain.Membership{UserID: "u-nadie", SalonID: "s1", Role: domain.RoleOwner}
	if _, err := env.svc.Absences(ajeno, "s1", localAt(t, 0, 0), localAt(t, 23, 0)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("actor ajeno: %v", err)
	}

	list, err := env.svc.Absences(env.admin, "s1", localAt(t, 0, 0), localAt(t, 23, 0))
	if err != nil {
		t.Fatalf("ausencias: %v", err)
	}
	if len(list) != 1 || list[0].TimeOff.ID != "off-1" {
		t.Fatalf("solo las ausencias del salón: %+v", list)
	}
}

// S-F2d3-2/4: "Reasignar todo" mueve los turnos de la ausencia a otra
// profesional, con el MISMO horario, y avisa (aviso interno + correo).
func TestCoverage_reassign_absence_moves_all(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, corte)
	env.emails.saved = append(env.emails.saved,
		savedBookingEmail{SalonID: "s1", BookingID: "b-10", Email: "lia@mail.com"},
		savedBookingEmail{SalonID: "s1", BookingID: "b-11", Email: "lia@mail.com"})

	res, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "bea", nil)
	if err != nil {
		t.Fatalf("reasignar: %v", err)
	}
	if res.Reassigned != 2 || len(res.Bookings) != 2 {
		t.Fatalf("reasignados: %+v", res)
	}
	for _, b := range res.Bookings {
		if b.ProfessionalID != "bea" {
			t.Fatalf("%s quedó en %q", b.ID, b.ProfessionalID)
		}
	}
	// El turno conserva su hora y su clienta (S-F2d3-4).
	if !res.Bookings[0].StartsAt.Equal(localAt(t, 10, 0).UTC()) || res.Bookings[0].ClientName != "Lía" {
		t.Fatalf("el turno se movió de hora: %+v", res.Bookings[0])
	}
	// Aviso interno: dos, uno por turno, con quién lo atiende ahora.
	if env.notifier.calls != 2 {
		t.Fatalf("avisos: %d", env.notifier.calls)
	}
	if texto := env.notifier.bodies[0]; !strings.Contains(texto, "Bea") {
		t.Fatalf("el aviso interno tiene que decir quién atiende: %q", texto)
	}
	// Correo a la clienta (T12): dos, diciendo que el turno se mantiene.
	sent := env.mailer.waitCount(t, 2)
	if !strings.Contains(sent[0].Text, "Bea") || !strings.Contains(sent[0].Text, "mantiene") {
		t.Fatalf("correo de la reasignación: %+v", sent[0])
	}
	if sent[0].To != "lia@mail.com" {
		t.Fatalf("destinatario: %q", sent[0].To)
	}
	// Los calendarios abiertos se refrescan.
	if _, n := env.events.typed("booking.updated"); n != 2 {
		t.Fatalf("esperaba dos booking.updated, hubo %d", n)
	}
}

// S-F2d3-3: si UN turno no se puede reasignar no se reasigna NINGUNO. El fake
// hace el rollback como la transacción de Postgres.
func TestCoverage_reassign_absence_is_all_or_nothing(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, corte)
	// Bea está libre a las 10 pero ocupada a las 11: la EXCLUDE corta el segundo.
	seedBooking(t, env, "b-bea", "bea", localAt(t, 11, 0), 45, corte)

	_, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "bea", nil)
	conflict := conflictOf(t, err)
	if !errors.Is(conflict, domain.ErrAlreadyExists) || conflict.Error() != "Ya tiene un turno a esa hora" {
		t.Fatalf("conflicto: %v", conflict)
	}
	if len(conflict.Bookings) != 1 || conflict.Bookings[0].ID != "b-11" {
		t.Fatalf("el detalle tiene que traer el turno que no entró: %+v", conflict.Bookings)
	}
	// Todo o nada: el primero tampoco se movió.
	for _, id := range []string{"b-10", "b-11"} {
		b, err := env.bookings.FindByID("s1", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if b.ProfessionalID != "ana" {
			t.Fatalf("%s se movió igual: %+v", id, b)
		}
	}
	if env.notifier.calls != 0 || env.mailer.count() != 0 {
		t.Fatalf("un fallo no puede avisar nada: %d avisos, %d correos", env.notifier.calls, env.mailer.count())
	}
	if _, n := env.events.typed("booking.updated"); n != 0 {
		t.Fatalf("un fallo no publica eventos: %d", n)
	}
}

// S-F2d3-3: las reglas del destino, cada una con su mensaje exacto.
func TestCoverage_reassign_absence_candidate_rules(t *testing.T) {
	casos := []struct {
		name  string
		kind  error
		msg   string
		setup func(t *testing.T, env *coverEnv) string // devuelve el destino
	}{
		{
			name: "no atiende clientes (recepción)", kind: domain.ErrValidation,
			msg:   domain.MsgCoverNotBookable,
			setup: func(*testing.T, *coverEnv) string { return "recep" },
		},
		{
			name: "de otro salón", kind: domain.ErrValidation,
			msg:   domain.MsgCoverNotBookable,
			setup: func(*testing.T, *coverEnv) string { return "u-otro" },
		},
		{
			name: "ausente en ese período", kind: domain.ErrValidation,
			msg: domain.MsgCoverNotScheduled,
			setup: func(t *testing.T, env *coverEnv) string {
				seedAbsence(t, env, "off-bea", "bea", localAt(t, 10, 0), localAt(t, 13, 0))
				return "bea"
			},
		},
		{
			name: "de descanso ese día (F2d1)", kind: domain.ErrValidation,
			msg: domain.MsgCoverNotScheduled,
			setup: func(t *testing.T, env *coverEnv) string {
				// Bea solo trabaja los miércoles: el martes 1 es su día libre.
				env.hours.MemberSeg["s1|bea"] = []domain.HoursSegment{{Weekday: 3, Start: "09:00", End: "19:00"}}
				return "bea"
			},
		},
		{
			name: "no hace ese servicio (F2c)", kind: domain.ErrValidation,
			msg: domain.MsgCoverWrongService,
			setup: func(t *testing.T, env *coverEnv) string {
				// La dueña hace cortes pero no keratinas.
				seedBooking(t, env, "b-ker", "ana", localAt(t, 10, 0), 60, keratinaID(t, env))
				return "duena"
			},
		},
	}
	for _, c := range casos {
		t.Run(c.name, func(t *testing.T) {
			env := newCoverEnv(t)
			seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
			if c.name != "no hace ese servicio (F2c)" {
				seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corteID(t, env))
			}
			destino := c.setup(t, env)
			_, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", destino, nil)
			conflict := conflictOf(t, err)
			if !errors.Is(conflict, c.kind) || conflict.Error() != c.msg {
				t.Fatalf("%s: %v (esperaba %q)", c.name, conflict, c.msg)
			}
			// Ningún pedido inválido deja un turno movido.
			for _, b := range env.bookings.All {
				if b.ProfessionalID != "ana" {
					t.Fatalf("turno movido por un pedido inválido: %+v", b)
				}
			}
			if env.notifier.calls != 0 || env.mailer.count() != 0 {
				t.Fatalf("un rechazo no avisa nada")
			}
		})
	}
}

// S-F2d3-2/3: se pueden mover TODOS los turnos de la ausencia o solo los que el
// salón elija; un turno que no es de esa ausencia es "Ese turno no es de esa
// ausencia" y no se mueve nada.
func TestCoverage_reassign_absence_selected_bookings(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, corte)
	seedBooking(t, env, "b-bea", "bea", localAt(t, 12, 0), 45, corte)

	// El turno de Bea no pertenece a la ausencia de Ana.
	_, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "duena", []string{"b-bea"})
	conflict := conflictOf(t, err)
	if !errors.Is(conflict, domain.ErrValidation) || conflict.Error() != domain.MsgCoverNotThisAbsence {
		t.Fatalf("turno ajeno a la ausencia: %v", conflict)
	}
	if b, _ := env.bookings.FindByID("s1", "b-bea"); b.ProfessionalID != "bea" {
		t.Fatalf("no se toca el turno de otra persona: %+v", b)
	}

	// Solo las 10:00: las 11:00 sigue con Ana.
	res, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "duena", []string{"b-10"})
	if err != nil {
		t.Fatalf("reasignar uno: %v", err)
	}
	if res.Reassigned != 1 || res.Bookings[0].ID != "b-10" {
		t.Fatalf("reasignados: %+v", res)
	}
	if b, _ := env.bookings.FindByID("s1", "b-11"); b.ProfessionalID != "ana" {
		t.Fatalf("las 11:00 no estaban en la lista: %+v", b)
	}
}

// S-F2d3-3 + tenant isolation: la ausencia de otro salón no existe acá.
func TestCoverage_reassign_absence_other_salon_is_not_found(t *testing.T) {
	env := newCoverEnv(t)
	if err := env.timeOff.Save(domain.TimeOff{
		ID: "off-s2", SalonID: "s2", UserID: "u-otro",
		StartsAt: localAt(t, 10, 0), EndsAt: localAt(t, 13, 0),
	}); err != nil {
		t.Fatalf("ausencia: %v", err)
	}
	if _, err := env.svc.ReassignAbsence(env.owner, "s1", "off-s2", "bea", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ausencia de otro salón: %v", err)
	}
	// Y la propia profesional no administra: no reasigna a nadie.
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	if _, err := env.svc.ReassignAbsence(env.ana, "s1", "off-1", "bea", nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("una profesional no reasigna: %v", err)
	}
}

// S-F2d3-2/3: un turno suelto se reasigna igual. Si NO tiene servicio, cualquier
// reservable que esté de turno sirve; si ya no está reservado, 409.
func TestCoverage_reassign_single_booking(t *testing.T) {
	env := newCoverEnv(t)
	// Sin service_id: la agenda lo cargó a mano. La dueña no hace cortes, pero
	// el turno no pide oficio.
	seedBooking(t, env, "b-libre", "ana", localAt(t, 10, 0), 30, "")
	env.emails.saved = append(env.emails.saved,
		savedBookingEmail{SalonID: "s1", BookingID: "b-libre", Email: "lia@mail.com"})

	res, err := env.svc.ReassignBooking(env.owner, "s1", "b-libre", "duena")
	if err != nil {
		t.Fatalf("reasignar suelto: %v", err)
	}
	if res.Reassigned != 1 || res.Bookings[0].ProfessionalID != "duena" {
		t.Fatalf("resultado: %+v", res)
	}
	if env.notifier.calls != 1 {
		t.Fatalf("aviso interno: %d", env.notifier.calls)
	}
	sent := env.mailer.waitCount(t, 1)
	if !strings.Contains(sent[0].Text, "Dueña") {
		t.Fatalf("el correo tiene que decir quién atiende: %+v", sent[0])
	}

	// Un turno que ya no está reservado no se reasigna.
	cerrado := seedBooking(t, env, "b-cancel", "ana", localAt(t, 12, 0), 30, "")
	cerrado.Status = domain.BookingCancelled
	if err := env.bookings.Update(cerrado); err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	_, err = env.svc.ReassignBooking(env.owner, "s1", "b-cancel", "bea")
	conflict := conflictOf(t, err)
	if !errors.Is(conflict, domain.ErrAlreadyExists) || conflict.Error() != domain.MsgCoverNotBooked {
		t.Fatalf("turno cerrado: %v", conflict)
	}
	// Un turno de otro salón no existe (404), ni siquiera para su dueña.
	if _, err := env.svc.ReassignBooking(env.ownerS2, "s2", "b-libre", "bea"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("turno de otro salón: %v", err)
	}
}

// S-F2d3-2: cancelar desde la reasignación registra el motivo en el aviso
// interno y avisa a la clienta que el salón la contacta.
func TestCoverage_cancel_booking_records_reason(t *testing.T) {
	env := newCoverEnv(t)
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, "")
	env.emails.saved = append(env.emails.saved,
		savedBookingEmail{SalonID: "s1", BookingID: "b-10", Email: "lia@mail.com"})

	cancelled, err := env.svc.CancelBooking(env.owner, "s1", "b-10", "  se enfermó ")
	if err != nil {
		t.Fatalf("cancelar: %v", err)
	}
	if cancelled.Status != domain.BookingCancelled {
		t.Fatalf("estado: %q", cancelled.Status)
	}
	if env.notifier.calls != 1 || !strings.Contains(env.notifier.bodies[0], "se enfermó") {
		t.Fatalf("el motivo tiene que quedar en el aviso interno: %+v", env.notifier.bodies)
	}
	sent := env.mailer.waitCount(t, 1)
	if !strings.Contains(sent[0].Text, "cancelado") || !strings.Contains(sent[0].Text, "se va a comunicar") {
		t.Fatalf("correo de la cancelación: %+v", sent[0])
	}
	if _, n := env.events.typed("booking.updated"); n != 1 {
		t.Fatalf("el calendario se tiene que enterar: %d", n)
	}
	// Cancelar es de dueño/administración: la profesional no.
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, "")
	if _, err := env.svc.CancelBooking(env.ana, "s1", "b-11", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("una profesional no cancela por acá: %v", err)
	}
	// El motivo no puede traer saltos de línea.
	_, err = env.svc.CancelBooking(env.owner, "s1", "b-11", "se\nenfermó")
	if err == nil || !strings.Contains(err.Error(), "saltos de línea") {
		t.Fatalf("motivo inválido: %v", err)
	}
}

// --- PRESERVE: lo que ya andaba y comparte código con F2d3 ---

// PRESERVE: PATCH del turno (estado y reprogramación) sigue igual: el motivo de
// compartir la lectura de turnos y el aviso.
func TestCoverage_preserve_booking_patch_status_and_reschedule(t *testing.T) {
	env := newCoverEnv(t)
	bookSvc := bkg.BookingService{Bookings: env.bookings, Memberships: env.mems, Settings: env.svc.Settings, Notifier: env.notifier}
	b := seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, "")

	// Estado: cancelar por el PATCH clásico.
	status := domain.BookingCancelled
	got, err := bookSvc.Update(env.reception, "s1", b.ID, bkg.BookingPatch{Status: &status})
	if err != nil {
		t.Fatalf("PATCH estado: %v", err)
	}
	if got.Status != domain.BookingCancelled || env.notifier.calls != 1 {
		t.Fatalf("PATCH estado: %+v (%d avisos)", got, env.notifier.calls)
	}

	// Reprogramación: mover horario del turno reservado.
	reservado := seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, "")
	newStart := localAt(t, 15, 0)
	newEnd := localAt(t, 15, 45)
	moved, err := bookSvc.Update(env.owner, "s1", reservado.ID, bkg.BookingPatch{StartsAt: &newStart, EndsAt: &newEnd})
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if !moved.StartsAt.Equal(newStart.UTC()) || moved.Status != domain.BookingBooked {
		t.Fatalf("reprogramado: %+v", moved)
	}
}

// PRESERVE: la disponibilidad pública no cambia por F2d3 (mismos huecos).
func TestCoverage_preserve_availability_slots(t *testing.T) {
	env := wideEnv(t) // 09:00-19:00, corte de 45, paso 30, dos profesionales
	day, err := env.avail.Day("navajas-oro", "2026-12-01", env.corte.ID, "")
	if err != nil {
		t.Fatalf("disponibilidad: %v", err)
	}
	if n := len(slotsOf(t, day.Slots, "prof2")); n != 19 {
		t.Fatalf("prof2 conserva sus 19 arranques, tiene %d", n)
	}
	if n := len(slotsOf(t, day.Slots, "prof1")); n != 19 {
		t.Fatalf("prof1 conserva sus 19 arranques, tiene %d", n)
	}
}

// PRESERVE: la reserva pública sigue dejando el aviso interno y el correo de
// confirmación igual que antes (la plantilla de F2d3 es otra).
func TestCoverage_preserve_public_booking_notice_and_email(t *testing.T) {
	env := newPublicEnv(t)
	env.book.Mailer = &fakeMailer{}
	env.book.Emails = &fakeBookingEmails{}
	in := publicInput()
	in.ServiceID = env.corte.ID
	in.ClientEmail = "lia@mail.com"

	b, err := env.book.Book("navajas-oro", in)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if env.notifier.calls != 1 {
		t.Fatalf("aviso interno: %d", env.notifier.calls)
	}
	mailer := env.book.Mailer.(*fakeMailer)
	sent := mailer.waitCount(t, 1)
	if !strings.Contains(sent[0].Subject, "Turno confirmado") || !strings.Contains(sent[0].Text, b.ClientName) {
		t.Fatalf("confirmación: %+v", sent[0])
	}
}

// El pedido sin destino sigue siendo "No hay a quién reasignar" (400): no es lo
// mismo que una candidata ocupada, que tiene su propio motivo.
func TestCoverage_reassign_without_target_keeps_message(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)

	_, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "", nil)
	if !errors.Is(err, domain.ErrValidation) || err.Error() != domain.MsgCoverNoCandidate {
		t.Fatalf("sin destino: %v", err)
	}
}

// S-F2d3-6: la reasignación guarda quién tenía el turno y lo conserva en los
// cambios siguientes; si el turno vuelve a esa persona se limpia. Se comprueba
// en el turno devuelto y en lo que quedó guardado.
func TestCoverage_reassign_tracks_who_covered(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)

	res, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "bea", nil)
	if err != nil {
		t.Fatalf("Ana -> Bea: %v", err)
	}
	if res.Bookings[0].CoveredFrom != "ana" {
		t.Fatalf("la respuesta tiene que decir que lo tenía Ana: %q", res.Bookings[0].CoveredFrom)
	}
	stored, err := env.bookings.FindByID("s1", "b-10")
	if err != nil {
		t.Fatalf("turno guardado: %v", err)
	}
	if stored.ProfessionalID != "bea" || stored.CoveredFrom != "ana" {
		t.Fatalf("lo guardado: %+v", stored)
	}

	// Bea -> Dueña: sigue diciendo Ana (no la última que lo tuvo).
	if _, err := env.svc.ReassignBooking(env.owner, "s1", "b-10", "duena"); err != nil {
		t.Fatalf("Bea -> Dueña: %v", err)
	}
	if stored, _ = env.bookings.FindByID("s1", "b-10"); stored.CoveredFrom != "ana" {
		t.Fatalf("el segundo cambio conserva a Ana: %q", stored.CoveredFrom)
	}

	// Un turno que vuelve a quien figura en covered_from: se limpia.
	seedBooking(t, env, "b-vuelta", "bea", localAt(t, 14, 0), 45, corte)
	if _, err := env.svc.ReassignBooking(env.owner, "s1", "b-vuelta", "duena"); err != nil {
		t.Fatalf("Bea -> Dueña: %v", err)
	}
	if stored, _ = env.bookings.FindByID("s1", "b-vuelta"); stored.CoveredFrom != "bea" {
		t.Fatalf("covered_from de la vuelta: %q", stored.CoveredFrom)
	}
	if _, err := env.svc.ReassignBooking(env.owner, "s1", "b-vuelta", "bea"); err != nil {
		t.Fatalf("Dueña -> Bea: %v", err)
	}
	if stored, _ = env.bookings.FindByID("s1", "b-vuelta"); stored.CoveredFrom != "" {
		t.Fatalf("el turno volvió a Bea: covered_from se limpia, quedó %q", stored.CoveredFrom)
	}
}

// PRESERVE S-F2d3-6: reprogramar y cambiar estado desde el PATCH clásico no
// tocan covered_from (solo la reasignación lo escribe).
func TestBooking_patch_leaves_covered_from(t *testing.T) {
	bookings := &fb{}
	mems := &fakeMemberships{}
	svc := bkg.BookingService{Bookings: bookings, Memberships: mems}
	rec := recepcionista(mems)
	miembro(mems, "prof1", "s1", domain.RoleProfessional)

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	b, err := domain.NewBooking("s1", "prof1", "Lía", "555", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("turno: %v", err)
	}
	b.ID = "b-1"
	b.CoveredFrom = "prof0"
	if err := bookings.Save(b); err != nil {
		t.Fatalf("guardar: %v", err)
	}

	newStart := start.Add(24 * time.Hour)
	newEnd := newStart.Add(45 * time.Minute)
	updated, err := svc.Update(rec, "s1", "b-1", bkg.BookingPatch{StartsAt: &newStart, EndsAt: &newEnd})
	if err != nil {
		t.Fatalf("reprogramar: %v", err)
	}
	if updated.CoveredFrom != "prof0" {
		t.Fatalf("reprogramar no toca covered_from: %q", updated.CoveredFrom)
	}
	status := domain.BookingDone
	updated, err = svc.Update(rec, "s1", "b-1", bkg.BookingPatch{Status: &status})
	if err != nil {
		t.Fatalf("cambiar estado: %v", err)
	}
	if updated.CoveredFrom != "prof0" {
		t.Fatalf("cambiar estado no toca covered_from: %q", updated.CoveredFrom)
	}
	if stored, _ := bookings.FindByID("s1", "b-1"); stored.CoveredFrom != "prof0" {
		t.Fatalf("lo guardado: %q", stored.CoveredFrom)
	}
}

// PRESERVE S-F2d3-6: si el "Reasignar todo" falla, el rollback tampoco deja
// covered_from escrito en el turno que llegó a moverse.
func TestCoverage_reassign_all_or_nothing_keeps_covered_from(t *testing.T) {
	env := newCoverEnv(t)
	corte := corteID(t, env)
	seedAbsence(t, env, "off-1", "ana", localAt(t, 10, 0), localAt(t, 13, 0))
	seedBooking(t, env, "b-10", "ana", localAt(t, 10, 0), 45, corte)
	seedBooking(t, env, "b-11", "ana", localAt(t, 11, 0), 45, corte)
	seedBooking(t, env, "b-bea", "bea", localAt(t, 11, 0), 45, corte)

	if _, err := env.svc.ReassignAbsence(env.owner, "s1", "off-1", "bea", nil); err == nil {
		t.Fatal("el segundo turno no entra: la reasignación tenía que fallar")
	}
	for _, id := range []string{"b-10", "b-11"} {
		stored, err := env.bookings.FindByID("s1", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if stored.CoveredFrom != "" {
			t.Fatalf("%s quedó con covered_from %q después del rollback", id, stored.CoveredFrom)
		}
	}
}
