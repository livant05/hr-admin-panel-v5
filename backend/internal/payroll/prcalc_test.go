package payroll

import "testing"

// TestCalculate exercises prCalc's port directly (hr_admin_panel.html:1870-
// 1890), by constructed Input/Result assertions rather than the fixture
// files (the fixture walk is golden_test.go, task 2.10). prCalc carries none
// of the five named porting hazards (all four calcLiq-side hazards belong to
// slice 3c; hazard 5 is a SQL-layer concern in slice 3e) -- these cases only
// pin prCalc's own arithmetic.
func TestCalculate(t *testing.T) {
	money := func(s string) Num { return MustParseDecimal(s) }

	cases := []struct {
		name string
		in   Input
		want Result
	}{
		{
			name: "factor1-no-attendance",
			in: Input{
				Salary:     money("1200"),
				Factor:     money("1"),
				Attendance: nil,
				Deduction:  nil,
			},
			want: Result{
				SalBase: money("1200"), AttDed: money("0"), OtAmt: money("0"),
				B: money("1200"), CSS: money("117"), SE: money("15"),
				ISR: money("57.5"), Ded: money("189.5"), DedQuota: money("0"),
				Net: money("1010.5"), PCSS: money("147"), PSE: money("18"),
				Dec: money("100"), DecCSSP: money("7.25"), Tot: money("1472.25"),
			},
		},
		{
			name: "factor1-attendance-work-type-28-counted",
			in: Input{
				Salary: money("1200"),
				Factor: money("1"),
				Attendance: &Attendance{
					HasData:        true,
					AbsentDays:     2,
					OvertimeAmount: money("45.5"),
				},
				Deduction: nil,
			},
			want: Result{
				SalBase: money("1200"), AttDed: money("80"), OtAmt: money("45.5"),
				B: money("1165.5"), CSS: money("113.63625"), SE: money("14.56875"),
				ISR: money("51.89375"), Ded: money("180.09875"), DedQuota: money("0"),
				Net: money("985.40125"), PCSS: money("142.77375"), PSE: money("17.4825"),
				Dec: money("97.125"), DecCSSP: money("7.0415625"), Tot: money("1429.9228125"),
			},
		},
		{
			// A present attendance record whose absences were already reduced
			// by the (SQL-layer, slice 3e) caller to zero because none of
			// them matched work_type==28 (e.g. codes 24/25/26/29/30). prCalc
			// itself never sees work_type -- it only sees the already-counted
			// AbsentDays, so this is arithmetically identical to "no
			// attendance" once AbsentDays is 0.
			name: "factor1-attendance-present-but-none-counted",
			in: Input{
				Salary: money("1000"),
				Factor: money("1"),
				Attendance: &Attendance{
					HasData:        true,
					AbsentDays:     0,
					OvertimeAmount: money("0"),
				},
				Deduction: nil,
			},
			want: Result{
				SalBase: money("1000"), AttDed: money("0"), OtAmt: money("0"),
				B: money("1000"), CSS: money("97.5"), SE: money("12.5"),
				ISR: money("25"), Ded: money("135"), DedQuota: money("0"),
				Net: money("865"), PCSS: money("122.5"), PSE: money("15"),
				Dec: money("83.333333"), DecCSSP: money("6.041667"), Tot: money("1226.875"),
			},
		},
		{
			name: "factor05-quincenal",
			in: Input{
				Salary:     money("1200"),
				Factor:     money("0.5"),
				Attendance: nil,
				Deduction:  nil,
			},
			want: Result{
				SalBase: money("600"), AttDed: money("0"), OtAmt: money("0"),
				B: money("600"), CSS: money("58.5"), SE: money("7.5"),
				ISR: money("28.75"), Ded: money("94.75"), DedQuota: money("0"),
				Net: money("505.25"), PCSS: money("73.5"), PSE: money("9"),
				Dec: money("50"), DecCSSP: money("3.625"), Tot: money("736.125"),
			},
		},
		{
			name: "active-deductions-subtracted-from-net",
			in: Input{
				Salary:     money("1500"),
				Factor:     money("1"),
				Attendance: nil,
				Deduction:  &Deduction{QuotaTotal: money("120.75")},
			},
			want: Result{
				SalBase: money("1500"), AttDed: money("0"), OtAmt: money("0"),
				B: money("1500"), CSS: money("146.25"), SE: money("18.75"),
				ISR: money("106.25"), Ded: money("271.25"), DedQuota: money("120.75"),
				Net: money("1108"), PCSS: money("183.75"), PSE: money("22.5"),
				Dec: money("125"), DecCSSP: money("9.0625"), Tot: money("1840.3125"),
			},
		},
		{
			// attDed (30 days * salary/30 = full salary) exceeds
			// salBase+otAmt, so b = max(0, ...) must clamp to exactly 0, not
			// go negative.
			name: "attded-exceeds-salbase-b-clamps-to-zero",
			in: Input{
				Salary: money("900"),
				Factor: money("1"),
				Attendance: &Attendance{
					HasData:        true,
					AbsentDays:     31,
					OvertimeAmount: money("0"),
				},
				Deduction: nil,
			},
			want: Result{
				SalBase: money("900"), AttDed: money("930"), OtAmt: money("0"),
				B: money("0"), CSS: money("0"), SE: money("0"),
				ISR: money("0"), Ded: money("0"), DedQuota: money("0"),
				Net: money("0"), PCSS: money("0"), PSE: money("0"),
				Dec: money("0"), DecCSSP: money("0"), Tot: money("0"),
			},
		},
		{
			// Defensive case: salary=0 must not panic or produce NaN. The
			// real divide-by-zero hazards (design hazards 1-4) all belong to
			// calcLiq (slice 3c), not prCalc.
			name: "zero-salary-does-not-panic",
			in: Input{
				Salary:     money("0"),
				Factor:     money("1"),
				Attendance: nil,
				Deduction:  nil,
			},
			want: Result{
				SalBase: money("0"), AttDed: money("0"), OtAmt: money("0"),
				B: money("0"), CSS: money("0"), SE: money("0"),
				ISR: money("0"), Ded: money("0"), DedQuota: money("0"),
				Net: money("0"), PCSS: money("0"), PSE: money("0"),
				Dec: money("0"), DecCSSP: money("0"), Tot: money("0"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Calculate(tc.in)
			if err != nil {
				t.Fatalf("Calculate returned unexpected error: %v", err)
			}

			check := func(field string, got, want Num) {
				if got.FloatString(6) != want.FloatString(6) {
					t.Errorf("%s = %s, want %s", field, got.FloatString(6), want.FloatString(6))
				}
			}

			check("SalBase", got.SalBase, tc.want.SalBase)
			check("AttDed", got.AttDed, tc.want.AttDed)
			check("OtAmt", got.OtAmt, tc.want.OtAmt)
			check("B", got.B, tc.want.B)
			check("CSS", got.CSS, tc.want.CSS)
			check("SE", got.SE, tc.want.SE)
			check("ISR", got.ISR, tc.want.ISR)
			check("Ded", got.Ded, tc.want.Ded)
			check("DedQuota", got.DedQuota, tc.want.DedQuota)
			check("Net", got.Net, tc.want.Net)
			check("PCSS", got.PCSS, tc.want.PCSS)
			check("PSE", got.PSE, tc.want.PSE)
			check("Dec", got.Dec, tc.want.Dec)
			check("DecCSSP", got.DecCSSP, tc.want.DecCSSP)
			check("Tot", got.Tot, tc.want.Tot)
		})
	}
}

func TestCalculate_ZeroFactorRejected(t *testing.T) {
	_, err := Calculate(Input{Salary: money1200, Factor: Zero()})
	if err == nil {
		t.Fatal("Calculate with factor=0 must return an error (it would divide by zero annualizing ISR), got nil")
	}
}

var money1200 = MustParseDecimal("1200")
