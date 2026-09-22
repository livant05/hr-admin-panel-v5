package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// This file is package handlers (white-box), not handlers_test, because
// requirePermission and permissionGranted are unexported. That means it
// cannot import internal/testutil -- testutil.go itself imports this
// package (for handlers.New), and an internal test file importing a
// package that imports the package under test is a real Go import cycle
// ("import cycle not allowed in test"). authzTestPool/authzSeedCompany
// below intentionally duplicate the minimal slice of testutil's Pool/
// Company behavior needed here: connect (fail loudly, never silently skip,
// unless SKIP_DB_TESTS=1), apply migrations once if the sentinel table is
// absent, and clean up via company cascade delete.

const authzTestSecret = "authz-test-secret"

func authzTestLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func authzMigrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// authzTestPool mirrors testutil.Pool exactly (see the file comment above
// for why it cannot simply call testutil.Pool).
func authzTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://rrhh_go:rrhh_go_dev@localhost:5434/rrhh_go_test?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		authzFailOrSkip(t, fmt.Sprintf("connect test db: %v", err))
		return nil
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		authzFailOrSkip(t, fmt.Sprintf("ping test db: %v", err))
		return nil
	}

	if err := authzApplyMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("apply migrations: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func authzFailOrSkip(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("SKIP_DB_TESTS") == "1" {
		t.Skip(msg)
		return
	}
	t.Fatal(msg)
}

func authzApplyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_name = 'departments'
	)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check sentinel table: %w", err)
	}
	if exists {
		return nil
	}

	files, err := filepath.Glob(filepath.Join(authzMigrationsDir(), "*.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, f := range files {
		if strings.Contains(filepath.Base(f), "seed") {
			continue
		}
		sqlBytes, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", f, err)
		}
	}

	return tx.Commit(ctx)
}

// authzSeedCompany inserts a bare company row (no user -- unlike
// testutil.Company, requirePermission never reads the users table) and
// registers cleanup via cascade delete.
func authzSeedCompany(t *testing.T, pool *pgxpool.Pool, name string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()

	var companyID string
	err := pool.QueryRow(ctx,
		`INSERT INTO companies (name) VALUES ($1) RETURNING id`, name,
	).Scan(&companyID)
	if err != nil {
		t.Fatalf("insert test company: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, companyID)
	})

	cid, err := stringToUUID(companyID)
	if err != nil {
		t.Fatalf("parse company id: %v", err)
	}
	return cid
}

// requirePermissionResult drives a.requirePermission through a real
// signer.Middleware-wrapped mux, exactly as a production route would, and
// returns the recorded response.
func requirePermissionResult(t *testing.T, a *API, signer *auth.Signer, token string, companyID pgtype.UUID, key string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		if a.requirePermission(w, r, companyID, key) {
			w.WriteHeader(http.StatusOK)
		}
	})
	h := signer.Middleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRequirePermission_AdminBypass pins design Q4: an admin role is always
// granted, without ever touching the roles table. This is load-bearing for
// a fresh tenant that has no roles rows yet -- a.Queries is deliberately
// left nil here: the admin bypass must return before any query is
// attempted, so this test needs no database at all.
func TestAuthz_AdminBypass(t *testing.T) {
	signer := auth.NewSigner(authzTestSecret, time.Hour)
	a := &API{Queries: nil, Log: authzTestLog()}

	tok, err := signer.Sign("user-1", "11111111-1111-1111-1111-111111111111", "admin")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	rec := requirePermissionResult(t, a, signer, tok, pgtype.UUID{}, "vacations")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected admin bypass to grant access (200), got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// The truthy-allow path (a non-admin role with permissions[key]=true in the
// roles table) is deliberately NOT re-proven here at the HTTP/DB level: it
// is already pinned exhaustively, without a database, by
// TestAuthz_PermissionGrantedTruthyPermissionAllows below (permissionGranted
// is exactly what requirePermission calls after a successful lookup), and it
// will be proven again end to end once a real caller exists -- Phase 2c's
// leave_requests approval workflow (design Q4's actual consumer of
// requirePermission("vacations")). Adding a third, redundant integration
// test here would duplicate coverage without adding safety.

// TestRequirePermission_MissingRoleFailsClosed pins Q4's fail-closed
// guarantee end to end: a non-admin role with no matching roles row (fresh
// tenant, or a role name nobody created) gets 403, never a silent allow --
// the opposite of the frontend's own hasPerm, which is fail-open for an
// undefined key.
func TestAuthz_MissingRoleFailsClosed(t *testing.T) {
	pool := authzTestPool(t)
	signer := auth.NewSigner(authzTestSecret, time.Hour)
	a := &API{Queries: db.New(pool), Log: authzTestLog()}

	companyID := authzSeedCompany(t, pool, "AuthzDenyCo")
	// Deliberately no roles row for "empleado" -- this is the case a fresh
	// tenant is in before any custom role is created.

	tok, err := signer.Sign("user-3", uuidToString(companyID), "empleado")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	rec := requirePermissionResult(t, a, signer, tok, companyID, "vacations")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 forbidden for a missing role row, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body (body=%s): %v", rec.Body.String(), err)
	}
	if body.Error.Code != codeForbidden {
		t.Fatalf("expected error.code=%s, got %q", codeForbidden, body.Error.Code)
	}
}

// TestPermissionGranted_TruthyPermissionAllows pins permissionGranted, the
// pure decision at the heart of requirePermission, against a range of JSON
// permission values -- no database needed, since the already-fetched row is
// passed in directly.
func TestAuthz_PermissionGrantedTruthyPermissionAllows(t *testing.T) {
	cases := []struct {
		name        string
		permissions string
		key         string
		want        bool
	}{
		{"boolean true grants", `{"vacations":true}`, "vacations", true},
		{"boolean false denies", `{"vacations":false}`, "vacations", false},
		{"absent key denies", `{"dashboard":true}`, "vacations", false},
		{"non-empty string grants", `{"vacations":"yes"}`, "vacations", true},
		{"empty string denies", `{"vacations":""}`, "vacations", false},
		{"zero number denies", `{"vacations":0}`, "vacations", false},
		{"nonzero number grants", `{"vacations":1}`, "vacations", true},
		{"null denies", `{"vacations":null}`, "vacations", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := db.Role{Permissions: json.RawMessage(tc.permissions)}
			got := permissionGranted(row, nil, tc.key)
			if got != tc.want {
				t.Fatalf("permissionGranted(%s, key=%s) = %v, want %v", tc.permissions, tc.key, got, tc.want)
			}
		})
	}
}

// TestPermissionGranted_MissingRoleFailsClosed pins the fail-closed half of
// Q4 directly against permissionGranted: unlike the frontend's hasPerm
// (hr_admin_panel.html:2410), which returns true for an undefined key, a
// missing role row -- or any other lookup failure -- must never grant
// access.
func TestAuthz_PermissionGrantedMissingRoleFailsClosed(t *testing.T) {
	// No role row at all -- GetRoleByName would return pgx.ErrNoRows for a
	// fresh tenant or an unrecognized role name.
	if permissionGranted(db.Role{}, pgx.ErrNoRows, "vacations") {
		t.Fatalf("expected a missing role row (pgx.ErrNoRows) to fail closed, got granted")
	}

	// Any other lookup error must also fail closed -- never fail open.
	if permissionGranted(db.Role{}, errors.New("connection reset"), "vacations") {
		t.Fatalf("expected a lookup error to fail closed, got granted")
	}
}
