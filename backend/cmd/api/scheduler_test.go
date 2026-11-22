package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner cuenta pasadas; panicOn hace explotar la primera, que la tarea
// tiene que recuperar sin morir.
type fakeRunner struct {
	mu      sync.Mutex
	calls   int
	panicOn int
	ticks   chan struct{}
}

func (f *fakeRunner) RunOnce(ctx context.Context, now time.Time) (int, int) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if f.ticks != nil {
		f.ticks <- struct{}{}
	}
	if n == f.panicOn {
		panic("boom del recordatorio")
	}
	return 1, 0
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// T12: la tarea periódica corre cada intervalo, recupera un panic y se apaga
// con el contexto del shutdown.
func TestRunReminders_ticks_panic_y_shutdown(t *testing.T) {
	runner := &fakeRunner{panicOn: 1, ticks: make(chan struct{}, 8)}
	var mu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runReminders(ctx, runner, 10*time.Millisecond, logf)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-runner.ticks:
		case <-time.After(2 * time.Second):
			t.Fatalf("esperaba el tick %d", i+1)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runReminders no terminó al cancelar el contexto")
	}
	if runner.count() < 2 {
		t.Fatalf("el panic no puede cortar la tarea: %d pasadas", runner.count())
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "panic") {
		t.Fatalf("el panic recuperado tiene que quedar en el log: %q", joined)
	}
	if !strings.Contains(joined, "enviados=1") {
		t.Fatalf("los contadores de la pasada se loguean: %q", joined)
	}
}

// Un contexto ya cancelado no ejecuta ninguna pasada.
func TestRunReminders_contexto_cancelado(t *testing.T) {
	runner := &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runReminders(ctx, runner, time.Millisecond, func(string, ...any) {})
	if runner.count() != 0 {
		t.Fatalf("no debe correr con el contexto cancelado: %d", runner.count())
	}
}
