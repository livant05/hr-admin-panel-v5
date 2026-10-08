// Package testutil is the shared integration-test harness bootstrapped in
// Phase 1 (design P5) — the first test infrastructure in this repository.
// It runs every handler test against a real Postgres 16 instance (the same
// container developers already start via docker-compose) using a dedicated
// rrhh_go_test database, and against the real production mux
// (api.Routes(signer)) so tests exercise middleware mounting, not bare
// handler funcs.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
)

// TestSecret signs every JWT minted by this harness.
const TestSecret = "test-secret-not-for-production"

const defaultTestDBURL = "postgres://rrhh_go:rrhh_go_dev@localhost:5434/rrhh_go_test?sslmode=disable"

// migrationsDir resolves backend/migrations relative to this source file, so
// it is independent of the calling test package's working directory.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// Pool connects to TEST_DB_URL (defaulting to the dedicated rrhh_go_test
// database in the docker-compose Postgres), applies non-seed migrations
// once, and registers pool teardown.
//
// On connection failure this calls t.Fatal, NOT t.Skip, unless SKIP_DB_TESTS=1
// is set explicitly: a leak test that silently skips when the DB is down
// gives a green CI and zero tenant protection. The opt-out must be an
// auditable environment variable, not the default.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = defaultTestDBURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		failOrSkip(t, fmt.Sprintf("connect test db: %v", err))
		return nil
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		failOrSkip(t, fmt.Sprintf("ping test db: %v", err))
		return nil
	}

	if err := applyMigrations(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("apply migrations: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func failOrSkip(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("SKIP_DB_TESTS") == "1" {
		t.Skip(msg)
		return
	}
	t.Fatal(msg)
}

// applyMigrations globs migrations/*.sql, sorts by filename, skips any file
// whose name contains "seed" (the seeded Ventatec company/user is fixture
// noise that would make list assertions fragile), and applies the rest
// inside one transaction if a sentinel table ("departments") is absent.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
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

	files, err := filepath.Glob(filepath.Join(migrationsDir(), "*.sql"))
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

// Server returns the real production mux — api.Routes(signer) — so tests
// exercise middleware mounting, not bare handler funcs.
func Server(t *testing.T, pool *pgxpool.Pool) (http.Handler, *auth.Signer) {
	t.Helper()
	signer := auth.NewSigner(TestSecret, time.Hour)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := handlers.New(pool, signer, log)
	return api.Routes(signer), signer
}

// Tenant is a company + admin user created for one test.
type Tenant struct {
	CompanyID string
	UserID    string
	Role      string
}

// Company inserts a company plus one admin user and registers
// t.Cleanup(DELETE FROM companies WHERE id = $1), which cascades away every
// row any test created under that tenant (every tenant table declares
// company_id ... ON DELETE CASCADE from companies).
func Company(t *testing.T, pool *pgxpool.Pool, name string) Tenant {
	t.Helper()
	ctx := context.Background()

	var companyID string
	err := pool.QueryRow(ctx,
		`INSERT INTO companies (name) VALUES ($1) RETURNING id`, name,
	).Scan(&companyID)
	if err != nil {
		t.Fatalf("insert test company: %v", err)
	}

	userID := uuid.NewString()
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, company_id, email, role, first_name, last_name, status)
		 VALUES ($1, $2, $3, 'admin', 'Test', 'Admin', 'active')`,
		userID, companyID, name+"-admin@example.test",
	)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, companyID)
	})

	return Tenant{CompanyID: companyID, UserID: userID, Role: "admin"}
}

// Token mints a real JWT via signer.Sign — same code path as Login.
func (tn Tenant) Token(t *testing.T, signer *auth.Signer) string {
	t.Helper()
	tok, err := signer.Sign(tn.UserID, tn.CompanyID, tn.Role)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return tok
}

// TokenAs mints a JWT for this tenant asserting a role other than the one
// Company created (always "admin"). The JWT role need not match a `users`
// row for the middleware to accept it — role comes from the signed claim,
// not a DB lookup. Needed by Phase 2's permission tests (design Q4/Q7),
// e.g. an "empleado" token to prove requirePermission denies it.
func (tn Tenant) TokenAs(t *testing.T, signer *auth.Signer, role string) string {
	t.Helper()
	tok, err := signer.Sign(tn.UserID, tn.CompanyID, role)
	if err != nil {
		t.Fatalf("sign test token as %s: %v", role, err)
	}
	return tok
}

// EmployeeFixture is a minimal employee row created directly via SQL for
// tests that need a real employee FK. Every Phase 2 table (attendance_logs,
// leave_balances, leave_requests, overtime_logs, deductions) requires one
// (design Q7).
type EmployeeFixture struct {
	ID        string
	CompanyID string
	Name      string
}

// employeeOptions holds Employee's optional fields. Unset fields default to
// a generated unique name, no department, and status "active".
type employeeOptions struct {
	firstName  string
	lastName   string
	department string
	status     string
}

// EmployeeOption customizes a fixture created by Employee.
type EmployeeOption func(*employeeOptions)

// EmployeeName overrides the generated first/last name.
func EmployeeName(firstName, lastName string) EmployeeOption {
	return func(o *employeeOptions) {
		o.firstName = firstName
		o.lastName = lastName
	}
}

// EmployeeDepartment sets the department string stored on the fixture — a
// plain TEXT column with no FK to departments (design phase1-design P6).
func EmployeeDepartment(department string) EmployeeOption {
	return func(o *employeeOptions) { o.department = department }
}

// EmployeeStatus overrides the default "active" status, e.g. to exercise
// the accrual scheduler's `WHERE e.status = 'active'` filter (design Q5a).
func EmployeeStatus(status string) EmployeeOption {
	return func(o *employeeOptions) { o.status = status }
}

// Employee inserts a minimal employee row directly via SQL (bypassing the
// HTTP API, matching Phase 1's own seedEmployee precedent) for the given
// tenant. No separate cleanup is registered — it cascades away with the
// tenant's company row, which Company already registers.
func Employee(t *testing.T, pool *pgxpool.Pool, tenant Tenant, opts ...EmployeeOption) EmployeeFixture {
	t.Helper()

	o := employeeOptions{
		firstName: "Test",
		lastName:  "Employee-" + uuid.NewString()[:8],
		status:    "active",
	}
	for _, opt := range opts {
		opt(&o)
	}

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO employees (company_id, first_name, last_name, department, status)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		 RETURNING id`,
		tenant.CompanyID, o.firstName, o.lastName, o.department, o.status,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test employee: %v", err)
	}

	return EmployeeFixture{
		ID:        id,
		CompanyID: tenant.CompanyID,
		Name:      o.firstName + " " + o.lastName,
	}
}

// AttendanceDays inserts n attendance_logs rows for the given employee, one
// per distinct day walking backward from today — idx_att_emp_date forces
// distinct (employee_id,date) pairs, so a single date cannot represent
// multiple days worked. Each row carries the given status. Used by the
// accrual scheduler tests to produce a known days_worked count, e.g. 23
// present days -> floor(23/11) = 2 (design Q5a/Q5b).
func AttendanceDays(t *testing.T, pool *pgxpool.Pool, companyID, employeeID, status string, n int) {
	t.Helper()
	ctx := context.Background()
	today := time.Now()

	for i := 0; i < n; i++ {
		date := today.AddDate(0, 0, -i)
		_, err := pool.Exec(ctx,
			`INSERT INTO attendance_logs (company_id, employee_id, date, status)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (employee_id, date) DO UPDATE SET status = EXCLUDED.status`,
			companyID, employeeID, date.Format("2006-01-02"), status,
		)
		if err != nil {
			t.Fatalf("insert test attendance day %d: %v", i, err)
		}
	}
}

// Do performs an in-process request (httptest, no listening socket). token
// may be "" to exercise the unauthenticated path.
func Do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// DecodeRows asserts the body is a JSON ARRAY (A3) and decodes it.
func DecodeRows[T any](t *testing.T, rec *httptest.ResponseRecorder) []T {
	t.Helper()
	var rows []T
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode rows (body=%s): %v", rec.Body.String(), err)
	}
	return rows
}

// DecodeRow decodes a single JSON object response body (e.g. GET
// /api/{resource}/{id}, or a bespoke admin action that returns one object)
// into T. Use DecodeRows for an array response (A3) -- most write endpoints
// still return a single-element array, not a bare object.
func DecodeRow[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var row T
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatalf("decode row (body=%s): %v", rec.Body.String(), err)
	}
	return row
}

// ErrCode extracts error.code (A2) for assertions.
func ErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body (body=%s): %v", rec.Body.String(), err)
	}
	return body.Error.Code
}
