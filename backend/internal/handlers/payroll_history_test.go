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

// ═══════════════════════ Slice 3f: the committing payroll run ═══════════════════════
// payrollHistoryRow decodes payroll_history responses (create/list/get, A3
// array shape on write/list, bare object on get).
type payrollHistoryRow struct {
	ID            string  `json:"id"`
	CompanyID     string  `json:"company_id"`
	Period        string  `json:"period"`
	Month         int32   `json:"month"`
	Year          int32   `json:"year"`
	MonthName     string  `json:"month_name"`
	EmployeeCount int32   `json:"employee_count"`
	TotalBruto    float64 `json:"total_bruto"`
	TotalIsr      float64 `json:"total_isr"`
	TotalNeto     float64 `json:"total_neto"`
	TotalEmpresa  float64 `json:"total_empresa"`
}

// mustCommitPayrollRun POSTs the exact payload shape savePayrollRun sends
// (hr_admin_panel.html:3121-3127) -- including the client-computed totals
// that design R4e/task 6.4 requires the server to ignore and overwrite --
// and fails the test if the commit does not succeed.
func mustCommitPayrollRun(t *testing.T, h http.Handler, tok string, year, month int, period string) payrollHistoryRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/payroll_history", tok, map[string]any{
		"period": period, "month": month, "year": year,
		"total_bruto": 999999, "total_isr": 999999, "total_neto": 999999, "total_empresa": 999999,
		"month_name": "Bogus",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit payroll run: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[payrollHistoryRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected the commit to return a single-element array, got %d elements", len(rows))
	}
	return rows[0]
}

// TestPayrollRun_WritesOneLedgerRowPerEmployee pins task 6.1/design R4: a
// committed run writes exactly one payroll_history aggregate row PLUS one
// employee_pay_records row (origin='run') per active employee, in one
// transaction.
func TestPayrollRun_WritesOneLedgerRowPerEmployee(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollRunLedgerCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(1000))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Bob", "Lee"), testutil.EmployeeSalary(1200))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Cara", "Nunez"), testutil.EmployeeSalary(900))

	run := mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")
	if run.EmployeeCount != 3 {
		t.Fatalf("expected employee_count=3, got %d", run.EmployeeCount)
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/"+run.ID, tok, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected the committed run to be readable back, got %d (body=%s)", getRec.Code, getRec.Body.String())
	}

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?period_year=2025&period_month=6", tok, nil,
	))
	if len(rows) != 3 {
		t.Fatalf("expected exactly 3 ledger rows (one per active employee), got %d", len(rows))
	}
	for _, row := range rows {
		if row.Origin != "run" {
			t.Fatalf("expected origin=run on every ledger row written by the commit, got %q", row.Origin)
		}
	}
}

// TestPayrollRun_RerunUpsertsNotDuplicates is the single most important test
// in this slice: it directly validates the user-settled upsert-by-period
// decision (obs #841/design R4a). Committing the SAME period twice must
// UPDATE the existing origin='run' rows, never create duplicates.
func TestPayrollRun_RerunUpsertsNotDuplicates(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollRerunCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(1000))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Bob", "Lee"), testutil.EmployeeSalary(1200))

	mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")
	mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?period_year=2025&period_month=6", tok, nil,
	))
	if len(rows) != 2 {
		t.Fatalf("expected the second run to UPSERT (still 2 ledger rows), got %d -- the partial unique index must have deduplicated by period, not appended", len(rows))
	}
}

// TestPayrollRun_PreservesManualAndCSVRows proves the partial index's WHERE
// clause correctly scopes the conflict target: a pre-existing origin='manual'
// row for the SAME employee+period must survive a run untouched, and the run
// writes its OWN separate origin='run' row alongside it (design R4a).
func TestPayrollRun_PreservesManualAndCSVRows(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollPreservesManualCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(1000))

	seedPayRecordRaw(t, pool, c.CompanyID, emp.ID, 2025, 6, "manual", 500, 500)

	mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?employee_id="+emp.ID+"&period_year=2025&period_month=6", tok, nil,
	))
	if len(rows) != 2 {
		t.Fatalf("expected the manual row to survive AND a new run row to be written (2 rows total), got %d", len(rows))
	}
	var manual, run *employeePayRecordRow
	for i := range rows {
		switch rows[i].Origin {
		case "manual":
			manual = &rows[i]
		case "run":
			run = &rows[i]
		}
	}
	if manual == nil || run == nil {
		t.Fatalf("expected exactly one manual row and one run row, got %+v", rows)
	}
	if manual.TotalEarned != 500 || manual.GrossSalary != 500 {
		t.Fatalf("expected the manual row's values to survive UNCHANGED, got %+v", manual)
	}
}

// TestPayrollRun_TotalEarnedInvariant pins design R4f's deliberate,
// documented inequality: on a run row, total_earned (= b) is NOT the sum of
// gross_salary + overtime_amount -- it is that sum MINUS the attendance
// deduction. This must not be "fixed"; it is approved as a faithful port.
func TestPayrollRun_TotalEarnedInvariant(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollInvariantCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	wt28 := int32(28)
	seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, 10, &wt28, "present")

	preview := testutil.DecodeRows[payrollPreviewRowFixture](t, testutil.Do(
		t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual", tok, nil,
	))
	previewRow := findPreviewRow(t, preview, emp.ID)
	if previewRow.AttDed == 0 {
		t.Fatalf("expected a nonzero att_ded from the seeded absence, got 0")
	}

	mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")

	rows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?employee_id="+emp.ID+"&period_year=2025&period_month=6", tok, nil,
	))
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 ledger row, got %d", len(rows))
	}
	row := rows[0]
	diff := (row.GrossSalary + row.OvertimeAmount - row.TotalEarned) - previewRow.AttDed
	if diff > 0.005 || diff < -0.005 {
		t.Fatalf("expected total_earned = gross_salary - att_ded + overtime_amount (design R4f): gross_salary=%v overtime_amount=%v total_earned=%v att_ded=%v",
			row.GrossSalary, row.OvertimeAmount, row.TotalEarned, previewRow.AttDed)
	}
}

// TestPayrollHistory_DeleteLeavesPayRecords pins design R5 (settled decision
// #2): deleting a payroll_history aggregate row must NOT cascade to the N
// employee_pay_records rows it wrote -- no FK exists between the tables, and
// this is the default, not an implementation.
func TestPayrollHistory_DeleteLeavesPayRecords(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollDeleteLeavesCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(1000))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Bob", "Lee"), testutil.EmployeeSalary(1200))

	run := mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")

	before := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?period_year=2025&period_month=6", tok, nil,
	))
	if len(before) != 2 {
		t.Fatalf("expected 2 ledger rows before delete, got %d", len(before))
	}

	delRec := testutil.Do(t, h, http.MethodDelete, "/api/payroll_history/"+run.ID, tok, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", delRec.Code, delRec.Body.String())
	}

	after := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?period_year=2025&period_month=6", tok, nil,
	))
	if len(after) != len(before) {
		t.Fatalf("expected the N ledger rows to SURVIVE the aggregate delete (no cascade), before=%d after=%d", len(before), len(after))
	}
	beforeByID := make(map[string]employeePayRecordRow, len(before))
	for _, r := range before {
		beforeByID[r.ID] = r
	}
	for _, r := range after {
		b, ok := beforeByID[r.ID]
		if !ok {
			t.Fatalf("ledger row %s disappeared after the aggregate delete", r.ID)
		}
		if b.TotalEarned != r.TotalEarned || b.GrossSalary != r.GrossSalary {
			t.Fatalf("expected ledger row %s to be byte-identical before/after delete, before=%+v after=%+v", r.ID, b, r)
		}
	}

	getRec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/"+run.ID, tok, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected the deleted run to be gone, got %d", getRec.Code)
	}
}

// TestPayrollRun_IsAtomic forces a mid-run failure deterministically (no
// race): Carol's salary makes her gross_salary overflow
// employee_pay_records.gross_salary's NUMERIC(10,2) (max ~99,999,999.99), so
// her upsert fails after Alice's and Bob's (alphabetically earlier,
// ListPayrollRunInputs orders by first_name) have already succeeded inside
// the same transaction. Zero rows must exist in EITHER table afterward --
// not a half-written payroll.
func TestPayrollRun_IsAtomic(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	c := testutil.Company(t, pool, "PayrollAtomicCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Alice", "First"), testutil.EmployeeSalary(1000))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Bob", "Second"), testutil.EmployeeSalary(1200))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Carol", "Third"), testutil.EmployeeSalary(100000000))

	rec := testutil.Do(t, h, http.MethodPost, "/api/payroll_history", tok, map[string]any{
		"period": "mensual", "month": 6, "year": 2025,
	})
	if rec.Code == http.StatusCreated {
		t.Fatalf("expected the run to fail (Carol's salary overflows gross_salary's NUMERIC(10,2)), got 201 (body=%s)", rec.Body.String())
	}

	historyRows := testutil.DecodeRows[payrollHistoryRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/payroll_history?year=2025&month=6", tok, nil,
	))
	if len(historyRows) != 0 {
		t.Fatalf("expected ZERO payroll_history rows after a failed run, got %d", len(historyRows))
	}

	ledgerRows := testutil.DecodeRows[employeePayRecordRow](t, testutil.Do(
		t, h, http.MethodGet, "/api/employee_pay_records?period_year=2025&period_month=6", tok, nil,
	))
	if len(ledgerRows) != 0 {
		t.Fatalf("expected ZERO employee_pay_records rows after a failed run (atomicity), got %d", len(ledgerRows))
	}
}
