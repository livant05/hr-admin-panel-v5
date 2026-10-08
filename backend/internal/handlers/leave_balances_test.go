package handlers_test

import (
	"net/http"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// leaveBalanceRow's numeric fields decode as float64, not string:
// pgtype.Numeric (pgx v5.11.0) implements json.Marshaler and emits a bare
// JSON number (e.g. 2.0), unlike pgtype.Time's lack of any Marshaler (see
// attendance_logs.go's hand-rolled clock-time conversion for that contrast).
type leaveBalanceRow struct {
	ID            string   `json:"id"`
	CompanyID     string   `json:"company_id"`
	EmployeeID    string   `json:"employee_id"`
	EmployeeName  string   `json:"employee_name"`
	Year          int32    `json:"year"`
	EarnedDays    float64  `json:"earned_days"`
	UsedDays      float64  `json:"used_days"`
	RemainingDays *float64 `json:"remaining_days"`
}

// TestLeaveBalances_CrossTenantLeak mirrors TestAttendanceLogs_CrossTenantLeak
// (design P5.3/Q8), adapted for a resource with no POST: fixtures are seeded
// directly via testutil.LeaveBalance since leave_balances rows are owned by
// the accrual job, not a client-facing create endpoint.
func TestLeaveBalances_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LeaveBalLeakA")
	b := testutil.Company(t, pool, "LeaveBalLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))

	balA := testutil.LeaveBalance(t, pool, a, empA.ID, "Ana Diaz", 2026, 10, 2)
	testutil.LeaveBalance(t, pool, b, empB.ID, "Luis Gomez", 2026, 5, 1)

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances/"+balA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 — that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and leaves row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_balances/"+balA.ID, tb, map[string]any{
			"used_days": 9,
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances/"+balA.ID, ta, nil)
		got := testutil.DecodeRow[leaveBalanceRow](t, getRec)
		if got.UsedDays != 2 {
			t.Fatalf("expected used_days to remain 2, got %v", got.UsedDays)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		aRows := testutil.DecodeRows[leaveBalanceRow](t, testutil.Do(t, h, http.MethodGet, "/api/leave_balances", ta, nil))
		for _, row := range aRows {
			if row.CompanyID != balA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", row)
			}
		}
		found := false
		for _, row := range aRows {
			if row.EmployeeName == "Ana Diaz" {
				found = true
			}
			if row.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's leave balance")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's balance")
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "LeaveBalEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[leaveBalanceRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestLeaveBalances_UpdateReturnsUpdatedRow closes Phase 1's open verify
// warning (design Q8) for leave_balances: PATCH must return 200, a
// single-element array, the changed field (used_days) holding the new
// value, and the change must persist. It also pins design Q3: earned_days
// and remaining_days are not accepted by this PATCH at all.
func TestLeaveBalances_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "LeaveBalUpdateCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	bal := testutil.LeaveBalance(t, pool, c, emp.ID, "Ana Diaz", 2026, 8, 0)

	rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_balances/"+bal.ID, tok, map[string]any{
		"used_days": 3,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[leaveBalanceRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected PATCH to return a single-element array, got %d elements", len(rows))
	}
	if rows[0].UsedDays != 3 {
		t.Fatalf("expected the changed field (used_days) to hold the new value, got %v", rows[0].UsedDays)
	}
	if rows[0].EarnedDays != 8 {
		t.Fatalf("expected earned_days to remain untouched by this PATCH, got %v", rows[0].EarnedDays)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances/"+bal.ID, tok, nil)
	got := testutil.DecodeRow[leaveBalanceRow](t, getRec)
	if got.UsedDays != 3 {
		t.Fatalf("expected the update to persist, got used_days=%v", got.UsedDays)
	}

	t.Run("rejects unknown fields (earned_days is not PATCH-able)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/leave_balances/"+bal.ID, tok, map[string]any{
			"earned_days": 100,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})
}

// TestLeaveBalances_FilterByIDReturnsSingleElementArray closes Phase 1's
// other open verify warning (design Q8): ?id=<uuid> must return exactly one
// element, and a foreign tenant's uuid must return an empty array, never a
// bare object or a 404 (phase1-design P7's Array.isArray contract).
func TestLeaveBalances_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LeaveBalFilterIDA")
	b := testutil.Company(t, pool, "LeaveBalFilterIDB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	bal := testutil.LeaveBalance(t, pool, a, empA.ID, "Ana Diaz", 2026, 4, 0)

	rec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances?id="+bal.ID, ta, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	rows := testutil.DecodeRows[leaveBalanceRow](t, rec)
	if len(rows) != 1 || rows[0].ID != bal.ID {
		t.Fatalf("expected exactly one element matching id=%s, got %+v", bal.ID, rows)
	}

	foreignRec := testutil.Do(t, h, http.MethodGet, "/api/leave_balances?id="+bal.ID, tb, nil)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty array, never 404) for a foreign tenant's uuid, got %d", foreignRec.Code)
	}
	foreignRows := testutil.DecodeRows[leaveBalanceRow](t, foreignRec)
	if len(foreignRows) != 0 {
		t.Fatalf("expected an empty array for a foreign tenant's uuid, got %d rows", len(foreignRows))
	}
}
