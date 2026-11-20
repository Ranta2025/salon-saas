package pgstore

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"salon-saas/internal/domain"
)

// pgError traduce códigos de Postgres a errores de dominio en el borde.
// Sin esto el cliente vería SQL crudo (fuga de internals) o un 500 mentiroso.
//
//	23505 unique_violation  -> ErrAlreadyExists (409)
//	23503 foreign_key       -> ErrValidation    (400): referencia inexistente
//	23514 check_violation   -> ErrValidation    (400): dato fuera de rango
//	23P01 exclusion         -> ErrAlreadyExists (409): solape de agenda
func pgError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505", "23P01":
			return domain.ErrAlreadyExists
		case "23503", "23514":
			return domain.ErrValidation
		}
	}
	return err
}
