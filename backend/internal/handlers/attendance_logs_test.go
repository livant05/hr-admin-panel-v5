package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
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

	t.Run("created_after never returns another tenant's newer rows", func(t *testing.T) {
		// B's row (created in the previous subtest) is newer than this cutoff,
		// so a missing company scope on the created_after query would leak it.
		bRows := testutil.DecodeRows[attendanceLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/attendance_logs", tb, nil))
		if len(bRows) == 0 {
			t.Fatal("precondition: company B must have a row")
		}
		for _, q := range []string{
			"?created_after=2000-01-01T00:00:00Z",
			"?created_after=2000-01-01T00:00:00Z&_order=created_at.desc&_limit=50",
		} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs"+q, ta, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got %d (%s)", q, rec.Code, rec.Body.String())
			}
			rows := testutil.DecodeRows[attendanceLogRow](t, rec)
			if len(rows) == 0 {
				t.Fatalf("%s: company A's own row must be listed", q)
			}
			for _, r := range rows {
				if r.CompanyID != logA.CompanyID || r.EmployeeName == "Luis Gomez" {
					t.Fatalf("%s: leaked a row from another tenant: %+v", q, r)
				}
			}
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

// seedAttendanceAt inserts a punch row straight into the table with an exact
// created_at, so ordering and the strict created_after cutoff are asserted
// against known instants instead of the wall clock.
func seedAttendanceAt(t *testing.T, pool *pgxpool.Pool, tenant testutil.Tenant, employeeID, name, createdAt string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO attendance_logs (company_id, employee_id, employee_name, date, status, created_at)
		 VALUES ($1, $2, $3, '2026-03-02', 'present', $4::timestamptz) RETURNING id`,
		tenant.CompanyID, employeeID, name, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed attendance at %s: %v", createdAt, err)
	}
	return id
}

func attendanceIDs(rows []attendanceLogRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestAttendanceLogs_CreatedAfter covers TT-1: the strict created_after cutoff,
// the newest-first order the live feed uses, and the order whitelist that is
// open on this endpoint only.
func TestAttendanceLogs_CreatedAfter(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AttAfterA")
	b := testutil.Company(t, pool, "AttAfterB")
	ta := a.Token(t, signer)

	e1 := testutil.Employee(t, pool, a, testutil.EmployeeName("Uno", "Primero"))
	e2 := testutil.Employee(t, pool, a, testutil.EmployeeName("Dos", "Segundo"))
	e3 := testutil.Employee(t, pool, a, testutil.EmployeeName("Tres", "Tercero"))
	e4 := testutil.Employee(t, pool, a, testutil.EmployeeName("Cuatro", "Cuarto"))
	eb := testutil.Employee(t, pool, b, testutil.EmployeeName("Bea", "Ajena"))

	const t1, t2, t3 = "2026-03-02T10:00:00Z", "2026-03-02T10:00:10Z", "2026-03-02T10:00:20Z"
	id1 := seedAttendanceAt(t, pool, a, e1.ID, "Uno Primero", t1)
	id2 := seedAttendanceAt(t, pool, a, e2.ID, "Dos Segundo", t2)
	id3 := seedAttendanceAt(t, pool, a, e3.ID, "Tres Tercero", t3)
	// Another company's rows are newer than every cutoff used below.
	seedAttendanceAt(t, pool, b, eb.ID, "Bea Ajena", "2026-03-02T11:00:00Z")

	list := func(t *testing.T, query string) []attendanceLogRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs"+query, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[attendanceLogRow](t, rec)
	}
	same := func(t *testing.T, label string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}

	t.Run("newer rows only, newest first", func(t *testing.T) {
		got := attendanceIDs(list(t, "?created_after="+t1+"&_order=created_at.desc&_limit=5"))
		same(t, "ids", got, []string{id3, id2})
		for _, id := range got {
			if id == id1 {
				t.Fatalf("the row created exactly at the cutoff must be excluded")
			}
		}
	})

	t.Run("the cutoff is strict", func(t *testing.T) {
		same(t, "after t2", attendanceIDs(list(t, "?created_after="+t2+"&_order=created_at.desc")), []string{id3})
		same(t, "after t3", attendanceIDs(list(t, "?created_after="+t3+"&_order=created_at.desc")), []string{})
	})

	t.Run("an offset form of the same instant behaves the same", func(t *testing.T) {
		got := attendanceIDs(list(t, "?created_after="+url.QueryEscape("2026-03-02T12:00:10+02:00")+"&_order=created_at.desc"))
		same(t, "after t2 (+02:00)", got, []string{id3})
	})

	t.Run("fractional seconds are honored", func(t *testing.T) {
		got := attendanceIDs(list(t, "?created_after=2026-03-02T10:00:19.999999Z&_order=created_at.desc"))
		same(t, "after t3-1us", got, []string{id3})
	})

	t.Run("without _order the filter still applies", func(t *testing.T) {
		got := list(t, "?created_after="+t1)
		ids := attendanceIDs(got)
		sort.Strings(ids)
		want := []string{id2, id3}
		sort.Strings(want)
		same(t, "ids", ids, want)
	})

	t.Run("combines with other filters", func(t *testing.T) {
		same(t, "employee filter", attendanceIDs(list(t, "?created_after="+t1+"&employee_id="+e3.ID)), []string{id3})
		same(t, "date filter", attendanceIDs(list(t, "?created_after="+t3+"&date=2026-03-02")), []string{})
	})

	t.Run("the live-feed seed call returns the newest row", func(t *testing.T) {
		same(t, "ids", attendanceIDs(list(t, "?_order=created_at.desc&_limit=1")), []string{id3})
	})

	t.Run("limit and offset page the ordered list", func(t *testing.T) {
		same(t, "page", attendanceIDs(list(t, "?_order=created_at.desc&_limit=1&_offset=1")), []string{id2})
	})

	t.Run("ties on created_at fall back to id descending", func(t *testing.T) {
		const tie = "2026-03-02T10:00:30Z"
		x := seedAttendanceAt(t, pool, a, e4.ID, "Cuatro Cuarto", tie)
		var y string
		err := pool.QueryRow(context.Background(),
			`INSERT INTO attendance_logs (company_id, employee_id, employee_name, date, status, created_at)
			 VALUES ($1, $2, 'Cuatro Cuarto', '2026-03-03', 'present', $3::timestamptz) RETURNING id`,
			a.CompanyID, e4.ID, tie).Scan(&y)
		if err != nil {
			t.Fatalf("seed tie row: %v", err)
		}
		want := []string{x, y}
		if y > x {
			want = []string{y, x}
		}
		same(t, "tie order", attendanceIDs(list(t, "?created_after="+t3+"&_order=created_at.desc")), want)
	})

	t.Run("a garbage created_after is validation_failed with fields.created_after", func(t *testing.T) {
		for _, v := range []string{"garbage", "2026-03-02", "10:00:00", "1700000000", ""} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs?created_after="+url.QueryEscape(v), ta, nil)
			assertAttendanceValidationFailed(t, rec, "created_after")
		}
	})

	t.Run("no created_after leaves the list unchanged", func(t *testing.T) {
		rows := list(t, "")
		if len(rows) < 3 {
			t.Fatalf("expected at least the three seeded rows, got %d", len(rows))
		}
		for _, r := range rows {
			if r.CompanyID != a.CompanyID {
				t.Fatalf("leaked a row from another tenant: %+v", r)
			}
		}
	})

	t.Run("only created_at.asc and created_at.desc are accepted _order values", func(t *testing.T) {
		for _, v := range []string{"date.desc", "date.asc", "created_at", "id.desc", "id.asc", "created_at.up", ""} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs?_order="+url.QueryEscape(v), ta, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("_order=%q: got %d (%s), want 400", v, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("_order=%q: error.code = %q, want validation_failed", v, code)
			}
		}
	})

	t.Run("every other endpoint still rejects _order", func(t *testing.T) {
		for _, path := range []string{
			"/api/departments", "/api/employees", "/api/leave_requests", "/api/overtime_logs",
			"/api/job_postings", "/api/zkteco_devices", "/api/users",
		} {
			rec := testutil.Do(t, h, http.MethodGet, path+"?_order=created_at.desc", ta, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", path, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("%s: error.code = %q, want validation_failed", path, code)
			}
		}
	})

	t.Run("created_after is not accepted on other endpoints", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/departments?created_after="+t1, ta, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
	})
}

// TestAttendanceLogs_CreatedAscCursor covers the live feed's ascending cursor
// (CO-S4b-1/2): oldest first after the cutoff, a deterministic id tiebreaker
// in the same direction as created_at, and _limit/_offset paging that returns
// every row exactly once across a page boundary, including a boundary that
// falls inside a run of rows sharing one created_at.
func TestAttendanceLogs_CreatedAscCursor(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AttAscA")
	b := testutil.Company(t, pool, "AttAscB")
	ta := a.Token(t, signer)
	eb := testutil.Employee(t, pool, b, testutil.EmployeeName("Bea", "Ajena"))

	// Seven rows: two older, then a run of four sharing one instant, then one
	// newer. Each row is its own (employee, date) pair.
	const (
		tOld1 = "2026-04-01T10:00:00Z"
		tOld2 = "2026-04-01T10:00:05Z"
		tTie  = "2026-04-01T10:00:10Z"
		tNew  = "2026-04-01T10:00:20Z"
	)
	seed := func(createdAt string) string {
		emp := testutil.Employee(t, pool, a, testutil.EmployeeName("Emp", createdAt))
		return seedAttendanceAt(t, pool, a, emp.ID, "Emp "+createdAt, createdAt)
	}
	idOld1, idOld2 := seed(tOld1), seed(tOld2)
	tie := []string{seed(tTie), seed(tTie), seed(tTie), seed(tTie)}
	idNew := seed(tNew)
	seedAttendanceAt(t, pool, b, eb.ID, "Bea Ajena", tNew)

	sortedTie := append([]string(nil), tie...)
	sort.Strings(sortedTie)
	wantAsc := append(append([]string{idOld1, idOld2}, sortedTie...), idNew)
	wantDesc := make([]string, len(wantAsc))
	for i, id := range wantAsc {
		wantDesc[len(wantAsc)-1-i] = id
	}

	list := func(t *testing.T, query string) []string {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, "/api/attendance_logs"+query, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return attendanceIDs(testutil.DecodeRows[attendanceLogRow](t, rec))
	}
	same := func(t *testing.T, label string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
	const scope = "?created_after=2026-04-01T09:59:59Z"

	t.Run("ascending is oldest first with id ascending on ties", func(t *testing.T) {
		same(t, "asc", list(t, scope+"&_order=created_at.asc"), wantAsc)
	})

	t.Run("descending ties break by id descending", func(t *testing.T) {
		same(t, "desc", list(t, scope+"&_order=created_at.desc"), wantDesc)
	})

	t.Run("the cutoff stays strict and tenant scoped in ascending order", func(t *testing.T) {
		same(t, "after tTie", list(t, "?created_after="+tTie+"&_order=created_at.asc"), []string{idNew})
		same(t, "after tNew", list(t, "?created_after="+tNew+"&_order=created_at.asc"), []string{})
	})

	pageAll := func(t *testing.T, order string, limit int) []string {
		t.Helper()
		var got []string
		for offset := 0; offset <= 100; offset += limit {
			page := list(t, fmt.Sprintf("%s&_order=%s&_limit=%d&_offset=%d", scope, order, limit, offset))
			got = append(got, page...)
			if len(page) < limit {
				return got
			}
		}
		t.Fatalf("paging %s never ended", order)
		return nil
	}

	t.Run("paging returns every row exactly once, ascending", func(t *testing.T) {
		for _, limit := range []int{1, 2, 3, 4} {
			same(t, fmt.Sprintf("asc limit=%d", limit), pageAll(t, "created_at.asc", limit), wantAsc)
		}
	})

	t.Run("paging returns every row exactly once, descending", func(t *testing.T) {
		for _, limit := range []int{1, 3} {
			same(t, fmt.Sprintf("desc limit=%d", limit), pageAll(t, "created_at.desc", limit), wantDesc)
		}
	})

	t.Run("an offset past the end is an empty page", func(t *testing.T) {
		same(t, "offset 7", list(t, scope+"&_order=created_at.asc&_limit=5&_offset=7"), []string{})
		same(t, "offset 6", list(t, scope+"&_order=created_at.asc&_limit=5&_offset=6"), []string{idNew})
	})
}
