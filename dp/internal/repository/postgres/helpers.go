package postgres

import (
	"errors"
	"fmt"

	"DP/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

func isPostgresError(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func requireAffected(operation string, command pgconn.CommandTag, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if command.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
