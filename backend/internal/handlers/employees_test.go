package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type employeeRow struct {
	ID          string  `json:"id"`
	CompanyID   string  `json:"company_id"`
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
	Department  string  `json:"department"`
	Status      string  `json:"status"`
	WeeklyHours float64 `json:"weekly_hours"`
	Salary      float64 `json:"salary"`
}

// TestEmployees_CrossTenantLeak mirrors TestDepartments_CrossTenantLeak
// (design P5.3) with the employees payload (33-field contract, but only
// first_name/last_name are required).
func TestEmployees_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EmpLeakA")
	b := testutil.Company(t, pool, "EmpLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/employees", ta, map[string]any{
		"first_name": "Ana", "last_name": "Diaz",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[employeeRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	empA := rows[0]

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/employees/"+empA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/employees/"+empA.ID, tb, map[string]any{
			"first_name": "Hacked", "last_name": "Hacked",
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/employees/"+empA.ID, ta, nil)
		var got employeeRow
		if err := decodeJSON(getRec, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.FirstName != "Ana" {
			t.Fatalf("expected first_name to remain Ana, got %q", got.FirstName)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/employees/"+empA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/employees/"+empA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected employee to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/employees", tb, map[string]any{
			"first_name": "Luis", "last_name": "Gomez",
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[employeeRow](t, testutil.Do(t, h, http.MethodGet, "/api/employees", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != empA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.FirstName == "Ana" {
				found = true
			}
			if r.FirstName == "Luis" {
				t.Fatalf("company A's list leaked company B's employee")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employees", ta, map[string]any{
			"first_name": "Sneaky", "last_name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/employees", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "EmpEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/employees", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[employeeRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestEmployees_RejectsMissingRequiredNames pins the spec's "Reject missing
// last name" scenario, extended (with the same rigor as
// TestDepartments_RejectsEmptyName) to cover both first_name and last_name,
// on both POST and PATCH, asserting no row is created/mutated.
func TestEmployees_RejectsMissingRequiredNames(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EmpRejectMissingNamesCo")
	tok := c.Token(t, signer)

	t.Run("create missing last_name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{"first_name": "Ana"})
		assertRejectedEmployeeNames(t, rec, "last_name")
	})

	t.Run("create missing first_name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{"last_name": "Diaz"})
		assertRejectedEmployeeNames(t, rec, "first_name")
	})

	t.Run("create with empty first_name and last_name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{"first_name": "", "last_name": "   "})
		assertRejectedEmployeeNames(t, rec, "first_name", "last_name")
	})

	rows := testutil.DecodeRows[employeeRow](t, testutil.Do(t, h, http.MethodGet, "/api/employees", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no employee to be created by a rejected request, got %d", len(rows))
	}

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Ana", "last_name": "Diaz",
	})
	emp := testutil.DecodeRows[employeeRow](t, createRec)[0]

	t.Run("update with empty last_name", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/employees/"+emp.ID, tok, map[string]any{
			"first_name": "Ana", "last_name": "",
		})
		assertRejectedEmployeeNames(t, rec, "last_name")
	})

	getRec := testutil.Do(t, h, http.MethodGet, "/api/employees/"+emp.ID, tok, nil)
	var got employeeRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.FirstName != "Ana" || got.LastName != "Diaz" {
		t.Fatalf("expected employee to remain Ana Diaz after rejected rename, got %q %q", got.FirstName, got.LastName)
	}
}

// assertRejectedEmployeeNames asserts the shared shape of a rejected
// missing/empty required-name request: 400, error.code=validation_failed,
// and a fields.<name> detail for every field expected to be flagged.
func assertRejectedEmployeeNames(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
	for _, f := range fields {
		if msg := errField(t, rec, f); msg == "" {
			t.Fatalf("expected error.fields.%s detail in body, got none (body=%s)", f, rec.Body.String())
		}
	}
}

// TestEmployees_DefaultsMatchClientFallbacks pins the spec's "Defaults match
// client fallbacks" scenario: an absent weekly_hours defaults to 48 and an
// absent status defaults to "active", mirroring saveEmp's own `||` fallbacks
// (hr_admin_panel.html:2629) so Go and JS never disagree on a default.
func TestEmployees_DefaultsMatchClientFallbacks(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EmpDefaultsCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Ana", "last_name": "Diaz",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	emp := testutil.DecodeRows[employeeRow](t, rec)[0]

	if emp.WeeklyHours != 48 {
		t.Fatalf("expected weekly_hours default 48, got %v", emp.WeeklyHours)
	}
	if emp.Status != "active" {
		t.Fatalf("expected status default active, got %q", emp.Status)
	}
	if emp.Salary != 0 {
		t.Fatalf("expected salary default 0, got %v", emp.Salary)
	}
}

// TestEmployees_FilterByStatus pins the spec's "Filter by status" scenario:
// GET /api/employees?status=active returns exactly the active rows for that
// tenant.
func TestEmployees_FilterByStatus(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EmpFilterStatusCo")
	tok := c.Token(t, signer)

	testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Ana", "last_name": "Diaz", "status": "active",
	})
	testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Luis", "last_name": "Gomez", "status": "active",
	})
	testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Rosa", "last_name": "Perez", "status": "inactive",
	})

	rec := testutil.Do(t, h, http.MethodGet, "/api/employees?status=active", tok, nil)
	rows := testutil.DecodeRows[employeeRow](t, rec)
	if len(rows) != 2 {
		t.Fatalf("expected exactly 2 active employees, got %d (body=%s)", len(rows), rec.Body.String())
	}
	for _, r := range rows {
		if r.Status != "active" {
			t.Fatalf("expected only active rows, got status=%q", r.Status)
		}
		if r.CompanyID != c.CompanyID {
			t.Fatalf("status filter leaked a row from another tenant: %+v", r)
		}
	}
}

// TestEmployees_SoftDeletePreservesHistory pins design P6.2: DELETE
// /api/employees/{id} MUST set status='inactive' and MUST NOT trigger any
// cascade — a seeded attendance_logs row referencing the employee must
// survive the delete untouched.
func TestEmployees_SoftDeletePreservesHistory(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EmpSoftDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Ana", "last_name": "Diaz", "status": "active",
	})
	emp := testutil.DecodeRows[employeeRow](t, createRec)[0]

	attendanceID := seedAttendanceLog(t, pool, c.CompanyID, emp.ID)

	rec := testutil.Do(t, h, http.MethodDelete, "/api/employees/"+emp.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/employees/"+emp.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the employee row to survive a soft delete, got %d", getRec.Code)
	}
	var got employeeRow
	if err := decodeJSON(getRec, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "inactive" {
		t.Fatalf("expected status=inactive after DELETE, got %q", got.Status)
	}

	if !attendanceLogExists(t, pool, attendanceID) {
		t.Fatalf("expected the attendance_logs row to survive the soft delete (no cascade should fire)")
	}
}

// TestEmployees_SoftDeleteOnAlreadyInactiveIs404 pins the affected-rows=0
// symmetry with DeleteDepartment: deactivating an already-inactive employee
// (or a nonexistent one) reports 404, matching the established
// `:execrows == 0 -> 404` convention used by every other resource's delete.
func TestEmployees_SoftDeleteOnAlreadyInactiveIs404(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EmpDoubleDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employees", tok, map[string]any{
		"first_name": "Ana", "last_name": "Diaz",
	})
	emp := testutil.DecodeRows[employeeRow](t, createRec)[0]

	first := testutil.Do(t, h, http.MethodDelete, "/api/employees/"+emp.ID, tok, nil)
	if first.Code != http.StatusNoContent {
		t.Fatalf("expected first delete to be 204, got %d", first.Code)
	}

	second := testutil.Do(t, h, http.MethodDelete, "/api/employees/"+emp.ID, tok, nil)
	if second.Code != http.StatusNotFound {
		t.Fatalf("expected second delete on an already-inactive employee to be 404, got %d", second.Code)
	}
}

// seedAttendanceLog inserts a minimal attendance_logs row referencing
// employeeID, so the soft-delete test can prove no cascade fires.
func seedAttendanceLog(t *testing.T, pool *pgxpool.Pool, companyID, employeeID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO attendance_logs (company_id, employee_id, date, status)
		 VALUES ($1, $2, CURRENT_DATE, 'present') RETURNING id`,
		companyID, employeeID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed attendance log: %v", err)
	}
	return id
}

// attendanceLogExists confirms the attendance_logs row was not cascaded away.
func attendanceLogExists(t *testing.T, pool *pgxpool.Pool, id string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM attendance_logs WHERE id = $1)`, id,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check attendance log existence: %v", err)
	}
	return exists
}
