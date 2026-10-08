package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type overtimeLogRow struct {
	ID           string  `json:"id"`
	CompanyID    string  `json:"company_id"`
	EmployeeID   string  `json:"employee_id"`
	EmployeeName string  `json:"employee_name"`
	Date         string  `json:"date"`
	Hours        float64 `json:"hours"`
	Type         string  `json:"type"`
	HourlyRate   float64 `json:"hourly_rate"`
	Amount       float64 `json:"amount"`
	Notes        string  `json:"notes"`
}

// TestOvertimeLogs_CrossTenantLeak mirrors TestAttendanceLogs_CrossTenantLeak
// (design P5.3/Q8), seeding a real employee fixture so the A1 rule 6 join
// has a tenant-owned employee_id to resolve.
func TestOvertimeLogs_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "OTLeakA")
	b := testutil.Company(t, pool, "OTLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", ta, map[string]any{
		"employee_id": empA.ID, "date": "2026-06-01", "hours": 2, "type": "regular", "hourly_rate": 2.50,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[overtimeLogRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	logA := rows[0]
	if logA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", logA.EmployeeName)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs/"+logA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 -- that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/overtime_logs/"+logA.ID, tb, map[string]any{
			"hours": 1, "type": "regular", "hourly_rate": 2.50,
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs/"+logA.ID, ta, nil)
		got := testutil.DecodeRow[overtimeLogRow](t, getRec)
		if got.Hours != 2 {
			t.Fatalf("expected hours to remain 2, got %v", got.Hours)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/overtime_logs/"+logA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs/"+logA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected overtime log to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", tb, map[string]any{
			"employee_id": empB.ID, "date": "2026-06-01", "hours": 1, "type": "regular", "hourly_rate": 3.00,
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[overtimeLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", ta, nil))
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
				t.Fatalf("company A's list leaked company B's overtime log")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's log")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", ta, map[string]any{
			"employee_id": empA.ID, "date": "2026-06-02", "hours": 2, "type": "regular", "hourly_rate": 2.50,
			"company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "OTEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[overtimeLogRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestOvertimeLogs_CrossTenantEmployeeIDRejected pins design Q2 (A1 rule 6):
// a client-supplied employee_id belonging to another tenant must be
// rejected as 404 -- the tenant check on employee_id IS the insert, so a
// foreign employee_id selects zero rows and no row is ever written under
// either tenant.
func TestOvertimeLogs_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "OTEmpIDRejectA")
	b := testutil.Company(t, pool, "OTEmpIDRejectB")
	ta := a.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Foreign", "Employee"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", ta, map[string]any{
		"employee_id": empB.ID, "date": "2026-06-03", "hours": 2, "type": "regular", "hourly_rate": 2.50,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	aRows := testutil.DecodeRows[overtimeLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", ta, nil))
	if len(aRows) != 0 {
		t.Fatalf("expected no overtime log to be created under the requesting tenant either, got %d", len(aRows))
	}
}

// TestOvertimeLogs_UnknownTypeRejected pins design Q6: saveOT's own
// OT_RATES[type]||1.25 fallback silently underpays on a typo'd type -- the
// Go handler rejects an unknown type outright instead.
func TestOvertimeLogs_UnknownTypeRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "OTUnknownTypeCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-06-04", "hours": 2, "type": "doble", "hourly_rate": 2.50,
	})
	assertOvertimeValidationFailed(t, rec, "type")

	rows := testutil.DecodeRows[overtimeLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no overtime log to be created by a rejected request, got %d", len(rows))
	}
}

// TestOvertimeLogs_AmountIsServerComputed pins the spec's "Amount is
// server-computed, not trusted" scenario (design Q6): a client-submitted
// amount that mismatches hourly_rate*rate*hours is ignored, and the
// persisted amount is the server-recomputed value.
func TestOvertimeLogs_AmountIsServerComputed(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "OTAmountCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	// hours=2, hourly_rate=10, type=regular (rate 1.25) -> 2*10*1.25 = 25.00.
	// The client submits an obviously mismatched amount (999999).
	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-06-05", "hours": 2, "type": "regular",
		"hourly_rate": 10, "amount": 999999,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[overtimeLogRow](t, rec)
	if rows[0].Amount != 25 {
		t.Fatalf("expected server-recomputed amount 25.00, got %v (client-submitted amount must be ignored)", rows[0].Amount)
	}

	t.Run("holiday rate recomputes on update too", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/overtime_logs/"+rows[0].ID, tok, map[string]any{
			"hours": 3, "type": "holiday", "hourly_rate": 10, "amount": 1,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		// hours=3, hourly_rate=10, type=holiday (rate 2.50) -> 3*10*2.50 = 75.00.
		updated := testutil.DecodeRows[overtimeLogRow](t, rec)[0]
		if updated.Amount != 75 {
			t.Fatalf("expected recomputed amount 75.00 on update, got %v", updated.Amount)
		}
	})
}

// TestOvertimeLogs_HoursOverCapRejected pins the spec's "Hours over the
// daily cap rejected" scenario: Art. 36 num. 4's 3-hour-per-record cap is
// enforced server-side, not just client-side.
func TestOvertimeLogs_HoursOverCapRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "OTHoursCapCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-06-06", "hours": 4, "type": "regular", "hourly_rate": 2.50,
	})
	assertOvertimeValidationFailed(t, rec, "hours")

	rows := testutil.DecodeRows[overtimeLogRow](t, testutil.Do(t, h, http.MethodGet, "/api/overtime_logs", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no overtime log to be created by a rejected request, got %d", len(rows))
	}
}

// TestOvertimeLogs_UpdateReturnsUpdatedRow closes Phase 1's open verify
// warning (design Q8): PATCH must return 200, a single-element array, the
// changed field holding the new value, and the change must persist.
func TestOvertimeLogs_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "OTUpdateCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", tok, map[string]any{
		"employee_id": emp.ID, "date": "2026-06-07", "hours": 1, "type": "regular", "hourly_rate": 2.50,
	})
	log := testutil.DecodeRows[overtimeLogRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/overtime_logs/"+log.ID, tok, map[string]any{
		"hours": 2, "type": "night", "hourly_rate": 2.50, "notes": "Turno extendido",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[overtimeLogRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].Type != "night" {
		t.Fatalf("expected the changed field (type) to hold the new value, got %q", rows[0].Type)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs/"+log.ID, tok, nil)
	got := testutil.DecodeRow[overtimeLogRow](t, getRec)
	if got.Type != "night" {
		t.Fatalf("expected the update to persist, got type=%q", got.Type)
	}
	if got.Notes != "Turno extendido" {
		t.Fatalf("expected notes to persist, got %q", got.Notes)
	}
}

// TestOvertimeLogs_FilterByIDReturnsSingleElementArray closes Phase 1's
// other open verify warning (design Q8): ?id=<uuid> must return exactly one
// element, and a foreign tenant's uuid must return an empty array, never a
// bare object or a 404 (phase1-design P7's Array.isArray contract).
func TestOvertimeLogs_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "OTFilterIDA")
	b := testutil.Company(t, pool, "OTFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", ta, map[string]any{
		"employee_id": empA.ID, "date": "2026-06-08", "hours": 2, "type": "regular", "hourly_rate": 2.50,
	})
	log := testutil.DecodeRows[overtimeLogRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs?id="+log.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[overtimeLogRow](t, rec)
	if len(rows) != 1 || rows[0].ID != log.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", log.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/overtime_logs?id="+log.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[overtimeLogRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}

// assertOvertimeValidationFailed asserts the shared shape of a rejected
// required-field/validation request: 400, error.code=validation_failed, and
// a fields.<name> detail for every field expected to be flagged. This
// project's established convention (every handler since Phase 1) maps
// validation_failed to 400 via writeFieldErr, not 422 -- followed here even
// though the spec's prose literally says "422" for these scenarios.
func assertOvertimeValidationFailed(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
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
