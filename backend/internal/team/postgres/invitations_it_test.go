// F2g1 contra Postgres real (SALON_IT=1): la migración 021 y las garantías duras
// del adaptador — el token se guarda solo hasheado, una pendiente por email y
// salón, y el canje es atómico (uno gana, el otro 404 genérico).
package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil/itdb"
)

// invitationFixtures deja dos salones limpios y su dueño.
func invitationFixtures(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := itdb.Pool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	itdb.ApplyMigrations(t, pool)

	for _, q := range []string{
		`DELETE FROM salons WHERE id IN ('s-it-f2g1','s-it-f2g1b')`,
		`DELETE FROM users WHERE id LIKE 'u-it-f2g1-%'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("limpieza: %v", err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,name,email,pass_hash) VALUES
			('u-it-f2g1-owner','Ana F2g1','u-it-f2g1-owner@test.com','x')`,
		`INSERT INTO salons(id,slug,name) VALUES
			('s-it-f2g1','s-it-f2g1','Salón F2g1'),
			('s-it-f2g1b','s-it-f2g1b','Salón F2g1 B')`,
		`INSERT INTO memberships(user_id,salon_id,role,attends) VALUES
			('u-it-f2g1-owner','s-it-f2g1','owner',true)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return pool, ctx
}

// pendingInvitation arma una invitación pendiente válida para el salón dado.
func pendingInvitation(t *testing.T, salonID, email, token string, now time.Time) domain.Invitation {
	t.Helper()
	inv, err := domain.NewInvitation(salonID, email, domain.RoleProfessional, true,
		domain.HashInvitationToken(token), "u-it-f2g1-owner", now)
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	return inv
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		found := false
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// S-F2g1-1/2: en la DB queda SOLO el SHA-256 del token. Se mira la fila entera
// serializada: si el token en claro estuviera en cualquier columna, aparecería.
func TestInvitations_IT_token_solo_hasheado(t *testing.T) {
	pool, ctx := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()
	token := "token-en-claro-que-no-puede-quedar-1234567890"
	inv := pendingInvitation(t, "s-it-f2g1", "lucia@mail.com", token, now)

	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(inv) }); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	var rowJSON string
	if err := pool.QueryRow(ctx,
		`SELECT to_jsonb(i)::text FROM invitations i WHERE id=$1`, inv.ID).Scan(&rowJSON); err != nil {
		t.Fatalf("leer la fila: %v", err)
	}
	if containsAll(rowJSON, token) {
		t.Fatalf("el token en claro no puede estar en la DB: %s", rowJSON)
	}
	if !containsAll(rowJSON, domain.HashInvitationToken(token)) {
		t.Fatalf("falta el hash del token: %s", rowJSON)
	}
	// Y la búsqueda por token hasheado lo encuentra (la única sin salon_id).
	got, err := repo.FindByTokenHash(domain.HashInvitationToken(token))
	if err != nil {
		t.Fatalf("FindByTokenHash: %v", err)
	}
	if got.ID != inv.ID || got.Email != "lucia@mail.com" || !got.Pending(now) {
		t.Fatalf("invitación leída: %+v", got)
	}
	// Un hash cualquiera no existe (el 404 del caso de uso).
	if _, err := repo.FindByTokenHash(domain.HashInvitationToken("otro")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token desconocido: %v", err)
	}
}

// S-F2g1-3: una sola pendiente por email y salón (índice único parcial). El
// mismo email en otro salón sí puede tener la suya, y una revocada no bloquea.
func TestInvitations_IT_una_pendiente_por_email(t *testing.T) {
	pool, ctx := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()

	first := pendingInvitation(t, "s-it-f2g1", "lucia@mail.com", "tok-a", now)
	second := pendingInvitation(t, "s-it-f2g1", "LUCIA@mail.com", "tok-b", now)
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(first) }); err != nil {
		t.Fatalf("primera: %v", err)
	}
	err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(second) })
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("segunda pendiente del mismo email: %v", err)
	}
	// Otro salón: permitido.
	other := pendingInvitation(t, "s-it-f2g1b", "lucia@mail.com", "tok-c", now)
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(other) }); err != nil {
		t.Fatalf("otro salón: %v", err)
	}
	// Revocar la primera libera el lugar (reinvitar).
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		return tx.RevokePendingByEmail("s-it-f2g1", "lucia@mail.com", now)
	}); err != nil {
		t.Fatalf("revocar pendiente: %v", err)
	}
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(second) }); err != nil {
		t.Fatalf("reinvitar después de revocar: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM invitations
		WHERE salon_id='s-it-f2g1' AND accepted_at IS NULL AND revoked_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pendientes del salón: %d (esperaba 1)", pending)
	}
}

// S-F2g1-3/4: listado scopeado por salón (pendientes de verdad: ni revocadas, ni
// vencidas, ni aceptadas) y las dos queries por id.
func TestInvitations_IT_listado_y_scope(t *testing.T) {
	pool, ctx := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()

	pending := pendingInvitation(t, "s-it-f2g1", "pendiente@mail.com", "tok-pending", now)
	expired := pendingInvitation(t, "s-it-f2g1", "vencida@mail.com", "tok-expired", now)
	expired.ExpiresAt = now.Add(-time.Minute)
	revoked := pendingInvitation(t, "s-it-f2g1", "revocada@mail.com", "tok-revoked", now)
	accepted := pendingInvitation(t, "s-it-f2g1", "aceptada@mail.com", "tok-accepted", now)
	foreign := pendingInvitation(t, "s-it-f2g1b", "ajena@mail.com", "tok-foreign", now)

	for _, inv := range []domain.Invitation{pending, expired, revoked, accepted, foreign} {
		if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(inv) }); err != nil {
			t.Fatalf("guardar %s: %v", inv.Email, err)
		}
	}
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		return tx.Revoke("s-it-f2g1", revoked.ID, now)
	}); err != nil {
		t.Fatalf("revocar: %v", err)
	}
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		_, err := tx.Claim(accepted.ID, "u-it-f2g1-owner")
		return err
	}); err != nil {
		t.Fatalf("aceptar: %v", err)
	}

	list, err := repo.ListPending("s-it-f2g1", now)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(list) != 1 || list[0].Email != "pendiente@mail.com" {
		t.Fatalf("pendientes (solo la que sirve): %+v", list)
	}
	// Scope: el id de otro salón no existe desde este.
	if _, err := repo.FindByID("s-it-f2g1", foreign.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invitación de otro salón: %v", err)
	}
	if got, err := repo.FindByID("s-it-f2g1b", foreign.ID); err != nil || got.SalonID != "s-it-f2g1b" {
		t.Fatalf("FindByID del salón correcto: %+v %v", got, err)
	}
	// Revocar de otro salón tampoco la toca.
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		return tx.Revoke("s-it-f2g1", foreign.ID, now)
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revocar de otro salón: %v", err)
	}
	// Contar pendientes (el tope de 50) y una revocada no cuenta.
	var n int
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		got, err := tx.CountPending("s-it-f2g1", now)
		n = got
		return err
	}); err != nil || n != 1 {
		t.Fatalf("pendientes contadas: %d %v", n, err)
	}
	_ = ctx
}

// S-F2g1-4: DOS canjes simultáneos del mismo token contra Postgres real: gana
// uno, el otro recibe ErrNotFound (el 404 genérico) y queda UNA membresía.
func TestInvitations_IT_claim_concurrente(t *testing.T) {
	pool, ctx := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()
	inv := pendingInvitation(t, "s-it-f2g1", "carrera@mail.com", "tok-carrera", now)
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(inv) }); err != nil {
		t.Fatalf("guardar: %v", err)
	}

	const intentos = 8
	var wg sync.WaitGroup
	errs := make([]error, intentos)
	for i := 0; i < intentos; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Mismo camino que el canje público: reclamar, crear la cuenta y la
			// membresía. Los perdedores se van en el Claim, antes de tocar nada.
			errs[i] = repo.InInvitationTx(func(tx ports.InvitationTx) error {
				if _, err := tx.Claim(inv.ID, "u-it-f2g1-nuevo"); err != nil {
					return err
				}
				if exists, err := tx.MembershipExists("s-it-f2g1", "u-it-f2g1-nuevo"); err != nil {
					return err
				} else if exists {
					return domain.ErrAlreadyExists
				}
				user, err := domain.NewUser("Nueva", "carrera@mail.com", "hash")
				if err != nil {
					return err
				}
				user.ID = "u-it-f2g1-nuevo"
				user.Email = "carrera@mail.com"
				if err := tx.SaveUser(user); err != nil {
					return err
				}
				m, err := inv.Membership("u-it-f2g1-nuevo")
				if err != nil {
					return err
				}
				return tx.SaveMembership(m)
			})
		}(i)
	}
	wg.Wait()

	wins, notFound := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrNotFound):
			notFound++
		default:
			t.Fatalf("error inesperado en la carrera: %v", err)
		}
	}
	if wins != 1 || notFound != intentos-1 {
		t.Fatalf("canjes: %d ganaron y %d recibieron 404 (esperaba 1 y %d)", wins, notFound, intentos-1)
	}
	var memberships int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM memberships
		WHERE salon_id='s-it-f2g1' AND user_id='u-it-f2g1-nuevo'`).Scan(&memberships); err != nil {
		t.Fatalf("contar membresías: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("membresías del ganador: %d (esperaba 1)", memberships)
	}
	var accepted int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM invitations WHERE id=$1 AND accepted_at IS NOT NULL`, inv.ID).Scan(&accepted); err != nil {
		t.Fatalf("contar aceptadas: %v", err)
	}
	if accepted != 1 {
		t.Fatalf("la invitación tiene que quedar aceptada una sola vez: %d", accepted)
	}
}

// S-F2g1-4: el canje es UNA transacción. Si la membresía falla (acá, una FK
// inválida), no queda la cuenta y la invitación sigue pendiente.
func TestInvitations_IT_canje_atomico(t *testing.T) {
	pool, ctx := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()
	inv := pendingInvitation(t, "s-it-f2g1", "atomico@mail.com", "tok-atomico", now)
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(inv) }); err != nil {
		t.Fatalf("guardar: %v", err)
	}
	user, err := domain.NewUser("Nueva", "atomico@mail.com", "hash")
	if err != nil {
		t.Fatalf("dominio: %v", err)
	}
	broken := domain.Membership{UserID: user.ID, SalonID: "s-no-existe", Role: domain.RoleProfessional, Attends: true}

	err = repo.InInvitationTx(func(tx ports.InvitationTx) error {
		if _, err := tx.Claim(inv.ID, user.ID); err != nil {
			return err
		}
		if err := tx.SaveUser(user); err != nil {
			return err
		}
		return tx.SaveMembership(broken)
	})
	if err == nil {
		t.Fatal("la membresía rota tiene que fallar")
	}
	var users, mems int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM users WHERE id=$1`, user.ID).Scan(&users); err != nil {
		t.Fatalf("contar cuentas: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*)::int FROM memberships WHERE user_id=$1`, user.ID).Scan(&mems); err != nil {
		t.Fatalf("contar membresías: %v", err)
	}
	if users != 0 || mems != 0 {
		t.Fatalf("el rollback tiene que dejar todo vacío: cuentas=%d membresías=%d", users, mems)
	}
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT accepted_at IS NULL FROM invitations WHERE id=$1`, inv.ID).Scan(&pending); err != nil {
		t.Fatalf("releer invitación: %v", err)
	}
	if !pending {
		t.Fatal("la invitación tiene que seguir pendiente después del rollback")
	}
}

// S-F2g1-4: vencida o revocada no se puede reclamar (el UPDATE condicional es la
// garantía, no solo el chequeo del caso de uso).
func TestInvitations_IT_claim_vencida_y_revocada(t *testing.T) {
	pool, _ := invitationFixtures(t)
	repo := Invitations{Pool: pool}
	now := time.Now().UTC()

	expired := pendingInvitation(t, "s-it-f2g1", "vencida@mail.com", "tok-v", now)
	expired.ExpiresAt = now.Add(-time.Second)
	revoked := pendingInvitation(t, "s-it-f2g1", "revocada@mail.com", "tok-r", now)
	for _, inv := range []domain.Invitation{expired, revoked} {
		if err := repo.InInvitationTx(func(tx ports.InvitationTx) error { return tx.Save(inv) }); err != nil {
			t.Fatalf("guardar %s: %v", inv.Email, err)
		}
	}
	if err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
		return tx.Revoke("s-it-f2g1", revoked.ID, now)
	}); err != nil {
		t.Fatalf("revocar: %v", err)
	}
	for _, inv := range []domain.Invitation{expired, revoked} {
		err := repo.InInvitationTx(func(tx ports.InvitationTx) error {
			_, err := tx.Claim(inv.ID, "u-it-f2g1-owner")
			return err
		})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("reclamar %s: %v (esperaba ErrNotFound)", inv.Email, err)
		}
	}
}
