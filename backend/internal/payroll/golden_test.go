package payroll

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
