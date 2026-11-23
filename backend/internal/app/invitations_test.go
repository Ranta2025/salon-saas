// F2g1 en el caso de uso: invitación (emitir, listar, revocar) y canje.
//
// Fakes del repositorio de invitaciones con la MISMA semántica que el adaptador
// Postgres: el canje es un UPDATE condicional, el token se busca hasheado y las
// escrituras de la transacción se revierten enteras si algo falla.
package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// --- fake del repositorio + transacción de invitaciones ---

type fakeInvitations struct {
	mu   sync.Mutex
	inTx bool
	// now es el reloj del canje (el UPDATE de Postgres usa now()).
	now   time.Time
	rows  map[string]domain.Invitation
	users *fakeUsers
	mems  *fakeMemberships
	// failMembership fuerza el fallo del último paso de la transacción (prueba
	// el "todo o nada" que en Postgres sostiene la transacción real).
	failMembership bool
	saves          int
	claims         int
}

func (f *fakeInvitations) lock() func() {
	if f.inTx {
		return func() {}
	}
	f.mu.Lock()
	return f.mu.Unlock
}

func (f *fakeInvitations) pending(i domain.Invitation) bool { return i.Pending(f.now) }

func (f *fakeInvitations) ListPending(salonID string, now time.Time) ([]domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	out := []domain.Invitation{}
	for _, row := range f.rows {
		if row.SalonID == salonID && row.Pending(now) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeInvitations) FindByID(salonID, id string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || row.SalonID != salonID {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return row, nil
}

func (f *fakeInvitations) FindByTokenHash(hash string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.rows {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.Invitation{}, domain.ErrNotFound
}

func (f *fakeInvitations) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Snapshot: si fn falla, nada de lo escrito queda (como el ROLLBACK).
	rows := map[string]domain.Invitation{}
	for k, v := range f.rows {
		rows[k] = v
	}
	users := map[string]domain.User{}
	for k, v := range f.users.byEmail {
		users[k] = v
	}
	mems := map[string]domain.Membership{}
	for k, v := range f.mems.byKey {
		mems[k] = v
	}
	f.inTx = true
	err := fn(f)
	f.inTx = false
	if err != nil {
		f.rows, f.users.byEmail, f.mems.byKey = rows, users, mems
	}
	return err
}

func (f *fakeInvitations) CountPending(salonID string, now time.Time) (int, error) {
	unlock := f.lock()
	defer unlock()
	n := 0
	for _, row := range f.rows {
		if row.SalonID == salonID && row.Pending(now) {
			n++
		}
	}
	return n, nil
}

func (f *fakeInvitations) RevokePendingByEmail(salonID, email string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	for id, row := range f.rows {
		if row.SalonID == salonID && row.Email == email && row.AcceptedAt == nil && row.RevokedAt == nil {
			at := now
			row.RevokedAt = &at
			f.rows[id] = row
		}
	}
	return nil
}

func (f *fakeInvitations) Save(i domain.Invitation) error {
	unlock := f.lock()
	defer unlock()
	for _, row := range f.rows {
		if row.TokenHash == i.TokenHash {
			return domain.ErrAlreadyExists
		}
		// Índice único parcial (salon_id, lower(email)) de las pendientes.
		if row.SalonID == i.SalonID && row.Email == i.Email && row.AcceptedAt == nil && row.RevokedAt == nil {
			return domain.ErrAlreadyExists
		}
	}
	f.saves++
	f.rows[i.ID] = i
	return nil
}

func (f *fakeInvitations) Claim(id, userID string) (domain.Invitation, error) {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || !f.pending(row) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	f.claims++
	at := f.now
	row.AcceptedAt = &at
	row.AcceptedBy = userID
	f.rows[id] = row
	return row, nil
}

func (f *fakeInvitations) Revoke(salonID, id string, now time.Time) error {
	unlock := f.lock()
	defer unlock()
	row, ok := f.rows[id]
	if !ok || row.SalonID != salonID || !f.pending(row) {
		return domain.ErrNotFound
	}
	at := now
	row.RevokedAt = &at
	f.rows[id] = row
	return nil
}

func (f *fakeInvitations) SaveUser(u domain.User) error { return f.users.Save(u) }

func (f *fakeInvitations) MembershipExists(salonID, userID string) (bool, error) {
	unlock := f.lock()
	defer unlock()
	m, ok := f.mems.byKey[userID+"|"+salonID]
	if !ok || !m.Active() {
		return false, nil
	}
	return true, nil
}

func (f *fakeInvitations) SaveMembership(m domain.Membership) error {
	unlock := f.lock()
	defer unlock()
	if f.failMembership {
		return fmt.Errorf("%w: una referencia del pedido no existe en el salón", domain.ErrValidation)
	}
	return f.mems.Save(m)
}

var (
	_ ports.InvitationRepository = (*fakeInvitations)(nil)
	_ ports.InvitationTxRunner   = (*fakeInvitations)(nil)
)

// countingInvHasher cuenta los hashes: la política de contraseña tiene que
// rechazar ANTES de gastar bcrypt (B-06).
type countingInvHasher struct{ hashes int }

func (h *countingInvHasher) Hash(plain string) (string, error) { h.hashes++; return "h:" + plain, nil }
func (h *countingInvHasher) Compare(hash, plain string) bool   { return hash == "h:"+plain }
func (h *countingInvHasher) DummyCompare(string) bool          { return false }

// --- entorno ---

type inviteEnv struct {
	users    *fakeUsers
	salons   *fakeSalons
	mems     *fakeMemberships
	settings *fakeSettings
	mailer   *fakeMailer
	inv      *fakeInvitations
	hasher   *countingInvHasher
	svc      InvitationService
	pub      InvitationPublicService
	now      time.Time
	tokens   int
	owner    domain.Membership
}

const inviteSalonName = "Navajas de Oro"

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	users := &fakeUsers{}
	salons := &fakeSalons{}
	if err := salons.Save(domain.Salon{ID: "s1", Slug: "navajas", Name: inviteSalonName}); err != nil {
		t.Fatalf("salón: %v", err)
	}
	if err := salons.Save(domain.Salon{ID: "s2", Slug: "otro", Name: "Otro Salón"}); err != nil {
		t.Fatalf("salón 2: %v", err)
	}
	settings := &fakeSettings{bySalon: map[string]domain.SalonSettings{}}
	st, err := domain.NewSalonSettings("s1", "America/Mexico_City", "09:00", "19:00", 30)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.bySalon["s1"] = st
	mems := &fakeMemberships{}
	owner := domain.Membership{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner, Attends: true}
	if err := mems.Save(owner); err != nil {
		t.Fatalf("owner: %v", err)
	}
	inv := &fakeInvitations{rows: map[string]domain.Invitation{}, users: users, mems: mems, now: now}
	env := &inviteEnv{
		users: users, salons: salons, mems: mems, settings: settings,
		mailer: &fakeMailer{}, inv: inv, hasher: &countingInvHasher{}, now: now, owner: owner,
	}
	genToken := func() (string, error) {
		env.tokens++
		return fmt.Sprintf("token-de-prueba-%02d-base64url", env.tokens), nil
	}
	env.svc = InvitationService{
		Invitations: inv, Tx: inv, Memberships: mems, Users: users, Salons: salons,
		Settings: settings, Mailer: env.mailer, AppURL: "http://localhost:8081",
		GenToken: genToken, Now: func() time.Time { return env.now },
	}
	env.pub = InvitationPublicService{
		Invitations: inv, Tx: inv, Users: users, Salons: salons,
		Hasher: env.hasher, Tokens: fakeTokens{}, Now: func() time.Time { return env.now },
	}
	return env
}

// invite emite una invitación válida del owner y devuelve el token del enlace.
func (e *inviteEnv) invite(t *testing.T, email string, role domain.Role) (CreatedInvitation, string) {
	t.Helper()
	created, err := e.svc.Invite(e.owner, "s1", InvitationInput{Email: email, Role: role})
	if err != nil {
		t.Fatalf("invitar %s: %v", email, err)
	}
	token := strings.TrimPrefix(created.Link, "http://localhost:8081/invitacion#")
	if token == "" || !strings.Contains(created.Link, "#") {
		t.Fatalf("el enlace tiene que llevar el token en el fragmento: %q", created.Link)
	}
	return created, token
}

// --- S-F2g1-3: emitir ---

func TestInvitation_Invite_emite_enlace_y_manda_el_correo(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "  Lucia@Mail.com ", domain.RoleProfessional)

	inv := created.Invitation
	if inv.Email != "lucia@mail.com" || inv.Role != domain.RoleProfessional || !inv.Attends {
		t.Fatalf("invitación: %+v", inv)
	}
	if !inv.ExpiresAt.Equal(env.now.Add(domain.InvitationTTL)) {
		t.Fatalf("vence a los 7 días: %v", inv.ExpiresAt)
	}
	// En la DB queda SOLO el hash: el token que viajó en el enlace no está.
	if inv.TokenHash != domain.HashInvitationToken(token) {
		t.Fatal("el token guardado tiene que ser el SHA-256 del que va en el enlace")
	}
	if strings.Contains(inv.TokenHash, token) || inv.TokenHash == token {
		t.Fatal("la invitación no puede guardar el token en claro")
	}

	if !created.EmailSent {
		t.Fatal("con mailer sano el correo sale")
	}
	msgs := env.mailer.messages()
	if len(msgs) != 1 {
		t.Fatalf("un correo, hubo %d", len(msgs))
	}
	msg := msgs[0]
	if msg.To != "lucia@mail.com" {
		t.Fatalf("destinatario: %q", msg.To)
	}
	if msg.Subject != inviteSalonName+" te invitó a su equipo" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	if !strings.Contains(msg.Text, created.Link) || !strings.Contains(msg.HTML, created.Link) {
		t.Fatal("el correo tiene que llevar el enlace en texto y en HTML")
	}
	if !strings.Contains(msg.Text, "profesional") {
		t.Fatalf("el rol va en español: %q", msg.Text)
	}
	// La fecha de vencimiento, en el día local del salón (01/03 12:00 UTC son las
	// 06:00 del 01/03 en México; el vencimiento es el 8 de marzo local).
	if !strings.Contains(msg.Text, "08/03/2027") {
		t.Fatalf("el correo tiene que decir la fecha de vencimiento: %q", msg.Text)
	}

	// El listado de pendientes la muestra y el token no aparece en ningún campo.
	list, err := env.svc.List(env.owner, "s1")
	if err != nil || len(list) != 1 {
		t.Fatalf("listado: %+v %v", list, err)
	}
	if list[0].TokenHash != inv.TokenHash {
		t.Fatalf("el listado no puede devolver otra cosa: %+v", list[0])
	}
}

// El HTML escapa lo que escribe otra persona: el nombre del salón lo pone el
// dueño y no puede inyectar etiquetas en el correo.
func TestInvitation_correo_escapa_el_html(t *testing.T) {
	msg := invitationMail("Bar <b>Oro</b> & Cía", domain.Invitation{
		Email: "x@mail.com", Role: domain.RoleProfessional, ExpiresAt: time.Date(2027, 3, 8, 12, 0, 0, 0, time.UTC),
	}, "http://localhost:8081/invitacion#tok", "08/03/2027")
	if msg.Subject != "Bar <b>Oro</b> & Cía te invitó a su equipo" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	if strings.Contains(msg.HTML, "<b>Oro</b>") {
		t.Fatalf("el HTML no puede llevar etiquetas del nombre: %q", msg.HTML)
	}
	if !strings.Contains(msg.HTML, "Bar &lt;b&gt;Oro&lt;/b&gt; &amp; Cía") {
		t.Fatalf("el nombre tiene que ir escapado en HTML: %q", msg.HTML)
	}
	if !strings.Contains(msg.Text, "Bar <b>Oro</b> & Cía") {
		t.Fatalf("el texto plano conserva el nombre tal cual: %q", msg.Text)
	}
}

// S-F2g1-3: la misma regla AddableBy de POST /members. Un admin no invita
// admins, nadie invita clientes ni dueños, y recepción/profesional no invitan.
func TestInvitation_roles(t *testing.T) {
	env := newInviteEnv(t)
	admin := domain.Membership{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin}
	if err := env.mems.Save(admin); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if err := env.users.Save(domain.User{ID: "u-admin", Name: "Carla", Email: "admin@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("cuenta del admin: %v", err)
	}
	reception := domain.Membership{UserID: "u-rec", SalonID: "s1", Role: domain.RoleReception}
	if err := env.mems.Save(reception); err != nil {
		t.Fatalf("recepción: %v", err)
	}

	cases := []struct {
		actor domain.Membership
		role  domain.Role
		kind  error
		text  string
	}{
		{admin, domain.RoleAdmin, domain.ErrValidation, "No podés invitar con ese rol"},
		{admin, domain.RoleOwner, domain.ErrValidation, "No podés invitar con ese rol"},
		{admin, domain.RoleClient, domain.ErrValidation, "No podés invitar con ese rol"},
		{env.owner, domain.RoleClient, domain.ErrValidation, "No podés invitar con ese rol"},
		{env.owner, domain.RoleOwner, domain.ErrValidation, "No podés invitar con ese rol"},
		{env.owner, domain.Role("jefe"), domain.ErrValidation, "No podés invitar con ese rol"},
		{reception, domain.RoleProfessional, domain.ErrForbidden, ""},
		{domain.Membership{UserID: "u-owner", SalonID: "s2", Role: domain.RoleOwner}, domain.RoleProfessional, domain.ErrForbidden, ""},
	}
	for _, c := range cases {
		_, err := env.svc.Invite(c.actor, "s1", InvitationInput{Email: "alguien@mail.com", Role: c.role})
		if !errors.Is(err, c.kind) {
			t.Fatalf("%s invita %s: %v (esperaba %v)", c.actor.Role, c.role, err, c.kind)
		}
		if c.text != "" && err.Error() != c.text {
			t.Fatalf("%s invita %s: mensaje %q", c.actor.Role, c.role, err.Error())
		}
	}
	// El admin sí invita profesionales y recepción.
	for _, role := range []domain.Role{domain.RoleProfessional, domain.RoleReception} {
		if _, err := env.svc.Invite(admin, "s1", InvitationInput{Email: "nuevo-" + string(role) + "@mail.com", Role: role}); err != nil {
			t.Fatalf("admin invita %s: %v", role, err)
		}
	}
	// Quien ya es parte del equipo no se invita.
	_, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "admin@mail.com", Role: domain.RoleProfessional})
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != "Esa persona ya es parte del equipo" {
		t.Fatalf("miembro existente: %v", err)
	}
}

// S-F2g1-5: la fecha de vencimiento del correo es la del día LOCAL del salón. Un
// vencimiento a las 02:00 UTC del 8 de marzo es el 7 de marzo en México: si el
// correo dijera 8, la persona lo buscaría un día tarde.
func TestInvitation_correo_fecha_local_del_salon(t *testing.T) {
	settings, err := domain.NewSalonSettings("s1", "America/Mexico_City", "09:00", "19:00", 30)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	expires := time.Date(2027, 3, 8, 2, 0, 0, 0, time.UTC)
	if got := expiryLabel(settings, expires); got != "07/03/2027" {
		t.Fatalf("etiqueta local: %q (esperaba 07/03/2027)", got)
	}
	// Una zona desconocida cae a UTC en vez de romper el correo.
	if got := expiryLabel(domain.SalonSettings{Timezone: "Marte/Olympus"}, expires); got != "08/03/2027" {
		t.Fatalf("zona inválida: %q", got)
	}
}

// S-F2g1-3: un email que ya es miembro da 409 con el texto del contrato; un
// email con forma inválida, 400 "Email inválido".
func TestInvitation_email_ya_es_miembro_y_email_invalido(t *testing.T) {
	env := newInviteEnv(t)
	if err := env.users.Save(domain.User{ID: "u-admin", Name: "Carla", Email: "carla@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	if err := env.mems.Save(domain.Membership{UserID: "u-admin", SalonID: "s1", Role: domain.RoleAdmin}); err != nil {
		t.Fatalf("membresía: %v", err)
	}
	if _, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "CARLA@mail.com", Role: domain.RoleProfessional}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("ya es miembro: %v", err)
	} else if err.Error() != "Esa persona ya es parte del equipo" {
		t.Fatalf("mensaje: %q", err.Error())
	}
	// Una cuenta que está en OTRO salón no bloquea la invitación de este: el
	// equipo es por salón y una persona puede trabajar en dos.
	if err := env.users.Save(domain.User{ID: "u-dani", Name: "Dani", Email: "dani@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("cuenta de Dani: %v", err)
	}
	if err := env.mems.Save(domain.Membership{UserID: "u-dani", SalonID: "s2", Role: domain.RoleProfessional}); err != nil {
		t.Fatalf("membresía en s2: %v", err)
	}
	if _, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "dani@mail.com", Role: domain.RoleReception}); err != nil {
		t.Fatalf("misma persona, otro salón: %v", err)
	}
	for _, bad := range []string{"", "   ", "sin-arroba", "a@b"} {
		_, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: bad, Role: domain.RoleProfessional})
		if !errors.Is(err, domain.ErrValidation) || err.Error() != "Email inválido" {
			t.Fatalf("email %q: %v", bad, err)
		}
	}
}

// S-F2g1-3: máximo 50 pendientes por salón. Reinvitar al mismo email revoca la
// anterior y no cuenta dos veces.
func TestInvitation_maximo_50_pendientes(t *testing.T) {
	env := newInviteEnv(t)
	for i := 0; i < domain.MaxPendingInvitations; i++ {
		if _, err := env.svc.Invite(env.owner, "s1", InvitationInput{
			Email: fmt.Sprintf("persona%d@mail.com", i), Role: domain.RoleProfessional,
		}); err != nil {
			t.Fatalf("invitación %d: %v", i, err)
		}
	}
	_, err := env.svc.Invite(env.owner, "s1", InvitationInput{Email: "otra@mail.com", Role: domain.RoleProfessional})
	if !errors.Is(err, domain.ErrValidation) || err.Error() != "Máximo 50 invitaciones pendientes" {
		t.Fatalf("tope: %v", err)
	}
	// Reinvitar a alguien que ya está en la lista no suma: revoca y vuelve a
	// emitir (la anterior deja de servir).
	created, token := env.invite(t, "persona0@mail.com", domain.RoleProfessional)
	if created.Invitation.Email != "persona0@mail.com" {
		t.Fatalf("reinvitación: %+v", created.Invitation)
	}
	if _, err := env.pub.Lookup(token); err != nil {
		t.Fatalf("el token nuevo sirve: %v", err)
	}
	// Y el listado sigue en 50 (una revocada no se lista).
	list, err := env.svc.List(env.owner, "s1")
	if err != nil || len(list) != domain.MaxPendingInvitations {
		t.Fatalf("pendientes: %d %v", len(list), err)
	}
}

// S-F2g1-3: reinvitar revoca el token viejo. El viejo responde el 404 genérico
// y el nuevo funciona.
func TestInvitation_reinvitar_revoca_el_token_viejo(t *testing.T) {
	env := newInviteEnv(t)
	_, viejo := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	_, nuevo := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	if viejo == nuevo {
		t.Fatal("la reinvitación emite un token nuevo")
	}
	if _, err := env.pub.Lookup(viejo); !errors.Is(err, domain.ErrNotFound) || err.Error() != "Invitación inválida o vencida" {
		t.Fatalf("token viejo: %v", err)
	}
	if _, err := env.pub.Lookup(nuevo); err != nil {
		t.Fatalf("token nuevo: %v", err)
	}
}

// S-F2g1-5: si el SMTP falla la invitación existe igual y email_sent=false.
func TestInvitation_correo_fallido_no_falla_el_alta(t *testing.T) {
	env := newInviteEnv(t)
	env.mailer.err = errors.New("smtp: conexión rechazada")
	created, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	if created.EmailSent {
		t.Fatal("con el SMTP caído email_sent tiene que ser false")
	}
	if _, err := env.pub.Lookup(token); err != nil {
		t.Fatalf("la invitación tiene que existir igual: %v", err)
	}
	// Sin mailer configurado (SMTP_HOST vacío) tampoco es un error.
	env2 := newInviteEnv(t)
	env2.svc.Mailer = nil
	created2, _ := env2.invite(t, "lucia@mail.com", domain.RoleProfessional)
	if created2.EmailSent {
		t.Fatal("sin mailer email_sent es false")
	}
}

// S-F2g1-3: revocar. Después de revocar el token da el 404 genérico y revocar
// dos veces es 404 (no un éxito silencioso).
func TestInvitation_revoke(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	if err := env.svc.Revoke(env.owner, "s1", created.Invitation.ID); err != nil {
		t.Fatalf("revocar: %v", err)
	}
	if _, err := env.pub.Lookup(token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token revocado: %v", err)
	}
	if err := env.svc.Revoke(env.owner, "s1", created.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revocar dos veces: %v", err)
	}
	// Otro salón no la revoca (404).
	created2, _ := env.invite(t, "otra@mail.com", domain.RoleProfessional)
	other := domain.Membership{UserID: "u-owner2", SalonID: "s2", Role: domain.RoleOwner}
	if err := env.mems.Save(other); err != nil {
		t.Fatalf("owner 2: %v", err)
	}
	if err := env.svc.Revoke(other, "s2", created2.Invitation.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("otro salón: %v", err)
	}
}

// --- S-F2g1-4: canje ---

func TestInvitationAccept_cuenta_nueva_crea_cuenta_membresia_y_sesion(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)

	jwt, err := env.pub.AcceptNew(token, "Lucía", "secreta123")
	if err != nil {
		t.Fatalf("aceptar: %v", err)
	}
	if jwt != "tok:"+strings.TrimPrefix(jwt, "tok:") || !strings.HasPrefix(jwt, "tok:") {
		t.Fatalf("la sesión es la del login (JWT del mismo emisor): %q", jwt)
	}
	uid := strings.TrimPrefix(jwt, "tok:")
	u, err := env.users.FindByID(uid)
	if err != nil {
		t.Fatalf("cuenta creada: %v", err)
	}
	if u.Email != "lucia@mail.com" || u.Name != "Lucía" {
		t.Fatalf("cuenta: %+v", u)
	}
	if u.PasswordHash != "h:secreta123" {
		t.Fatalf("la clave se guarda hasheada: %q", u.PasswordHash)
	}
	m, err := env.mems.Find(uid, "s1")
	if err != nil {
		t.Fatalf("membresía: %v", err)
	}
	if m.Role != domain.RoleProfessional || !m.Attends {
		t.Fatalf("membresía con el rol y la marca de la invitación: %+v", m)
	}
	row := env.inv.rows[created.Invitation.ID]
	if row.AcceptedAt == nil || row.AcceptedBy != uid {
		t.Fatalf("la invitación queda aceptada: %+v", row)
	}
	// Un solo uso: el mismo token ya no sirve.
	if _, err := env.pub.AcceptNew(token, "Otra", "secreta123"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segundo uso: %v", err)
	}
}

// B-06: la política de contraseña es la misma del registro y rechaza ANTES de
// gastar bcrypt: la invitación sigue pendiente y no se crea cuenta.
func TestInvitationAccept_politica_de_clave_antes_de_bcrypt(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)

	for _, bad := range []string{"", "corta", strings.Repeat("x", 73)} {
		if _, err := env.pub.AcceptNew(token, "Lucía", bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("clave %q: %v", bad, err)
		}
	}
	if env.hasher.hashes != 0 {
		t.Fatalf("bcrypt no se puede gastar con una clave inválida: %d hashes", env.hasher.hashes)
	}
	if len(env.users.byEmail) != 0 {
		t.Fatalf("no se crea cuenta: %+v", env.users.byEmail)
	}
	if row := env.inv.rows[created.Invitation.ID]; row.AcceptedAt != nil {
		t.Fatal("la invitación queda pendiente")
	}
	// Nombre vacío: tampoco crea nada (y la validación es del dominio).
	if _, err := env.pub.AcceptNew(token, "  ", "secreta123"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nombre vacío: %v", err)
	}
	if env.hasher.hashes != 0 {
		t.Fatal("una clave válida con nombre vacío no debe llegar a bcrypt")
	}
}

func TestInvitationAccept_cuenta_existente_da_409(t *testing.T) {
	env := newInviteEnv(t)
	_, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	if err := env.users.Save(domain.User{ID: "u-lucia", Name: "Lucía", Email: "lucia@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	_, err := env.pub.AcceptNew(token, "Lucía", "secreta123")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("cuenta existente: %v", err)
	}
	if err.Error() != "Ya tenés cuenta: iniciá sesión para aceptar la invitación" {
		t.Fatalf("mensaje: %q", err.Error())
	}
}

// S-F2g1-4: la cuenta existente se une SOLO si el email coincide con el de la
// invitación; si no, 403 con el texto del contrato.
func TestInvitationAccept_cuenta_existente_otro_email_da_403(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleReception)
	if err := env.users.Save(domain.User{ID: "u-otra", Name: "Otra", Email: "otra@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	_, _, err := env.pub.AcceptExisting("u-otra", token)
	if !errors.Is(err, domain.ErrForbidden) || err.Error() != "Esta invitación es para otra cuenta" {
		t.Fatalf("otro email: %v", err)
	}
	if row := env.inv.rows[created.Invitation.ID]; row.AcceptedAt != nil {
		t.Fatal("el intento fallido no gasta la invitación")
	}
	if _, _, err := env.pub.AcceptExisting("u-no-existe", token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("usuario inexistente: %v", err)
	}

	// Y con el email correcto se une con el rol de la invitación.
	if err := env.users.Save(domain.User{ID: "u-lucia", Name: "Lucía", Email: "Lucia@Mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	salon, inv, err := env.pub.AcceptExisting("u-lucia", token)
	if err != nil {
		t.Fatalf("aceptar existente: %v", err)
	}
	if salon.ID != "s1" || inv.ID != created.Invitation.ID {
		t.Fatalf("salón/invitación: %+v %+v", salon, inv)
	}
	m, err := env.mems.Find("u-lucia", "s1")
	if err != nil || m.Role != domain.RoleReception || m.Attends {
		t.Fatalf("membresía de recepción: %+v %v", m, err)
	}
	if _, _, err := env.pub.AcceptExisting("u-lucia", token); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segundo canje autenticado: %v", err)
	}
}

// S-F2g2-4: aceptar una invitación con una cuenta EXISTENTE que fue dada de baja
// la REACTIVA con el rol de la invitación (el canje no cambia de rol a un activo,
// pero sí vuelve a entrar a quien ya no está).
func TestInvitationAccept_cuenta_existente_reactiva_una_baja(t *testing.T) {
	env := newInviteEnv(t)
	if err := env.users.Save(domain.User{ID: "u-lucia", Name: "Lucía", Email: "lucia@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	salon := env.now
	env.mems.byKey["u-lucia|s1"] = domain.Membership{
		UserID: "u-lucia", SalonID: "s1", Role: domain.RoleProfessional,
		Attends: true, Trade: "Barbero", EndedAt: &salon,
	}
	created, token := env.invite(t, "lucia@mail.com", domain.RoleReception)
	if _, _, err := env.pub.AcceptExisting("u-lucia", token); err != nil {
		t.Fatalf("aceptar estando de baja: %v", err)
	}
	m, err := env.mems.Find("u-lucia", "s1")
	if err != nil {
		t.Fatalf("la cuenta tiene que volver a estar activa: %v", err)
	}
	if m.Role != domain.RoleReception || m.Attends {
		t.Fatalf("el rol y la marca son los de la invitación: %+v", m)
	}
	if row := env.inv.rows[created.Invitation.ID]; row.AcceptedAt == nil {
		t.Fatal("la invitación tiene que quedar aceptada")
	}

	// Y a alguien que YA está activo no se le cambia el rol: 409 (comportamiento
	// de siempre).
	env.mems.byKey["u-lucia|s1"] = domain.Membership{
		UserID: "u-lucia", SalonID: "s1", Role: domain.RoleReception,
	}
	_, token2 := env.invite(t, "otra@mail.com", domain.RoleProfessional)
	if err := env.users.Save(domain.User{ID: "u-otra", Name: "Otra", Email: "otra@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	env.mems.byKey["u-otra|s1"] = domain.Membership{UserID: "u-otra", SalonID: "s1", Role: domain.RoleProfessional, Attends: true}
	if _, _, err := env.pub.AcceptExisting("u-otra", token2); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("activo aceptando otra invitación: %v", err)
	}
}

// S-F2g1-4: sin oráculo. Token desconocido, vencido, revocado y ya usado dan la
// MISMA respuesta (mismo kind y mismo texto). Ningún caso revela si el email
// tiene cuenta.
func TestInvitationAccept_sin_oraculo(t *testing.T) {
	env := newInviteEnv(t)
	_, usado := env.invite(t, "usado@mail.com", domain.RoleProfessional)
	if _, err := env.pub.AcceptNew(usado, "Usado", "secreta123"); err != nil {
		t.Fatalf("canje previo: %v", err)
	}
	_, revocado := env.invite(t, "revocado@mail.com", domain.RoleProfessional)
	if err := env.svc.Revoke(env.owner, "s1", env.inv.rows[env.lastID(t, "revocado@mail.com")].ID); err != nil {
		t.Fatalf("revocar: %v", err)
	}
	_, vencido := env.invite(t, "vencido@mail.com", domain.RoleProfessional)
	env.now = env.now.Add(domain.InvitationTTL)
	env.inv.now = env.now

	tokens := map[string]string{
		"desconocido": "token-que-no-existe",
		"usado":       usado,
		"revocado":    revocado,
		"vencido":     vencido,
		"vacío":       "",
	}
	for nombre, tok := range tokens {
		_, err := env.pub.AcceptNew(tok, "Alguien", "secreta123")
		if !errors.Is(err, domain.ErrNotFound) || err.Error() != "Invitación inválida o vencida" {
			t.Fatalf("%s: %v", nombre, err)
		}
		if _, err := env.pub.Lookup(tok); !errors.Is(err, domain.ErrNotFound) || err.Error() != "Invitación inválida o vencida" {
			t.Fatalf("lookup %s: %v", nombre, err)
		}
	}
	if len(env.users.byEmail) != 1 {
		t.Fatalf("solo la primera invitación creó cuenta: %+v", env.users.byEmail)
	}
}

// lastID busca el id de la invitación pendiente de ese email (los tests no
// conocen el id antes de crearla).
func (e *inviteEnv) lastID(t *testing.T, email string) string {
	t.Helper()
	for id, row := range e.inv.rows {
		if row.Email == email {
			return id
		}
	}
	t.Fatalf("no hay invitación para %s", email)
	return ""
}

// El canje es TODO O NADA: si la membresía falla no queda cuenta y la
// invitación sigue pendiente. (El test de Postgres lo prueba con la transacción
// real; este prueba que el caso de uso no la gasta antes de tiempo.)
func TestInvitationAccept_transaccion_atomica(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleProfessional)
	env.inv.failMembership = true
	if _, err := env.pub.AcceptNew(token, "Lucía", "secreta123"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("fallo de membresía: %v", err)
	}
	if len(env.users.byEmail) != 0 {
		t.Fatalf("no puede quedar la cuenta: %+v", env.users.byEmail)
	}
	if row := env.inv.rows[created.Invitation.ID]; row.AcceptedAt != nil {
		t.Fatal("la invitación no se puede gastar si la membresía falló")
	}
	// Y con la falla resuelta el mismo token todavía sirve.
	env.inv.failMembership = false
	if _, err := env.pub.AcceptNew(token, "Lucía", "secreta123"); err != nil {
		t.Fatalf("reintento: %v", err)
	}
}

// S-F2g1-4: el lookup no necesita sesión y devuelve salón, rol en español, el
// email enmascarado, el vencimiento y si la dirección ya tiene cuenta.
func TestInvitationLookup(t *testing.T) {
	env := newInviteEnv(t)
	created, token := env.invite(t, "lucia@mail.com", domain.RoleAdmin)
	got, err := env.pub.Lookup(token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.SalonName != inviteSalonName || got.Role != domain.RoleAdmin || got.RoleLabel != "administración" {
		t.Fatalf("lookup: %+v", got)
	}
	if got.EmailHint != "l***@mail.com" {
		t.Fatalf("hint: %q", got.EmailHint)
	}
	if !got.ExpiresAt.Equal(created.Invitation.ExpiresAt) {
		t.Fatalf("vencimiento: %v", got.ExpiresAt)
	}
	if got.HasAccount {
		t.Fatal("todavía no tiene cuenta")
	}
	// Con cuenta, has_account lo dice (lo único que se revela es el booleano).
	if err := env.users.Save(domain.User{ID: "u-lucia", Name: "Lucía", Email: "lucia@mail.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("usuario: %v", err)
	}
	if got, _ = env.pub.Lookup(token); !got.HasAccount {
		t.Fatalf("con cuenta: %+v", got)
	}
	// y el error del lookup de un token inválido no distingue nada.
	if _, err := env.pub.Lookup("token-que-no-existe"); !errors.Is(err, domain.ErrNotFound) || err.Error() != "Invitación inválida o vencida" {
		t.Fatalf("token inválido: %v", err)
	}
}
