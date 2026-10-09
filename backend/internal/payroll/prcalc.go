package payroll

import "errors"

// ErrZeroFactor is returned by Calculate when Input.Factor is zero: the JS
// formula divides the taxable base by factor to annualize it for the ISR
// bracket (`annual = (b/factor)*13`), so a zero factor would divide by zero.
// No fixture or real caller ever passes factor=0 (it is always 1 for
// "mensual" or 1/2 for "quincenal", resolved server-side), but Num.Div never
// panics, so Calculate surfaces this defensively as an error instead.
var ErrZeroFactor = errors.New("payroll: Calculate requires a non-zero factor")

// Attendance mirrors prCalc's attData argument (hr_admin_panel.html:1870-
// 1890). AbsentDays is the already-counted work_type==28 absence days for
// the period -- the SQL-layer reduction that decides which attendance rows
// count as an absence (hazard 5) happens upstream, in slice 3e's
// ListPayrollRunInputs; Calculate itself never sees work_type.
type Attendance struct {
	HasData        bool // attData.hasData -- ANY attendance row in the period
	AbsentDays     int
	OvertimeAmount Num
}

// Deduction mirrors prCalc's dedData argument: the sum of the employee's
// active deduction quotas for the period.
type Deduction struct {
	QuotaTotal Num
}

// Input mirrors prCalc(e, factor, attData, dedData)'s arguments. Attendance
// and Deduction are pointers so a nil value mirrors the JS attData/dedData
// being absent (every money field they would have contributed is then 0,
// exactly as prCalc's own `attData.hasData ? ... : 0` / `dedData.quotaTotal
// || 0` ternaries resolve).
type Input struct {
	Salary     Num // employees.salary
	Factor     Num // 1 (mensual) or 1/2 (quincenal) -- server-derived
	Attendance *Attendance
	Deduction  *Deduction
}

// Result mirrors prCalc's returned object field-for-field, PLUS SalBase,
// which the JS computes but does not return -- the live-run write path
// (slice 3f) needs it for the total_earned mapping.
type Result struct {
	SalBase Num // e.salary * factor
	AttDed  Num // hasData ? (salary/30)*absentDays : 0
	OtAmt   Num // hasData ? attData.otAmount : 0

	B Num // max(0, salBase - attDed + otAmt) -- the taxable/net base

	CSS Num // b * 0.0975 (PR.CSS_E)
	SE  Num // b * 0.0125 (PR.SE_E)
	ISR Num // (AnnualISR(b/factor*13) / 12) * factor

	Ded      Num // css + se + isr
	DedQuota Num // dedData.quotaTotal || 0
	Net      Num // b - ded - dedQuota

	PCSS Num // b * 0.1225 (PR.CSS_P)
	PSE  Num // b * 0.015  (PR.SE_P)

	Dec     Num // décimo provision, b * 1/12 (PR.DEC)
	DecCSSP Num // dec * 0.0725 -- CSS patronal sobre Décimo, Ley 51/2005

	Tot Num // b + pcss + pse + dec + decCSSP -- costo empresa
}

// Calculate is a byte-faithful port of prCalc (hr_admin_panel.html:1870-
// 1890, constants at line 1817). prCalc carries none of the five porting
// hazards named by the design: all four calcLiq-side hazards (two
// weeks-per-month divisors, floorYears' round-not-floor, the ISR-rate
// zero-annual guard, the primaMonths minimum-1 clamp) belong to slice 3c's
// CalculateLiquidation, and hazard 5 (work_type=0 falsy-in-JS) is a
// SQL-layer concern implemented in slice 3e's ListPayrollRunInputs, not in
// this pure function at all.
func Calculate(in Input) (Result, error) {
	salBase := in.Salary.Mul(in.Factor)

	attDed := Zero()
	otAmt := Zero()
	if in.Attendance != nil && in.Attendance.HasData {
		perDay, err := in.Salary.Div(daysPerMonth)
		if err != nil {
			// Unreachable: daysPerMonth is a fixed, nonzero constant ("30").
			return Result{}, err
		}
		attDed = perDay.Mul(FromInt(int64(in.Attendance.AbsentDays)))
		otAmt = in.Attendance.OvertimeAmount
	}

	b := salBase.Sub(attDed).Add(otAmt).Max(Zero())

	css := b.Mul(cssEmployee)
	se := b.Mul(seEmployee)

	if in.Factor.IsZero() {
		return Result{}, ErrZeroFactor
	}
	perFactor, err := b.Div(in.Factor)
	if err != nil {
		// Unreachable: the IsZero check above already rejected a zero factor.
		return Result{}, err
	}
	annual := perFactor.Mul(annualizeMonths)

	// isr = (AnnualISR(annual) / 12) * factor, expressed as a multiplication
	// by decimoRate (1/12 exactly) rather than a Div, so no error path is
	// needed for a constant that is never zero.
	isr := AnnualISR(annual).Mul(decimoRate).Mul(in.Factor)

	ded := css.Add(se).Add(isr)

	dedQuota := Zero()
	if in.Deduction != nil {
		dedQuota = in.Deduction.QuotaTotal
	}
	net := b.Sub(ded).Sub(dedQuota)

	pcss := b.Mul(cssEmployer)
	pse := b.Mul(seEmployer)
	dec := b.Mul(decimoRate)
	decCSSP := dec.Mul(decimoCSSEmployer)
	tot := b.Add(pcss).Add(pse).Add(dec).Add(decCSSP)

	return Result{
		SalBase: salBase, AttDed: attDed, OtAmt: otAmt,
		B: b, CSS: css, SE: se, ISR: isr,
		Ded: ded, DedQuota: dedQuota, Net: net,
		PCSS: pcss, PSE: pse, Dec: dec, DecCSSP: decCSSP, Tot: tot,
	}, nil
}
