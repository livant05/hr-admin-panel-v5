package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

func runCLI(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, func(k string) string { return env[k] })
	return code, out.String(), errb.String()
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRun_UsageErrors(t *testing.T) {
	if code, _, stderr := runCLI(t, nil, "--db-url", "postgres://x"); code != 2 || stderr == "" {
		t.Fatalf("missing --dir: code=%d stderr=%q, want 2 and a message", code, stderr)
	}
	if code, _, stderr := runCLI(t, nil, "--dir", "testdata/export"); code != 2 || stderr == "" {
		t.Fatalf("missing db url: code=%d stderr=%q, want 2 and a message", code, stderr)
	}
	if code, _, _ := runCLI(t, nil, "--bogus"); code != 2 {
		t.Fatalf("unknown flag: code=%d, want 2", code)
	}
}

func TestRun_EndToEnd(t *testing.T) {
	pool, url := testutil.IsolatedSchema(t)
	env := map[string]string{"DB_URL": url} // --db-url falls back to DB_URL like cmd/api

	code, stdout, stderr := runCLI(t, env, "--dir", "testdata/export", "--dry-run")
	if code != 0 {
		t.Fatalf("dry run exit=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if countRows(t, pool, "employees") != 0 {
		t.Fatalf("dry run wrote employees")
	}

	code, stdout, stderr = runCLI(t, env, "--dir", "testdata/export")
	if code != 0 {
		t.Fatalf("import exit=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if got := countRows(t, pool, "employees"); got != 2 {
		t.Fatalf("employees = %d, want 2", got)
	}
	if got := countRows(t, pool, "companies"); got != 2 { // seed + Acme
		t.Fatalf("companies = %d, want 2", got)
	}

	// Re-run through the explicit flag: idempotent, still exit 0.
	code, _, stderr = runCLI(t, nil, "--dir", "testdata/export", "--db-url", url)
	if code != 0 {
		t.Fatalf("rerun exit=%d stderr=%q", code, stderr)
	}
	if got := countRows(t, pool, "attendance_logs"); got != 2 {
		t.Fatalf("attendance after rerun = %d, want 2", got)
	}
}

func TestRun_InvalidRowsFailWithReportUnlessSkipped(t *testing.T) {
	pool, url := testutil.IsolatedSchema(t)

	code, stdout, stderr := runCLI(t, nil, "--dir", "testdata/export_invalid", "--db-url", url)
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (stderr=%q)", code, stderr)
	}
	if !bytes.Contains([]byte(stdout), []byte("33333333-3333-4333-8333-333333333332")) {
		t.Fatalf("report should list the invalid row id:\n%s", stdout)
	}
	if countRows(t, pool, "employees") != 0 {
		t.Fatalf("aborted run wrote rows")
	}

	code, _, stderr = runCLI(t, nil, "--dir", "testdata/export_invalid", "--db-url", url, "--skip-invalid")
	if code != 0 {
		t.Fatalf("--skip-invalid exit=%d stderr=%q", code, stderr)
	}
	if got := countRows(t, pool, "employees"); got != 1 {
		t.Fatalf("employees = %d, want 1", got)
	}
}
