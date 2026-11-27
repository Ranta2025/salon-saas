package postgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
	waitlistuc "salon-saas/internal/waitlist"
)

// blockDayAt ocupa el hueco de la profesional el día LOCAL a `offset` días de hoy
// (de 09:00 a 12:00, como blockFrom): ese día queda sin horarios.
func (e *itWaitlistEnv) blockDayAt(t *testing.T, offset int) string {
	t.Helper()
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatalf("zona: %v", err)
	}
	d := time.Now().In(loc).AddDate(0, 0, offset)
	start := time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, loc)
	b, err := domain.NewBooking(e.salonID, e.profID, "Ocupa", "", start.UTC(), start.Add(3*time.Hour).UTC())
	if err != nil {
		t.Fatalf("reserva: %v", err)
	}
	if err := e.bookings.Save(b); err != nil {
		t.Fatalf("guardar reserva: %v", err)
	}
	return start.Format("2006-01-02")
}

func (e *itWaitlistEnv) activeCount(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx,
		`SELECT count(*) FROM waitlist_entries WHERE salon_id=$1 AND left_at IS NULL `+where,
		append([]any{e.salonID}, args...)...).Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	return n
}

// Verificador independiente (MEDIUM, reproducido contra Postgres real con el código
// anterior: 6 altas simultáneas del mismo email en 6 días llenos dejaban 6 filas
// activas con el tope en 3). El conteo y el insert corren ahora en UNA transacción
// bajo el lock `waitlist-write:<salón>`: los topes se respetan exactos.
func TestWaitlist_IT_topes_atomicos_con_altas_simultaneas(t *testing.T) {
	env := newItWaitlistEnv(t)
	const n = 6
	days := make([]string, 0, n)
	for i := 0; i < n; i++ {
		days = append(days, env.blockDayAt(t, 2+i))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var created, limited, other int
	start := make(chan struct{})
	for _, d := range days {
		wg.Add(1)
		go func(day string) {
			defer wg.Done()
			<-start
			res, err := env.svc.Join(env.slug, waitlistuc.WaitlistJoinInput{
				ServiceID: env.service.ID, Date: day, Name: "Lía", Email: "lia@mail.com",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && res.Created:
				created++
			case err != nil && err.Error() == domain.MsgWaitlistEmailLimit:
				limited++
			default:
				other++
				t.Errorf("resultado inesperado: %+v %v", res, err)
			}
		}(d)
	}
	close(start)
	wg.Wait()
	if got := env.activeCount(t, `AND lower(email)='lia@mail.com'`); got != domain.MaxWaitlistActivePerEmail {
		t.Fatalf("el tope por email es %d y quedaron %d avisos activos", domain.MaxWaitlistActivePerEmail, got)
	}
	if created != domain.MaxWaitlistActivePerEmail || limited != n-domain.MaxWaitlistActivePerEmail || other != 0 {
		t.Fatalf("esperaba %d altas y %d rechazos, hubo %d / %d / otros %d",
			domain.MaxWaitlistActivePerEmail, n-domain.MaxWaitlistActivePerEmail, created, limited, other)
	}
}

// Mismo defecto con el tope por SALÓN: con 499 activos sembrados, altas
// simultáneas de emails distintos solo dejan entrar UNA (500 exactos).
func TestWaitlist_IT_tope_por_salon_atomico(t *testing.T) {
	env := newItWaitlistEnv(t)
	future := time.Now().AddDate(0, 0, 12).Format("2006-01-02")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO waitlist_entries(id,salon_id,service_id,day,name,email,created_at)
		 SELECT md5(random()::text || i::text), $1, $2, $3::date, 'N', 'n' || i || '@mail.com', now()
		   FROM generate_series(1, $4) AS i`,
		env.salonID, env.service.ID, future, domain.MaxWaitlistActivePerSalon-1); err != nil {
		t.Fatalf("sembrar: %v", err)
	}
	const n = 12
	days := make([]string, 0, n)
	for i := 0; i < n; i++ {
		days = append(days, env.blockDayAt(t, 2+i))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var created, full int
	start := make(chan struct{})
	for i, d := range days {
		wg.Add(1)
		go func(i int, day string) {
			defer wg.Done()
			<-start
			res, err := env.svc.Join(env.slug, waitlistuc.WaitlistJoinInput{
				ServiceID: env.service.ID, Date: day, Name: "Ana", Email: "nueva" + string(rune('a'+i)) + "@mail.com",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && res.Created:
				created++
			case err != nil && errors.Is(err, domain.ErrAlreadyExists) && err.Error() == domain.MsgWaitlistSalonFull:
				full++
			default:
				t.Errorf("resultado inesperado: %+v %v", res, err)
			}
		}(i, d)
	}
	close(start)
	wg.Wait()
	if got := env.activeCount(t, ``); got != domain.MaxWaitlistActivePerSalon {
		t.Fatalf("el tope por salón es %d y quedaron %d activos", domain.MaxWaitlistActivePerSalon, got)
	}
	if created != 1 || full != n-1 {
		t.Fatalf("entra UNA y las demás reciben «completa»: %d / %d", created, full)
	}
}

// Revisión nativa R3-rejoin-after-notified, contra Postgres real: tras un aviso la
// persona vuelve a anotarse (el día se llenó otra vez): la entrada vieja queda de
// baja y la nueva espera. El índice único parcial no choca (la vieja se cerró en la
// misma transacción, antes del insert).
func TestWaitlist_IT_volver_a_anotarse_tras_un_aviso(t *testing.T) {
	env := newItWaitlistEnv(t)
	day, _ := itWaitlistDay(t)
	env.blockFrom(t, 0)
	in := waitlistuc.WaitlistJoinInput{ServiceID: env.service.ID, ProfessionalID: env.profID, Date: day, Name: "Lía", Email: "lia@mail.com"}
	if res, err := env.svc.Join(env.slug, in); err != nil || !res.Created {
		t.Fatalf("alta: %+v %v", res, err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE waitlist_entries SET notify_claimed_at=now(), notify_attempts=1, notified_at=now() WHERE salon_id=$1`,
		env.salonID); err != nil {
		t.Fatalf("marcar avisada: %v", err)
	}
	res, err := env.svc.Join(env.slug, in)
	if err != nil || !res.Created || res.Status != domain.WaitlistStatusWaiting {
		t.Fatalf("volver a anotarse crea otra: %+v %v", res, err)
	}
	var total, left, waiting int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*), count(*) FILTER (WHERE left_at IS NOT NULL),
		        count(*) FILTER (WHERE left_at IS NULL AND notified_at IS NULL)
		   FROM waitlist_entries WHERE salon_id=$1`, env.salonID).Scan(&total, &left, &waiting); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if total != 2 || left != 1 || waiting != 1 {
		t.Fatalf("esperaba 2 filas (1 de baja, 1 esperando), hay %d / %d / %d", total, left, waiting)
	}
	// Un repetido de la que ESPERA sigue siendo idempotente (200, sin otra fila).
	if again, err := env.svc.Join(env.slug, in); err != nil || again.Created {
		t.Fatalf("repetido de la que espera: %+v %v", again, err)
	}
	if got := env.activeCount(t, ``); got != 1 {
		t.Fatalf("tenía que quedar 1 activa, hay %d", got)
	}
}
