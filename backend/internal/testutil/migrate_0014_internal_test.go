package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// splitSQLStatements splits a migration file the way psql -f runs it: one
// statement per top-level ';' line ending, with $$ bodies kept whole.
func splitSQLStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	inDollar := false
	for _, line := range strings.Split(sql, "\n") {
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.Count(line, "$$")%2 == 1 {
			inDollar = !inDollar
		}
		if !inDollar && strings.HasSuffix(strings.TrimSpace(line), ";") {
			stmts = append(stmts, cur.String())
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

// TestMigration0014_FailureKeepsTheOldLedgerIndex pins the index order of
// 0014: the runbook applies it with psql -f, one autocommitted statement at a
// time. When the payroll_history unique index fails on pre-existing duplicate
// runs, the old run-ledger index must still exist, because the previous binary
// keeps upserting with ON CONFLICT on it until the operator fixes the data.
// After the operator deletes the duplicate, applying the file again (the
// runbook's recovery) must finish the migration.
func TestMigration0014_FailureKeepsTheOldLedgerIndex(t *testing.T) {
	pool, _ := IsolatedSchema(t)
	ctx := context.Background()

	// Back to the pre-0014 index layout.
	DropIndexInIsolatedSchema(t, pool, "uq_payroll_history_period")
	DropIndexInIsolatedSchema(t, pool, "uq_epr_run_period_v2")
	if _, err := pool.Exec(ctx, `CREATE UNIQUE INDEX uq_epr_run_period
		ON employee_pay_records (company_id, employee_id, period_year, period_month)
		WHERE origin = 'run'`); err != nil {
		t.Fatalf("recreate old index: %v", err)
	}

	var companyID string
	if err := pool.QueryRow(ctx, `INSERT INTO companies (name) VALUES ('Dup0014Co') RETURNING id`).Scan(&companyID); err != nil {
		t.Fatalf("insert company: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO payroll_history (company_id, period, month, year)
			VALUES ($1, 'mensual', 9, 2026)`, companyID); err != nil {
			t.Fatalf("insert duplicate history row: %v", err)
		}
	}

	b, err := os.ReadFile(filepath.Join(migrationsDir(), "0014_payroll_periods.sql"))
	if err != nil {
		t.Fatalf("read 0014: %v", err)
	}
	var applyErr error
	for _, stmt := range splitSQLStatements(string(b)) {
		if _, applyErr = pool.Exec(ctx, stmt); applyErr != nil {
			break // ON_ERROR_STOP=1
		}
	}
	if applyErr == nil {
		t.Fatal("0014 must fail on duplicate payroll_history runs")
	}
	// The failure must be the history unique index itself, not some earlier
	// statement failing for an unrelated reason.
	var pgErr *pgconn.PgError
	if !errors.As(applyErr, &pgErr) || pgErr.Code != "23505" || !strings.Contains(pgErr.Message, "uq_payroll_history_period") {
		t.Fatalf("expected a unique violation creating uq_payroll_history_period, got: %v", applyErr)
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'uq_epr_run_period')`).Scan(&exists); err != nil {
		t.Fatalf("check old index: %v", err)
	}
	if !exists {
		t.Fatalf("uq_epr_run_period was dropped before the failing statement (%v); the previous binary's run upsert would break", applyErr)
	}

	// Recovery: keep the newest duplicate, apply the whole file again.
	if _, err := pool.Exec(ctx, `DELETE FROM payroll_history WHERE id = (
		SELECT id FROM payroll_history WHERE company_id = $1 ORDER BY created_at, id LIMIT 1)`, companyID); err != nil {
		t.Fatalf("delete the older duplicate: %v", err)
	}
	for _, stmt := range splitSQLStatements(string(b)) {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("re-applying 0014 after the cleanup failed: %v\n%s", err, stmt)
		}
	}
	for index, want := range map[string]bool{
		"uq_payroll_history_period": true,
		"uq_epr_run_period_v2":      true,
		"uq_epr_run_period":         false,
	} {
		var got bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema() AND indexname = $1)`, index).Scan(&got); err != nil {
			t.Fatalf("check %s: %v", index, err)
		}
		if got != want {
			t.Fatalf("after re-applying 0014, index %s exists=%v, want %v", index, got, want)
		}
	}
}
