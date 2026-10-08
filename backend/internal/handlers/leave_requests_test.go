package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type leaveRequestRow struct {
	ID           string `json:"id"`
	CompanyID    string `json:"company_id"`
	EmployeeID   string `json:"employee_id"`
	EmployeeName string `json:"employee_name"`
	Type         string `json:"type"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Days         *int32 `json:"days"`
	Status       string `json:"status"`
	Notes        string `json:"notes"`
}

// TestLeaveRequests_CrossTenantLeak mirrors TestAttendanceLogs_CrossTenantLeak
// (design P5.3), seeding a real employee fixture so the A1 rule 6 join has a
// tenant-owned employee_id to resolve.
func TestLeaveRequests_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LeaveReqLeakA")
	b := testutil.Company(t, pool, "LeaveReqLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", ta, map[string]any{
		"employee_id": empA.ID, "start_date": "2026-02-01", "end_date": "2026-02-05",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[leaveRequestRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	reqA := rows[0]
	if reqA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", reqA.EmployeeName)
	}
	if reqA.Status != "pending" {
		t.Fatalf("expected a new leave request to default to status=pending, got %q", reqA.Status)
	}
	if reqA.Type != "vacaciones" {
		t.Fatalf("expected the default type 'vacaciones', got %q", reqA.Type)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+reqA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+reqA.ID, tb, map[string]any{
			"status": "approved",
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+reqA.ID, ta, nil)
		got := testutil.DecodeRow[leaveRequestRow](t, getRec)
		if got.Status != "pending" {
			t.Fatalf("expected status to remain pending, got %q", got.Status)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", tb, map[string]any{
			"employee_id": empB.ID, "start_date": "2026-02-01", "end_date": "2026-02-03",
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[leaveRequestRow](t, testutil.Do(t, h, http.MethodGet, "/api/leave_requests", ta, nil))
		for _, row := range aRows {
			if row.CompanyID != reqA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", row)
			}
		}
		found := false
		for _, row := range aRows {
			if row.EmployeeName == "Ana Diaz" {
				found = true
			}
			if row.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's leave request")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's request")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", ta, map[string]any{
			"employee_id": empA.ID, "start_date": "2026-02-10", "end_date": "2026-02-12", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "LeaveReqEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[leaveRequestRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestLeaveRequests_CrossTenantEmployeeIDRejected pins design Q2 (A1 rule
// 6): a client-supplied employee_id belonging to another tenant must be
// rejected as 404 — the tenant check on employee_id IS the insert, so a
// foreign employee_id selects zero rows and no row is ever written under
// either tenant.
func TestLeaveRequests_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LeaveReqEmpIDRejectA")
	b := testutil.Company(t, pool, "LeaveReqEmpIDRejectB")
	ta := a.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Foreign", "Employee"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", ta, map[string]any{
		"employee_id": empB.ID, "start_date": "2026-03-01", "end_date": "2026-03-02",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign employee_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	aRows := testutil.DecodeRows[leaveRequestRow](t, testutil.Do(t, h, http.MethodGet, "/api/leave_requests", ta, nil))
	if len(aRows) != 0 {
		t.Fatalf("expected no leave request to be created under the requesting tenant either, got %d", len(aRows))
	}
}

// TestLeaveRequests_RejectsMissingRequiredFields pins the spec's "Required
// fields rejected" scenario: employee_id, start_date, end_date are all
// required.
func TestLeaveRequests_RejectsMissingRequiredFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveReqRequiredFieldsCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	t.Run("missing start_date", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", tok, map[string]any{
			"employee_id": emp.ID, "end_date": "2026-04-05",
		})
		assertLeaveRequestValidationFailed(t, rec, "start_date")
	})

	t.Run("missing end_date", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", tok, map[string]any{
			"employee_id": emp.ID, "start_date": "2026-04-01",
		})
		assertLeaveRequestValidationFailed(t, rec, "end_date")
	})

	t.Run("missing employee_id", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", tok, map[string]any{
			"start_date": "2026-04-01", "end_date": "2026-04-05",
		})
		assertLeaveRequestValidationFailed(t, rec, "employee_id")
	})

	rows := testutil.DecodeRows[leaveRequestRow](t, testutil.Do(t, h, http.MethodGet, "/api/leave_requests", tok, nil))
	if len(rows) != 0 {
		t.Fatalf("expected no leave request to be created by a rejected request, got %d", len(rows))
	}
}

// TestLeaveRequests_ApprovalRequiresPermission pins design Q4: an
// "empleado" token gets 403 attempting to approve/reject, and the request
// stays pending — requirePermission fails closed on a missing roles row.
func TestLeaveRequests_ApprovalRequiresPermission(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveReqPermCo")
	admin := c.Token(t, signer)
	empleado := c.TokenAs(t, signer, "empleado")
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", admin, map[string]any{
		"employee_id": emp.ID, "start_date": "2026-05-01", "end_date": "2026-05-03",
	})
	leaveReq := testutil.DecodeRows[leaveRequestRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+leaveReq.ID, empleado, map[string]any{
		"status": "approved",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an empleado token with no vacations permission, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "forbidden" {
		t.Fatalf("expected error.code=forbidden, got %q", code)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+leaveReq.ID, admin, nil)
	got := testutil.DecodeRow[leaveRequestRow](t, getRec)
	if got.Status != "pending" {
		t.Fatalf("expected the request to remain pending after a denied approval attempt, got %q", got.Status)
	}
}

// TestLeaveRequests_NonPendingPatchConflicts pins design Q4's SQL-closed
// race: once a request is approved or rejected, a further PATCH (even one
// requesting a different target status) is 409, not a silent transition.
func TestLeaveRequests_NonPendingPatchConflicts(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveReqNonPendingCo")
	admin := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", admin, map[string]any{
		"employee_id": emp.ID, "start_date": "2026-06-01", "end_date": "2026-06-03",
	})
	leaveReq := testutil.DecodeRows[leaveRequestRow](t, createRec)[0]

	firstApprove := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+leaveReq.ID, admin, map[string]any{
		"status": "approved",
	})
	if firstApprove.Code != http.StatusOK {
		t.Fatalf("expected the first approval to succeed with 200, got %d (body=%s)", firstApprove.Code, firstApprove.Body.String())
	}

	secondPatch := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+leaveReq.ID, admin, map[string]any{
		"status": "rejected",
	})
	if secondPatch.Code != http.StatusConflict {
		t.Fatalf("expected a PATCH on an already-decided request to be 409, got %d (body=%s)", secondPatch.Code, secondPatch.Body.String())
	}
	if code := testutil.ErrCode(t, secondPatch); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+leaveReq.ID, admin, nil)
	got := testutil.DecodeRow[leaveRequestRow](t, getRec)
	if got.Status != "approved" {
		t.Fatalf("expected the request to remain approved (the first decision), got %q", got.Status)
	}
}

// TestLeaveRequests_ApprovalWithoutNotesPreservesExisting pins a correctness
// fix found while implementing UpdateLeaveRequest: approveLeave
// (hr_admin_panel.html:2918) sends only {status}, so an absent notes field
// must leave the existing value untouched rather than being written as the
// request's zero-value (NULL), mirroring updateRoleRequest's
// "leave the other field untouched" PATCH precedent.
func TestLeaveRequests_ApprovalWithoutNotesPreservesExisting(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveReqPreserveNotesCo")
	admin := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", admin, map[string]any{
		"employee_id": emp.ID, "start_date": "2026-10-01", "end_date": "2026-10-03", "notes": "Cita médica",
	})
	leaveReq := testutil.DecodeRows[leaveRequestRow](t, createRec)[0]
	if leaveReq.Notes != "Cita médica" {
		t.Fatalf("expected the created request to carry the submitted notes, got %q", leaveReq.Notes)
	}

	rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+leaveReq.ID, admin, map[string]any{
		"status": "approved",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[leaveRequestRow](t, rec)
	if rows[0].Notes != "Cita médica" {
		t.Fatalf("expected notes to be preserved when the PATCH omits them, got %q", rows[0].Notes)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+leaveReq.ID, admin, nil)
	got := testutil.DecodeRow[leaveRequestRow](t, getRec)
	if got.Notes != "Cita médica" {
		t.Fatalf("expected the preserved notes to persist, got %q", got.Notes)
	}
}

// TestLeaveRequests_UpdateReturnsUpdatedRow closes Phase 1's open verify
// warning (design Q8): PATCH must return 200, a single-element array, the
// changed field holding the new value, and the change must persist.
func TestLeaveRequests_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveReqUpdateCo")
	admin := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", admin, map[string]any{
		"employee_id": emp.ID, "start_date": "2026-07-01", "end_date": "2026-07-05",
	})
	leaveReq := testutil.DecodeRows[leaveRequestRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+leaveReq.ID, admin, map[string]any{
		"status": "rejected", "notes": "Cobertura insuficiente",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[leaveRequestRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].Status != "rejected" {
		t.Fatalf("expected the changed field (status) to hold the new value, got %q", rows[0].Status)
	}
	if rows[0].Notes != "Cobertura insuficiente" {
		t.Fatalf("expected notes to be set, got %q", rows[0].Notes)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests/"+leaveReq.ID, admin, nil)
	got := testutil.DecodeRow[leaveRequestRow](t, getRec)
	if got.Status != "rejected" {
		t.Fatalf("expected the update to persist, got status=%q", got.Status)
	}

	t.Run("start_date and end_date remain immutable", func(t *testing.T) {
		if got.StartDate != leaveReq.StartDate || got.EndDate != leaveReq.EndDate {
			t.Fatalf("expected start_date/end_date unchanged by a status PATCH, got %q/%q", got.StartDate, got.EndDate)
		}
	})

	t.Run("rejects unknown fields (start_date is not PATCH-able)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", admin, map[string]any{
			"employee_id": emp.ID, "start_date": "2026-07-10", "end_date": "2026-07-12",
		})
		second := testutil.DecodeRows[leaveRequestRow](t, rec)[0]

		patchRec := testutil.Do(t, h, http.MethodPatch, "/api/leave_requests/"+second.ID, admin, map[string]any{
			"status": "approved", "start_date": "2026-08-01",
		})
		if patchRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", patchRec.Code)
		}
	})
}

// TestLeaveRequests_FilterByIDReturnsSingleElementArray closes Phase 1's
// other open verify warning (design Q8): ?id=<uuid> must return exactly one
// element, and a foreign tenant's uuid must return an empty array, never a
// bare object or a 404 (phase1-design P7's Array.isArray contract).
func TestLeaveRequests_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LeaveReqFilterIDA")
	b := testutil.Company(t, pool, "LeaveReqFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))

	createRec := testutil.Do(t, h, http.MethodPost, "/api/leave_requests", ta, map[string]any{
		"employee_id": empA.ID, "start_date": "2026-09-01", "end_date": "2026-09-03",
	})
	leaveReq := testutil.DecodeRows[leaveRequestRow](t, createRec)[0]

	rec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests?id="+leaveReq.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[leaveRequestRow](t, rec)
	if len(rows) != 1 || rows[0].ID != leaveReq.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", leaveReq.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/leave_requests?id="+leaveReq.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[leaveRequestRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}

// assertLeaveRequestValidationFailed asserts the shared shape of a rejected
// required-field request: 400, error.code=validation_failed, and a
// fields.<name> detail for every field expected to be flagged.
func assertLeaveRequestValidationFailed(t *testing.T, rec *httptest.ResponseRecorder, fields ...string) {
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
