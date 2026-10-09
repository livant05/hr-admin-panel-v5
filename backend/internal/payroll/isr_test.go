package payroll

import "testing"

// TestAnnualISR_Boundaries pins the one 3-bracket progressive ISR function
// shared by prCalc's own ISR calculation (this slice) and, once ported in
// slice 3c, calcLiq's isrAnual and isr94 (R1e consolidation) -- all three JS
// call sites use this identical bracket table, reused here as a single Go
// function rather than duplicated three times.
func TestAnnualISR_Boundaries(t *testing.T) {
	cases := []struct {
		name   string
		annual string
		want   string
	}{
		// Exactly 11000 belongs to the <=11000 bracket (isrAnnual=0), not the
		// next one.
		{"boundary-11000-belongs-to-lower-bracket", "11000", "0"},
		// Exactly 50000 belongs to the 11000-50000 bracket:
		// (50000-11000)*0.15 = 5850.
		{"boundary-50000-belongs-to-middle-bracket", "50000", "5850"},
		// Above 50000 exercises the top bracket:
		// 5850 + (60000-50000)*0.25 = 8350.
		{"above-50000-uses-top-bracket", "60000", "8350"},
		// Zero must not panic and must not divide by anything.
		{"zero-annual", "0", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			annual := MustParseDecimal(tc.annual)
			got := AnnualISR(annual).FloatString(2)
			want := MustParseDecimal(tc.want).FloatString(2)
			if got != want {
				t.Errorf("AnnualISR(%s) = %s, want %s", tc.annual, got, want)
			}
		})
	}
}
