package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeReviewRunner cuenta pasadas y permite fallar o reventar.
type fakeReviewRunner struct {
	mu    sync.Mutex
	calls int
	sent  int
	fail  int
	panic bool
}

func (f *fakeReviewRunner) RunOnce(context.Context, time.Time) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.panic {
		panic("boom")
	}
	return f.sent, f.fail
}

func (f *fakeReviewRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// F13/D3: la tarea tickea con el intervalo pedido y se detiene con el contexto.
func TestRunReviews_ticksAndStops(t *testing.T) {
	runner := &fakeReviewRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	go runReviews(ctx, runner, time.Millisecond, func(string, ...any) {})
	deadline := time.Now().Add(2 * time.Second)
	for runner.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("el barrido no llegó a 2 pasadas (lleva %d)", runner.count())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	time.Sleep(10 * time.Millisecond)
	before := runner.count()
	time.Sleep(10 * time.Millisecond)
	if after := runner.count(); after != before {
		t.Fatalf("siguió barriendo tras el shutdown: %d -> %d", before, after)
	}
}

// REVIEW_INTERVAL=0 deshabilita la tarea (no arranca) y un contexto ya cancelado
// tampoco.
func TestRunReviews_disabled(t *testing.T) {
	runner := &fakeReviewRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runReviews(ctx, runner, 0, func(string, ...any) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("con interval 0 la tarea no debe correr")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	runReviews(cancelled, runner, time.Millisecond, func(string, ...any) {})
	if runner.count() != 0 {
		t.Fatalf("no debe haber pasadas: %d", runner.count())
	}
}

// Un panic de una pasada no se lleva la goroutine ni los ticks siguientes, y solo
// se loguean contadores.
func TestRunReviews_panicSafe(t *testing.T) {
	runner := &fakeReviewRunner{panic: true}
	var mu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, format)
	}
	runReviewPass(context.Background(), runner, logf)
	if runner.count() != 1 {
		t.Fatalf("la pasada corre: %d", runner.count())
	}
	runner.mu.Lock()
	runner.panic = false
	runner.sent = 1
	runner.mu.Unlock()
	runReviewPass(context.Background(), runner, logf)
	if runner.count() != 2 {
		t.Fatalf("tras el panic se sigue: %d", runner.count())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 {
		t.Fatalf("se loguea el panic y la pasada con envíos: %v", logs)
	}
	// Nunca direcciones ni cuerpos: solo formatos con contadores.
	for _, l := range logs {
		if l == "" || len(l) > 80 {
			t.Fatalf("log raro: %q", l)
		}
	}
}
