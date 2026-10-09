package payroll

// AnnualISR implements the one progressive income-tax bracket formula shared
// by all three JS call sites that compute it: prCalc's own ISR (ported here,
// slice 3b), and calcLiq's isrAnual and isr94 (ported in slice 3c). All three
// use this identical bracket table (hr_admin_panel.html:1879, confirmed
// identical at the other two sites), so Go has one function instead of three
// copies that could silently diverge (R1e consolidation).
//
//	annual <= 11000            -> 0
//	11000  <  annual <= 50000  -> (annual-11000) * 0.15
//	annual >  50000            -> 5850 + (annual-50000) * 0.25
func AnnualISR(annual Num) Num {
	switch {
	case annual.Cmp(isrBracket1Cap) <= 0:
		return Zero()
	case annual.Cmp(isrBracket2Cap) <= 0:
		return annual.Sub(isrBracket1Cap).Mul(isrRateLow)
	default:
		return isrBracket2Base.Add(annual.Sub(isrBracket2Cap).Mul(isrRateHigh))
	}
}
