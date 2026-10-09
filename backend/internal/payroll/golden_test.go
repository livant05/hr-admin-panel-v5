package payroll

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// prcalcFixture mirrors the JSON shape generate.mjs writes under
// testdata/prcalc/*.json (slice 3a): {"name","note","js_source_sha",
// "input":{...},"expected":{...},"expected_deviation":{...}}. Every numeric
// field decodes as json.Number (never float64), so the fixture's exact
// literal digits -- not a float64 round-trip -- feed ParseDecimal.
type prcalcFixture struct {
	Name              string                 `json:"name"`
	Note              string                 `json:"note"`
	JSSourceSHA       string                 `json:"js_source_sha"`
	Input             prcalcFixtureInput     `json:"input"`
	Expected          map[string]json.Number `json:"expected"`
	ExpectedDeviation map[string]json.Number `json:"expected_deviation"`
}

type prcalcFixtureInput struct {
	E struct {
		Salary json.Number `json:"salary"`
	} `json:"e"`
	Factor  json.Number           `json:"factor"`
	AttData *prcalcFixtureAttData `json:"attData"`
	DedData *prcalcFixtureDedData `json:"dedData"`
}

type prcalcFixtureAttData struct {
	HasData    bool        `json:"hasData"`
	AbsentDays int         `json:"absentDays"`
	OtAmount   json.Number `json:"otAmount"`
}

type prcalcFixtureDedData struct {
	QuotaTotal json.Number `json:"quotaTotal"`
}

func (fx prcalcFixture) toInput() (Input, error) {
	salary, err := ParseDecimal(fx.Input.E.Salary.String())
	if err != nil {
		return Input{}, err
	}
	factor, err := ParseDecimal(fx.Input.Factor.String())
	if err != nil {
		return Input{}, err
	}

	in := Input{Salary: salary, Factor: factor}

	if fx.Input.AttData != nil {
		otAmt, err := ParseDecimal(fx.Input.AttData.OtAmount.String())
		if err != nil {
			return Input{}, err
		}
		in.Attendance = &Attendance{
			HasData:        fx.Input.AttData.HasData,
			AbsentDays:     fx.Input.AttData.AbsentDays,
			OvertimeAmount: otAmt,
		}
	}

	if fx.Input.DedData != nil {
		quota, err := ParseDecimal(fx.Input.DedData.QuotaTotal.String())
		if err != nil {
			return Input{}, err
		}
		in.Deduction = &Deduction{QuotaTotal: quota}
	}

	return in, nil
}

// resultFields exposes Result's fields by their JSON fixture key, so the
// golden walk below can iterate the fixture's "expected" map generically
// instead of hand-wiring 14 field comparisons.
func resultFields(r Result) map[string]Num {
	return map[string]Num{
		"b":        r.B,
		"css":      r.CSS,
		"se":       r.SE,
		"isr":      r.ISR,
		"ded":      r.Ded,
		"dedQuota": r.DedQuota,
		"net":      r.Net,
		"pcss":     r.PCSS,
		"pse":      r.PSE,
		"dec":      r.Dec,
		"decCSSP":  r.DecCSSP,
		"tot":      r.Tot,
		"attDed":   r.AttDed,
		"otAmt":    r.OtAmt,
	}
}

// knownGoldenTieDeviations documents the one fixture+field pair, found while
// implementing this slice, where the live JS's own float64 computation lands
// fractionally BELOW an exact decimal .xx5 rounding tie -- a floating-point
// representation artifact in the JS source itself, not a bug in this port's
// exact-rational arithmetic -- so the JS's effective rounded value and this
// package's FloatString(2) of the mathematically exact value resolve the tie
// in opposite directions.
//
// Confirmed two ways: (1) `node tools/goldens/generate.mjs --warn-ties`
// (slice 3a's own tooling, run during this slice's implementation) flags
// exactly "prcalc/factor05-quincenal.decCSSP = 3.6249999999999996 is near a
// .xx5 rounding tie"; (2) running the vendored
// tools/goldens/legacy/prcalc-calcliq.js directly,
// prCalc({salary:1200}, 0.5, null, null).decCSSP === 3.6249999999999996,
// whose .toFixed(2) is "3.62" -- strictly below the exact mathematical value
// 50 * 0.0725 = 3.625 that this package's exact rational arithmetic computes
// (which FloatString(2) then rounds, correctly, half-away-from-zero, to
// "3.63").
//
// Design R2 explicitly anticipated this exact failure mode ("The one
// expected failure mode is a value landing within ~1e-15 of a .xx5 tie...
// flagged... for manual review rather than silent acceptance") and
// explicitly rejects hiding it behind an epsilon comparison. This map is
// that manual review, scoped to the one field it actually affects across
// all 10 prcalc fixtures (confirmed: no other field in any prcalc fixture is
// affected -- the fixture's own "tot", which sums decCSSP downstream, is NOT
// affected because the JS's floating-point addition of
// 600+73.5+9+50+3.6249999999999996 happens to re-round to exactly
// 736.125 in float64, matching this port's exact 736.125 exactly).
//
// This asserts the documented ACTUAL Go value explicitly (never a blind
// skip), so a future change to this field's computation still fails the
// test instead of silently passing. The testdata/calcliq fixtures (slice
// 3c's scope) show several more --warn-ties hits -- flagged here for 3c's
// implementer, not resolved in this slice.
var knownGoldenTieDeviations = map[string]map[string]string{
	"factor05-quincenal.json": {"decCSSP": "3.63"},
}

// TestPrCalcGolden walks every fixture under testdata/prcalc/*.json
// (generated in slice 3a from the live JS) and asserts Calculate's output
// against it field-by-field, comparing at FloatString(2) -- every prCalc
// field is money, none are ratios -- via exact string equality, never an
// epsilon (design R2's binding comparison rule). A field present in
// expected_deviation overrides the corresponding expected value: none of
// the 10 prcalc fixtures carry one (hazard 3's NaN deviation belongs to
// calcLiq's zero-salary-nan fixture, slice 3c), so this is exercised here
// only for forward compatibility with that fixture shape.
func TestPrCalcGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/prcalc/*.json")
	if err != nil {
		t.Fatalf("globbing testdata/prcalc: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found under testdata/prcalc -- slice 3a's golden-fixture toolchain must run first")
	}

	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}

			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			var fx prcalcFixture
			if err := dec.Decode(&fx); err != nil {
				t.Fatalf("decoding %s: %v", path, err)
			}

			in, err := fx.toInput()
			if err != nil {
				t.Fatalf("%s: building Input: %v", fx.Name, err)
			}

			got, err := Calculate(in)
			if err != nil {
				t.Fatalf("%s: Calculate returned unexpected error: %v", fx.Name, err)
			}

			actual := resultFields(got)

			effective := map[string]json.Number{}
			for k, v := range fx.Expected {
				effective[k] = v
			}
			for k, v := range fx.ExpectedDeviation {
				effective[k] = v
			}

			for field, wantLiteral := range effective {
				gotNum, ok := actual[field]
				if !ok {
					t.Fatalf("%s: fixture asserts unknown field %q", fx.Name, field)
				}

				if deviationValue, isKnownTie := knownGoldenTieDeviations[filepath.Base(path)][field]; isKnownTie {
					if got := gotNum.FloatString(2); got != deviationValue {
						t.Errorf("%s: field %q = %s, want documented tie-deviation value %s (see knownGoldenTieDeviations)", fx.Name, field, got, deviationValue)
					}
					continue
				}

				wantNum, err := ParseDecimal(wantLiteral.String())
				if err != nil {
					t.Fatalf("%s: fixture field %q is not a valid decimal literal: %v", fx.Name, field, err)
				}
				if got, want := gotNum.FloatString(2), wantNum.FloatString(2); got != want {
					t.Errorf("%s: field %q = %s, want %s", fx.Name, field, got, want)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────
// calcLiq golden walk (slice 3c)
// ─────────────────────────────────────────────────────────────────────────

// calcliqFixture mirrors the JSON shape generate.mjs writes under
// testdata/calcliq/*.json (slice 3a): {"name","note","js_source_sha",
// "input":{"employee":{...},"elements":{...},"dedChecks":[...]},
// "expected":{...},"expected_deviation":{...},"unexposed":[...]}. Fields
// listed in "unexposed" (always "years","totalMonths" -- printLiq never
// exposes either, and 3a's harness could not recover them from innerHTML)
// are skipped entirely: this port still computes LiquidationResult.Years
// internally (preaviso's bracket thresholds and PrimaMonths' acumulados
// branch both consume it), but there is no fixture value to assert it
// against.
type calcliqFixture struct {
	Name              string                     `json:"name"`
	Note              string                     `json:"note"`
	JSSourceSHA       string                     `json:"js_source_sha"`
	Input             calcliqFixtureInput        `json:"input"`
	Expected          map[string]json.RawMessage `json:"expected"`
	ExpectedDeviation map[string]json.RawMessage `json:"expected_deviation"`
	Unexposed         []string                   `json:"unexposed"`
}

type calcliqFixtureInput struct {
	Employee  calcliqFixtureEmployee   `json:"employee"`
	Elements  calcliqFixtureElements   `json:"elements"`
	DedChecks []calcliqFixtureDedCheck `json:"dedChecks"`
}

type calcliqFixtureEmployee struct {
	Salary    json.Number `json:"salary"`
	StartDate string      `json:"start_date"`
}

// calcliqFixtureElements mirrors the raw #liq-* form element values. Every
// numeric one is captured as a JSON STRING by generate.mjs (`gv(id)` returns
// the element's .value, which is always a string), so these decode as plain
// Go strings and feed ParseDecimal directly -- never json.Number here.
type calcliqFixtureElements struct {
	LiqDate      string `json:"liq-date"`
	LiqReason    string `json:"liq-reason"`
	LiqSalPend   string `json:"liq-sal-pend"`
	LiqOtros     string `json:"liq-otros"`
	LiqVac       string `json:"liq-vac"`
	LiqDec       string `json:"liq-dec"`
	LiqAcumVac   string `json:"liq-acum-vac"`
	LiqAcumDec   string `json:"liq-acum-dec"`
	LiqAcumPrima string `json:"liq-acum-prima"`
	LiqSal30     string `json:"liq-sal30"`
	LiqAcum6m    string `json:"liq-acum-6m"`
}

type calcliqFixtureDedCheck struct {
	ID      string      `json:"id"`
	Desc    string      `json:"desc"`
	Quota   json.Number `json:"quota"`
	Checked bool        `json:"checked"`
}

func (fx calcliqFixture) toInput(t *testing.T) LiquidationInput {
	t.Helper()

	salary, err := ParseDecimal(fx.Input.Employee.Salary.String())
	if err != nil {
		t.Fatalf("%s: parsing employee.salary: %v", fx.Name, err)
	}
	startDate, err := time.Parse("2006-01-02", fx.Input.Employee.StartDate)
	if err != nil {
		t.Fatalf("%s: parsing employee.start_date: %v", fx.Name, err)
	}
	exitDate, err := time.Parse("2006-01-02", fx.Input.Elements.LiqDate)
	if err != nil {
		t.Fatalf("%s: parsing elements.liq-date: %v", fx.Name, err)
	}
	reason, ok := ParseReason(fx.Input.Elements.LiqReason)
	if !ok {
		t.Fatalf("%s: elements.liq-reason %q is not a recognized Reason", fx.Name, fx.Input.Elements.LiqReason)
	}

	parseElement := func(field, raw string) Num {
		n, err := ParseDecimal(raw)
		if err != nil {
			t.Fatalf("%s: parsing elements.%s = %q: %v", fx.Name, field, raw, err)
		}
		return n
	}

	dedTotal := Zero()
	for _, d := range fx.Input.DedChecks {
		if !d.Checked {
			continue
		}
		quota, err := ParseDecimal(d.Quota.String())
		if err != nil {
			t.Fatalf("%s: parsing dedChecks quota %q: %v", fx.Name, d.Quota, err)
		}
		dedTotal = dedTotal.Add(quota)
	}

	return LiquidationInput{
		Salary:    salary,
		StartDate: startDate,
		ExitDate:  exitDate,
		Reason:    reason,

		SalPend:   parseElement("liq-sal-pend", fx.Input.Elements.LiqSalPend),
		Otros:     parseElement("liq-otros", fx.Input.Elements.LiqOtros),
		VacDays:   parseElement("liq-vac", fx.Input.Elements.LiqVac),
		DecMonths: parseElement("liq-dec", fx.Input.Elements.LiqDec),

		AcumVac:   parseElement("liq-acum-vac", fx.Input.Elements.LiqAcumVac),
		AcumDec:   parseElement("liq-acum-dec", fx.Input.Elements.LiqAcumDec),
		AcumPrima: parseElement("liq-acum-prima", fx.Input.Elements.LiqAcumPrima),
		Acum6m:    parseElement("liq-acum-6m", fx.Input.Elements.LiqAcum6m),
		Sal30Raw:  parseElement("liq-sal30", fx.Input.Elements.LiqSal30),

		DeductionQuotaTotal: dedTotal,
	}
}

// calcliqResultFields exposes LiquidationResult's fields by their JSON
// fixture key, mirroring resultFields' role for the prCalc golden walk.
func calcliqResultFields(r LiquidationResult) map[string]Num {
	return map[string]Num{
		"salario":         r.Salario,
		"preaviso":        r.Preaviso,
		"vacProp":         r.VacProp,
		"decProp":         r.DecProp,
		"parcial":         r.Parcial,
		"primaTotal":      r.PrimaTotal,
		"primaMonths":     r.PrimaMonths,
		"primaMensual":    r.PrimaMensual,
		"primaSemanal":    r.PrimaSemanal,
		"antigSem":        r.AntigSem,
		"primaDeduccion":  r.PrimaDeduccion,
		"antigSemNeta":    r.AntigSemNeta,
		"indem6mMensual":  r.Indem6mMensual,
		"sal30":           r.Sal30,
		"indemSemanalFav": r.IndemSemanalFav,
		"indemBase":       r.IndemBase,
		"recargo":         r.Recargo,
		"recargoPct":      r.RecargoPct,
		"indemnizacion":   r.Indemnizacion,
		"total":           r.Total,
		"css91":           r.CSS91,
		"se92":            r.SE92,
		"isr93":           r.ISR93,
		"isr94":           r.ISR94,
		"cssBase":         r.CSSBase,
		"art701Sujeta":    r.Art701Sujeta,
		"isrRate":         r.ISRRate,
		"floorYears":      r.RoundedYears, // hazard 2: JSON key stays "floorYears" for print/template compatibility
		"indemWeeks":      r.IndemWeeks,
		"totalLegal":      r.TotalLegal,
		"totalDed":        r.TotalDed,
		"netTotal":        r.NetTotal,
	}
}

// calcliqRatioFields are compared at FloatString(6) (design R2's "ratios"
// precision); every other field above is money or a bounded count and is
// compared at FloatString(2) (storage precision, NUMERIC(_,2)).
var calcliqRatioFields = map[string]bool{
	"isrRate":    true,
	"recargoPct": true,
}

// calcliqNonResultFields are fixture "expected" keys that do not map to any
// LiquidationResult Num field: "otros" simply echoes the #liq-otros input
// value back (no corresponding computed output field -- Otros stays an
// input in LiquidationInput, per design), and "checkedDeds" is a
// descriptive array ({desc,quota} per checked deduction), not a Num.
// "years"/"totalMonths" are handled separately via the fixture's own
// "unexposed" list, not here.
var calcliqNonResultFields = map[string]bool{
	"otros":       true,
	"checkedDeds": true,
}

// knownCalcLiqGoldenTieDeviations documents any calcliq fixture+field pair
// where the live JS's own float64 computation lands on the opposite side of
// an exact decimal .xx5 rounding tie from this port's exact-rational
// arithmetic (the same failure mode 3b found and resolved for
// prcalc/factor05-quincenal.decCSSP -- see knownGoldenTieDeviations above).
// `node tools/goldens/generate.mjs --warn-ties` (run during this slice)
// flagged 6 calcliq fixture+field pairs near a .xx5 boundary
// (only-acum-vac/prima/6m/sal30.css91; indem-weeks-under-10/over-10.
// {css91,se92}) -- every one was independently verified (by computing
// cssBase*0.0975 / cssBase*0.0125 by hand from each fixture's own cssBase
// value) to be a GENUINE exact tie with NO perturbation in the stored
// literal (e.g. "105.625", not "105.62499999999999" or
// "89.37500000000001" landing strictly above the tie) -- i.e. the raw JSON
// literal and this port's exact computation round through the IDENTICAL
// FloatString(2) half-away-from-zero path to the same two-decimal value.
// None of them actually diverge, so this map is empty: it exists (like
// 3b's) to make that verification an asserted, documented fact instead of
// an unexamined "the test happened to pass."
var knownCalcLiqGoldenTieDeviations = map[string]map[string]string{}

// TestCalcLiqGolden walks every fixture under testdata/calcliq/*.json
// (generated in slice 3a from the live JS) and asserts
// CalculateLiquidation's output against it field-by-field, at FloatString(2)
// for money/counts and FloatString(6) for ratios (isrRate, recargoPct),
// via exact string equality -- design R2's binding comparison rule, never
// an epsilon. Covers the full calcliq fixture inventory: all 10 reasons ×
// acumulados-present, the 5-reason acumulados-absent sample, the 5
// single-branch cases, both indem-weeks boundary cases,
// art701-sujeta-clamped-to-zero, zero-salary-nan (hazard 3, corrected --
// asserts isrRate=0, matching the real JS, not a NaN deviation), and
// one-month-service (hazard 4's primaMonths>=1 clamp).
func TestCalcLiqGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/calcliq/*.json")
	if err != nil {
		t.Fatalf("globbing testdata/calcliq: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found under testdata/calcliq -- slice 3a's golden-fixture toolchain must run first")
	}

	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}

			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			var fx calcliqFixture
			if err := dec.Decode(&fx); err != nil {
				t.Fatalf("decoding %s: %v", path, err)
			}

			in := fx.toInput(t)

			got, err := CalculateLiquidation(in)
			if err != nil {
				t.Fatalf("%s: CalculateLiquidation returned unexpected error: %v", fx.Name, err)
			}

			actual := calcliqResultFields(got)

			unexposed := map[string]bool{}
			for _, f := range fx.Unexposed {
				unexposed[f] = true
			}

			effective := map[string]json.RawMessage{}
			for k, v := range fx.Expected {
				effective[k] = v
			}
			for k, v := range fx.ExpectedDeviation {
				effective[k] = v
			}

			for field, raw := range effective {
				if unexposed[field] || calcliqNonResultFields[field] {
					continue
				}

				gotNum, ok := actual[field]
				if !ok {
					t.Fatalf("%s: fixture asserts unknown field %q", fx.Name, field)
				}

				prec := 2
				if calcliqRatioFields[field] {
					prec = 6
				}

				if deviationValue, isKnownTie := knownCalcLiqGoldenTieDeviations[filepath.Base(path)][field]; isKnownTie {
					if got := gotNum.FloatString(prec); got != deviationValue {
						t.Errorf("%s: field %q = %s, want documented tie-deviation value %s (see knownCalcLiqGoldenTieDeviations)", fx.Name, field, got, deviationValue)
					}
					continue
				}

				var wantLiteral json.Number
				if err := json.Unmarshal(raw, &wantLiteral); err != nil {
					t.Fatalf("%s: fixture field %q is not a valid decimal literal: %v", fx.Name, field, err)
				}
				wantNum, err := ParseDecimal(wantLiteral.String())
				if err != nil {
					t.Fatalf("%s: fixture field %q is not a valid decimal literal: %v", fx.Name, field, err)
				}
				if got, want := gotNum.FloatString(prec), wantNum.FloatString(prec); got != want {
					t.Errorf("%s: field %q = %s, want %s", fx.Name, field, got, want)
				}
			}
		})
	}
}
