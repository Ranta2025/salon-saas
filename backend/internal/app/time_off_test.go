package app

// F2d2: el caso de uso de las ausencias. Reglas probadas acá (sin DB ni HTTP):
// quién las ve, qué motivo ve cada rol, quién las carga y borra, las
// validaciones y los turnos en conflicto del alta (que NO se cancelan solos).
import (
	"errors"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

type timeOffEnv struct {
	svc       TimeOffService
	timeOff   *fakeTimeOff
	bookings  *fb
	mems      *fakeMemberships
	owner     domain.Membership
	admin     domain.Membership
	reception domain.Membership
	pro1      domain.Membership
	pro2      domain.Membership
	// proOtro pertenece a OTRO salón (tenant isolation).
	proOtro domain.Membership
}

// newTimeOffEnv: salón s1 con dueño, administración, recepción y dos
// profesionales; s2 con una profesional. Los turnos viven en el fake de siempre.
func newTimeOffEnv(t *testing.T) *timeOffEnv {
	t.Helper()
	mems := &fakeMemberships{}
	env := &timeOffEnv{
		mems:      mems,
		owner:     domain.Membership{UserID: "u-own", SalonID: "s1", Role: domain.RoleOwner, Attends: true},
		admin:     domain.Membership{UserID: "u-adm", SalonID: "s1", Role: domain.RoleAdmin, Attends: false},
		reception: domain.Membership{UserID: "u-rec", SalonID: "s1", Role: domain.RoleReception},
		pro1:      domain.Membership{UserID: "u-pro1", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		pro2:      domain.Membership{UserID: "u-pro2", SalonID: "s1", Role: domain.RoleProfessional, Attends: true},
		proOtro:   domain.Membership{UserID: "u-pro-otro", SalonID: "s2", Role: domain.RoleProfessional, Attends: true},
	}
	for _, m := range []domain.Membership{env.owner, env.admin, env.reception, env.pro1, env.pro2, env.proOtro} {
		if err := mems.Save(m); err != nil {
			t.Fatalf("membership: %v", err)
		}
	}
	env.timeOff = &fakeTimeOff{}
	env.bookings = &fb{}
	env.svc = TimeOffService{TimeOff: env.timeOff, Bookings: env.bookings, Memberships: mems}
	return env
}

// at arma un instante futuro (las ausencias no pueden haber terminado).
func at(dayOffset, hour, minute int) time.Time {
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Duration(dayOffset) * 24 * time.Hour)
	return base.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// S-F2d2-5: el motivo lo ve dueño/administración y cada profesional el suyo;
// una compañera no ve el motivo ajeno y recepción no ve ninguno. La ausencia, en
// cambio, la ve cualquier miembro: la agenda la necesita.
func TestTimeOff_list_privacy(t *testing.T) {
	env := newTimeOffEnv(t)

	vacaciones, err := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 0, 0), at(2, 0, 0), "Vacaciones")
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	if _, err := env.svc.Create(env.owner, "s1", "u-pro2", at(3, 0, 0), at(4, 0, 0), "Médico"); err != nil {
		t.Fatalf("segunda alta: %v", err)
	}
	if vacaciones.TimeOff.Reason != "Vacaciones" {
		t.Fatalf("el alta devuelve el motivo completo: %+v", vacaciones.TimeOff)
	}

	cases := []struct {
		name     string
		actor    domain.Membership
		wantByID map[string]string // userID -> razón visible
	}{
		{"dueña", env.owner, map[string]string{"u-pro1": "Vacaciones", "u-pro2": "Médico"}},
		{"administración", env.admin, map[string]string{"u-pro1": "Vacaciones", "u-pro2": "Médico"}},
		{"la propia profesional", env.pro1, map[string]string{"u-pro1": "Vacaciones", "u-pro2": ""}},
		{"compañera", env.pro2, map[string]string{"u-pro1": "", "u-pro2": "Médico"}},
		{"recepción", env.reception, map[string]string{"u-pro1": "", "u-pro2": ""}},
	}
	for _, c := range cases {
		list, err := env.svc.List(c.actor, "s1", at(0, 0, 0), at(7, 0, 0))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(list) != 2 {
			t.Fatalf("%s: %d ausencias (la agenda las ve todas): %+v", c.name, len(list), list)
		}
		for _, off := range list {
			if got, want := off.Reason, c.wantByID[off.UserID]; got != want {
				t.Fatalf("%s: motivo de %s = %q (esperaba %q)", c.name, off.UserID, got, want)
			}
			if off.ID == "" || off.UserID == "" || off.StartsAt.IsZero() {
				t.Fatalf("%s: ausencia incompleta: %+v", c.name, off)
			}
		}
	}

	// Un ajeno al salón no lista: 403 (el salon_id de la ruta no es la
	// credencial, la membresía sí).
	if _, err := env.svc.List(env.proOtro, "s1", at(0, 0, 0), at(7, 0, 0)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ajeno al salón: %v", err)
	}
}

// S-F2d2-5: el rango de la lista se valida antes de tocar la DB.
func TestTimeOff_list_range(t *testing.T) {
	env := newTimeOffEnv(t)
	from := at(0, 0, 0)

	if _, err := env.svc.List(env.owner, "s1", time.Time{}, from); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != domain.MsgTimeOffRangeMissing {
		t.Fatalf("sin from: %v", err)
	}
	if _, err := env.svc.List(env.owner, "s1", from, from); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != domain.MsgTimeOffRangeOrder {
		t.Fatalf("rango vacío: %v", err)
	}
	if _, err := env.svc.List(env.owner, "s1", from, from.Add(93*24*time.Hour)); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != domain.MsgTimeOffRangeTooLong {
		t.Fatalf("rango de 93 días: %v", err)
	}

	// La DB caída no se disfraza de "no hay ausencias": el error sube.
	env.timeOff.readErr = errors.New("db caída")
	if _, err := env.svc.List(env.owner, "s1", from, from.Add(24*time.Hour)); err == nil {
		t.Fatal("la caída de la DB tiene que subir, no devolver una lista vacía")
	}
}

// S-F2d2-2/4/5: el alta. Solo dueño/administración, solo miembros reservables,
// un miembro de otro salón es 404 y dos ausencias de la misma persona no se
// pisan. La respuesta trae los turnos `booked` de ESA persona que se pisan (para
// reasignarlos o cancelarlos a mano: F2d3), y NO cancela nada.
func TestTimeOff_create(t *testing.T) {
	env := newTimeOffEnv(t)

	// Turnos de la persona en el período, más ruido que NO puede aparecer:
	// cancelado, de otra persona y fuera del período.
	booked := func(id, prof string, start time.Time, status domain.BookingStatus) domain.Booking {
		return domain.Booking{
			ID: id, SalonID: "s1", ProfessionalID: prof, ClientName: "Clienta " + id,
			StartsAt: start, EndsAt: start.Add(45 * time.Minute), Status: status,
		}
	}
	env.bookings.all = []domain.Booking{
		booked("b1", "u-pro1", at(1, 10, 0), domain.BookingBooked),
		// Empieza ANTES de la ausencia y la pisa: tiene que aparecer igual.
		booked("b2", "u-pro1", at(1, 9, 30), domain.BookingBooked),
		booked("b3", "u-pro1", at(1, 11, 0), domain.BookingCancelled),
		booked("b4", "u-pro2", at(1, 10, 0), domain.BookingBooked),
		booked("b5", "u-pro1", at(5, 10, 0), domain.BookingBooked),
	}

	got, err := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 10, 0), at(1, 12, 0), "Médico")
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	if got.TimeOff.Reason != "Médico" || got.TimeOff.UserID != "u-pro1" || got.TimeOff.CreatedBy != "u-own" {
		t.Fatalf("ausencia: %+v", got.TimeOff)
	}
	var ids []string
	for _, b := range got.Conflicts {
		ids = append(ids, b.ID)
		if b.ProfessionalID != "u-pro1" {
			t.Fatalf("conflicto de otra persona: %+v", b)
		}
		if b.Status != domain.BookingBooked {
			t.Fatalf("solo turnos booked cuentan como conflicto: %+v", b)
		}
	}
	// Ordenados por inicio: b2 arranca 09:30 y b1 a las 10:00.
	if len(ids) != 2 || ids[0] != "b2" || ids[1] != "b1" {
		t.Fatalf("conflictos: %v (esperaba [b2 b1])", ids)
	}
	// Crear una ausencia NO cancela turnos (S-F2d2-4).
	for _, b := range env.bookings.all {
		if b.ID == "b1" && b.Status != domain.BookingBooked {
			t.Fatalf("el turno existente cambió de estado: %+v", b)
		}
	}

	// Solapamiento con otra ausencia de la MISMA persona: 400 y no se guarda.
	if _, err := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 11, 0), at(1, 13, 0), ""); !errors.Is(err, domain.ErrValidation) ||
		err.Error() != domain.MsgTimeOffOverlap {
		t.Fatalf("solapamiento: %v", err)
	}
	// La misma ventana para OTRA persona es válida (el período es por persona).
	if _, err := env.svc.Create(env.owner, "s1", "u-pro2", at(1, 11, 0), at(1, 13, 0), ""); err != nil {
		t.Fatalf("misma ventana para otra persona: %v", err)
	}

	cases := []struct {
		name    string
		actor   domain.Membership
		salonID string
		userID  string
		start   time.Time
		end     time.Time
		reason  string
		wantErr error
		wantMsg string
	}{
		{"profesional no carga", env.pro1, "s1", "u-pro1", at(6, 0, 0), at(7, 0, 0), "", domain.ErrForbidden, ""},
		{"recepción no carga", env.reception, "s1", "u-pro1", at(6, 0, 0), at(7, 0, 0), "", domain.ErrForbidden, ""},
		{"ajena al salón", env.proOtro, "s1", "u-pro1", at(6, 0, 0), at(7, 0, 0), "", domain.ErrForbidden, ""},
		{"miembro de otro salón", env.owner, "s1", "u-pro-otro", at(6, 0, 0), at(7, 0, 0), "", domain.ErrNotFound, ""},
		{"recepción no tiene ausencias", env.owner, "s1", "u-rec", at(6, 0, 0), at(7, 0, 0), "", domain.ErrValidation, domain.MsgTimeOffRole},
		{"dueña que no atiende", env.owner, "s1", "u-adm", at(6, 0, 0), at(7, 0, 0), "", domain.ErrValidation, domain.MsgTimeOffRole},
		{"fin antes del inicio", env.owner, "s1", "u-pro1", at(7, 0, 0), at(6, 0, 0), "", domain.ErrValidation, domain.MsgTimeOffOrder},
		{"más de 90 días", env.owner, "s1", "u-pro1", at(6, 0, 0), at(6, 0, 0).Add(91 * 24 * time.Hour), "", domain.ErrValidation, domain.MsgTimeOffTooLong},
		{"ya terminó", env.owner, "s1", "u-pro1", at(-2, 0, 0), at(-1, 0, 0), "", domain.ErrValidation, domain.MsgTimeOffPast},
	}
	for _, c := range cases {
		_, err := env.svc.Create(c.actor, c.salonID, c.userID, c.start, c.end, c.reason)
		if !errors.Is(err, c.wantErr) {
			t.Fatalf("%s: %v (esperaba %v)", c.name, err, c.wantErr)
		}
		if c.wantMsg != "" && err.Error() != c.wantMsg {
			t.Fatalf("%s: %q (esperaba %q)", c.name, err.Error(), c.wantMsg)
		}
	}
	// Ninguno de los intentos inválidos dejó filas.
	if n := len(env.timeOff.all); n != 2 {
		t.Fatalf("las altas inválidas no se guardan, hay %d", n)
	}
}

// S-F2d2-5: borrar. Solo dueño/administración; una id de otro salón es 404 (no
// un borrado silencioso) y la de este salón desaparece.
func TestTimeOff_delete(t *testing.T) {
	env := newTimeOffEnv(t)
	miAusencia, err := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 0, 0), at(2, 0, 0), "")
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	// El alta en el otro salón necesita a alguien que administre ESE salón.
	owner2 := domain.Membership{UserID: "u-own2", SalonID: "s2", Role: domain.RoleOwner, Attends: true}
	if err := env.mems.Save(owner2); err != nil {
		t.Fatalf("membership del otro salón: %v", err)
	}
	otroSalon, err := env.svc.Create(owner2, "s2", "u-pro-otro", at(1, 0, 0), at(2, 0, 0), "")
	if err != nil {
		t.Fatalf("alta en el otro salón: %v", err)
	}

	if err := env.svc.Delete(env.pro1, "s1", miAusencia.TimeOff.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional borra: %v", err)
	}
	if err := env.svc.Delete(env.owner, "s1", otroSalon.TimeOff.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("id de otro salón: %v (esperaba ErrNotFound)", err)
	}
	if err := env.svc.Delete(env.owner, "s1", "no-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("id inexistente: %v", err)
	}
	if err := env.svc.Delete(env.owner, "s1", miAusencia.TimeOff.ID); err != nil {
		t.Fatalf("borrar la propia: %v", err)
	}
	if _, err := env.timeOff.FindByID("s1", miAusencia.TimeOff.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tiene que estar borrada: %v", err)
	}
}

// S-F2d2-2: dos altas a la vez pasan las dos el chequeo previo y la DB
// (EXCLUDE) rechaza la segunda. El usuario tiene que ver el mismo mensaje del
// contrato (400), no un "ya existe" genérico (409).
func TestTimeOff_db_overlap_race_shows_contract_message(t *testing.T) {
	env := newTimeOffEnv(t)
	if _, err := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 0, 0), at(2, 0, 0), ""); err != nil {
		t.Fatalf("alta: %v", err)
	}
	_, want := env.svc.Create(env.owner, "s1", "u-pro1", at(1, 12, 0), at(1, 18, 0), "")
	if want == nil {
		t.Fatal("el chequeo previo debía rechazar el solape")
	}

	race := newTimeOffEnv(t)
	race.timeOff.saveErr = domain.ErrAlreadyExists
	_, got := race.svc.Create(race.owner, "s1", "u-pro1", at(3, 0, 0), at(4, 0, 0), "")
	if !errors.Is(got, domain.ErrValidation) || errors.Is(got, domain.ErrAlreadyExists) {
		t.Fatalf("la carrera debe ser un 400 de validación, dio %v", got)
	}
	if got.Error() != want.Error() {
		t.Fatalf("mensaje %q, esperaba el del contrato %q", got.Error(), want.Error())
	}
}
