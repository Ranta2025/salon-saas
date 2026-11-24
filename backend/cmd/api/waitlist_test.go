package main

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// F7: la tarea periódica de la lista de espera y su configuración
// (WAITLIST_INTERVAL). La pasada del barrido en sí se prueba en app y en los IT
// de Postgres; acá se fija el ciclo de vida y que 0 la deshabilita.

// fakeWaitlistRunner cuenta pasadas y puede reventar en la primera.
type fakeWaitlistRunner struct {
	passes atomic.Int32
	panic  bool
	mu     sync.Mutex
}

func (f *fakeWaitlistRunner) RunOnce(_ context.Context, _ time.Time) (int, int) {
	n := f.passes.Add(1)
	if f.panic && n == 1 {
		panic("boom")
	}
	return 0, 0
}

// Con interval 0 la tarea NO arranca: el contrato pide que 0 la deshabilite.
func TestRunWaitlist_deshabilitada_con_cero(t *testing.T) {
	runner := &fakeWaitlistRunner{}
	done := make(chan struct{})
	go func() {
		runWaitlist(context.Background(), runner, 0, func(string, ...any) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("con interval 0 la tarea tiene que terminar de inmediato")
	}
	if runner.passes.Load() != 0 {
		t.Fatalf("con interval 0 no se barre, hubo %d pasadas", runner.passes.Load())
	}
	// Negativo: tampoco.
	runWaitlist(context.Background(), runner, -time.Second, func(string, ...any) {})
	if runner.passes.Load() != 0 {
		t.Fatalf("con interval negativo no se barre, hubo %d pasadas", runner.passes.Load())
	}
}

// Tickea mientras el contexto vive, se detiene al cancelar y un panic de una
// pasada no se lleva la goroutine.
func TestRunWaitlist_tickea_y_se_detiene(t *testing.T) {
	runner := &fakeWaitlistRunner{panic: true}
	var panics atomic.Int32
	logf := func(format string, args ...any) {
		if strings.Contains(format, "panic") {
			panics.Add(1)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runWaitlist(ctx, runner, 5*time.Millisecond, logf)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for runner.passes.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("la tarea tiene que terminar cuando se cancela el contexto")
	}
	if runner.passes.Load() < 3 {
		t.Fatalf("esperaba al menos 3 pasadas, hubo %d", runner.passes.Load())
	}
	if panics.Load() == 0 {
		t.Fatal("el panic de una pasada tiene que quedar recuperado y logueado")
	}
	// Un contexto ya cancelado no arranca nada.
	before := runner.passes.Load()
	runWaitlist(ctx, runner, time.Millisecond, logf)
	if runner.passes.Load() != before {
		t.Fatal("un contexto cancelado no puede disparar una pasada")
	}
}

// WAITLIST_INTERVAL: 60s por defecto, 0 deshabilita, negativo o ilegible es
// error de configuración (nunca un default silencioso).
func TestLoadConfig_WaitlistInterval(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}
	cfg, err := loadConfig(loadEnv(base))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.WaitlistInterval != 60*time.Second {
		t.Fatalf("default: %s", cfg.WaitlistInterval)
	}

	for _, raw := range []string{"0", "2s", "90s"} {
		env := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug", "WAITLIST_INTERVAL": raw}
		cfg, err := loadConfig(loadEnv(env))
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		want, _ := time.ParseDuration(raw)
		if cfg.WaitlistInterval != want {
			t.Fatalf("%q: dio %s", raw, cfg.WaitlistInterval)
		}
	}
	for _, raw := range []string{"-5s", "cada rato"} {
		env := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug", "WAITLIST_INTERVAL": raw}
		if _, err := loadConfig(loadEnv(env)); err == nil || !strings.Contains(err.Error(), "WAITLIST_INTERVAL") {
			t.Fatalf("%q tiene que fallar nombrando la variable, dio %v", raw, err)
		}
	}
}

// El composition root monta las cuatro rutas de F7 y cablea la tarea.
func TestBuildRouter_waitlist(t *testing.T) {
	cfg := config{
		DatabaseURL: "postgres://salon:salon@127.0.0.1:5432/salon?sslmode=disable",
		JWTSecret:   strings.Repeat("s", 40),
		CORSOrigins: []string{"http://localhost:5173"},
		MediaDir:    t.TempDir(),
		Port:        "0",
	}
	router, deps, _ := buildRouter(cfg, nil)
	if deps.Waitlist.Service.Waitlist == nil || deps.Waitlist.Service.Tx == nil {
		t.Fatal("la lista de espera tiene que recibir su repositorio y su transacción")
	}
	if deps.PublicWaitlistLeaveLimit == nil {
		t.Fatal("la baja de la lista necesita su propio rate limit por IP")
	}
	have := map[string]bool{}
	for _, r := range router.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"POST /api/v1/public/salons/:slug/waitlist",
		"POST /api/v1/public/waitlist/leave",
		"GET /api/v1/salons/:id/waitlist",
		"DELETE /api/v1/salons/:id/waitlist/:eid",
	} {
		if !have[want] {
			t.Fatalf("falta la ruta %q (registradas: %d)", want, len(have))
		}
	}
}
