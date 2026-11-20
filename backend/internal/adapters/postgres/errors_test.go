// Mapeo de códigos de Postgres a errores de dominio. Es unitario (sin DB): lo
// que importa es que NINGÚN código concreto llegue al cliente como 500 ni como
// mensaje de pgx.
package pgstore

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"salon-saas/internal/domain"
)

func TestPgError_mapea_codigos(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"23505", domain.ErrAlreadyExists}, // unique_violation
		{"23P01", domain.ErrAlreadyExists}, // exclusion_violation (solape)
		{"23503", domain.ErrValidation},    // foreign_key_violation
		{"23514", domain.ErrValidation},    // check_violation
		{"22003", domain.ErrValidation},    // numeric_value_out_of_range (B-03)
	}
	for _, tc := range cases {
		got := pgError(&pgconn.PgError{Code: tc.code, Message: `duplicate key value violates unique constraint "sales_client_op_uniq"`})
		if !errors.Is(got, tc.want) {
			t.Fatalf("código %s: esperaba %v, dio %v", tc.code, tc.want, got)
		}
		// El mensaje no puede contener SQL ni nombres de constraints.
		if msg := got.Error(); msg == "" || containsSQL(msg) {
			t.Fatalf("código %s: mensaje con internals: %q", tc.code, msg)
		}
	}

	// Cualquier otro error pasa tal cual (será un 500 genérico, logueado).
	raw := errors.New("connection reset")
	if got := pgError(raw); !errors.Is(got, raw) {
		t.Fatalf("un error desconocido no se debe reescribir: %v", got)
	}
	// Un error que no es de Postgres pero envuelve un PgError igual se traduce.
	wrapped := errors.Join(errors.New("exec"), &pgconn.PgError{Code: "23505"})
	if got := pgError(wrapped); !errors.Is(got, domain.ErrAlreadyExists) {
		t.Fatalf("PgError envuelto: %v", got)
	}
}

func containsSQL(msg string) bool {
	for _, needle := range []string{"constraint", "duplicate key", "SQLSTATE", "SELECT", "INSERT", "pq:"} {
		if len(msg) >= len(needle) && indexOfStr(msg, needle) >= 0 {
			return true
		}
	}
	return false
}

func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
