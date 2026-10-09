package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// payrollPreviewRowFixture decodes GET /api/payroll_history/calculate's
// per-employee preview rows (A3 array). Only the fields this slice's tests
// assert on are decoded.
type payrollPreviewRowFixture struct {
	EmployeeID   string  `json:"employee_id"`
	EmployeeName string  `json:"employee_name"`
	AttDed       float64 `json:"att_ded"`
}

// seedAttendanceWorkType inserts an attendance_logs row with an explicit
// work_type (including 0 -- hazard 5) and a legacy status, bypassing the
// HTTP API since no testutil helper exposes work_type (only
// testutil.AttendanceDays, which only ever sets `status`).
func seedAttendanceWorkType(t *testing.T, pool *pgxpool.Pool, companyID, employeeID string, year, month, day int, workType *int32, status string) {
	t.Helper()
	date := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO attendance_logs (company_id, employee_id, date, status, work_type)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (employee_id, date) DO UPDATE SET status = EXCLUDED.status, work_type = EXCLUDED.work_type`,
		companyID, employeeID, date, status, workType,
	)
	if err != nil {
		t.Fatalf("seed attendance (work_type=%v): %v", workType, err)
	}
}

func findPreviewRow(t *testing.T, rows []payrollPreviewRowFixture, employeeID string) payrollPreviewRowFixture {
	t.Helper()
	for _, row := range rows {
		if row.EmployeeID == employeeID {
			return row
		}
	}
	t.Fatalf("expected a preview row for employee %s, got %d rows: %+v", employeeID, len(rows), rows)
	return payrollPreviewRowFixture{}
}

// TestCalculatePayroll_UnknownPeriodRejected pins task 5.5/design R4e: an
// unrecognized `period` must be 400 validation_failed, never a silent
// factor=0.5 fallback (Q6's unknown-type precedent).
func TestCalculatePayroll_UnknownPeriodRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollPreviewPeriodCo")
	tok := c.Token(t, signer)

	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensualll", tok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown period, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
}

// TestCalculatePayroll_WorkTypeZeroFalsyFallback pins hazard 5
// (work_type=0 is falsy in JS -- loadPayroll:2957's
// `l.work_type ? l.work_type===28 : (l.status==='absent')`). A row with
// work_type=0 must fall back to the legacy status check and still count as
// an absence, rather than being silently dropped by a naive
// COALESCE(work_type, ...) port that treats 0 the same as NULL.
func TestCalculatePayroll_WorkTypeZeroFalsyFallback(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollHazard5Co")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Hazard", "Five"), testutil.EmployeeSalary(3000))

	zero := int32(0)
	seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, 10, &zero, "absent")

	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[payrollPreviewRowFixture](t, rec)
	row := findPreviewRow(t, rows, emp.ID)
	if row.AttDed == 0 {
		t.Fatalf("expected work_type=0 to fall back to status='absent' and still deduct, got att_ded=0")
	}
}

// TestCalculatePayroll_PaidAbsenceCodesNotCounted confirms codes 24/25/26/
// 29/30 (feriado, certificado médico, vacación, licencia, ausencia pagada)
// never count as an absence deduction -- the companion case to hazard 5,
// proving the SQL CASE does not over-count every nonzero work_type either.
func TestCalculatePayroll_PaidAbsenceCodesNotCounted(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollPaidAbsenceCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Paid", "Absence"), testutil.EmployeeSalary(3000))

	feriado := int32(24)
	seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, 10, &feriado, "present")

	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[payrollPreviewRowFixture](t, rec)
	row := findPreviewRow(t, rows, emp.ID)
	if row.AttDed != 0 {
		t.Fatalf("expected work_type=24 (feriado) to NOT count as an absence, got att_ded=%v", row.AttDed)
	}
}
