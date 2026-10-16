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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/scheduler"
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

// migrationLockKey is the pg_advisory_xact_lock key serializing migration
// application across concurrently running test packages.
const migrationLockKey = 7261008810

// firstIdempotentMigration is the number of the first migration written to be
// safely re-applied. Every migration numbered at or above it MUST be
// idempotent: this harness re-applies it on every Pool() call.
const firstIdempotentMigration = 10

// UsersEmailLowerIndex is the name of the unique lower(email) index created by
// migration 0010. Tests share this constant instead of repeating the literal.
const UsersEmailLowerIndex = "idx_users_email_lower"

// isIdempotentMigration reports whether a migration file is numbered
// >= firstIdempotentMigration.
func isIdempotentMigration(base string) bool {
	if len(base) < 4 {
		return false
	}
	n, err := strconv.Atoi(base[:4])
	return err == nil && n >= firstIdempotentMigration
}

// dedupeLegacyEmailsSQL rewrites, in TEST databases only, every user whose
// lower(email) collides with an older row (oldest created_at, then id, wins).
// Old fixtures stored fixed emails, so a long-lived test database may hold
// case-insensitive duplicates that would abort migration 0010. Never part of
// the production migration path.
const dedupeLegacyEmailsSQL = `
UPDATE users u
SET email = 'dedup-' || u.id::text || '-' || u.email
FROM (
  SELECT id, row_number() OVER (PARTITION BY lower(email) ORDER BY created_at, id) AS rn
  FROM users WHERE email IS NOT NULL
) r
WHERE r.id = u.id AND r.rn > 1`

// applyMigrations globs migrations/*.sql, sorts by filename, skips any file
// whose name contains "seed" (the seeded Ventatec company/user is fixture
// noise that would make list assertions fragile), and applies the rest
// inside one transaction if a sentinel table ("departments") is absent. Files
// numbered >= 0010 are idempotent and applied on every call.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
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

	// Concurrent `go test ./...` packages each call this; the advisory lock
	// serializes them so the idempotent files below never race.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	// The sentinel check MUST follow the lock: checked before it, two callers
	// on a fresh database could both see "absent" and the loser would re-apply
	// the non-idempotent pre-0010 files. Scoped to current_schema() so a
	// schema-isolated pool is judged by its own tables, not public's.
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'departments'
	)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check sentinel table: %w", err)
	}

	// An already-initialized test database may hold legacy case-insensitive
	// duplicate emails; heal them before any idempotent migration (0010+)
	// re-applies the unique index.
	if exists {
		if _, err := tx.Exec(ctx, dedupeLegacyEmailsSQL); err != nil {
			return fmt.Errorf("dedupe legacy test emails: %w", err)
		}
	}

	for _, f := range files {
		base := filepath.Base(f)
		if strings.Contains(base, "seed") {
			continue
		}
		// Files below 0010 predate idempotency and only run on a fresh
		// database (sentinel table absent). From 0010 on every migration is
		// idempotent and is always re-applied, so an already-initialized
		// test database picks up new migrations automatically.
		if exists && !isIdempotentMigration(base) {
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

// ServerWithScheduler is Server plus a wired api.Scheduler, needed only by
// the POST /api/leave_balances/accrue tests (design Q5c) -- every other
// handler test uses the plain Server, whose API has a nil Scheduler.
// handlers.New deliberately takes no scheduler parameter (most callers,
// including Server, don't need one), so this sets the field directly on the
// constructed *API before building the mux, matching cmd/api/main.go's own
// wiring order.
func ServerWithScheduler(t *testing.T, pool *pgxpool.Pool, sched *scheduler.Scheduler) (http.Handler, *auth.Signer) {
	t.Helper()
	signer := auth.NewSigner(TestSecret, time.Hour)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := handlers.New(pool, signer, log)
	api.Scheduler = sched
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
		userID, companyID, name+"-admin-"+uuid.NewString()[:8]+"@example.test",
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
	tok, err := signer.Sign(tn.UserID, tn.CompanyID, tn.Role, 0)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return tok
}

// TokenAs mints a JWT for a fresh active user of this tenant holding role.
// The auth middleware takes role and company from the users row, not from the
// token, so asserting a role means a row that has it: every call inserts a
// new user (never the tenant's admin), keeping tokens for different roles
// independent of each other.
func (tn Tenant) TokenAs(t *testing.T, pool *pgxpool.Pool, signer *auth.Signer, role string) string {
	t.Helper()
	userID := uuid.NewString()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, company_id, email, role, first_name, last_name, status)
		 VALUES ($1, $2, $3, $4, 'Test', 'Role', 'active')`,
		userID, tn.CompanyID, "role-"+uuid.NewString()[:8]+"@example.test", role,
	)
	if err != nil {
		t.Fatalf("insert test user with role %s: %v", role, err)
	}
	tok, err := signer.Sign(userID, tn.CompanyID, role, 0)
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
// a generated unique name, no department, status "active", and salary 0
// (matching the employees.salary column's own DEFAULT 0).
type employeeOptions struct {
	firstName  string
	lastName   string
	department string
	status     string
	salary     float64
	startDate  string // "" means NULL (unset); otherwise "YYYY-MM-DD"
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

// EmployeeSalary overrides the default 0 salary, needed by any test that
// exercises payroll.Calculate against a real employee row (slice 3e's
// payroll preview tests -- a salary=0 employee makes attDed = (0/30)*n = 0
// regardless of absent days, which would make the hazard-5 assertions
// vacuously true).
func EmployeeSalary(salary float64) EmployeeOption {
	return func(o *employeeOptions) { o.salary = salary }
}

// EmployeeStartDate overrides the default NULL start_date ("YYYY-MM-DD"),
// needed by any test that exercises payroll.CalculateLiquidation against a
// real employee row (slice 3g's liquidación tests -- the calculation reads
// employees.start_date via GetEmployeeForLiquidation and rejects a NULL one).
func EmployeeStartDate(date string) EmployeeOption {
	return func(o *employeeOptions) { o.startDate = date }
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
		`INSERT INTO employees (company_id, first_name, last_name, department, status, salary, start_date)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, '')::date)
		 RETURNING id`,
		tenant.CompanyID, o.firstName, o.lastName, o.department, o.status, o.salary, o.startDate,
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

// LeaveBalanceFixture is a leave_balances row created directly via SQL.
// leave_balances has no POST endpoint (design Q3 — rows are owned by the
// accrual scheduler, slice 2c2), so every test needing a balance row to
// GET/PATCH must seed it this way, matching Employee's same bypass-the-API
// precedent.
type LeaveBalanceFixture struct {
	ID         string
	CompanyID  string
	EmployeeID string
}

// LeaveBalance inserts a minimal leave_balances row directly via SQL for the
// given tenant/employee/year. No separate cleanup is registered — it
// cascades away with the tenant's company row, which Company already
// registers.
func LeaveBalance(t *testing.T, pool *pgxpool.Pool, tenant Tenant, employeeID, employeeName string, year int, earnedDays, usedDays float64) LeaveBalanceFixture {
	t.Helper()

	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO leave_balances (company_id, employee_id, employee_name, year, earned_days, used_days)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		tenant.CompanyID, employeeID, employeeName, year, earnedDays, usedDays,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test leave balance: %v", err)
	}

	return LeaveBalanceFixture{ID: id, CompanyID: tenant.CompanyID, EmployeeID: employeeID}
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

// newSchemaURL creates an empty, uniquely named schema in the test database
// (dropped on cleanup) and returns its name plus a connection URL whose
// search_path points at it, with public as fallback for the uuid-ossp
// functions. It applies no migrations.
func newSchemaURL(t *testing.T) (schema, isoURL string) {
	t.Helper()
	admin := Pool(t)

	schema = "imp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	})

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = defaultTestDBURL
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatalf("parse test db url: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return schema, u.String()
}

// IsolatedSchema creates a fresh Postgres schema in the test database, applies
// every migration INCLUDING the seed into it, and returns a pool whose
// search_path points at that schema (with public as fallback for the
// uuid-ossp functions) plus a connection URL carrying the same search_path.
// It exists for tests that bulk-load data (the Supabase import tool): they
// must not touch the shared tables other packages' tests read, and the seed
// company/admin must be present so "seed untouched" can be asserted. The
// schema is dropped on cleanup.
//
// Caveat: 0007's constraint guard looks up pg_constraint by name only, so it
// sees epr_origin_chk from the public schema and skips creating it here.
func IsolatedSchema(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	schema, isoURL := newSchemaURL(t)

	pool, err := pgxpool.New(ctx, isoURL)
	if err != nil {
		t.Fatalf("connect isolated schema: %v", err)
	}
	t.Cleanup(pool.Close)

	files, err := filepath.Glob(filepath.Join(migrationsDir(), "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(files)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := tx.Exec(ctx, string(b)); err != nil {
			t.Fatalf("apply %s to %s: %v", filepath.Base(f), schema, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit schema migrations: %v", err)
	}
	return pool, isoURL
}

// DropIndexInIsolatedSchema drops the named index from the schema the pool is
// isolated into. The DROP is schema-qualified, so it can never resolve to a
// same-named index in public through search_path; the test fails instead when
// the pool is not schema-isolated or the index is absent from its schema.
func DropIndexInIsolatedSchema(t *testing.T, pool *pgxpool.Pool, index string) {
	t.Helper()
	ctx := context.Background()

	var schema string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if schema == "public" {
		t.Fatalf("refusing to drop index %s: pool is on public, not an isolated schema", index)
	}
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)`,
		schema, index).Scan(&exists); err != nil {
		t.Fatalf("check index %s.%s: %v", schema, index, err)
	}
	if !exists {
		t.Fatalf("index %s is absent from isolated schema %s", index, schema)
	}
	if _, err := pool.Exec(ctx, `DROP INDEX `+pgx.Identifier{schema, index}.Sanitize()); err != nil {
		t.Fatalf("drop index %s.%s: %v", schema, index, err)
	}
}
