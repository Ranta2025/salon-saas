// Tarea periódica de recordatorios (T12). Corre en su propia goroutine, con el
// contexto del shutdown elegante: al recibir SIGINT/SIGTERM deja de tickear y
// lo reclamado sin enviar vuelve solo (gracia del claim).
package main

import (
	"context"
	"time"
)

// reminderRunner es lo que la tarea necesita de ReminderService (los tests usan
// un doble; el composition root pasa el caso de uso real).
type reminderRunner interface {
	RunOnce(ctx context.Context, now time.Time) (sent, failed int)
}

// runReminders tickea cada interval hasta que ctx se cancele. Nunca deja morir
// la goroutine por un panic de un envío: lo recupera, lo loguea y sigue con el
// próximo tick.
func runReminders(ctx context.Context, runner reminderRunner, interval time.Duration, logf func(string, ...any)) {
	if ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runReminderPass(ctx, runner, logf)
		}
	}
}

// runReminderPass es UNA pasada con recuperación de panic. Solo se loguean
// contadores: nunca direcciones ni cuerpos de correo.
func runReminderPass(ctx context.Context, runner reminderRunner, logf func(string, ...any)) {
	defer func() {
		if r := recover(); r != nil {
			logf("recordatorios: panic recuperado: %v", r)
		}
	}()
	sent, failed := runner.RunOnce(ctx, time.Now().UTC())
	if sent > 0 || failed > 0 {
		logf("recordatorios: enviados=%d fallidos=%d", sent, failed)
	}
}

// waitlistRunner es lo que la tarea de la lista de espera necesita (F7): el
// caso de uso real o un doble en los tests.
type waitlistRunner interface {
	RunOnce(ctx context.Context, now time.Time) (notified, failed int)
}

// runWaitlist barre la lista de espera cada interval hasta que ctx se cancele
// (F7/D1). Es una goroutine más, con el MISMO contexto del shutdown que los
// recordatorios: al recibir SIGINT/SIGTERM deja de tickear y lo reclamado sin
// enviar vuelve solo pasada la gracia del reclamo. Con interval <= 0 no arranca
// (el contrato pide que 0 deshabilite la tarea).
func runWaitlist(ctx context.Context, runner waitlistRunner, interval time.Duration, logf func(string, ...any)) {
	if ctx.Err() != nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWaitlistPass(ctx, runner, logf)
		}
	}
}

// runWaitlistPass es UNA pasada con recuperación de panic: un envío que revienta
// no puede llevarse la goroutine (ni los ticks siguientes). Solo se loguean
// contadores: nunca direcciones ni cuerpos de correo.
func runWaitlistPass(ctx context.Context, runner waitlistRunner, logf func(string, ...any)) {
	defer func() {
		if r := recover(); r != nil {
			logf("lista de espera: panic recuperado: %v", r)
		}
	}()
	notified, failed := runner.RunOnce(ctx, time.Now().UTC())
	if notified > 0 || failed > 0 {
		logf("lista de espera: avisados=%d fallidos=%d", notified, failed)
	}
}
