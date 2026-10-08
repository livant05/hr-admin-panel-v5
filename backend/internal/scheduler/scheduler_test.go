package scheduler_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/scheduler"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// discardLogger builds a *slog.Logger that writes nowhere, matching
// testutil.Server's own internal logger (io.Discard) — scheduler tests
// construct a *scheduler.Scheduler directly rather than through the HTTP
// layer, so they build their own db.Queries/logger instead of reaching into
// testutil.Server's private API struct.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// companyUUID parses a testutil.Tenant's string CompanyID into pgtype.UUID.
// AccrueCompany's signature is non-nullable pgtype.UUID (design Q5b), so
// every caller -- including this test harness -- must convert explicitly;
// there is no handler-side helper to reach for here (stringToUUID is
// unexported in internal/handlers).
func companyUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse company id %q: %v", s, err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// TestScheduler_AccrueCompanyIsIdempotent pins design Q5b (the single most
// important property to preserve when porting accrue_vacation_days()):
// running AccrueCompany three times in a row against the same
// tenant/employee/year yields byte-identical earned_days (23 present days ->
// floor(23/11)=2, every run), and a pre-existing used_days value is never
// reset by a re-run -- the ON CONFLICT clause touches only earned_days.
func TestScheduler_AccrueCompanyIsIdempotent(t *testing.T) {
	pool := testutil.Pool(t)
	sched := scheduler.New(pool, db.New(pool), discardLogger(), true, 2)

	c := testutil.Company(t, pool, "SchedIdempotentCo")
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	testutil.AttendanceDays(t, pool, c.CompanyID, emp.ID, "present", 23)

	year := time.Now().Year()
	// Pre-seed an existing balance with a nonzero used_days so the test can
	// prove the accrual job never resets it.
	testutil.LeaveBalance(t, pool, c, emp.ID, emp.Name, year, 0, 5)

	cid := companyUUID(t, c.CompanyID)

	for i := 0; i < 3; i++ {
		rows, err := sched.AccrueCompany(context.Background(), cid)
		if err != nil {
			t.Fatalf("run %d: AccrueCompany: %v", i, err)
		}
		if len(rows) != 1 {
			t.Fatalf("run %d: expected exactly one leave_balances row for the one employee, got %d", i, len(rows))
		}
		row := rows[0]
		earned := numericToFloat(t, row.EarnedDays)
		used := numericToFloat(t, row.UsedDays)
		if earned != 2 {
			t.Fatalf("run %d: expected floor(23/11)=2, got %v", i, earned)
		}
		if used != 5 {
			t.Fatalf("run %d: expected the pre-existing used_days=5 to be preserved, got %v", i, used)
		}
	}
}

// TestScheduler_AccrueCompanyDoesNotTouchOtherTenant pins the design's named
// A1 exception boundary from the other side: AccrueCompany, scoped to one
// tenant, must never create or modify a leave_balances row for a different
// tenant, even when that other tenant has an employee with accruable
// attendance.
func TestScheduler_AccrueCompanyDoesNotTouchOtherTenant(t *testing.T) {
	pool := testutil.Pool(t)
	sched := scheduler.New(pool, db.New(pool), discardLogger(), true, 2)

	a := testutil.Company(t, pool, "SchedTenantIsoA")
	b := testutil.Company(t, pool, "SchedTenantIsoB")
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))
	testutil.AttendanceDays(t, pool, a.CompanyID, empA.ID, "present", 23)
	testutil.AttendanceDays(t, pool, b.CompanyID, empB.ID, "present", 23)

	rows, err := sched.AccrueCompany(context.Background(), companyUUID(t, a.CompanyID))
	if err != nil {
		t.Fatalf("AccrueCompany: %v", err)
	}
	for _, row := range rows {
		if row.CompanyID != companyUUID(t, b.CompanyID) {
			continue
		}
		t.Fatalf("AccrueCompany(A) returned a row for tenant B: %+v", row)
	}

	queries := db.New(pool)
	bRows, err := queries.ListLeaveBalances(context.Background(), db.ListLeaveBalancesParams{
		CompanyID: companyUUID(t, b.CompanyID),
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("list tenant B balances: %v", err)
	}
	if len(bRows) != 0 {
		t.Fatalf("expected AccrueCompany(A) to leave tenant B with zero leave_balances rows, got %d", len(bRows))
	}
}

// TestScheduler_AccrueAllTouchesEveryTenant is the sole AccrueAll test in
// this package (design P5.2 extension, this phase's own Q7 note:
// "AccrueAll crosses every tenant, so a test invoking it races other tests'
// fixtures. Rule: tests exercise AccrueCompany; AccrueAll gets exactly one
// non-parallel test"). It does not call t.Parallel(), matching every other
// test in this codebase (none of which call it), so it runs strictly
// sequentially relative to every fixture-creating test in the suite.
func TestScheduler_AccrueAllTouchesEveryTenant(t *testing.T) {
	pool := testutil.Pool(t)
	sched := scheduler.New(pool, db.New(pool), discardLogger(), true, 2)

	a := testutil.Company(t, pool, "SchedAllA")
	b := testutil.Company(t, pool, "SchedAllB")
	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"))
	testutil.AttendanceDays(t, pool, a.CompanyID, empA.ID, "present", 23)
	testutil.AttendanceDays(t, pool, b.CompanyID, empB.ID, "present", 11)

	n, err := sched.AccrueAll(context.Background())
	if err != nil {
		t.Fatalf("AccrueAll: %v", err)
	}
	if n < 2 {
		t.Fatalf("expected AccrueAll to upsert at least the 2 employees just seeded, got %d", n)
	}

	queries := db.New(pool)
	aRows, err := queries.ListLeaveBalances(context.Background(), db.ListLeaveBalancesParams{
		CompanyID: companyUUID(t, a.CompanyID), Limit: 100,
	})
	if err != nil {
		t.Fatalf("list tenant A balances: %v", err)
	}
	if len(aRows) != 1 || numericToFloat(t, aRows[0].EarnedDays) != 2 {
		t.Fatalf("expected tenant A to have one balance with earned_days=2 (floor(23/11)), got %+v", aRows)
	}

	bRows, err := queries.ListLeaveBalances(context.Background(), db.ListLeaveBalancesParams{
		CompanyID: companyUUID(t, b.CompanyID), Limit: 100,
	})
	if err != nil {
		t.Fatalf("list tenant B balances: %v", err)
	}
	if len(bRows) != 1 || numericToFloat(t, bRows[0].EarnedDays) != 1 {
		t.Fatalf("expected tenant B to have one balance with earned_days=1 (floor(11/11)), got %+v", bRows)
	}
}

// numericToFloat converts a pgtype.Numeric (which has no plain .Float64()
// ergonomic in this codebase's current usage) to a float64 for assertions,
// matching leave_balances_test.go's own established float64 handling of
// NUMERIC-backed columns (pgtype.Numeric implements json.Marshaler, unlike
// pgtype.Time -- see slice 2c1's key finding).
func numericToFloat(t *testing.T, n pgtype.Numeric) float64 {
	t.Helper()
	f, err := n.Float64Value()
	if err != nil {
		t.Fatalf("convert numeric to float64: %v", err)
	}
	return f.Float64
}
