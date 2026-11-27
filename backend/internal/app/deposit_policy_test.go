package app

import (
	"errors"
	"testing"

	dep "salon-saas/internal/deposits"
	"salon-saas/internal/domain"
)

// input arma el cuerpo de PUT con las cuatro claves presentes.
func policyInput(enabled bool, percent, hold, refund int) dep.DepositPolicyInput {
	return dep.DepositPolicyInput{Enabled: &enabled, Percent: &percent, HoldMinutes: &hold, RefundHours: &refund}
}

func TestDepositPolicy_Get_roles(t *testing.T) {
	e := newDepEnv(t)
	// Sin fila: Saved=false y sin error (el handler muestra los nulls de D3).
	for _, actor := range []domain.Membership{e.owner, e.admin, e.recep, e.prof, e.client} {
		p, err := e.policy.Get(actor, "s1")
		if err != nil {
			t.Fatalf("Get de %s: %v", actor.Role, err)
		}
		if p.Saved || p.Enabled || p.Percent != 0 || p.HoldMinutes != 0 || p.RefundHours != 0 {
			t.Fatalf("política sin guardar = %+v", p)
		}
	}
	// Dado de baja y otro salón: 403.
	if _, err := e.policy.Get(e.removed, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("dado de baja: %v", err)
	}
	if _, err := e.policy.Get(e.other, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("otro salón: %v", err)
	}
}

func TestDepositPolicy_Put_permiso_antes_del_cuerpo(t *testing.T) {
	e := newDepEnv(t)
	// Reception, profesional, clienta y baja: 403 aunque el cuerpo esté VACÍO.
	for _, actor := range []domain.Membership{e.recep, e.prof, e.client, e.removed} {
		_, err := e.policy.Put(actor, "s1", dep.DepositPolicyInput{})
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: %v", actor.Role, err)
		}
	}
	// Y RequireManager (lo que el handler llama ANTES de leer el cuerpo) también.
	if err := e.policy.RequireManager(e.recep, "s1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RequireManager recepción: %v", err)
	}
	if err := e.policy.RequireManager(e.owner, "s1"); err != nil {
		t.Fatalf("RequireManager dueño: %v", err)
	}
	if e.policies.savedCount() != 0 {
		t.Fatal("un 403 no puede guardar nada")
	}
}

func TestDepositPolicy_Put_claves_obligatorias(t *testing.T) {
	e := newDepEnv(t)
	enabled, percent, hold, refund := true, 30, 30, 24
	cases := map[string]dep.DepositPolicyInput{
		"todas":   {},
		"enabled": {Percent: &percent, HoldMinutes: &hold, RefundHours: &refund},
		"percent": {Enabled: &enabled, HoldMinutes: &hold, RefundHours: &refund},
		"hold":    {Enabled: &enabled, Percent: &percent, RefundHours: &refund},
		"refund":  {Enabled: &enabled, Percent: &percent, HoldMinutes: &hold},
	}
	for name, in := range cases {
		_, err := e.policy.Put(e.owner, "s1", in)
		if err == nil || err.Error() != domain.MsgDepositPolicyMissing {
			t.Fatalf("%s: err = %v, quiero %q", name, err, domain.MsgDepositPolicyMissing)
		}
	}
	if e.policies.savedCount() != 0 {
		t.Fatal("con una clave faltante no se guarda nada")
	}
}

func TestDepositPolicy_Put_rangos(t *testing.T) {
	e := newDepEnv(t)
	cases := []struct {
		name     string
		in       dep.DepositPolicyInput
		wantText string
	}{
		{"porcentaje bajo", policyInput(true, 9, 30, 24), domain.MsgDepositPercentRange},
		{"porcentaje alto", policyInput(true, 101, 30, 24), domain.MsgDepositPercentRange},
		{"espera baja", policyInput(true, 30, 4, 24), domain.MsgDepositHoldRange},
		{"espera alta", policyInput(true, 30, 121, 24), domain.MsgDepositHoldRange},
		{"devolución negativa", policyInput(true, 30, 30, -1), domain.MsgDepositRefundRange},
		{"devolución alta", policyInput(true, 30, 30, 169), domain.MsgDepositRefundRange},
		// El orden importa: el porcentaje se valida primero.
		{"varias a la vez", policyInput(true, 9, 1, 999), domain.MsgDepositPercentRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.policy.Put(e.owner, "s1", tc.in)
			if err == nil || err.Error() != tc.wantText {
				t.Fatalf("err = %v, quiero %q", err, tc.wantText)
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("el rango es 400, es %v", err)
			}
		})
	}
	if e.policies.savedCount() != 0 {
		t.Fatal("con un rango inválido no se guarda nada")
	}
}

// Activar la seña sin banco configurado es 409 (D3/D12) y no guarda nada.
func TestDepositPolicy_Put_activar_sin_banco_409(t *testing.T) {
	e := newDepEnv(t)
	e.policy.Gateway = nil
	_, err := e.policy.Put(e.owner, "s1", policyInput(true, 30, 30, 24))
	requireKind(t, err, domain.ErrAlreadyExists)
	if err == nil || err.Error() != domain.MsgDepositGatewayUnavailable {
		t.Fatalf("err = %v, quiero %q", err, domain.MsgDepositGatewayUnavailable)
	}
	if e.policies.savedCount() != 0 {
		t.Fatal("sin banco no se guarda la política")
	}

	// Con el banco apagado (NoopGateway) es el mismo 409.
	e.policy.Gateway = ports_noopGateway{}
	_, err = e.policy.Put(e.owner, "s1", policyInput(true, 30, 30, 24))
	requireKind(t, err, domain.ErrAlreadyExists)

	// Apagada SÍ se puede guardar sin banco: no cobra nada.
	if _, err := e.policy.Put(e.owner, "s1", policyInput(false, 30, 30, 24)); err != nil {
		t.Fatalf("apagada sin banco: %v", err)
	}
	if e.policies.savedCount() != 1 {
		t.Fatalf("guardados = %d, quiero 1", e.policies.savedCount())
	}
}

func TestDepositPolicy_Put_ok_y_Get(t *testing.T) {
	e := newDepEnv(t)
	for _, actor := range []domain.Membership{e.owner, e.admin} {
		saved, err := e.policy.Put(actor, "s1", policyInput(true, 30, 45, 24))
		if err != nil {
			t.Fatalf("%s: %v", actor.Role, err)
		}
		if !saved.Saved || !saved.Enabled || saved.Percent != 30 || saved.HoldMinutes != 45 || saved.RefundHours != 24 {
			t.Fatalf("guardada = %+v", saved)
		}
		if !saved.UpdatedAt.Equal(depClock) {
			t.Fatalf("UpdatedAt = %s, quiero %s", saved.UpdatedAt, depClock)
		}
		got, err := e.policy.Get(e.recep, "s1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Percent != 30 || got.HoldMinutes != 45 || got.RefundHours != 24 || !got.Enabled || !got.Saved {
			t.Fatalf("leída = %+v", got)
		}
	}
	// Los bordes válidos también se guardan.
	if _, err := e.policy.Put(e.owner, "s1", policyInput(true, 100, 120, 168)); err != nil {
		t.Fatalf("bordes: %v", err)
	}
	if _, err := e.policy.Put(e.owner, "s1", policyInput(true, 10, 5, 0)); err != nil {
		t.Fatalf("bordes: %v", err)
	}
}
