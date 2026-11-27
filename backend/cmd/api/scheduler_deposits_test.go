package main

import (
	"context"

	"salon-saas/internal/app"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDepositRunner cuenta pasadas y permite fallar o reventar.
type fakeDepositRunner struct {
	mu    sync.Mutex
	calls int
	err   error
	panic bool
	res   app.DepositSweepResult
}

func (f *fakeDepositRunner) Sweep(int) (app.DepositSweepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.panic {
		panic("boom")
	}
	return f.res, f.err
}

func (f *fakeDepositRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitCalls espera hasta que el runner lleve n pasadas (o falla por tiempo).
func waitCalls(t *testing.T, r *fakeDepositRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.count() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("el barrido no llegó a %d pasadas (lleva %d)", n, r.count())
}

// D9: el barrido tickea con el intervalo pedido y se detiene con el contexto.
func TestRunDeposits_ticksAndStops(t *testing.T) {
	runner := &fakeDepositRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	logs := make([]string, 0, 4)
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, format)
	}
	go runDeposits(ctx, runner, time.Millisecond, logf)
	waitCalls(t, runner, 2)
	cancel()
	// Después de cancelar no hay pasadas nuevas.
	time.Sleep(10 * time.Millisecond)
	before := runner.count()
	time.Sleep(10 * time.Millisecond)
	if after := runner.count(); after != before {
		t.Fatalf("siguió barriendo tras el shutdown: %d -> %d", before, after)
	}
}

// DEPOSIT_INTERVAL=0 deshabilita la tarea (no arranca) y un contexto ya
// cancelado tampoco.
func TestRunDeposits_disabled(t *testing.T) {
	runner := &fakeDepositRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		runDeposits(ctx, runner, 0, func(string, ...any) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("con interval 0 la tarea no debe correr")
	}
	runDeposits(context.Background(), runner, 0, func(string, ...any) {})
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	runDeposits(cancelled, runner, time.Millisecond, func(string, ...any) {})
	if runner.count() != 0 {
		t.Fatalf("no debe haber pasadas: %d", runner.count())
	}
}

// Un panic de una pasada no se lleva la goroutine ni los ticks siguientes.
func TestRunDeposits_panicSafe(t *testing.T) {
	runner := &fakeDepositRunner{panic: true}
	runner.res = app.DepositSweepResult{Expired: 1, Cancelled: 1}
	runDepositPass(runner, func(string, ...any) {})
	if runner.count() != 1 {
		t.Fatalf("la pasada corre: %d", runner.count())
	}
	// El runner deja de fallar y la pasada siguiente trabaja normal.
	runner.mu.Lock()
	runner.panic = false
	runner.err = nil
	runner.mu.Unlock()
	runDepositPass(runner, func(string, ...any) {})
	if runner.count() != 2 {
		t.Fatalf("tras el panic se sigue: %d", runner.count())
	}
}

// D12: DEPOSIT_INTERVAL es una duración de Go; 60 s por defecto, 0 deshabilita el
// barrido y un valor negativo (o ilegible) impide arrancar en vez de caer en un
// default silencioso. PAYMENT_SIM_SECRET vacío deja el NoopGateway y en release un
// secreto corto se rechaza (mismo mínimo que JWT_SECRET).
func TestLoadConfig_deposits(t *testing.T) {
	base := func(extra map[string]string) map[string]string {
		env := map[string]string{"JWT_SECRET": "secreto", "GIN_MODE": "debug"}
		for k, v := range extra {
			env[k] = v
		}
		return env
	}
	cfg, err := loadConfig(loadEnv(base(nil)))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.DepositInterval != 60*time.Second {
		t.Fatalf("DEPOSIT_INTERVAL default = 60s, dio %s", cfg.DepositInterval)
	}
	if cfg.PaymentSimSecret != "" {
		t.Fatalf("sin PAYMENT_SIM_SECRET no hay banco (NoopGateway), dio %q", cfg.PaymentSimSecret)
	}
	if cfg.PaymentCallbackURL != "http://127.0.0.1:"+cfg.Port+"/api/v1/payments/callback" {
		t.Fatalf("PAYMENT_CALLBACK_URL default: %q", cfg.PaymentCallbackURL)
	}
	// 0 deshabilita: es una configuración VÁLIDA, no un error.
	cfg, err = loadConfig(loadEnv(base(map[string]string{"DEPOSIT_INTERVAL": "0"})))
	if err != nil {
		t.Fatalf("DEPOSIT_INTERVAL=0: %v", err)
	}
	if cfg.DepositInterval != 0 {
		t.Fatalf("DEPOSIT_INTERVAL=0 dio %s", cfg.DepositInterval)
	}
	// 2s es válido (el barrido de la demo).
	cfg, err = loadConfig(loadEnv(base(map[string]string{"DEPOSIT_INTERVAL": "2s"})))
	if err != nil || cfg.DepositInterval != 2*time.Second {
		t.Fatalf("DEPOSIT_INTERVAL=2s: %s (%v)", cfg.DepositInterval, err)
	}
	// Negativo e ilegible: error de arranque que nombra la variable.
	for _, bad := range []string{"-1s", "-60s", "cada rato"} {
		if _, err := loadConfig(loadEnv(base(map[string]string{"DEPOSIT_INTERVAL": bad}))); err == nil {
			t.Fatalf("DEPOSIT_INTERVAL=%q tiene que ser error de arranque", bad)
		} else if !strings.Contains(err.Error(), "DEPOSIT_INTERVAL") {
			t.Fatalf("DEPOSIT_INTERVAL=%q: el error tiene que nombrar la variable: %v", bad, err)
		}
	}
	// En release el secreto del banco exige el mínimo de JWT_SECRET.
	release := map[string]string{"GIN_MODE": "release", "JWT_SECRET": strings.Repeat("j", 32)}
	short := base(release)
	short["PAYMENT_SIM_SECRET"] = "corto"
	if _, err := loadConfig(loadEnv(short)); err == nil || !strings.Contains(err.Error(), "PAYMENT_SIM_SECRET") {
		t.Fatalf("secreto corto en release: %v", err)
	}
	long := base(release)
	long["PAYMENT_SIM_SECRET"] = strings.Repeat("b", 32)
	cfg, err = loadConfig(loadEnv(long))
	if err != nil || cfg.PaymentSimSecret == "" {
		t.Fatalf("secreto válido en release: %q (%v)", cfg.PaymentSimSecret, err)
	}
}
