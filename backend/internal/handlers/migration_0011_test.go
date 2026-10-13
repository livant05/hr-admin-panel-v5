package handlers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

func read0011(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0011_zkteco_ingestion.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 0011 migration: %v", err)
	}
	return string(b)
}

// TestMigration0011_RejectionsTableShape pins ING-9 storage: the table, its
// composite primary key, the absence of a company_id column (A1 rule 4) and
// the employees(company_id, cedula) index.
func TestMigration0011_RejectionsTableShape(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'zkteco_ingest_rejections'`)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	sort.Strings(cols)
	want := []string{"count", "last_pin", "last_seen", "reason", "serial_number"}
	if len(cols) != len(want) {
		t.Fatalf("columns = %v, want %v (and no company_id)", cols, want)
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Fatalf("columns = %v, want %v (and no company_id)", cols, want)
		}
	}

	var pk []string
	pkRows, err := pool.Query(ctx,
		`SELECT a.attname
		 FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		 WHERE i.indrelid = 'zkteco_ingest_rejections'::regclass AND i.indisprimary
		 ORDER BY a.attname`)
	if err != nil {
		t.Fatalf("pk: %v", err)
	}
	for pkRows.Next() {
		var c string
		if err := pkRows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		pk = append(pk, c)
	}
	pkRows.Close()
	if len(pk) != 2 || pk[0] != "reason" || pk[1] != "serial_number" {
		t.Fatalf("primary key = %v, want [reason serial_number]", pk)
	}

	var idxDef string
	if err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_emp_co_cedula'`,
	).Scan(&idxDef); err != nil {
		t.Fatalf("idx_emp_co_cedula missing: %v", err)
	}
}

// TestMigration0011_ReasonCheck pins the closed reason set: every reason the
// code can persist (handlers.RejectionReasons) inserts; anything else violates the CHECK (SQLSTATE 23514).
func TestMigration0011_ReasonCheck(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()

	// The reasons come from the Go constants, so a reason added to the code
	// without a matching CHECK change fails here instead of at runtime.
	reasons := handlers.RejectionReasons()
	if len(reasons) == 0 {
		t.Fatal("handlers.RejectionReasons() is empty: nothing would be checked")
	}
	for _, reason := range reasons {
		if _, err := pool.Exec(ctx,
			`INSERT INTO zkteco_ingest_rejections (serial_number, reason) VALUES ('SN1', $1)`, reason); err != nil {
			t.Errorf("reason %q rejected: %v", reason, err)
		}
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO zkteco_ingest_rejections (serial_number, reason) VALUES ('SN1', 'made_up')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("unknown reason: want check violation 23514, got %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO zkteco_ingest_rejections (serial_number, reason) VALUES ('SN1', 'malformed')`)
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate (serial, reason): want unique violation 23505, got %v", err)
	}
}

// TestMigration0011_Idempotent re-applies the file; it must not error and
// must keep existing counter rows (the harness re-applies every >=0010 file
// on each run, and existing volumes may run it twice).
func TestMigration0011_Idempotent(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()
	sql := read0011(t)

	if _, err := pool.Exec(ctx,
		`INSERT INTO zkteco_ingest_rejections (serial_number, reason, count) VALUES ('SN1', 'malformed', 7)`); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("re-apply run %d: %v", run, err)
		}
	}
	var n int64
	if err := pool.QueryRow(ctx,
		`SELECT count FROM zkteco_ingest_rejections WHERE serial_number = 'SN1' AND reason = 'malformed'`).Scan(&n); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if n != 7 {
		t.Fatalf("counter = %d after re-apply, want 7", n)
	}
}
