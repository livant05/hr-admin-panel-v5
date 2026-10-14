package handlers_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
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
