package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/livant05/rrhh-go/internal/payroll"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// liquidationHistoryRow decodes the HTTP response shape of every
// liquidation_history endpoint (A3/A2 envelopes). Breakdown/Inputs stay
// json.RawMessage so each test can decode them into whatever shape it needs
// (a generic map for the round-trip walk, a typed struct for the inputs
// assertions).
type liquidationHistoryRow struct {
	ID           string          `json:"id"`
	CompanyID    string          `json:"company_id"`
	EmployeeID   string          `json:"employee_id"`
	EmployeeName string          `json:"employee_name"`
	Reason       string          `json:"reason"`
	ExitDate     string          `json:"exit_date"`
	TotalAmount  float64         `json:"total_amount"`
	Notes        string          `json:"notes"`
	StartDate    string          `json:"start_date"`
	Breakdown    json.RawMessage `json:"breakdown"`
	Inputs       json.RawMessage `json:"inputs"`
	CalcVersion  string          `json:"calc_version"`
}

// liquidationInputsDecoded mirrors liquidation_history.go's
// liquidationInputsPayload (unexported, so tests decode into their own
// equivalent shape rather than reaching across the package boundary).
type liquidationInputsDecoded struct {
	Salary              json.Number `json:"salary"`
	SalPend             json.Number `json:"salPend"`
	Otros               json.Number `json:"otros"`
	VacDays             json.Number `json:"vacDays"`
	DecMonths           json.Number `json:"decMonths"`
	AcumVac             json.Number `json:"acumVac"`
	AcumDec             json.Number `json:"acumDec"`
	AcumPrima           json.Number `json:"acumPrima"`
	Acum6m              json.Number `json:"acum6m"`
	Sal30Raw            json.Number `json:"sal30Raw"`
	DeductionIDs        []string    `json:"deductionIds"`
	DeductionQuotaTotal json.Number `json:"deductionQuotaTotal"`
	Branches            struct {
		VacProp bool `json:"vacProp"`
		DecProp bool `json:"decProp"`
		Prima   bool `json:"prima"`
		Indem6m bool `json:"indem6m"`
		Sal30   bool `json:"sal30"`
	} `json:"branches"`
}

// TestLiquidationHistory_CrossTenantLeak mirrors
// TestEmployeePayRecords_CrossTenantLeak (design P5.3/Q8). 6, not 7,
// subtests: this resource has no PATCH (design R7 -- immutable historical
// record), so there is no "patch other tenant row" subtest to mirror.
func TestLiquidationHistory_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LiqLeakA")
	b := testutil.Company(t, pool, "LiqLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empA := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"), testutil.EmployeeStartDate("2019-01-01"))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeName("Luis", "Gomez"), testutil.EmployeeStartDate("2019-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", ta, map[string]any{
		"employee_id": empA.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[liquidationHistoryRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	recA := rows[0]
	if recA.EmployeeName != "Ana Diaz" {
		t.Fatalf("expected server-derived employee_name 'Ana Diaz', got %q", recA.EmployeeName)
	}

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history/"+recA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403 -- that would disclose existence), got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected error.code=not_found, got %q", code)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/liquidation_history/"+recA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}

		getRec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history/"+recA.ID, ta, nil)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected the row to survive the cross-tenant delete attempt, got %d", getRec.Code)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		createRec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tb, map[string]any{
			"employee_id": empB.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
		})
		if createRec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createRec.Code)
		}

		aRows := testutil.DecodeRows[liquidationHistoryRow](t, testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", ta, nil))
		found := false
		for _, r := range aRows {
			if r.CompanyID != recA.CompanyID {
				t.Fatalf("company A's list leaked a row from another tenant: %+v", r)
			}
			if r.EmployeeName == "Ana Diaz" {
				found = true
			}
			if r.EmployeeName == "Luis Gomez" {
				t.Fatalf("company A's list leaked company B's liquidación")
			}
		}
		if !found {
			t.Fatalf("expected company A's list to contain Ana Diaz's liquidación")
		}
	})

	t.Run("company_id in body is rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", ta, map[string]any{
			"employee_id": empA.ID, "reason": "voluntaria", "exit_date": "2024-01-01", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 (DisallowUnknownFields), got %d", rec.Code)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		c := testutil.Company(t, pool, "LiqEmptyListCompany")
		tok := c.Token(t, signer)
		rec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", tok, nil)
		if rec.Body.String() == "null" || rec.Body.String() == "null\n" {
			t.Fatalf("expected [], got literal null")
		}
		rows := testutil.DecodeRows[liquidationHistoryRow](t, rec)
		if rows == nil {
			t.Fatalf("expected a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("expected zero rows for a fresh tenant, got %d", len(rows))
		}
	})
}

// TestLiquidationHistory_CrossTenantEmployeeIDRejected pins the spec's
// "Foreign employee_id rejected" scenario (A1 rule 6 class, same shape as
// TestEmployeePayRecords_CrossTenantEmployeeIDRejected): a foreign
// employee_id must be rejected as 404, with no row created under EITHER
// tenant.
func TestLiquidationHistory_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "LiqXEmpA")
	b := testutil.Company(t, pool, "LiqXEmpB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	empB := testutil.Employee(t, pool, b, testutil.EmployeeStartDate("2020-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", ta, map[string]any{
		"employee_id": empB.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected error.code=not_found, got %q", code)
	}

	listA := testutil.DecodeRows[liquidationHistoryRow](t, testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", ta, nil))
	if len(listA) != 0 {
		t.Fatalf("expected no row created under company A, got %d", len(listA))
	}
	listB := testutil.DecodeRows[liquidationHistoryRow](t, testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", tb, nil))
	if len(listB) != 0 {
		t.Fatalf("expected no row created under company B either (foreign id, never written under any tenant), got %d", len(listB))
	}
}

// TestLiquidation_BreakdownRoundTrips ties the whole chain together: the
// slice 3a golden fixture "reason-indem_25-acumulados-present" -> the pure
// payroll.CalculateLiquidation function (slice 3c, already golden-tested
// via TestCalcLiqGolden) -> this slice's HTTP-persisted breakdown JSONB, all
// agreeing at the exact same FloatString precision golden_test.go itself
// asserts at (design R2: money/counts at FloatString(2), isrRate/
// recargoPct at FloatString(6), via exact string equality, never an
// epsilon). Asserts all 35 calculated breakdown keys plus totalMonths (36
// total) are present and correct.
func TestLiquidation_BreakdownRoundTrips(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqBreakdownRT")
	tok := tenant.Token(t, signer)

	emp := testutil.Employee(t, pool, tenant,
		testutil.EmployeeName("Ana", "Gómez"),
		testutil.EmployeeSalary(1500),
		testutil.EmployeeStartDate("2018-03-10"),
	)

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID,
		"reason":      "indem_25",
		"exit_date":   "2024-03-10",
		"sal_pend":    1500,
		"otros":       50,
		"vac_days":    0,
		"dec_months":  0,
		"acum_vac":    1200,
		"acum_dec":    1400,
		"acum_prima":  9000,
		"acum_6m":     7500,
		"sal30":       1550,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[liquidationHistoryRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected create to return a single-element array, got %d elements", len(rows))
	}
	row := rows[0]

	// The expected result comes straight from the pure function the golden
	// fixture already validates (3c), run against the EXACT same inputs
	// this request sent -- not a hand-copied literal -- so this assertion
	// genuinely chains golden fixture -> pure function -> persisted JSONB.
	startDate, err := time.Parse("2006-01-02", "2018-03-10")
	if err != nil {
		t.Fatalf("parsing start date: %v", err)
	}
	exitDate, err := time.Parse("2006-01-02", "2024-03-10")
	if err != nil {
		t.Fatalf("parsing exit date: %v", err)
	}
	want, err := payroll.CalculateLiquidation(payroll.LiquidationInput{
		Salary:    payroll.FromInt(1500),
		StartDate: startDate,
		ExitDate:  exitDate,
		Reason:    payroll.ReasonIndem25,
		SalPend:   payroll.FromInt(1500),
		Otros:     payroll.FromInt(50),
		VacDays:   payroll.Zero(),
		DecMonths: payroll.Zero(),
		AcumVac:   payroll.FromInt(1200),
		AcumDec:   payroll.FromInt(1400),
		AcumPrima: payroll.FromInt(9000),
		Acum6m:    payroll.FromInt(7500),
		Sal30Raw:  payroll.FromInt(1550),
	})
	if err != nil {
		t.Fatalf("building expected LiquidationResult: %v", err)
	}

	var breakdown map[string]json.Number
	if err := json.Unmarshal(row.Breakdown, &breakdown); err != nil {
		t.Fatalf("decoding breakdown: %v (raw=%s)", err, row.Breakdown)
	}
	if len(breakdown) != 36 {
		t.Fatalf("expected 36 breakdown keys (35 calculated fields + totalMonths), got %d: %v", len(breakdown), breakdown)
	}

	wantFields := map[string]payroll.Num{
		"years": want.Years, "floorYears": want.RoundedYears,
		"salario": want.Salario, "preaviso": want.Preaviso, "vacProp": want.VacProp,
		"decProp": want.DecProp, "parcial": want.Parcial,
		"primaMonths": want.PrimaMonths, "primaTotal": want.PrimaTotal,
		"primaMensual": want.PrimaMensual, "primaSemanal": want.PrimaSemanal,
		"antigSem": want.AntigSem, "primaDeduccion": want.PrimaDeduccion,
		"antigSemNeta": want.AntigSemNeta, "indem6mMensual": want.Indem6mMensual,
		"sal30": want.Sal30, "indemSemanalFav": want.IndemSemanalFav,
		"indemWeeks": want.IndemWeeks, "indemBase": want.IndemBase,
		"recargo": want.Recargo, "indemnizacion": want.Indemnizacion,
		"total": want.Total, "cssBase": want.CSSBase, "css91": want.CSS91,
		"se92": want.SE92, "isrAnual": want.ISRAnual, "isr93": want.ISR93,
		"art701Base": want.Art701Base, "art701Sujeta": want.Art701Sujeta,
		"isr94": want.ISR94, "totalLegal": want.TotalLegal, "totalDed": want.TotalDed,
		"netTotal": want.NetTotal, "isrRate": want.ISRRate, "recargoPct": want.RecargoPct,
	}
	ratioFields := map[string]bool{"isrRate": true, "recargoPct": true}

	for field, wantNum := range wantFields {
		gotLiteral, ok := breakdown[field]
		if !ok {
			t.Fatalf("breakdown missing key %q", field)
		}
		prec := 2
		if ratioFields[field] {
			prec = 6
		}
		gotNum, err := payroll.ParseDecimal(gotLiteral.String())
		if err != nil {
			t.Fatalf("breakdown[%q] = %q is not a decimal literal: %v", field, gotLiteral, err)
		}
		if got, wantStr := gotNum.FloatString(prec), wantNum.FloatString(prec); got != wantStr {
			t.Errorf("breakdown[%q] = %s, want %s", field, got, wantStr)
		}
	}

	if gotMonths, ok := breakdown["totalMonths"]; !ok || gotMonths.String() != fmt.Sprintf("%d", want.TotalMonths) {
		t.Errorf("breakdown[totalMonths] = %v, want %d", gotMonths, want.TotalMonths)
	}

	if gotAmt, wantAmt := fmt.Sprintf("%.2f", row.TotalAmount), want.NetTotal.FloatString(2); gotAmt != wantAmt {
		t.Errorf("total_amount = %s, want netTotal = %s (design R6: total_amount keeps meaning netTotal)", gotAmt, wantAmt)
	}

	var inputs liquidationInputsDecoded
	if err := json.Unmarshal(row.Inputs, &inputs); err != nil {
		t.Fatalf("decoding inputs: %v (raw=%s)", err, row.Inputs)
	}
	wantInputs := map[string]string{
		"salary": "1500.00", "salPend": "1500.00", "otros": "50.00",
		"vacDays": "0.00", "decMonths": "0.00",
		"acumVac": "1200.00", "acumDec": "1400.00", "acumPrima": "9000.00",
		"acum6m": "7500.00", "sal30Raw": "1550.00", "deductionQuotaTotal": "0.00",
	}
	gotInputs := map[string]string{
		"salary": inputs.Salary.String(), "salPend": inputs.SalPend.String(), "otros": inputs.Otros.String(),
		"vacDays": inputs.VacDays.String(), "decMonths": inputs.DecMonths.String(),
		"acumVac": inputs.AcumVac.String(), "acumDec": inputs.AcumDec.String(), "acumPrima": inputs.AcumPrima.String(),
		"acum6m": inputs.Acum6m.String(), "sal30Raw": inputs.Sal30Raw.String(), "deductionQuotaTotal": inputs.DeductionQuotaTotal.String(),
	}
	for field, want := range wantInputs {
		if got := gotInputs[field]; got != want {
			t.Errorf("inputs[%q] = %s, want %s", field, got, want)
		}
	}

	// All five acumulados inputs were > 0 in this fixture, so all five
	// independent branches must have fired (spec's "not a single shared
	// flag" requirement, hazard from payroll/liquidation.go).
	if !inputs.Branches.VacProp || !inputs.Branches.DecProp || !inputs.Branches.Prima || !inputs.Branches.Indem6m || !inputs.Branches.Sal30 {
		t.Errorf("expected all 5 branch flags true (every acumulados input was > 0), got %+v", inputs.Branches)
	}

	if row.CalcVersion != payroll.CalcVersion {
		t.Errorf("calc_version = %q, want %q", row.CalcVersion, payroll.CalcVersion)
	}
}

// TestLiquidation_BreakdownIsJSONObjectNotBase64 is the P2.4 base64 trap
// (first caught on roles.permissions), applied to the two new JSONB
// columns: a jsonb column MUST round-trip as a genuine JSON object through
// this API, never as a base64-encoded string.
func TestLiquidation_BreakdownIsJSONObjectNotBase64(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqNotBase64")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeStartDate("2020-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	row := testutil.DecodeRows[liquidationHistoryRow](t, rec)[0]

	for name, raw := range map[string]json.RawMessage{"breakdown": row.Breakdown, "inputs": row.Inputs} {
		trimmed := string(raw)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			t.Fatalf("%s: expected a JSON object (starts with '{'), got: %s", name, trimmed)
		}
		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Fatalf("%s: not a valid JSON object: %v (raw=%s)", name, err, raw)
		}
		if len(asMap) == 0 {
			t.Fatalf("%s: decoded to an empty object", name)
		}
	}
}

// TestLiquidation_TotalAmountEqualsNetTotal pins design R6's verified fact:
// total_amount keeps meaning netTotal (saveLiqHistory's own call signature,
// hr_admin_panel.html:3756), never repurposed to mean `total` (the
// pre-deduction figure).
func TestLiquidation_TotalAmountEqualsNetTotal(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqTotalAmount")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeSalary(800), testutil.EmployeeStartDate("2021-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "injustificada", "exit_date": "2024-01-01", "sal_pend": 800,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	row := testutil.DecodeRows[liquidationHistoryRow](t, rec)[0]

	var breakdown map[string]json.Number
	if err := json.Unmarshal(row.Breakdown, &breakdown); err != nil {
		t.Fatalf("decoding breakdown: %v", err)
	}
	netTotal, ok := breakdown["netTotal"]
	if !ok {
		t.Fatalf("breakdown missing netTotal")
	}
	netTotalNum, err := payroll.ParseDecimal(netTotal.String())
	if err != nil {
		t.Fatalf("parsing netTotal: %v", err)
	}
	totalField, ok := breakdown["total"]
	if !ok {
		t.Fatalf("breakdown missing total")
	}
	if netTotal.String() == totalField.String() {
		t.Fatalf("test construction error: netTotal and total must differ (indemnizacion/legal deductions nonzero) for this assertion to be meaningful")
	}

	if gotAmt, wantAmt := fmt.Sprintf("%.2f", row.TotalAmount), netTotalNum.FloatString(2); gotAmt != wantAmt {
		t.Errorf("total_amount = %s, want netTotal = %s", gotAmt, wantAmt)
	}
}

// TestLiquidation_UnknownReasonRejected pins payroll.ParseReason's
// allowlist behavior at the HTTP layer: a typo'd/unrecognized reason is
// 400 validation_failed, never a silent fall-through to "voluntaria"
// (the live JS's own unguarded behavior, Q6 precedent).
func TestLiquidation_UnknownReasonRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqUnknownReason")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeStartDate("2020-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "renuncia", "exit_date": "2024-01-01",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
}

// TestLiquidation_ExitBeforeStartRejected pins payroll.ErrExitBeforeStart
// at the HTTP layer (design R1d: this port deliberately rejects rather
// than clamping, since Num.RoundHalfUp only matches JS Math.round for
// non-negative values -- see payroll/liquidation.go).
func TestLiquidation_ExitBeforeStartRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqExitBeforeStart")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeStartDate("2024-06-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "voluntaria", "exit_date": "2023-01-01",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "validation_failed" {
		t.Fatalf("expected error.code=validation_failed, got %q", code)
	}
}

// TestLiquidationHistory_FilterByIDReturnsSingleElementArray (Q8) --
// every List endpoint's `id` filter must return a single-element array for
// a matching id, not a bare object.
func TestLiquidationHistory_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqFilterByID")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeStartDate("2020-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	created := testutil.DecodeRows[liquidationHistoryRow](t, rec)[0]

	listRec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history?id="+created.ID, tok, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listRec.Code)
	}
	rows := testutil.DecodeRows[liquidationHistoryRow](t, listRec)
	if len(rows) != 1 {
		t.Fatalf("expected exactly one row for id filter, got %d", len(rows))
	}
	if rows[0].ID != created.ID {
		t.Fatalf("expected the filtered row to be %s, got %s", created.ID, rows[0].ID)
	}
}

// TestLiquidation_InputsCapturesBranches reuses the slice 3a
// "only-acum-vac" golden fixture's exact input shape (salary=1000,
// start_date=2020-01-01, exit_date=2024-01-01, sal_pend=500, vac_days=10,
// dec_months=1, acum_vac=5000, everything else 0) to assert the persisted
// inputs.branches records EXACTLY one flag true -- the spec's
// "not a single shared flag" requirement, pinned at the HTTP layer.
func TestLiquidation_InputsCapturesBranches(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqOnlyAcumVac")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeSalary(1000), testutil.EmployeeStartDate("2020-01-01"))

	rec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "voluntaria", "exit_date": "2024-01-01",
		"sal_pend": 500, "otros": 0, "vac_days": 10, "dec_months": 1,
		"acum_vac": 5000, "acum_dec": 0, "acum_prima": 0, "acum_6m": 0, "sal30": 0,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	row := testutil.DecodeRows[liquidationHistoryRow](t, rec)[0]

	var inputs liquidationInputsDecoded
	if err := json.Unmarshal(row.Inputs, &inputs); err != nil {
		t.Fatalf("decoding inputs: %v (raw=%s)", err, row.Inputs)
	}

	if !inputs.Branches.VacProp {
		t.Errorf("expected branches.vacProp=true (acum_vac=5000 > 0)")
	}
	if inputs.Branches.DecProp || inputs.Branches.Prima || inputs.Branches.Indem6m || inputs.Branches.Sal30 {
		t.Errorf("expected exactly ONE branch flag true (vacProp), got %+v -- not a single shared flag", inputs.Branches)
	}
	if inputs.AcumVac.String() != "5000.00" {
		t.Errorf("inputs.acumVac = %s, want 5000.00", inputs.AcumVac)
	}
}

// TestLiquidationHistory_DeleteDoesNotCascadeToEmployeePayRecords confirms
// deleting a liquidation_history row only removes that row -- consistent
// with 3f's no-cascade decision (design R5) for payroll_history, even
// though liquidation_history has no FK relationship to employee_pay_records
// at all, so this is not a design choice here, it is structurally the only
// possible outcome. Pinned anyway, as the deliberate "default, not an
// implementation" contract every other table-pair in this phase states
// explicitly.
func TestLiquidationHistory_DeleteDoesNotCascadeToEmployeePayRecords(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqNoCascade")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeSalary(1000), testutil.EmployeeStartDate("2019-01-01"))

	payRec := testutil.Do(t, h, http.MethodPost, "/api/employee_pay_records", tok, map[string]any{
		"employee_id": emp.ID, "period_year": 2024, "period_month": 1,
	})
	if payRec.Code != http.StatusCreated {
		t.Fatalf("seed employee_pay_records: expected 201, got %d (body=%s)", payRec.Code, payRec.Body.String())
	}
	payRecID := testutil.DecodeRows[employeePayRecordRow](t, payRec)[0].ID

	liqRec := testutil.Do(t, h, http.MethodPost, "/api/liquidation_history", tok, map[string]any{
		"employee_id": emp.ID, "reason": "voluntaria", "exit_date": "2024-06-01",
	})
	if liqRec.Code != http.StatusCreated {
		t.Fatalf("create liquidation: expected 201, got %d (body=%s)", liqRec.Code, liqRec.Body.String())
	}
	liqID := testutil.DecodeRows[liquidationHistoryRow](t, liqRec)[0].ID

	delRec := testutil.Do(t, h, http.MethodDelete, "/api/liquidation_history/"+liqID, tok, nil)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", delRec.Code)
	}

	getLiq := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history/"+liqID, tok, nil)
	if getLiq.Code != http.StatusNotFound {
		t.Fatalf("expected the liquidation row to be gone, got %d", getLiq.Code)
	}

	getPayRec := testutil.Do(t, h, http.MethodGet, "/api/employee_pay_records/"+payRecID, tok, nil)
	if getPayRec.Code != http.StatusOK {
		t.Fatalf("expected the employee_pay_records row to survive the liquidation delete unchanged, got %d", getPayRec.Code)
	}
}

// TestCalculateLiquidation_PreviewWritesNothing (task 7.3's preview
// endpoint) confirms GET /api/liquidation_history/calculate returns the
// breakdown directly (a bare object) and persists no row.
func TestCalculateLiquidation_PreviewWritesNothing(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	tenant := testutil.Company(t, pool, "LiqPreview")
	tok := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeSalary(1000), testutil.EmployeeStartDate("2020-01-01"))

	url := fmt.Sprintf("/api/liquidation_history/calculate?employee_id=%s&reason=voluntaria&exit_date=2024-01-01&sal_pend=500", emp.ID)
	rec := testutil.Do(t, h, http.MethodGet, url, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var breakdown map[string]json.Number
	if err := json.Unmarshal(rec.Body.Bytes(), &breakdown); err != nil {
		t.Fatalf("decoding preview response: %v (body=%s)", err, rec.Body.String())
	}
	if _, ok := breakdown["netTotal"]; !ok {
		t.Fatalf("expected the preview response to carry a netTotal field, got %v", breakdown)
	}

	listRec := testutil.Do(t, h, http.MethodGet, "/api/liquidation_history", tok, nil)
	rows := testutil.DecodeRows[liquidationHistoryRow](t, listRec)
	if len(rows) != 0 {
		t.Fatalf("expected the preview to write nothing, found %d persisted rows", len(rows))
	}
}
