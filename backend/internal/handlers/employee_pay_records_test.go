package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type employeePayRecordRow struct {
	ID              string  `json:"id"`
	CompanyID       string  `json:"company_id"`
	EmployeeID      *string `json:"employee_id"`
	EmployeeName    string  `json:"employee_name"`
	Cedula          string  `json:"cedula"`
	NumeroPlanilla  string  `json:"numero_planilla"`
	CentroCosto     string  `json:"centro_costo"`
	Periodo         string  `json:"periodo"`
	PeriodYear      int32   `json:"period_year"`
	PeriodMonth     int32   `json:"period_month"`
	GrossSalary     float64 `json:"gross_salary"`
	OvertimeAmount  float64 `json:"overtime_amount"`
	Commissions     float64 `json:"commissions"`
	Bonuses         float64 `json:"bonuses"`
	VacationsPaid   float64 `json:"vacations_paid"`
	OtherIncome     float64 `json:"other_income"`
	TotalEarned     float64 `json:"total_earned"`
	CssEmployee     float64 `json:"css_employee"`
	SeEmployee      float64 `json:"se_employee"`
	Isr             float64 `json:"isr"`
	OtherDeductions float64 `json:"other_deductions"`
	NetSalary       float64 `json:"net_salary"`
	Notes           string  `json:"notes"`
	Origin          string  `json:"origin"`
}

// TestEmployeePayRecords_CrossTenantLeak mirrors TestDeductions_CrossTenantLeak
// (design P5.3/Q8), seeding a real employee fixture so the A1 rule 6 join
// has a tenant-owned employee_id to resolve.
func TestEmployeePayRecords_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EprLeakA")
	b := testutil.Company(t, pool, "EprLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", ta, map[string]any{
		"employee_id": empA.ID, "period_year": 2025, "period_month": 6,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[employeePayRecordRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	recA := rows[0]
	if recA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", recA.EmployeeName)
	}
	if recA.Origin != "manual" {
		t.Fatalf("expected origin to default to 'manual', got %q", recA.Origin)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+recA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 -- that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/employee_pay_records/"+recA.ID, tb, map[string]any{
			"period_year": 2025, "period_month": 6, "gross_salary": 9999,
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+recA.ID, ta, nil)
		got := testutil.DecodeRow[employeePayRecordRow](t, getRec)
		if got.GrossSalary != 0 {
			t.Fatalf("expected gross_salary to remain unchanged, got %v", got.GrossSalary)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/employee_pay_records/"+recA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+recA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected the row to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tb, map[string]any{
			"employee_id": empB.ID, "period_year": 2025, "period_month": 6,
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != recA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.EmployeeName == "Ana Diaz" {
				found = true
			}
			if r.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's pay record")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's pay record")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", ta, map[string]any{
			"employee_id": empA.ID, "period_year": 2025, "period_month": 6, "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("origin in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", ta, map[string]any{
			"employee_id": empA.ID, "period_year": 2025, "period_month": 6, "origin": "run",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (origin is server-owned, never client-supplied), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "EprEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[employeePayRecordRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestEmployeePayRecords_CrossTenantEmployeeIDRejected pins the spec's
// "Foreign employee_id rejected on employee_pay_records write" scenario (A1
// rule 6, Q2 class): a client-supplied employee_id belonging to another
// tenant must be rejected as 404, and no row may be created under either
// tenant.
func TestEmployeePayRecords_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EprEmpIDRejectA")
	b := testutil.Company(t, pool, "EprEmpIDRejectB")
	ta := a.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Foreign", "Employee"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", ta, map[string]any{
		"employee_id": empB.ID, "period_year": 2025, "period_month": 6,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	aRows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records", ta, nil))
	if len(aRows) != 0 {
		t.Fatalf("expected no pay record to be created under the requesting tenant either, got %d", len(aRows))
	}
}

// TestEmployeePayRecords_DualPath pins the spec's "Manual entry with
// employee_id succeeds" and "Manual entry without employee_id succeeds
// (dual-path)" scenarios: employee_id present derives employee_name/cedula
// from the employees row (ignoring client-sent values); employee_id absent
// stores employee_name/cedula exactly as sent (the CSV-import path).
func TestEmployeePayRecords_DualPath(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprDualPathCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	t.Run("employee_id present derives name and cedula, ignoring client values", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
			"employee_id": emp.ID, "employee_name": "Spoofed Name", "cedula": "0-000-000",
			"period_year": 2025, "period_month": 6,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		row := testutil.DecodeRows[employeePayRecordRow](t, rec)[0]
		if row.EmployeeID == nil || *row.EmployeeID != emp.ID {
			t.Fatalf("expected employee_id to persist as %q, got %v", emp.ID, row.EmployeeID)
		}
		if row.EmployeeName != "Ana Diaz" {
			t.Fatalf("expected the server-derived employee_name 'Ana Diaz', got %q", row.EmployeeName)
		}
		if row.Cedula == "0-000-000" {
			t.Fatalf("expected the client-supplied cedula to be ignored and derived instead, got %q", row.Cedula)
		}
	})

	t.Run("employee_id absent stores name and cedula as sent (CSV path)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
			"employee_id": nil, "employee_name": "Unmatched Name", "cedula": "8-888-888",
			"period_year": 2024, "period_month": 12,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201 for a null employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		row := testutil.DecodeRows[employeePayRecordRow](t, rec)[0]
		if row.EmployeeID != nil {
			t.Fatalf("expected employee_id to persist as null, got %v", *row.EmployeeID)
		}
		if row.EmployeeName != "Unmatched Name" {
			t.Fatalf("expected the client-supplied employee_name to be stored as sent, got %q", row.EmployeeName)
		}
		if row.Cedula != "8-888-888" {
			t.Fatalf("expected the client-supplied cedula to be stored as sent, got %q", row.Cedula)
		}
	})

	t.Run("omitting employee_id entirely behaves the same as explicit null", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
			"employee_name": "Another Unmatched", "period_year": 2024, "period_month": 11,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		row := testutil.DecodeRows[employeePayRecordRow](t, rec)[0]
		if row.EmployeeID != nil {
			t.Fatalf("expected employee_id to be null when omitted, got %v", *row.EmployeeID)
		}
	})
}

// TestEmployeePayRecords_CedulaAndExtraColumnsRoundTrip pins design B5.1:
// cedula, numero_planilla, centro_costo, and periodo must all round-trip
// exactly, since exportPayRecordsCSV reads them back for reconciliation.
func TestEmployeePayRecords_CedulaAndExtraColumnsRoundTrip(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprRoundTripCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": nil, "employee_name": "Unmatched", "cedula": "8-123-456",
		"numero_planilla": "PL-001", "centro_costo": "CC-01", "periodo": "Junio 2025",
		"period_year": 2025, "period_month": 6,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	row := testutil.DecodeRows[employeePayRecordRow](t, rec)[0]
	if row.Cedula != "8-123-456" {
		t.Fatalf("expected cedula to round-trip, got %q", row.Cedula)
	}
	if row.NumeroPlanilla != "PL-001" {
		t.Fatalf("expected numero_planilla to round-trip, got %q", row.NumeroPlanilla)
	}
	if row.CentroCosto != "CC-01" {
		t.Fatalf("expected centro_costo to round-trip, got %q", row.CentroCosto)
	}
	if row.Periodo != "Junio 2025" {
		t.Fatalf("expected periodo to round-trip, got %q", row.Periodo)
	}
}

// TestEmployeePayRecords_PeriodMonthZeroRejected pins task 4.3/R8: an
// out-of-range period_month must be a named validation_failed field error,
// not a raw 23514 check-constraint violation.
func TestEmployeePayRecords_PeriodMonthZeroRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprPeriodZeroCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": nil, "employee_name": "Someone", "period_year": 2025, "period_month": 0,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
	if msg := errField(t, rec, "period_month"); msg == "" {
		t.Fatalf("expected a named period_month field error, got none (body=%s)", rec.Body.String())
	}

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no row to be created by a rejected request, got %d", len(rows))
	}
}

// TestEmployeePayRecords_TotalEarnedRecomputedNetSalaryNot pins design R4f
// (task 4.3): total_earned is always recomputed server-side from the income
// columns; net_salary is stored exactly as submitted, even when the two are
// arithmetically inconsistent.
func TestEmployeePayRecords_TotalEarnedRecomputedNetSalaryNot(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprTotalEarnedCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": nil, "employee_name": "Someone", "period_year": 2025, "period_month": 6,
		"gross_salary": 1000, "overtime_amount": 100, "commissions": 0, "bonuses": 0,
		"vacations_paid": 0, "other_income": 0,
		"total_earned": 9999, // deliberately inconsistent -- must be ignored and recomputed
		"net_salary":   850,  // deliberately inconsistent -- must be stored as sent
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	row := testutil.DecodeRows[employeePayRecordRow](t, rec)[0]
	if row.TotalEarned != 1100 {
		t.Fatalf("expected total_earned to be recomputed to 1100 (gross+overtime), got %v", row.TotalEarned)
	}
	if row.NetSalary != 850 {
		t.Fatalf("expected net_salary to be stored exactly as submitted (850), got %v", row.NetSalary)
	}
}

// TestEmployeePayRecords_UpdateReturnsUpdatedRow closes Phase 1/2's open
// verify warning (design Q8): PATCH must return 200, a single-element
// array, the changed field holding the new value, and the change must
// persist.
func TestEmployeePayRecords_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprUpdateCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": nil, "employee_name": "Someone", "period_year": 2025, "period_month": 6,
		"gross_salary": 1000,
	})
	row := testutil.DecodeRows[employeePayRecordRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/employee_pay_records/"+row.ID, tok, map[string]any{
		"period_year": 2025, "period_month": 6, "gross_salary": 1500, "notes": "corrected",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[employeePayRecordRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].GrossSalary != 1500 {
		t.Fatalf("expected the changed field (gross_salary) to hold the new value, got %v", rows[0].GrossSalary)
	}
	if rows[0].Notes != "corrected" {
		t.Fatalf("expected notes to be updated, got %q", rows[0].Notes)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+row.ID, tok, nil)
	got := testutil.DecodeRow[employeePayRecordRow](t, getRec)
	if got.GrossSalary != 1500 {
		t.Fatalf("expected the update to persist, got gross_salary=%v", got.GrossSalary)
	}
}

// TestEmployeePayRecords_FilterByIDReturnsSingleElementArray closes the
// other half of design Q8: ?id=<uuid> must return exactly one element, and
// a foreign tenant's uuid must return an empty array, never a bare object
// or a 404.
func TestEmployeePayRecords_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EprFilterIDA")
	b := testutil.Company(t, pool, "EprFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", ta, map[string]any{
		"employee_id": nil, "employee_name": "Someone", "period_year": 2025, "period_month": 6,
	})
	row := testutil.DecodeRows[employeePayRecordRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records?id="+row.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[employeePayRecordRow](t, rec)
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", row.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records?id="+row.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[employeePayRecordRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}

// TestEmployeePayRecords_ListFilteredByEmployeeAndPeriod pins the spec's
// "List filtered by employee and period returns only matching rows"
// scenario -- new server-side filtering the current JS lacks.
func TestEmployeePayRecords_ListFilteredByEmployeeAndPeriod(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprListFilterCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	other := testutil.Employee(t, pool, c, testutil.EmployeeName("Luis", "Gomez"))

	mustCreate := func(employeeID string, year, month int) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
			"employee_id": employeeID, "period_year": year, "period_month": month,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	}
	mustCreate(emp.ID, 2024, 1)
	mustCreate(emp.ID, 2025, 6)
	mustCreate(other.ID, 2025, 6)

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?employee_id="+emp.ID+"&period_year=2025", tok, nil,
	))
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 matching row, got %d", len(rows))
	}
	if rows[0].EmployeeName != "Ana Diaz" || rows[0].PeriodYear != 2025 {
		t.Fatalf("expected Ana Diaz's 2025 row, got %+v", rows[0])
	}
}

// TestEmployeePayRecords_DeleteHardDeletes pins the spec's "List and delete
// with filters" requirement: DELETE hard-deletes, no soft-delete semantics
// apply to this table (unlike deductions).
func TestEmployeePayRecords_DeleteHardDeletes(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "EprDeleteCo")
	tok := c.Token(t, signer)

	createRec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": nil, "employee_name": "Someone", "period_year": 2025, "period_month": 6,
	})
	row := testutil.DecodeRows[employeePayRecordRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodDelete, "/api/employee_pay_records/"+row.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+row.ID, tok, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected the row to be gone (hard delete, not soft), got %d", getRec.Code)
	}

	t.Run("deleting an already-deleted row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/employee_pay_records/"+row.ID, tok, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for a second delete on an already-deleted row, got %d", rec.Code)
		}
	})
}

// employeePayBasesRow decodes GET /api/employee_pay_records/bases' bare
// object response (A3 single resource).
type employeePayBasesRow struct {
	AcumPrima float64 `json:"acum_prima"`
	Acum6m    float64 `json:"acum_6m"`
	Sal30     float64 `json:"sal30"`
	AcumVac   float64 `json:"acum_vac"`
	AcumDec   float64 `json:"acum_dec"`
	Months    int64   `json:"months"`
}

// seedPayRecordRaw inserts an employee_pay_records row directly via SQL,
// bypassing the HTTP API (which rejects a client-supplied `origin`, design
// R4a) -- needed to construct the manual+run tied-period fixture
// TestPayBases_DeduplicatesByPeriod requires.
func seedPayRecordRaw(t *testing.T, pool *pgxpool.Pool, companyID, employeeID string, year, month int, origin string, totalEarned, grossSalary float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO employee_pay_records (company_id, employee_id, period_year, period_month, gross_salary, total_earned, origin)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		companyID, employeeID, year, month, grossSalary, totalEarned, origin,
	)
	if err != nil {
		t.Fatalf("seed pay record (origin=%s): %v", origin, err)
	}
}

// TestPayBases_DeduplicatesByPeriod pins the user-approved settled decision
// (obs #841 / design R9): a manual row and a run row for the SAME
// employee+period must be de-duplicated to exactly one period, with the
// run-origin row's values winning the tie -- never summed, never
// double-counted.
func TestPayBases_DeduplicatesByPeriod(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayBasesDedupCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	seedPayRecordRaw(t, pool, c.CompanyID, emp.ID, 2025, 6, "manual", 1000, 900)
	seedPayRecordRaw(t, pool, c.CompanyID, emp.ID, 2025, 6, "run", 1200, 1100)

	rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/bases?employee_id="+emp.ID, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	bases := testutil.DecodeRow[employeePayBasesRow](t, rec)

	if bases.Months != 1 {
		t.Fatalf("expected the tied period to be counted exactly once, got months=%d", bases.Months)
	}
	if bases.AcumPrima != 1200 {
		t.Fatalf("expected acum_prima to use the run-origin row's total_earned (1200), got %v -- it must not sum both rows (2200) or prefer manual (1000)", bases.AcumPrima)
	}
	if bases.AcumDec != 1200 {
		t.Fatalf("expected acum_dec to use the run-origin row's total_earned (1200), got %v", bases.AcumDec)
	}
	if bases.Sal30 != 1100 {
		t.Fatalf("expected sal30 to use the run-origin row's gross_salary (1100), got %v", bases.Sal30)
	}
	if bases.AcumVac != 1100 {
		t.Fatalf("expected acum_vac to equal the run row's gross_salary alone (no other income columns set), got %v", bases.AcumVac)
	}
	if bases.Acum6m != 1100 {
		t.Fatalf("expected acum_6m to equal the run row's gross_salary alone, got %v", bases.Acum6m)
	}
}

// TestPayBases_EmptyHistoryReturnsZerosWithMonthsZero pins the spec's "bare
// object, never null, never 404" contract for an employee with no
// employee_pay_records history at all.
func TestPayBases_EmptyHistoryReturnsZerosWithMonthsZero(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayBasesEmptyCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("No", "History"))

	rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/bases?employee_id="+emp.ID, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (a bare zeroed object, never 404), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "null" || body == "null\n" {
		t.Fatalf("expected a zeroed object, got literal null")
	}
	bases := testutil.DecodeRow[employeePayBasesRow](t, rec)
	if bases.Months != 0 {
		t.Fatalf("expected months=0 for an employee with no history, got %d", bases.Months)
	}
	if bases.AcumPrima != 0 || bases.AcumDec != 0 || bases.AcumVac != 0 || bases.Acum6m != 0 || bases.Sal30 != 0 {
		t.Fatalf("expected all-zero sums for an employee with no history, got %+v", bases)
	}
}
