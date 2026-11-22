package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// S-F2g1-2: el token de una invitación son 32 bytes aleatorios en base64url (sin
// relleno, seguro para un fragmento de URL) y se guarda SOLO su SHA-256. La
// comparación no necesita el token en claro: hash del token contra el guardado.
func TestInvitationToken_generacion_y_hash(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		token, err := NewInvitationToken()
		if err != nil {
			t.Fatalf("generar: %v", err)
		}
		if seen[token] {
			t.Fatalf("token repetido: %q", token)
		}
		seen[token] = true
		// 32 bytes en base64url sin relleno = 43 caracteres.
		if len(token) != 43 {
			t.Fatalf("largo del token = %d, esperaba 43 (%q)", len(token), token)
		}
		for _, r := range token {
			urlSafe := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
			if !urlSafe {
				t.Fatalf("el token lleva un caracter que no es base64url: %q", token)
			}
		}
		hash := HashInvitationToken(token)
		if len(hash) != 64 {
			t.Fatalf("el hash debe ser SHA-256 en hex (64): %q", hash)
		}
		if strings.Contains(hash, token) || hash == token {
			t.Fatal("el hash no puede contener el token en claro")
		}
		if !InvitationTokenMatches(hash, token) {
			t.Fatal("el hash del mismo token debe coincidir")
		}
		if InvitationTokenMatches(hash, token+"x") {
			t.Fatal("otro token no puede coincidir")
		}
		if InvitationTokenMatches("", token) {
			t.Fatal("un hash vacío no coincide con nada")
		}
	}
	// Determinista: el mismo token siempre da el mismo hash (es lo que se busca
	// por índice único en la DB).
	if HashInvitationToken("abc") != HashInvitationToken("abc") {
		t.Fatal("el hash tiene que ser determinista")
	}
}

// S-F2g1-2: los cuatro estados no se confunden. Una invitación que vence justo
// ahora ya no está pendiente (el canje usa expires_at > now()).
func TestInvitation_estados(t *testing.T) {
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	pending := Invitation{ExpiresAt: now.Add(time.Hour)}
	if got := pending.State(now); got != InvitationPending {
		t.Fatalf("pendiente: %q", got)
	}
	if !pending.Pending(now) || pending.Expired(now) {
		t.Fatalf("pendiente: Pending=%v Expired=%v", pending.Pending(now), pending.Expired(now))
	}
	if got := (Invitation{ExpiresAt: now}).State(now); got != InvitationExpired {
		t.Fatalf("vence justo ahora: %q (debe ser vencida)", got)
	}
	acceptedAt := now.Add(-time.Minute)
	accepted := Invitation{ExpiresAt: now.Add(time.Hour), AcceptedAt: &acceptedAt, AcceptedBy: "u1"}
	if got := accepted.State(now); got != InvitationAccepted {
		t.Fatalf("aceptada: %q", got)
	}
	if accepted.Pending(now) {
		t.Fatal("una aceptada no está pendiente")
	}
	revokedAt := now.Add(-time.Minute)
	revoked := Invitation{ExpiresAt: now.Add(time.Hour), RevokedAt: &revokedAt}
	if got := revoked.State(now); got != InvitationRevoked {
		t.Fatalf("revocada: %q", got)
	}
	if revoked.Pending(now) {
		t.Fatal("una revocada no está pendiente")
	}
	// Aceptada y revocada a la vez (no debería pasar): aceptada manda, porque el
	// canje ya ocurrió.
	both := Invitation{ExpiresAt: now.Add(time.Hour), AcceptedAt: &acceptedAt, RevokedAt: &revokedAt}
	if got := both.State(now); got != InvitationAccepted {
		t.Fatalf("aceptada y revocada: %q", got)
	}
}

// S-F2g1-2: el email se normaliza y valida como el de una cuenta. El que no
// tiene forma de email es ErrValidation (400).
func TestNewInvitation_email_y_rol(t *testing.T) {
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	hash := HashInvitationToken("token")

	inv, err := NewInvitation("s1", "  Lucia@Mail.com ", RoleProfessional, true, hash, "u-owner", now)
	if err != nil {
		t.Fatalf("alta válida: %v", err)
	}
	if inv.Email != "lucia@mail.com" {
		t.Fatalf("email normalizado: %q", inv.Email)
	}
	if inv.Role != RoleProfessional || !inv.Attends {
		t.Fatalf("rol y marca: %+v", inv)
	}
	if inv.TokenHash != hash {
		t.Fatal("el token se guarda hasheado, tal cual vino")
	}
	if !inv.ExpiresAt.Equal(now.Add(InvitationTTL)) {
		t.Fatalf("vence a los 7 días: %v (esperaba %v)", inv.ExpiresAt, now.Add(InvitationTTL))
	}
	if inv.ID == "" || inv.SalonID != "s1" || inv.CreatedBy != "u-owner" || !inv.CreatedAt.Equal(now) {
		t.Fatalf("campos del alta: %+v", inv)
	}
	if inv.State(now) != InvitationPending {
		t.Fatalf("nace pendiente: %q", inv.State(now))
	}

	// Cliente y dueño no se invitan nunca (y el rol tiene que existir).
	for _, role := range []Role{RoleClient, RoleOwner, Role("jefe")} {
		if _, err := NewInvitation("s1", "x@mail.com", role, false, hash, "u", now); !errors.Is(err, ErrValidation) {
			t.Fatalf("rol %q: %v (esperaba ErrValidation)", role, err)
		} else if err.Error() != "No podés invitar con ese rol" {
			t.Fatalf("rol %q: mensaje %q", role, err.Error())
		}
	}
	// La marca de "atiende clientes" se valida con la misma regla del PATCH:
	// recepción no se enciende y un profesional no se apaga.
	if _, err := NewInvitation("s1", "r@mail.com", RoleReception, true, hash, "u", now); err == nil ||
		err.Error() != "Ese rol no atiende clientes" {
		t.Fatalf("recepción que atiende: %v", err)
	}
	if _, err := NewInvitation("s1", "p@mail.com", RoleProfessional, false, hash, "u", now); err == nil ||
		err.Error() != "Las profesionales siempre atienden" {
		t.Fatalf("profesional que no atiende: %v", err)
	}
	// Emails sin forma de email o demasiado largos.
	for _, email := range []string{"", "   ", "sin-arroba", "a@b", strings.Repeat("a", 250) + "@mail.com"} {
		if _, err := NewInvitation("s1", email, RoleProfessional, true, hash, "u", now); !errors.Is(err, ErrValidation) {
			t.Fatalf("email %q: %v (esperaba ErrValidation)", email, err)
		}
	}
	// Falta el hash del token: es un bug del caso de uso, no del pedido.
	if _, err := NewInvitation("s1", "x@mail.com", RoleProfessional, true, "", "u", now); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin hash: %v", err)
	}
}

// S-F2g1-2: la misma regla de `POST /members` (Role.AddableBy) decide qué rol
// puede invitar cada quien. Nadie invita clientes ni un segundo dueño.
func TestRole_Invitable_by_actor(t *testing.T) {
	cases := []struct {
		actor, target Role
		allowed       bool
	}{
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleProfessional, true},
		{RoleOwner, RoleReception, true},
		{RoleOwner, RoleOwner, false},
		{RoleOwner, RoleClient, false},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleProfessional, true},
		{RoleAdmin, RoleReception, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleClient, false},
		{RoleProfessional, RoleReception, false},
		{RoleReception, RoleProfessional, false},
	}
	for _, c := range cases {
		if got := c.target.InvitableBy(c.actor); got != c.allowed {
			t.Fatalf("%s invita %s = %v, esperaba %v", c.actor, c.target, got, c.allowed)
		}
	}
}

// S-F2g1-4: el lookup público muestra el email enmascarado. Nunca la dirección
// completa: quien tiene el token ya la conoce, quien no, no la aprende de acá.
func TestInvitation_email_hint(t *testing.T) {
	cases := map[string]string{
		"lucia@mail.com":    "l***@mail.com",
		"a@b.com":           "a***@b.com",
		"bruno.prof@x.com":  "b***@x.com",
		"lucia@mail.com.co": "l***@mail.com.co",
	}
	for email, want := range cases {
		inv := Invitation{Email: email}
		if got := inv.EmailHint(); got != want {
			t.Fatalf("hint de %q = %q, esperaba %q", email, got, want)
		}
	}
	// Sin arroba (no debería existir) el hint no filtra nada raro.
	if got := (Invitation{Email: "sin-arroba"}).EmailHint(); strings.Contains(got, "sin-arroba") {
		t.Fatalf("el hint no puede devolver el valor entero: %q", got)
	}
}

// S-F2g1-4: la membresía que nace de la invitación lleva el rol Y la marca que
// se guardaron al invitar (una administración que atiende sigue atendiendo).
func TestInvitation_membership(t *testing.T) {
	now := time.Now().UTC()
	pro, err := NewInvitation("s1", "ana@mail.com", RoleProfessional, true, "h", "u", now)
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	m, err := pro.Membership("u-new")
	if err != nil {
		t.Fatalf("membresía: %v", err)
	}
	if m.UserID != "u-new" || m.SalonID != "s1" || m.Role != RoleProfessional || !m.Attends || !m.Bookable() {
		t.Fatalf("membresía del profesional: %+v", m)
	}
	adm, err := NewInvitation("s1", "bea@mail.com", RoleAdmin, true, "h", "u", now)
	if err != nil {
		t.Fatalf("alta admin: %v", err)
	}
	m, err = adm.Membership("u-adm")
	if err != nil || !m.Bookable() {
		t.Fatalf("admin que atiende: %+v %v", m, err)
	}
	rec, err := NewInvitation("s1", "ceci@mail.com", RoleReception, false, "h", "u", now)
	if err != nil {
		t.Fatalf("alta recepción: %v", err)
	}
	m, err = rec.Membership("u-rec")
	if err != nil || m.Bookable() || m.Attends {
		t.Fatalf("recepción no atiende: %+v %v", m, err)
	}
}

// S-F2g1-3: "Email inválido" es el mensaje del contrato. La validación es la de
// las cuentas (la misma que usa el registro), así no hay dos políticas.
func TestValidateEmail_igual_que_las_cuentas(t *testing.T) {
	for _, ok := range []string{"a@b.co", "lucia@mail.com", "x.y+z@sub.dominio.co"} {
		if err := ValidateEmail(ok); err != nil {
			t.Fatalf("%q debería ser válido: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "sin-arroba", "a@b", strings.Repeat("a", 250) + "@mail.com"} {
		if err := ValidateEmail(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("%q debería rechazarse: %v", bad, err)
		}
	}
}
