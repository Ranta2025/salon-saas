package pgstore

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"salon-saas/internal/domain"
)

// pgError traduce códigos de Postgres a errores de dominio en el borde.
// Sin esto el cliente vería SQL crudo (fuga de internals) o un 500 mentiroso.
//
//	23505 unique_violation          -> ErrAlreadyExists (409)
//	23503 foreign_key               -> ErrValidation    (400): referencia inexistente
//	23514 check_violation           -> ErrValidation    (400): dato fuera de rango
//	23P01 exclusion                 -> ErrAlreadyExists (409): solape de agenda
//	22003 numeric_value_out_of_range -> ErrValidation   (400): monto fuera de rango
//
// Los mensajes son del dominio (español, sin SQL): el handler los devuelve tal
// cual y el cliente nunca ve el nombre de una tabla ni un mensaje de pgx.
func pgError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505", "23P01":
			return domain.ErrAlreadyExists
		case "23503":
			return fmt.Errorf("%w: una referencia del pedido no existe en el salón", domain.ErrValidation)
		case "23514":
			return fmt.Errorf("%w: un dato del pedido está fuera del rango permitido", domain.ErrValidation)
		case "22003":
			return fmt.Errorf("%w: el monto supera el máximo permitido", domain.ErrValidation)
		}
	}
	return err
}
