// Concurrencia REAL de F15 contra Postgres (requiere SALON_IT=1): 30 canjes
// paralelos del MISMO token dejan exactamente un ganador.
//
// Lo que se prueba acá no se puede probar con un fake: el UPDATE condicional
// (`used_at IS NULL AND expires_at > now()`) toma el row lock de la fila y el
// perdedor ve 0 filas. Si se quita `used_at IS NULL` del WHERE, los 30 canjes
// "ganan" (éxito) y este test falla: es el mutante que pide el contrato.
package postgres

import (
	"sync"
	"testing"
	"time"

	"salon-saas/internal/domain"
)

func TestF15Race_thirtyParallelConsumeResetOneWins(t *testing.T) {
	f := f15Fixtures(t)
	tok, _ := f.reset(t, f15Ana, time.Now().UTC())
	hash := domain.HashRecoveryToken(tok)

	const n = 30
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := f.repo.ConsumeReset(hash, "h-ganadora-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
			errs[i] = err
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("canjes exitosos: %d, esperaba exactamente 1 (%v)", wins, errs)
	}
	for i, err := range errs {
		if err != nil && err != domain.ErrNotFound {
			t.Errorf("canje %d: error inesperado %v (los perdedores dicen ErrNotFound)", i, err)
		}
	}
	// La fila quedó usada UNA vez y el usuario tiene una sola contraseña nueva.
	if got := f.count(t, `SELECT count(*) FROM password_resets WHERE token_hash=$1 AND used_at IS NOT NULL`, hash); got != 1 {
		t.Errorf("pedidos usados: %d", got)
	}
	var hashSaved string
	var changed *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT pass_hash, password_changed_at FROM users WHERE id=$1`, f15Ana).
		Scan(&hashSaved, &changed); err != nil {
		t.Fatal(err)
	}
	if changed == nil {
		t.Fatal("el canje ganador no marcó password_changed_at")
	}
	var usedAt *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT used_at FROM password_resets WHERE token_hash=$1`, hash).Scan(&usedAt); err != nil {
		t.Fatal(err)
	}
	if usedAt == nil || !usedAt.Equal(*changed) {
		t.Errorf("used_at=%v password_changed_at=%v: tienen que ser el mismo instante de la base", usedAt, changed)
	}
	// Un segundo intento posterior tampoco revive el token.
	if err := f.repo.ConsumeReset(hash, "h-otra"); err != domain.ErrNotFound {
		t.Fatalf("el token ya gastado sigue sirviendo: %v", err)
	}
}
