package pgstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// txDefaultTimeout es el tope de InSalonTx (lock del salón + trabajo). Los
// puertos no llevan contexto, así que el tope vive en el adaptador: alcanza
// para que una transacción trabada no retenga una conexión del pool ni el lock
// del salón de forma indefinida.
const txDefaultTimeout = 30 * time.Second

func txTimeout(configured time.Duration) time.Duration { return TxTimeout(configured) }

// TxTimeout es la versión exportada de txTimeout para los adaptadores que ya
// viven en su módulo por funcionalidad (`internal/<feature>/postgres`).
func TxTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return txDefaultTimeout
}

// rollbackTx deshace con su PROPIO contexto: si el de la transacción ya venció,
// el rollback tiene que poder ejecutarse igual para liberar la conexión.
func rollbackTx(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// RollbackTx es la versión exportada de rollbackTx para los adaptadores que ya
// viven en su módulo por funcionalidad (`internal/<feature>/postgres`).
func RollbackTx(tx pgx.Tx) { rollbackTx(tx) }
