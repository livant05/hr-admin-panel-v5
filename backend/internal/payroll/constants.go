package payroll

// Every statutory literal used by this package, as an exact decimal string
// literal (never a float64), one per line, each commented with its source
// line in hr_admin_panel.html (read-only). This file is the single source of
// truth for every statutory literal in Phase 3 -- including the constants
// that only calcLiq's port (slice 3c) consumes, declared here rather than
// duplicated in liquidation.go.

var (
	// PR constants, hr_admin_panel.html:1817.
	cssEmployee = MustParseDecimal("0.0975") // PR.CSS_E
	seEmployee  = MustParseDecimal("0.0125") // PR.SE_E
	cssEmployer = MustParseDecimal("0.1225") // PR.CSS_P
	seEmployer  = MustParseDecimal("0.015")  // PR.SE_P
	decimoRate  = FromFrac(1, 12)            // PR.DEC = 1/12, held EXACTLY
	// (the JS float64 equivalent is 0.08333333333333333)

	decimoCSSEmployer = MustParseDecimal("0.0725") // Ley 51/2005, line 1888

	// ISR progressive brackets -- identical in prCalc (line 1879) and, once
	// ported in 3c, in calcLiq TWICE (isrAnual, isr94). One Go function
	// (AnnualISR, isr.go) with three JS call sites (R1e consolidation).
	isrBracket1Cap  = MustParseDecimal("11000")
	isrBracket2Cap  = MustParseDecimal("50000")
	isrRateLow      = MustParseDecimal("0.15")
	isrRateHigh     = MustParseDecimal("0.25")
	isrBracket2Base = MustParseDecimal("5850")
	annualizeMonths = MustParseDecimal("13") // 12 months + XIII (décimo)

	daysPerMonth = MustParseDecimal("30")
	daysPerYear  = MustParseDecimal("365.25")

	// HAZARD 1 (calcLiq only, slice 3c) -- two DIFFERENT weeks-per-month
	// divisors in the same function. Never interchanged or shared.
	weeksPerMonthPreaviso = MustParseDecimal("4.333")  // preaviso ONLY
	weeksPerMonthPrima    = MustParseDecimal("4.3333") // prima + indemnización ONLY

	vacDivisor = MustParseDecimal("11")
	decDivisor = MustParseDecimal("12")

	recargo25 = MustParseDecimal("0.25")
	recargo50 = MustParseDecimal("0.50")

	art701Exemption = MustParseDecimal("5000") // Art. 701 flat exemption
	art701PctDiv    = MustParseDecimal("100")  // roundedYears/100 discount
)

// primaMonthsCap, primaYearsCap, indemWeekThreshold and indemWeekMultiplier
// (calcLiq only, slice 3c) are small bounded integers, not Num, since they
// never participate in Num arithmetic directly -- they gate a Min/clamp or a
// branch condition expressed via FromInt/int comparisons in liquidation.go.
const (
	primaMonthsCap = 60 // months
	primaYearsCap  = 5  // years of sal*12

	// HAZARD 4 (calcLiq only, slice 3c): primaMonths is a DIVISOR
	// (primaMensual = primaTotal / primaMonths). This minimum-1 clamp is the
	// only thing preventing a second division by zero -- load-bearing.
	primaMonthsMin = 1

	indemWeekThreshold  = 10 // <=10 years: 1 week/yr; beyond: 2 weeks/yr
	indemWeekMultiplier = 2
)

// CalcVersion tags every persisted liquidación breakdown (design R6, slice
// 3g). The legal re-derivation of these constants is out of scope for this
// phase; when that change lands, a historical record must still say which
// formula produced it.
const CalcVersion = "js-port-1"
