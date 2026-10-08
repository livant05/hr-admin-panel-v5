package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type attendanceLogRow struct {
	ID           string  `json:"id"`
	CompanyID    string  `json:"company_id"`
	EmployeeID   string  `json:"employee_id"`
	EmployeeName string  `json:"employee_name"`
	Department   string  `json:"department"`
	Date         string  `json:"date"`
	TimeIn       *string `json:"time_in"`
	TimeOut      *string `json:"time_out"`
	Status       string  `json:"status"`
	WorkType     *int32  `json:"work_type"`
	Notes        string  `json:"notes"`
}

// TestAttendanceLogs_CrossTenantLeak mirrors TestDepartments_CrossTenantLeak
// (design P5.3), seeding a real employee fixture so the A1 rule 6 join has a
// tenant-owned employee_id to resolve.
func TestAttendanceLogs_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AttLeakA")
	b := testutil.Company(t, pool, "AttLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeDepartment("Ventas"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", ta, map[string]any{
		"employee_id": empA.ID, "date": "2026-01-05", "status": "present",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[attendanceLogRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	logA := rows[0]
	if logA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", logA.EmployeeName)
	}
	if logA.Department != "Ventas" {
		t.Fatalf("expected server-derived department 'Ventas', got %q", logA.Department)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs/"+logA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/attendance_logs/"+logA.ID, tb, map[string]any{
			"status": "absent",
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs/"+logA.ID, ta, nil)
		got := testutil.DecodeRow[attendanceLogRow](t, getRec)
		if got.Status != "present" {
			t.Fatalf("expected status to remain present, got %q", got.Status)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/attendance_logs/"+logA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs/"+logA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected attendance log to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tb, map[string]any{
			"employee_id": empB.ID, "date": "2026-01-05", "status": "present",
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[attendanceLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != logA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.EmployeeName == "Ana Diaz" {
				found = true
			}
			if r.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's attendance log")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's log")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", ta, map[string]any{
			"employee_id": empA.ID, "date": "2026-01-06", "status": "present", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "AttEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[attendanceLogRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestAttendanceLogs_CrossTenantEmployeeIDRejected pins design Q2 (A1 rule
// 6): a client-supplied employee_id belonging to another tenant must be
// rejected as 404 — the tenant check on employee_id IS the insert
// (INSERT ... SELECT ... FROM employees WHERE e.id=$2 AND e.company_id=$1),
// so a foreign employee_id selects zero rows and no row is ever written
// under either tenant.
func TestAttendanceLogs_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AttEmpIDRejectA")
	b := testutil.Company(t, pool, "AttEmpIDRejectB")
	ta := a.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Foreign", "Employee"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", ta, map[string]any{
		"employee_id": empB.ID, "date": "2026-01-07", "status": "present",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	if countAttendanceLogsForEmployee(t, pool, empB.ID) != 0 {
		t.Fatalf("expected no attendance log row to be created for the foreign employee_id")
	}

	aRows := testutil.DecodeRows[attendanceLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", ta, nil))
	if len(aRows) != 0 {
		t.Fatalf("expected no attendance log to be created under the requesting tenant either, got %d", len(aRows))
	}
}

// TestAttendanceLogs_DuplicateUpsertsNotConflicts pins design Q2's second
// rider: POST is an upsert on (employee_id,date), matching saveAttendance's
// own existing-row lookup and generateTestData's on_conflict=employee_id,date
// raw fetch. A second create for the same pair updates the row in place
// instead of 409ing.
func TestAttendanceLogs_DuplicateUpsertsNotConflicts(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "AttUpsertCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	first := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-01-08", "status": "present", "time_in": "08:00",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("expected first create to be 201, got %d (body=%s)", first.Code, first.Body.String())
	}

	second := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-01-08", "status": "late", "time_in": "09:15",
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("expected the duplicate (employee_id,date) create to upsert with 201, not 409, got %d (body=%s)", second.Code, second.Body.String())
	}
	rows := testutil.DecodeRows[attendanceLogRow](t, second)
	if len(rows) != 1 {
		t.Fatalf("expected a single-element array, got %d", len(rows))
	}
	if rows[0].Status != "late" {
		t.Fatalf("expected the upsert to update status to late, got %q", rows[0].Status)
	}

	if countAttendanceLogsForEmployee(t, pool, emp.ID) != 1 {
		t.Fatalf("expected exactly one row for (employee_id,date), the upsert must not have inserted a second row")
	}
}

// TestAttendanceLogs_RejectsMissingRequiredFields pins the spec's "Required
// fields rejected" scenario: employee_id and date are both required.
func TestAttendanceLogs_RejectsMissingRequiredFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "AttRequiredFieldsCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	t.Run("missing employee_id", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tok, map[string]any{"date": "2026-01-09"})
		assertAttendanceValidationFailed(t, rec, "employee_id")
	})

	t.Run("missing date", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tok, map[string]any{"employee_id": emp.ID})
		assertAttendanceValidationFailed(t, rec, "date")
	})

	rows := testutil.DecodeRows[attendanceLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no attendance log to be created by a rejected request, got %d", len(rows))
	}
}

// TestAttendanceLogs_UpdateReturnsUpdatedRow closes Phase 1's open verify
// warning (design Q8): PATCH must return 200, a single-element array, the
// changed field holding the new value, and the change must persist.
func TestAttendanceLogs_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "AttUpdateCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-01-10", "status": "present",
	})
	log := testutil.DecodeRows[attendanceLogRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/attendance_logs/"+log.ID, tok, map[string]any{
		"status": "late", "time_in": "09:30", "notes": "Tráfico",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[attendanceLogRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].Status != "late" {
		t.Fatalf("expected the changed field (status) to hold the new value, got %q", rows[0].Status)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs/"+log.ID, tok, nil)
	got := testutil.DecodeRow[attendanceLogRow](t, getRec)
	if got.Status != "late" {
		t.Fatalf("expected the update to persist, got status=%q", got.Status)
	}
	if got.TimeIn == nil || *got.TimeIn != "09:30" {
		t.Fatalf("expected time_in to persist as 09:30, got %v", got.TimeIn)
	}
	if got.Notes != "Tráfico" {
		t.Fatalf("expected notes to persist, got %q", got.Notes)
	}
}

// TestAttendanceLogs_FilterByIDReturnsSingleElementArray closes Phase 1's
// other open verify warning (design Q8): ?id=<uuid> must return exactly one
// element, and a foreign tenant's uuid must return an empty array, never a
// bare object or a 404 (phase1-design P7's Array.isArray contract).
func TestAttendanceLogs_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AttFilterIDA")
	b := testutil.Company(t, pool, "AttFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/attendance_logs", ta, map[string]any{
		"employee_id": empA.ID, "date": "2026-01-11", "status": "present",
	})
	log := testutil.DecodeRows[attendanceLogRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs?id="+log.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[attendanceLogRow](t, rec)
	if len(rows) != 1 || rows[0].ID != log.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", log.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs?id="+log.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[attendanceLogRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}

// assertAttendanceValidationFailed asserts the shared shape of a rejected
// required-field request: 400, error.code=validation_failed, and a
// fields.<name> detail for every field expected to be flagged.
func assertAttendanceValidationFailed(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
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

// countAttendanceLogsForEmployee verifies a cross-tenant rejected (or
// upserted) write did not leave an unexpected row count behind.
func countAttendanceLogsForEmployee(t *testing.T, pool *pgxpool.Pool, employeeID string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM attendance_logs WHERE employee_id = $1`, employeeID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count attendance logs for employee: %v", err)
	}
	return n
}
