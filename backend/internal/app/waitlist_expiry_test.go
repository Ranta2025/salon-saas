package app

import (
	"strconv"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

// Revisión nativa R4-001 (CRITICAL): los topes de D4 contaban TODA entrada sin
// baja, también las de días que ya pasaron, y no hay purga. Tres avisos viejos
// bloqueaban para siempre a ese email y 500 viejos llenaban el salón. Un aviso
// cuyo día ya pasó no ocupa cupo: solo cuentan los de hoy en adelante, en la
// zona del salón.
func TestWaitlistJoin_los_topes_no_cuentan_dias_pasados(t *testing.T) {
	env := newWaitlistEnv(t)
	day, _ := wlDay(t, 2)
	env.blockDay(t, day)
	now := time.Now()

	// Tres entradas sin baja del mismo email, TODAS de días ya pasados.
	for i := 0; i < domain.MaxWaitlistActivePerEmail; i++ {
		past, _ := wlDay(t, -(i + 1))
		env.wlSeed(t, env.corte.ID, "", past, "Lía", "a@mail.com", now.Add(time.Duration(i)*time.Minute))
	}
	res, err := env.svc.Join(waitlistSlug, WaitlistJoinInput{ServiceID: env.corte.ID, Date: day, Name: "Lía", Email: "a@mail.com"})
	if err != nil || !res.Created {
		t.Fatalf("los avisos de días pasados no cuentan para el tope por email: %+v %v", res, err)
	}

	// 500 entradas sin baja de días pasados: el salón NO está completo.
	env2 := newWaitlistEnv(t)
	env2.blockDay(t, day)
	past, _ := wlDay(t, -1)
	for i := 0; i < domain.MaxWaitlistActivePerSalon; i++ {
		env2.wlSeed(t, env2.corte.ID, "", past, "N", "n"+strconv.Itoa(i)+"@mail.com", now)
	}
	res, err = env2.svc.Join(waitlistSlug, WaitlistJoinInput{ServiceID: env2.corte.ID, Date: day, Name: "Lía", Email: "nueva@mail.com"})
	if err != nil || !res.Created {
		t.Fatalf("los avisos de días pasados no cuentan para el tope por salón: %+v %v", res, err)
	}
}
