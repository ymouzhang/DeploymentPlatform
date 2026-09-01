package postgres

import "DP/internal/access"

var (
	ErrConflict     = access.ErrConflict
	ErrInUse        = access.ErrInUse
	ErrInvalidInput = access.ErrInvalidInput
	ErrNotFound     = access.ErrNotFound
	ErrProtected    = access.ErrProtected
)
