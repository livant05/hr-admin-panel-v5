package handlers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/livant05/rrhh-go/internal/testutil"
)

func read0012(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0012_zkteco_tenant_scoping.sql")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 0012 migration: %v", err)
	}
	return string(b)
}

// TestMigration0012_UnknownPinsUniquePerDevice pins CO-S5a-8: the same PIN may
// be unresolved on two devices (two rows), but never twice on one device.
func TestMigration0012_UnknownPinsUniquePerDevice(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()

	for _, sn := range []string{"SN-A", "SN-B"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO zkteco_unknown_pins (pin, serial_number) VALUES ('1234', $1)`, sn); err != nil {
			t.Fatalf("same pin on device %s must be allowed: %v", sn, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM zkteco_unknown_pins WHERE pin = '1234'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows for pin 1234 = %d err=%v, want 2", n, err)
	}

	_, err := pool.Exec(ctx, `INSERT INTO zkteco_unknown_pins (pin, serial_number) VALUES ('1234', 'SN-A')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate (serial, pin): want unique violation 23505, got %v", err)
	}
}

// TestMigration0012_Idempotent re-applies the file on a schema that already
// ran it: no error, and existing rows survive. Existing volumes may run it
// twice and the harness re-applies it on every run.
func TestMigration0012_Idempotent(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()
	sql := read0012(t)

	if _, err := pool.Exec(ctx,
		`INSERT INTO zkteco_unknown_pins (pin, serial_number, attempts) VALUES ('9', 'SN-A', 5)`); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("re-apply run %d: %v", run, err)
		}
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM zkteco_unknown_pins WHERE pin = '9' AND serial_number = 'SN-A'`).Scan(&attempts); err != nil || attempts != 5 {
		t.Fatalf("attempts = %d err=%v after re-apply, want 5", attempts, err)
	}
	// The per-device key must still hold after the re-apply.
	if _, err := pool.Exec(ctx,
		`INSERT INTO zkteco_unknown_pins (pin, serial_number) VALUES ('9', 'SN-B')`); err != nil {
		t.Fatalf("same pin on another device after re-apply: %v", err)
	}
}
