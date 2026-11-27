package team

import (
	"errors"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Hallazgo A del verificador independiente de F2g1: la parte HTML del correo
// terminaba el enlace con `</a></p>\n` dentro de un string crudo (backticks), y
// el correo mostraba un "\n" literal después del enlace.
func TestInvitation_correo_html_sin_barra_n_literal(t *testing.T) {
	msg := invitationMail("Navajas", domain.Invitation{
		Email: "x@mail.com", Role: domain.RoleProfessional,
		ExpiresAt: time.Date(2027, 3, 8, 12, 0, 0, 0, time.UTC),
	}, "http://localhost:8081/invitacion#tok", "08/03/2027")
	if strings.Contains(msg.HTML, `\n`) {
		t.Fatalf("el HTML no puede llevar un \\n literal: %q", msg.HTML)
	}
	if !strings.Contains(msg.HTML, "</a></p>\n") {
		t.Fatalf("después del enlace tiene que haber un salto de línea real: %q", msg.HTML)
	}
}

// userConflictRunner simula la carrera real: otra transacción creó la cuenta con
// ese email entre el chequeo del caso de uso y el INSERT (el UNIQUE de users).
type userConflictRunner struct{ *fakeInvitations }
type userConflictTx struct{ ports.InvitationTx }

func (userConflictTx) SaveUser(domain.User) error { return domain.ErrAlreadyExists }
func (r userConflictRunner) InInvitationTx(fn func(tx ports.InvitationTx) error) error {
	return r.fakeInvitations.InInvitationTx(func(tx ports.InvitationTx) error { return fn(userConflictTx{tx}) })
}

// Hallazgo C: dos canjes simultáneos de invitaciones de DOS salones para el mismo
// email nuevo: el que pierde el UNIQUE de users recibía el "ya existe" crudo. El
// contrato manda el mismo mensaje que cuando la cuenta ya existía de antes, y la
// invitación del que perdió sigue pendiente (la transacción se deshace).
func TestInvitationAccept_cuenta_creada_en_la_carrera_da_el_mensaje_del_contrato(t *testing.T) {
	env := newInviteEnv(t)
	_, token := env.invite(t, "nueva@mail.com", domain.RoleProfessional)
	env.pub.Tx = userConflictRunner{env.inv}

	_, err := env.pub.AcceptNew(token, "Lucía", "secreta123")
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("esperaba 409, dio %v", err)
	}
	if got, want := err.Error(), "Ya tenés cuenta: iniciá sesión para aceptar la invitación"; got != want {
		t.Fatalf("mensaje = %q, esperaba %q", got, want)
	}
	if _, err := env.pub.Lookup(token); err != nil {
		t.Fatalf("la invitación del que perdió la carrera tiene que seguir pendiente: %v", err)
	}
}
