package handlers_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// quincenaPreviewRow decodes the money fields the quincena tests assert on.
type quincenaPreviewRow struct {
	EmployeeID string  `json:"employee_id"`
	SalBase    float64 `json:"sal_base"`
	AttDed     float64 `json:"att_ded"`
	OtAmt      float64 `json:"ot_amt"`
	B          float64 `json:"b"`
	Ded        float64 `json:"ded"`
	DedQuota   float64 `json:"ded_quota"`
	Net        float64 `json:"net"`
}

func seedOvertime(t *testing.T, pool *pgxpool.Pool, companyID, employeeID string, year, month, day int, amount float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO overtime_logs (company_id, employee_id, date, hours, amount)
		 VALUES ($1, $2, $3, 1, $4)`,
		companyID, employeeID, fmt.Sprintf("%04d-%02d-%02d", year, month, day), amount)
	if err != nil {
		t.Fatalf("seed overtime: %v", err)
	}
}

func seedDeductionQuota(t *testing.T, pool *pgxpool.Pool, companyID, employeeID string, quota float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO deductions (company_id, employee_id, quota, status) VALUES ($1, $2, $3, 'active')`,
		companyID, employeeID, quota)
	if err != nil {
		t.Fatalf("seed deduction: %v", err)
	}
}

func previewPeriod(t *testing.T, h http.Handler, tok, empID, period string) quincenaPreviewRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period="+period, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview %s: expected 200, got %d (body=%s)", period, rec.Code, rec.Body.String())
	}
	for _, r := range testutil.DecodeRows[quincenaPreviewRow](t, rec) {
		if r.EmployeeID == empID {
			return r
		}
	}
	t.Fatalf("preview %s: employee %s missing", period, empID)
	return quincenaPreviewRow{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// TestCalculatePayroll_PeriodVocabulary: the UI sends quincenal1/quincenal2;
// the legacy 'quincenal' value is no longer accepted.
func TestCalculatePayroll_PeriodVocabulary(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaVocabCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	for _, p := range []string{"mensual", "quincenal1", "quincenal2"} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period="+p, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("period %s: expected 200, got %d (body=%s)", p, rec.Code, rec.Body.String())
		}
	}
	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=quincenal", tok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy 'quincenal': expected 400, got %d", rec.Code)
	}
	rec = testutil.Do(t, h, http.MethodPost, "/api/payroll_history", tok, map[string]any{"period": "quincenal", "month": 6, "year": 2025})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy 'quincenal' on POST: expected 400, got %d", rec.Code)
	}
}

// TestCalculatePayroll_QuincenaSplitsAbsencesByRange: absences are counted
// only inside the half's date range (1-15 / 16-end), mensual sees both.
func TestCalculatePayroll_QuincenaSplitsAbsencesByRange(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaAbsenceCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	wt28 := int32(28)
	for _, day := range []int{10, 15, 16, 30} { // 15 closes the 1st half, 16 opens the 2nd
		seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, day, &wt28, "present")
	}

	// salary/30 = 100 per absent day, never scaled by the factor.
	cases := map[string]float64{"quincenal1": 200, "quincenal2": 200, "mensual": 400}
	for period, want := range cases {
		if got := previewPeriod(t, h, tok, emp.ID, period).AttDed; !near(got, want) {
			t.Fatalf("%s: att_ded = %v, want %v", period, got, want)
		}
	}
}

// TestCalculatePayroll_QuincenaSplitsOvertimeByRange: overtime amounts land
// in the half that contains their date.
func TestCalculatePayroll_QuincenaSplitsOvertimeByRange(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaOvertimeCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	// Attendance presence in both halves: overtime only counts when the period has attendance data.
	wt1 := int32(1)
	seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, 2, &wt1, "present")
	seedAttendanceWorkType(t, pool, c.CompanyID, emp.ID, 2025, 6, 18, &wt1, "present")
	seedOvertime(t, pool, c.CompanyID, emp.ID, 2025, 6, 15, 100)
	seedOvertime(t, pool, c.CompanyID, emp.ID, 2025, 6, 16, 60)
	seedOvertime(t, pool, c.CompanyID, emp.ID, 2025, 6, 30, 40)

	cases := map[string]float64{"quincenal1": 100, "quincenal2": 100, "mensual": 200}
	for period, want := range cases {
		if got := previewPeriod(t, h, tok, emp.ID, period).OtAmt; !near(got, want) {
			t.Fatalf("%s: ot_amt = %v, want %v", period, got, want)
		}
	}
}

// TestCalculatePayroll_QuotaScaledByFactor: the user's decision -- the
// deduction quota is halved in each quincena and full in mensual, so the two
// halves sum to one full quota.
func TestCalculatePayroll_QuotaScaledByFactor(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaQuotaCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))
	seedDeductionQuota(t, pool, c.CompanyID, emp.ID, 80)

	q1 := previewPeriod(t, h, tok, emp.ID, "quincenal1")
	q2 := previewPeriod(t, h, tok, emp.ID, "quincenal2")
	mens := previewPeriod(t, h, tok, emp.ID, "mensual")

	if !near(mens.DedQuota, 80) || !near(q1.DedQuota, 40) || !near(q2.DedQuota, 40) {
		t.Fatalf("ded_quota mensual/q1/q2 = %v/%v/%v, want 80/40/40", mens.DedQuota, q1.DedQuota, q2.DedQuota)
	}
	if !near(q1.SalBase, 1500) || !near(mens.SalBase, 3000) {
		t.Fatalf("sal_base q1/mensual = %v/%v, want 1500/3000", q1.SalBase, mens.SalBase)
	}
	if want := q1.B - q1.Ded - q1.DedQuota; !near(q1.Net, want) {
		t.Fatalf("q1 net = %v, want b-ded-quota = %v", q1.Net, want)
	}
}

// ═══════════════════════ T2: persistence per period ═══════════════════════

type quincenaLedgerRow struct {
	ID              string  `json:"id"`
	Periodo         string  `json:"periodo"`
	Period          string  `json:"period"`
	PeriodYear      int32   `json:"period_year"`
	PeriodMonth     int32   `json:"period_month"`
	GrossSalary     float64 `json:"gross_salary"`
	OtherDeductions float64 `json:"other_deductions"`
	Origin          string  `json:"origin"`
}

func ledgerRows(t *testing.T, h http.Handler, tok, empID string) []quincenaLedgerRow {
	t.Helper()
	return testutil.DecodeRows[quincenaLedgerRow](t, testutil.Do(t, h, http.MethodGet,
		"/api/employee_pay_records?employee_id="+empID+"&period_year=2025&period_month=6", tok, nil))
}

func historyRows(t *testing.T, h http.Handler, tok string) []payrollHistoryRow {
	t.Helper()
	return testutil.DecodeRows[payrollHistoryRow](t, testutil.Do(t, h, http.MethodGet,
		"/api/payroll_history?year=2025&month=6", tok, nil))
}

func postRun(t *testing.T, h http.Handler, tok, period string) int {
	t.Helper()
	return testutil.Do(t, h, http.MethodPost, "/api/payroll_history", tok,
		map[string]any{"period": period, "month": 6, "year": 2025}).Code
}

// TestPayrollRun_TwoQuincenasKeepTwoRows: the 2nd quincena must not
// overwrite the 1st -- two history rows, two ledger rows per employee, and
// the halved quotas sum to one full quota.
func TestPayrollRun_TwoQuincenasKeepTwoRows(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaTwoRowsCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))
	seedDeductionQuota(t, pool, c.CompanyID, emp.ID, 80)

	if code := postRun(t, h, tok, "quincenal1"); code != http.StatusCreated {
		t.Fatalf("q1: got %d", code)
	}
	if code := postRun(t, h, tok, "quincenal2"); code != http.StatusCreated {
		t.Fatalf("q2: got %d", code)
	}

	if got := len(historyRows(t, h, tok)); got != 2 {
		t.Fatalf("expected 2 history rows, got %d", got)
	}
	rows := ledgerRows(t, h, tok, emp.ID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 ledger rows (one per quincena), got %d", len(rows))
	}
	labels := map[string]string{}
	var quota, gross float64
	for _, r := range rows {
		labels[r.Period] = r.Periodo
		quota += r.OtherDeductions
		gross += r.GrossSalary
	}
	if labels["quincenal1"] != "Junio 2025 - 1ra Quincena" || labels["quincenal2"] != "Junio 2025 - 2da Quincena" {
		t.Fatalf("unexpected period labels: %v", labels)
	}
	if !near(quota, 80) || !near(gross, 3000) {
		t.Fatalf("halves must sum to one month: quota=%v gross=%v", quota, gross)
	}
}

// TestPayrollRun_RerunReplacesHistory: re-running a period leaves exactly one
// history row for it, carrying the new totals.
func TestPayrollRun_RerunReplacesHistory(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaRerunCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	if code := postRun(t, h, tok, "quincenal1"); code != http.StatusCreated {
		t.Fatalf("first run: got %d", code)
	}
	before := historyRows(t, h, tok)
	if _, err := pool.Exec(context.Background(), `UPDATE employees SET salary = 4000 WHERE id = $1`, emp.ID); err != nil {
		t.Fatal(err)
	}
	if code := postRun(t, h, tok, "quincenal1"); code != http.StatusCreated {
		t.Fatalf("re-run: got %d", code)
	}

	after := historyRows(t, h, tok)
	if len(after) != 1 || len(before) != 1 {
		t.Fatalf("expected exactly 1 history row before/after, got %d/%d", len(before), len(after))
	}
	if !near(after[0].TotalBruto, 2000) {
		t.Fatalf("history must carry the re-run totals: total_bruto=%v, want 2000", after[0].TotalBruto)
	}
	if rows := ledgerRows(t, h, tok, emp.ID); len(rows) != 1 || !near(rows[0].GrossSalary, 2000) {
		t.Fatalf("ledger must hold one replaced row, got %+v", rows)
	}
}

// TestPayrollRun_ConcurrentDoubleSubmit defines the behaviour for a double
// click: the advisory lock serializes the two requests, both succeed (201,
// the second one replaces the first), and exactly one history row and one
// ledger row remain.
func TestPayrollRun_ConcurrentDoubleSubmit(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaConcurrentCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	const n = 6
	codes := make(chan int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes <- postRun(t, h, tok, "quincenal1")
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusCreated {
			t.Fatalf("every concurrent submit must succeed with 201, got %d", code)
		}
	}
	if got := len(historyRows(t, h, tok)); got != 1 {
		t.Fatalf("expected exactly 1 history row, got %d", got)
	}
	if got := len(ledgerRows(t, h, tok, emp.ID)); got != 1 {
		t.Fatalf("expected exactly 1 ledger row, got %d", got)
	}
}

// TestPayrollRun_MensualAndQuincenaAreExclusive: a month is paid either as
// one mensual run or as quincenas, never both (409 either way).
func TestPayrollRun_MensualAndQuincenaAreExclusive(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaExclusiveCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	if code := postRun(t, h, tok, "quincenal1"); code != http.StatusCreated {
		t.Fatalf("q1: got %d", code)
	}
	rec := testutil.Do(t, h, http.MethodPost, "/api/payroll_history", tok,
		map[string]any{"period": "mensual", "month": 6, "year": 2025})
	if rec.Code != http.StatusConflict {
		t.Fatalf("mensual after quincena: expected 409, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("expected error.code=conflict, got %q", code)
	}
	if got := len(historyRows(t, h, tok)); got != 1 {
		t.Fatalf("the rejected run must not write history, got %d rows", got)
	}

	c2 := testutil.Company(t, pool, "QuincenaExclusiveCo2")
	tok2 := c2.Token(t, signer)
	testutil.Employee(t, pool, c2, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))
	if code := postRun(t, h, tok2, "mensual"); code != http.StatusCreated {
		t.Fatalf("mensual: got %d", code)
	}
	if code := postRun(t, h, tok2, "quincenal2"); code != http.StatusConflict {
		t.Fatalf("quincena after mensual: expected 409, got %d", code)
	}
}

// TestPayrollRun_ExclusivityHoldsAfterHistoryDelete: deleting a history row
// leaves the ledger rows (settled decision #2), and those still block the
// opposite kind of run -- otherwise liquidation bases would double count.
func TestPayrollRun_ExclusivityHoldsAfterHistoryDelete(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaExclusiveLedgerCo")
	tok := c.Token(t, signer)
	testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	run := mustCommitPayrollRun(t, h, tok, 2025, 6, "mensual")
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/payroll_history/"+run.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec.Code)
	}
	if code := postRun(t, h, tok, "quincenal1"); code != http.StatusConflict {
		t.Fatalf("quincena over a surviving mensual ledger row: expected 409, got %d", code)
	}
}

// TestPayrollHistory_UniquePerPeriodBackstop: the DB itself refuses a second
// history row for the same (company, year, month, period).
func TestPayrollHistory_UniquePerPeriodBackstop(t *testing.T) {
	pool := testutil.Pool(t)
	c := testutil.Company(t, pool, "QuincenaUniqueCo")
	ins := func(period string) error {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO payroll_history (company_id, period, month, year) VALUES ($1, $2, 6, 2025)`, c.CompanyID, period)
		return err
	}
	if err := ins("quincenal1"); err != nil {
		t.Fatal(err)
	}
	if err := ins("quincenal1"); err == nil {
		t.Fatal("expected a unique violation for a duplicate period")
	}
	if err := ins("quincenal2"); err != nil {
		t.Fatalf("a different period must be allowed: %v", err)
	}
	if err := ins("bogus"); err == nil {
		t.Fatal("expected the period CHECK to reject an unknown value")
	}
}

// TestPayBases_AggregatesQuincenasIntoOneMonth: a month paid in two quincenas
// counts as ONE month in the liquidation windows, with the halves summed.
func TestPayBases_AggregatesQuincenasIntoOneMonth(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "QuincenaBasesCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))

	seed := func(year, month int, period, origin string, gross, earned float64) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO employee_pay_records (company_id, employee_id, period_year, period_month, period, gross_salary, total_earned, origin)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			c.CompanyID, emp.ID, year, month, period, gross, earned, origin)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seed(2025, 5, "mensual", "run", 3000, 3000)
	seed(2025, 6, "quincenal1", "run", 1500, 1400)
	seed(2025, 6, "quincenal2", "run", 1500, 1450)
	seed(2025, 6, "mensual", "manual", 9999, 9999) // a run month beats a manual row for the same month

	rec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/bases?employee_id="+emp.ID, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bases: got %d (%s)", rec.Code, rec.Body.String())
	}
	b := testutil.DecodeRow[employeePayBasesRow](t, rec)
	if b.Months != 2 {
		t.Fatalf("expected 2 months (May + June), got %d", b.Months)
	}
	if !near(b.AcumPrima, 3000+1400+1450) {
		t.Fatalf("acum_prima = %v, want %v", b.AcumPrima, 3000+1400+1450)
	}
	if !near(b.Sal30, 3000) {
		t.Fatalf("sal30 must be the summed gross of the latest month (3000), got %v", b.Sal30)
	}
}

// The payslip asks for one employee: ?employee_id= limits the preview to that
// employee (same figures as the full preview), so printing N slips does not
// compute the whole company N times.
func TestCalculatePayroll_EmployeeFilter(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CalcEmployeeFilterCo")
	other := testutil.Company(t, pool, "CalcEmployeeFilterOtherCo")
	tok := c.Token(t, signer)
	ana := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeSalary(3000))
	testutil.Employee(t, pool, c, testutil.EmployeeName("Luis", "Pinto"), testutil.EmployeeSalary(2000))
	foreign := testutil.Employee(t, pool, other, testutil.EmployeeName("Rosa", "Mena"))
	seedOvertime(t, pool, c.CompanyID, ana.ID, 2025, 6, 20, 30)

	full := previewPeriod(t, h, tok, ana.ID, "quincenal2")
	rec := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=quincenal2&employee_id="+ana.ID, tok, nil)
	rows := testutil.DecodeRows[quincenaPreviewRow](t, rec)
	if rec.Code != http.StatusOK || len(rows) != 1 || rows[0] != full {
		t.Fatalf("filtered preview: %d %+v, want exactly Ana's full-preview row %+v", rec.Code, rows, full)
	}
	if rows := testutil.DecodeRows[quincenaPreviewRow](t, testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual&employee_id="+foreign.ID, tok, nil)); len(rows) != 0 {
		t.Fatalf("another company's employee: %d rows, want 0", len(rows))
	}
	if all := testutil.DecodeRows[quincenaPreviewRow](t, testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual", tok, nil)); len(all) != 2 {
		t.Fatalf("without the filter: %d rows, want 2 (unchanged)", len(all))
	}
	bad := testutil.Do(t, h, http.MethodGet, "/api/payroll_history/calculate?year=2025&month=6&period=mensual&employee_id=x", tok, nil)
	if bad.Code != http.StatusBadRequest || errField(t, bad, "employee_id") == "" {
		t.Fatalf("malformed employee_id: got %d %s, want 400 with fields.employee_id", bad.Code, bad.Body.String())
	}
}
