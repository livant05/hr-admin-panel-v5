package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SeedPasswordHash is the bcrypt hash of the development password that
// migrations/0003_seed.sql stores on the seed admin. Anyone with the
// repository knows the password, so no account carrying this hash, active or
// not, may exist in production. A test keeps it identical to the migration.
const SeedPasswordHash = "$2a$10$d8UdlIbA4nESHywDqYk6h.L2W6qk9YEUiPqNnUm5qgQmKKGAwk9Q."

// QueryRower is the part of *pgxpool.Pool, *pgx.Conn and pgx.Tx the seed check
// needs.
type QueryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SeedCredentialPresent reports whether any user row, whatever its status,
// still has the committed seed password hash. An inactive row counts: one
// UPDATE would reactivate a login whose password is public. It matches on the
// hash, not on the seed email or id, so a copy of the seed credential under
// another account is caught too. A query error is returned as an error: the
// caller must treat it as "unknown", never as "clean".
func SeedCredentialPresent(ctx context.Context, q QueryRower) (bool, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE password_hash = $1`,
		SeedPasswordHash).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check seed credential: %w", err)
	}
	return n > 0, nil
}
