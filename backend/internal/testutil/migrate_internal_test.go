package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestApplyMigrations_SelfHealsLegacyDuplicateEmails pins the test-DB
// self-heal: a long-lived test database may hold case-insensitive duplicate
// emails from old fixtures, which would make the 0010 re-apply abort and fail
// every DB test. applyMigrations must de-duplicate them first, keeping the
// oldest row untouched.
func TestApplyMigrations_SelfHealsLegacyDuplicateEmails(t *testing.T) {
	pool, _ := IsolatedSchema(t)
	ctx := context.Background()

	DropIndexInIsolatedSchema(t, pool, UsersEmailLowerIndex)
	var companyID string
	if err := pool.QueryRow(ctx, `INSERT INTO companies (name) VALUES ('SelfHealCo') RETURNING id`).Scan(&companyID); err != nil {
		t.Fatalf("insert company: %v", err)
	}
	var oldID, newID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (company_id, email, created_at) VALUES ($1, 'Dup@x.test', now() - interval '1 day') RETURNING id`, companyID).Scan(&oldID); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (company_id, email) VALUES ($1, 'dup@x.test') RETURNING id`, companyID).Scan(&newID); err != nil {
		t.Fatalf("insert new: %v", err)
	}

	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("applyMigrations must self-heal duplicates: %v", err)
	}

	var oldEmail, newEmail string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, oldID).Scan(&oldEmail); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, newID).Scan(&newEmail); err != nil {
		t.Fatal(err)
	}
	if oldEmail != "Dup@x.test" {
		t.Fatalf("oldest row must keep its email, got %q", oldEmail)
	}
	if newEmail == "dup@x.test" {
		t.Fatal("newer duplicate must be rewritten")
	}

	// Idempotent: a second pass is a no-op.
	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("second applyMigrations: %v", err)
	}
}

// TestApplyMigrations_ConcurrentFreshDatabaseAppliesLegacyFilesOnce pins the
// sentinel/lock ordering: two callers that both arrive at a FRESH database
// must not both decide "not initialized" and re-apply the non-idempotent
// pre-0010 files. The test holds the migration advisory lock itself, starts
// both callers, waits until both are blocked on the lock, then releases it, so
// the interleaving is deterministic rather than timing-dependent.
func TestApplyMigrations_ConcurrentFreshDatabaseAppliesLegacyFilesOnce(t *testing.T) {
	admin := Pool(t)
	ctx := context.Background()
	_, isoURL := newSchemaURL(t)

	appName := "migrate-race-" + uuid.NewString()[:8]
	const callers = 2
	pools := make([]*pgxpool.Pool, callers)
	for i := range pools {
		cfg, err := pgxpool.ParseConfig(isoURL)
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		cfg.ConnConfig.RuntimeParams["application_name"] = appName
		pools[i], err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("connect caller %d: %v", i, err)
		}
		t.Cleanup(pools[i].Close)
	}

	holder, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}

	errs := make(chan error, callers)
	for _, p := range pools {
		go func(p *pgxpool.Pool) { errs <- applyMigrations(ctx, p) }(p)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := admin.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a USING (pid)
			WHERE l.locktype = 'advisory' AND NOT l.granted AND a.application_name = $1`,
			appName).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting == callers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d callers blocked on the migration lock, saw %d", callers, waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("release migration lock: %v", err)
	}

	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent applyMigrations on a fresh database failed: %v", err)

		}
	}

	var usersTable, emailIndex bool
	if err := pools[0].QueryRow(ctx, `SELECT to_regclass(current_schema() || '.users') IS NOT NULL, to_regclass(current_schema() || '.' || $1) IS NOT NULL`, UsersEmailLowerIndex).
		Scan(&usersTable, &emailIndex); err != nil {
		t.Fatalf("inspect fresh schema: %v", err)
	}
	if !usersTable || !emailIndex {
		t.Fatalf("fresh schema must end fully migrated, users=%v %s=%v", usersTable, UsersEmailLowerIndex, emailIndex)
	}
}
