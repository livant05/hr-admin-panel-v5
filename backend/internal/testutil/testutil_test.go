package testutil_test

import (
	"context"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// TestEmployee_CreatesFixtureUnderTenant pins the Phase 2 testutil.Employee
// helper (design Q7): every Phase 2 table needs a real employee FK, and the
// fixture must be scoped to the tenant that created it, with option
// overrides taking effect.
func TestEmployee_CreatesFixtureUnderTenant(t *testing.T) {
	pool := testutil.Pool(t)
	tenant := testutil.Company(t, pool, "TestutilEmployeeCo")

	emp := testutil.Employee(t, pool, tenant,
		testutil.EmployeeName("Ada", "Lovelace"),
		testutil.EmployeeDepartment("Engineering"),
	)

	if emp.ID == "" {
		t.Fatalf("expected a generated employee id")
	}
	if emp.CompanyID != tenant.CompanyID {
		t.Fatalf("expected fixture company_id=%s, got %s", tenant.CompanyID, emp.CompanyID)
	}
	if emp.Name != "Ada Lovelace" {
		t.Fatalf("expected name %q, got %q", "Ada Lovelace", emp.Name)
	}

	var department, status string
	err := pool.QueryRow(context.Background(),
		`SELECT department, status FROM employees WHERE id = $1 AND company_id = $2`,
		emp.ID, tenant.CompanyID,
	).Scan(&department, &status)
	if err != nil {
		t.Fatalf("read back employee: %v", err)
	}
	if department != "Engineering" {
		t.Fatalf("expected department=Engineering, got %q", department)
	}
	if status != "active" {
		t.Fatalf("expected default status=active, got %q", status)
	}
}

// TestEmployee_DefaultsWithNoDepartment pins the NULLIF empty-string ->
// NULL behavior for an option-free fixture (no department override).
func TestEmployee_DefaultsWithNoDepartment(t *testing.T) {
	pool := testutil.Pool(t)
	tenant := testutil.Company(t, pool, "TestutilEmployeeDefaultsCo")

	emp := testutil.Employee(t, pool, tenant)

	var department *string
	err := pool.QueryRow(context.Background(),
		`SELECT department FROM employees WHERE id = $1`, emp.ID,
	).Scan(&department)
	if err != nil {
		t.Fatalf("read back employee: %v", err)
	}
	if department != nil {
		t.Fatalf("expected department to be NULL when no option is given, got %q", *department)
	}
}

// TestAttendanceDays_InsertsDistinctDaysWithStatus pins the Phase 2
// scheduler fixture helper (design Q5a/Q7): n days must land as n distinct
// (employee_id,date) rows, all carrying the requested status, so
// floor(days_worked/11) is derivable deterministically in scheduler tests.
func TestAttendanceDays_InsertsDistinctDaysWithStatus(t *testing.T) {
	pool := testutil.Pool(t)
	tenant := testutil.Company(t, pool, "TestutilAttendanceCo")
	emp := testutil.Employee(t, pool, tenant)

	testutil.AttendanceDays(t, pool, tenant.CompanyID, emp.ID, "present", 23)

	var count int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM attendance_logs WHERE employee_id = $1 AND status = 'present'`,
		emp.ID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count attendance rows: %v", err)
	}
	if count != 23 {
		t.Fatalf("expected 23 distinct present days, got %d", count)
	}
}

// TestTenant_TokenAsMintsRequestedRole pins Tenant.TokenAs: the minted JWT
// must carry the requested role, not the tenant's own "admin" role, and be
// accepted by the real middleware end to end.
func TestTenant_TokenAsMintsRequestedRole(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	tenant := testutil.Company(t, pool, "TestutilTokenAsCo")

	tok := tenant.TokenAs(t, signer, "empleado")

	claims, err := signer.Parse(tok)
	if err != nil {
		t.Fatalf("parse minted token: %v", err)
	}
	if claims.Role != "empleado" {
		t.Fatalf("expected role=empleado, got %q", claims.Role)
	}

	// The middleware must accept the token even though no `users` row has
	// role='empleado' -- role comes from the signed claim, not a DB lookup.
	rec := testutil.Do(t, h, "GET", "/api/departments", tok, nil)
	if rec.Code == 401 {
		t.Fatalf("expected the empleado token to be accepted by the middleware, got 401 (body=%s)", rec.Body.String())
	}
}

// TestDecodeRow_DecodesSingleObject pins testutil.DecodeRow against a real
// single-object endpoint (GET /api/departments/{id}).
func TestDecodeRow_DecodesSingleObject(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	tenant := testutil.Company(t, pool, "TestutilDecodeRowCo")
	tok := tenant.Token(t, signer)

	createRec := testutil.Do(t, h, "POST", "/api/departments", tok, map[string]string{"name": "Ventas"})
	type deptRow struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	created := testutil.DecodeRows[deptRow](t, createRec)[0]

	getRec := testutil.Do(t, h, "GET", "/api/departments/"+created.ID, tok, nil)
	got := testutil.DecodeRow[deptRow](t, getRec)
	if got.ID != created.ID || got.Name != "Ventas" {
		t.Fatalf("expected decoded row to match created department, got %+v", got)
	}
}
