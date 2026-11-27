// El banco que NO está configurado (F11, D5/D12): sin PAYMENT_SIM_SECRET no se
// cobra, no se reembolsa y no se valida ningún callback. Lo importante acá es que
// un reembolso imposible devuelva ERROR y no nil: un barrido que creyera que
// devolvió el dinero cerraría la seña como `refunded` sin haberlo hecho.
package ports

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func TestNoopGateway_disabled(t *testing.T) {
	var g PaymentGateway = NoopGateway{}
	if g.Enabled() {
		t.Fatal("sin banco configurado Enabled() tiene que ser false")
	}
}

// CreateCharge: el 409 del contrato, no un cargo fantasma.
func TestNoopGateway_createChargeFails(t *testing.T) {
	var g PaymentGateway = NoopGateway{}
	ch, err := g.CreateCharge(context.Background(), ChargeRequest{
		Reference: "b-1", AmountCents: 4500, Description: "Corte", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err == nil {
		t.Fatalf("sin banco no se puede crear un cargo: %+v", ch)
	}
	if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgDepositGatewayUnavailable {
		t.Fatalf("error del cargo: %v", err)
	}
	if ch.ID != "" {
		t.Fatalf("un cargo imposible no devuelve id: %+v", ch)
	}
}

// Refund: un reembolso que no se hizo NUNCA puede devolver nil.
func TestNoopGateway_refundReportsFailure(t *testing.T) {
	var g PaymentGateway = NoopGateway{}
	if err := g.Refund(context.Background(), "cargo-cualquiera", 4500, "reserva cancelada"); err == nil {
		t.Fatal("Refund sin banco no puede devolver nil: parecería que devolvió el dinero")
	} else if !errors.Is(err, domain.ErrAlreadyExists) || err.Error() != domain.MsgDepositGatewayUnavailable {
		t.Fatalf("error del reembolso: %v", err)
	}
}

// ParseCallback: sin banco no hay firma que valga; el rechazo es el uniforme.
func TestNoopGateway_parseCallbackRejects(t *testing.T) {
	var g PaymentGateway = NoopGateway{}
	body := []byte(`{"event_id":"ev-1"}`)
	if _, err := g.ParseCallback(http.Header{}, body, time.Now()); err == nil {
		t.Fatal("sin banco no se acepta ningún callback")
	} else if err.Error() != domain.MsgPaymentSignatureInvalid {
		t.Fatalf("el rechazo tiene que ser el uniforme: %v", err)
	}
}
