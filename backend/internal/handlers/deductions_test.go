package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type deductionRow struct {
	ID             string  `json:"id"`
	CompanyID      string  `json:"company_id"`
	EmployeeID     *string `json:"employee_id"`
	EmployeeName   string  `json:"employee_name"`
	Type           string  `json:"type"`
	Description    string  `json:"description"`
	TotalAmount    float64 `json:"total_amount"`
	Quota          float64 `json:"quota"`
	Remaining      float64 `json:"remaining"`
	StartDate      *string `json:"start_date"`
	Status         string  `json:"status"`
	Cedula         string  `json:"cedula"`
	AcreedorNombre string  `json:"acreedor_nombre"`
	AcreedorCodigo string  `json:"acreedor_codigo"`
	TipoPago       string  `json:"tipo_pago"`
	CentroCosto    string  `json:"centro_costo"`
	Prioridad      *int32  `json:"prioridad"`
	EndDate        *string `json:"end_date"`
	Periodo        string  `json:"periodo"`
	NumeroPlanilla string  `json:"numero_planilla"`
}

// TestDeductions_CrossTenantLeak mirrors TestAttendanceLogs_CrossTenantLeak
// (design P5.3/Q8), seeding a real employee fixture so the A1 rule 6 join
// has a tenant-owned employee_id to resolve.
func TestDeductions_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "DedLeakA")
	b := testutil.Company(t, pool, "DedLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", ta, map[string]any{
		"employee_id": empA.ID, "quota": 50,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[deductionRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	dedA := rows[0]
	if dedA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", dedA.EmployeeName)
	}
	if dedA.Type != "prestamo" {
		t.Fatalf("expected the default type 'prestamo', got %q", dedA.Type)
	}
	if dedA.Status != "active" {
		t.Fatalf("expected the default status 'active', got %q", dedA.Status)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/deductions/"+dedA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 -- that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/deductions/"+dedA.ID, tb, map[string]any{
			"quota": 999,
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/deductions/"+dedA.ID, ta, nil)
		got := testutil.DecodeRow[deductionRow](t, getRec)
		if got.Quota != 50 {
			t.Fatalf("expected quota to remain 50, got %v", got.Quota)
		}
	})

	t.Run("delete other tenant row is 404 and row survives uncancelled", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/deductions/"+dedA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/deductions/"+dedA.ID, ta, nil)
		got := testutil.DecodeRow[deductionRow](t, getRec)
		if got.Status != "active" {
			t.Fatalf("expected deduction to survive the cross-tenant delete attempt still active, got %q", got.Status)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tb, map[string]any{
			"employee_id": empB.ID, "quota": 20,
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[deductionRow](t, testutil.Do(t, h, http.MethodGet, "/api/deductions", ta, nil))
		for _, r := range aRows {
			if r.CompanyID != dedA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
		}
		found := false
		for _, r := range aRows {
			if r.EmployeeName == "Ana Diaz" {
				found = true
			}
			if r.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's deduction")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's deduction")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", ta, map[string]any{
			"employee_id": empA.ID, "quota": 50, "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/deductions", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "DedEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/deductions", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[deductionRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestDeductions_CrossTenantEmployeeIDRejected pins design Q2 (A1 rule 6,
// "Mandatory new RED test class, all five tables"): a client-supplied
// employee_id belonging to another tenant must be rejected as 404 when
// employee_id IS provided -- the tenant check on employee_id IS the insert,
// so a foreign employee_id selects zero rows and no row is ever written
// under either tenant.
func TestDeductions_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "DedEmpIDRejectA")
	b := testutil.Company(t, pool, "DedEmpIDRejectB")
	ta := a.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Foreign", "Employee"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", ta, map[string]any{
		"employee_id": empB.ID, "quota": 50,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	aRows := testutil.DecodeRows[deductionRow](t, testutil.Do(t, h, http.MethodGet, "/api/deductions", ta, nil))
	if len(aRows) != 0 {
		t.Fatalf("expected no deduction to be created under the requesting tenant either, got %d", len(aRows))
	}
}

// TestDeductions_MissingQuotaRejected pins the spec's "Missing quota
// rejected" scenario: quota absent or 0 is rejected, matching saveDed's own
// client-side check. This project's established convention (every handler
// since Phase 1) maps validation_failed to 400 via writeFieldErr, not 422
// -- followed here even though the spec's prose literally says "422".
func TestDeductions_MissingQuotaRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "DedMissingQuotaCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	t.Run("quota absent", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{"employee_id": emp.ID})
		assertDeductionValidationFailed(t, rec, "quota")
	})

	t.Run("quota zero", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{"employee_id": emp.ID, "quota": 0})
		assertDeductionValidationFailed(t, rec, "quota")
	})

	rows := testutil.DecodeRows[deductionRow](t, testutil.Do(t, h, http.MethodGet, "/api/deductions", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no deduction to be created by a rejected request, got %d", len(rows))
	}
}

// TestDeductions_NullableEmployeeIDAccepted pins design Q1/Q2: migration
// 0006 drops deductions.employee_id's NOT NULL constraint specifically so
// importDedCSV can send employee_id:null for a name/cedula matching no
// employee. When employee_id is absent, the client-supplied employee_name/
// cedula are stored exactly as sent (the one exception to "derive, never
// trust" -- scoped to this path only).
func TestDeductions_NullableEmployeeIDAccepted(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "DedNullEmpCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{
		"employee_id": nil, "employee_name": "Unmatched Name", "cedula": "8-888-888", "quota": 30,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for a null employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[deductionRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected a single-element array, got %d", len(rows))
	}
	row := rows[0]
	if row.EmployeeID != nil {
		t.Fatalf("expected employee_id to persist as null, got %v", *row.EmployeeID)
	}
	if row.EmployeeName != "Unmatched Name" {
		t.Fatalf("expected the client-supplied employee_name to be stored as sent, got %q", row.EmployeeName)
	}
	if row.Cedula != "8-888-888" {
		t.Fatalf("expected the client-supplied cedula to be stored as sent, got %q", row.Cedula)
	}

	t.Run("omitting employee_id entirely behaves the same as explicit null", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{
			"employee_name": "Another Unmatched", "quota": 15,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		row := testutil.DecodeRows[deductionRow](t, rec)[0]
		if row.EmployeeID != nil {
			t.Fatalf("expected employee_id to be null when omitted, got %v", *row.EmployeeID)
		}
	})
}

// TestDeductions_DeleteSoftCancels pins design Q3: DELETE sets
// status='cancelled' rather than removing the row -- exportDedCSV already
// reads status==='cancelled' and saldaDed already writes 'paid', so the
// value must survive as a readable row, not disappear.
func TestDeductions_DeleteSoftCancels(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "DedSoftDeleteCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{
		"employee_id": emp.ID, "quota": 40,
	})
	ded := testutil.DecodeRows[deductionRow](t, createRec)[0]
	if ded.Status != "active" {
		t.Fatalf("expected the created deduction to default to status=active, got %q", ded.Status)
	}

	rec := testutil.Do(t, h, http.MethodDelete, "/api/deductions/"+ded.ID, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/deductions/"+ded.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the row to still exist (soft delete, not a hard delete), got %d", getRec.Code)
	}
	got := testutil.DecodeRow[deductionRow](t, getRec)
	if got.Status != "cancelled" {
		t.Fatalf("expected status to become 'cancelled', got %q", got.Status)
	}

	t.Run("deleting an already-cancelled row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/deductions/"+ded.ID, tok, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for a second delete on an already-cancelled row (DeactivateEmployee precedent), got %d", rec.Code)
		}
	})
}

// TestDeductions_UpdateReturnsUpdatedRow closes Phase 1's open verify
// warning (design Q8): PATCH must return 200, a single-element array, the
// changed field holding the new value, and the change must persist.
func TestDeductions_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "DedUpdateCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/deductions", tok, map[string]any{
		"employee_id": emp.ID, "quota": 60,
	})
	ded := testutil.DecodeRows[deductionRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/deductions/"+ded.ID, tok, map[string]any{
		"quota": 100, "status": "paid", "remaining": 0,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[deductionRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].Quota != 100 {
		t.Fatalf("expected the changed field (quota) to hold the new value, got %v", rows[0].Quota)
	}
	if rows[0].Status != "paid" {
		t.Fatalf("expected status to be updated to 'paid', got %q", rows[0].Status)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/deductions/"+ded.ID, tok, nil)
	got := testutil.DecodeRow[deductionRow](t, getRec)
	if got.Quota != 100 || got.Status != "paid" {
		t.Fatalf("expected the update to persist, got quota=%v status=%q", got.Quota, got.Status)
	}
}

// TestDeductions_FilterByIDReturnsSingleElementArray closes Phase 1's other
// open verify warning (design Q8): ?id=<uuid> must return exactly one
// element, and a foreign tenant's uuid must return an empty array, never a
// bare object or a 404 (phase1-design P7's Array.isArray contract).
func TestDeductions_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "DedFilterIDA")
	b := testutil.Company(t, pool, "DedFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/deductions", ta, map[string]any{
		"employee_id": empA.ID, "quota": 50,
	})
	ded := testutil.DecodeRows[deductionRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodGet, "/api/deductions?id="+ded.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[deductionRow](t, rec)
	if len(rows) != 1 || rows[0].ID != ded.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", ded.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/deductions?id="+ded.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[deductionRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}

// assertDeductionValidationFailed asserts the shared shape of a rejected
// required-field request: 400, error.code=validation_failed, and a
// fields.<name> detail for every field expected to be flagged.
func assertDeductionValidationFailed(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
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
