package handlers_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// run0009 executes the 0009 migration file against the test database. The
// file is applied directly (not through testutil's sentinel-guarded
// applyMigrations) because the test database may already have been
// initialised before 0009 existed.
func run0009(t *testing.T, ctx context.Context, exec func(ctx context.Context, sql string, args ...any) error) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "0009_medical_permission.sql")
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read 0009 migration: %v", err)
	}
	if err := exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("apply 0009 migration: %v", err)
	}
}

// TestMigration0009_SeedsMedicalPermission pins the 0009 backfill: every
// role lacking `medical` gets it, true exactly when the role has attendance
// access by the frontend's semantics (boolean true, or an object with at
// least one truthy sub-key), and a second run changes nothing.
func TestMigration0009_SeedsMedicalPermission(t *testing.T) {
	pool := testutil.Pool(t)
	ctx := context.Background()
	c := testutil.Company(t, pool, "Mig0009Co")

	cases := []struct {
		name  string
		perms string
		want  string // expected value of permissions->'medical' as JSON text
	}{
		{"att_true", `{"attendance": true, "dashboard": true}`, `true`},
		{"att_false", `{"attendance": false}`, `false`},
		{"att_obj_true_sub", `{"attendance": {"view": false, "edit": true}}`, `true`},
		{"att_obj_all_false", `{"attendance": {"view": false, "edit": false}}`, `false`},
		{"att_obj_empty", `{"attendance": {}}`, `false`},
		{"att_number", `{"attendance": 1}`, `true`},
		{"att_string", `{"attendance": "yes"}`, `true`},
		{"att_absent", `{"dashboard": true}`, `false`},
		{"empty_object", `{}`, `false`},
		{"has_medical_false", `{"attendance": true, "medical": false}`, `false`},
		{"has_medical_true", `{"attendance": false, "medical": true}`, `true`},
		{"json_null", `null`, `false`},
		{"json_array", `[1]`, `false`},
	}
	for _, tc := range cases {
		if _, err := pool.Exec(ctx,
			`INSERT INTO roles (company_id, name, permissions) VALUES ($1, $2, $3::jsonb)`,
			c.CompanyID, tc.name, tc.perms); err != nil {
			t.Fatalf("insert role %s: %v", tc.name, err)
		}
	}

	exec := func(ctx context.Context, sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	snapshot := func() map[string]string {
		rows, err := pool.Query(ctx,
			`SELECT name, permissions::text FROM roles WHERE company_id = $1`, c.CompanyID)
		if err != nil {
			t.Fatalf("snapshot roles: %v", err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var name, perms string
			if err := rows.Scan(&name, &perms); err != nil {
				t.Fatal(err)
			}
			out[name] = perms
		}
		return out
	}
	medicalOf := func(name string) string {
		var v *string
		if err := pool.QueryRow(ctx,
			`SELECT (permissions->'medical')::text FROM roles WHERE company_id = $1 AND name = $2`,
			c.CompanyID, name).Scan(&v); err != nil {
			t.Fatalf("read medical for %s: %v", name, err)
		}
		if v == nil {
			return "<absent>"
		}
		return *v
	}

	run0009(t, ctx, exec)
	for _, tc := range cases {
		if got := medicalOf(tc.name); got != tc.want {
			t.Errorf("%s: permissions=%s: want medical=%s, got %s", tc.name, tc.perms, tc.want, got)
		}
	}

	first := snapshot()
	run0009(t, ctx, exec)
	second := snapshot()
	for name, perms := range first {
		if second[name] != perms {
			t.Errorf("%s: second run changed permissions: %s -> %s", name, perms, second[name])
		}
	}
}
