package app

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// Verificador independiente (MEDIUM, reproducido contra Postgres real): seis altas
// simultáneas del MISMO email en seis días llenos dejaban seis filas activas con
// el tope en 3. Cuenta y alta eran llamadas sueltas, sin el lock de la lista. La
// compuerta obliga a que todas las altas lean el conteo antes de que alguna
// escriba: sin transacción con lock, TODAS ven 0 y TODAS crean.
func TestWaitlistJoin_los_topes_son_atomicos(t *testing.T) {
	env := newWaitlistEnv(t)
	const n = 6
	days := make([]string, 0, n)
	for i := 0; i < n; i++ {
		d, _ := wlDay(t, 2+i)
		env.blockDay(t, d)
		days = append(days, d)
	}
	var arrived int32
	release := make(chan struct{})
	env.wl.countGate = func() {
		if atomic.AddInt32(&arrived, 1) == n {
			close(release)
		}
		select {
		case <-release:
		case <-time.After(300 * time.Millisecond):
		}
	}

	var wg sync.WaitGroup
	for _, d := range days {
		wg.Add(1)
		go func(day string) {
			defer wg.Done()
			_, _ = env.svc.Join(waitlistSlug, WaitlistJoinInput{
				ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com",
			})
		}(d)
	}
	wg.Wait()
	if got := env.entryCount(); got != domain.MaxWaitlistActivePerEmail {
		t.Fatalf("el tope por email es %d y quedaron %d avisos activos", domain.MaxWaitlistActivePerEmail, got)
	}
}

// Revisión nativa R3-rejoin-after-notified: una persona avisada que no reservó y
// vuelve a anotarse (el día se llenó otra vez) recibía 200 'notified' sin crear
// nada, así que nunca más se la avisaba. D6: una entrada se avisa UNA vez y
// volver a unirse crea OTRA. La vieja se cierra (baja) y la nueva espera.
func TestWaitlistJoin_volver_a_anotarse_tras_un_aviso_crea_otra(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)
	old, _ := env.wlSeed(t, env.corte.ID, "", day, "Lía", "a@mail.com", time.Now().Add(-time.Hour))
	sent := time.Now().Add(-30 * time.Minute).UTC()
	env.wl.entries[0].NotifyClaimedAt = &sent
	env.wl.entries[0].NotifiedAt = &sent

	res, err := env.svc.Join(waitlistSlug, WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"})
	if err != nil || !res.Created || res.Status != domain.WaitlistStatusWaiting {
		t.Fatalf("volver a anotarse tras un aviso crea otra entrada que espera: %+v %v", res, err)
	}
	if env.entryCount() != 2 {
		t.Fatalf("tenían que quedar la vieja y la nueva, hay %d", env.entryCount())
	}
	if env.findEntry(t, old.ID).LeftAt == nil {
		t.Fatalf("la entrada ya avisada se cierra (baja) para dejar lugar a la nueva")
	}
	// Idempotencia intacta: un repetido de la que ESPERA sigue siendo 200 sin escribir.
	again, err := env.svc.Join(waitlistSlug, WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"})
	if err != nil || again.Created || again.Status != domain.WaitlistStatusWaiting || env.entryCount() != 2 {
		t.Fatalf("repetido de la que espera: %+v %v (filas %d)", again, err, env.entryCount())
	}
}

// El alta toma el lock `waitlist-write:<salón>` de la lista (el MISMO del barrido):
// el mutex del fake serializa la transacción entera, así que sin esta aserción un
// Join que se olvida de pedir el lock pasaría todos los tests de arriba.
func TestWaitlistJoin_toma_el_lock_de_la_lista_del_salon(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)
	if _, err := env.svc.Join(waitlistSlug, WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"}); err != nil {
		t.Fatalf("alta: %v", err)
	}
	found := false
	for _, l := range env.wl.locks {
		if l == "waitlist-write:s1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("el alta no pidió el lock de la lista del salón: %v", env.wl.locks)
	}
}
