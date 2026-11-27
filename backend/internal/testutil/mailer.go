package testutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/ports"
)

// FakeMailer registra los mensajes. Con Release no nulo, Send se queda
// bloqueado hasta que el test lo libere: prueba que la operación no espera al
// correo. Es la copia exportada del fake de los tests de app (los _test.go no se
// importan entre paquetes).
type FakeMailer struct {
	Mu      sync.Mutex
	Sent    []ports.Message
	Err     error
	ErrFor  map[string]error
	Started chan struct{}
	Release chan struct{}
}

func (f *FakeMailer) Send(ctx context.Context, msg ports.Message) error {
	if f.Started != nil {
		select {
		case f.Started <- struct{}{}:
		default:
		}
	}
	if f.Release != nil {
		select {
		case <-f.Release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if err, ok := f.ErrFor[msg.To]; ok {
		return err
	}
	if f.Err != nil {
		return f.Err
	}
	f.Sent = append(f.Sent, msg)
	return nil
}

// Count es cuántos mensajes salieron.
func (f *FakeMailer) Count() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return len(f.Sent)
}

// Messages es una copia de lo enviado.
func (f *FakeMailer) Messages() []ports.Message {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return append([]ports.Message(nil), f.Sent...)
}

// WaitCount espera (hasta 2 s) a que se hayan enviado n mensajes: los avisos
// viajan en goroutine y el test no puede asumir el instante.
func (f *FakeMailer) WaitCount(t *testing.T, n int) []ports.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs := f.Messages()
		if len(msgs) >= n {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperaba %d mensajes enviados, hubo %d", n, len(msgs))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var _ ports.MailSender = (*FakeMailer)(nil)
