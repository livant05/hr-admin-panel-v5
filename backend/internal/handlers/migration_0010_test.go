package handlers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

const uniqueEmailIndex = "idx_users_email_lower"

func read0010(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0010_users_email_lower_unique.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 0010 migration: %v", err)
	}
	return string(b)
}

// TestMigration0010_DuplicatesAbortAndNameThem pins AUTH-3 (migration with
// duplicates): the migration fails loudly, lists the duplicate address and
// creates no index.
func TestMigration0010_DuplicatesAbortAndNameThem(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()
	sql := read0010(t)

	if _, err := pool.Exec(ctx, `DROP INDEX IF EXISTS `+uniqueEmailIndex); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	var companyID string
	if err := pool.QueryRow(ctx, `INSERT INTO companies (name) VALUES ('Mig0010Co') RETURNING id`).Scan(&companyID); err != nil {
		t.Fatalf("insert company: %v", err)
	}
	for _, email := range []string{"Ana@x.test", "ana@x.test"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (company_id, email) VALUES ($1, $2)`, companyID, email); err != nil {
			t.Fatalf("insert %s: %v", email, err)
		}
	}

	_, err := pool.Exec(ctx, sql)
	if err == nil {
		t.Fatal("expected migration to fail on case-insensitive duplicates")
	}
	if !strings.Contains(err.Error(), "ana@x.test") {
		t.Fatalf("error must name the duplicate address, got: %v", err)
	}
	if indexExists(t, pool, ctx) {
		t.Fatal("index must not exist after a failed migration")
	}
}

// TestMigration0010_CleanDataIsIdempotentAndEnforcesUniqueness covers the
// happy path: it applies twice without error and then rejects a
// case-variant duplicate with SQLSTATE 23505.
func TestMigration0010_CleanDataIsIdempotentAndEnforcesUniqueness(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()
	sql := read0010(t)

	if _, err := pool.Exec(ctx, `DROP INDEX IF EXISTS `+uniqueEmailIndex); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	var companyID string
	if err := pool.QueryRow(ctx, `INSERT INTO companies (name) VALUES ('Mig0010CleanCo') RETURNING id`).Scan(&companyID); err != nil {
		t.Fatalf("insert company: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (company_id, email) VALUES ($1, 'ana@corp.com')`, companyID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if !indexExists(t, pool, ctx) {
		t.Fatal("index must exist after migration")
	}

	_, err := pool.Exec(ctx, `INSERT INTO users (company_id, email) VALUES ($1, 'ANA@corp.com')`, companyID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected unique violation 23505, got %v", err)
	}
	// NULL emails stay allowed (NULLs are distinct).
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO users (company_id, email) VALUES ($1, NULL)`, companyID); err != nil {
			t.Fatalf("NULL email insert %d: %v", i, err)
		}
	}
}

func indexExists(t *testing.T, pool *pgxpool.Pool, ctx context.Context) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1)`,
		uniqueEmailIndex).Scan(&exists)
	if err != nil {
		t.Fatalf("check index: %v", err)
	}
	return exists
}
