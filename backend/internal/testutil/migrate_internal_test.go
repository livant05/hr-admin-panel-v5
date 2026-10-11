package testutil

import (
	"context"
	"testing"
)

// TestApplyMigrations_SelfHealsLegacyDuplicateEmails pins the test-DB
// self-heal: a long-lived test database may hold case-insensitive duplicate
// emails from old fixtures, which would make the 0010 re-apply abort and fail
// every DB test. applyMigrations must de-duplicate them first, keeping the
// oldest row untouched.
func TestApplyMigrations_SelfHealsLegacyDuplicateEmails(t *testing.T) {
	pool, _ := IsolatedSchema(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `DROP INDEX IF EXISTS idx_users_email_lower`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
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
