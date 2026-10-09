package payroll

import (
	"errors"
	"time"
)

// Reason is the termination-reason enum accepted by CalculateLiquidation
// (hr_admin_panel.html:3682). Exactly 10 values are valid.
type Reason string

const (
	ReasonVoluntaria      Reason = "voluntaria"
	ReasonSinPrev         Reason = "sin_prev"
	ReasonAcuerdo         Reason = "acuerdo"
	ReasonJustificada     Reason = "justificada"
	ReasonInjustificada   Reason = "injustificada"
	ReasonDespidoPreaviso Reason = "despido_preaviso"
	ReasonIndem25         Reason = "indem_25"
	ReasonIndem50         Reason = "indem_50"
	ReasonPension         Reason = "pension"
	ReasonVencimiento     Reason = "vencimiento"
)

// ParseReason is the server-side allowlist for the 10 valid termination
// reasons. An unrecognized string returns (_, false) rather than silently
// matching the JS's own fall-through default: `reason=gv('liq-reason')||
// 'voluntaria'` followed by a chain of `===` checks means any unrecognized
// value in the live JS behaves exactly like "voluntaria" (no preaviso, no
// indemnización) with no warning -- the Q6 unknown-type precedent, applied
// here so a typo'd reason is rejected instead of silently underpaying the
// entire indemnización.
func ParseReason(s string) (Reason, bool) {
	switch Reason(s) {
	case ReasonVoluntaria, ReasonSinPrev, ReasonAcuerdo, ReasonJustificada,
		ReasonInjustificada, ReasonDespidoPreaviso, ReasonIndem25, ReasonIndem50,
		ReasonPension, ReasonVencimiento:
		return Reason(s), true
	default:
		return "", false
	}
}

// ErrExitBeforeStart is returned when ExitDate precedes StartDate. The live
// JS clamps both `years` and `totalMonths` to a minimum of 0 in this case
// (`Math.max(0, ...)` at hr_admin_panel.html:63,66) rather than rejecting the
// input. This port deliberately diverges (design R1d): Num.RoundHalfUp only
// reproduces JS Math.round semantics for non-negative values (ties toward
// +Infinity, hazard 2), so a negative totalMonths would round incorrectly.
// Rejecting the input outright is safer than silently clamping a termination
// date that precedes the hire date.
var ErrExitBeforeStart = errors.New("payroll: liquidation exit date precedes start date")

// Branches records which side of each of the FIVE INDEPENDENT acumulados
// checks fired (the JS's own `acumX > 0 ? precise : fallback` ternaries).
// Persisted alongside the raw inputs (design R6, slice 3g) so a historical
// liquidación can always be proven to have used a particular formula, not
// merely inferred from a value both branches could have produced.
type Branches struct {
	VacProp bool // AcumVac   > 0
	DecProp bool // AcumDec   > 0
	Prima   bool // AcumPrima > 0
	Indem6m bool // Acum6m    > 0
	Sal30   bool // Sal30Raw  > 0
}

// LiquidationInput mirrors calcLiq's inputs (hr_admin_panel.html:3577-3766,
// form elements at 836-849).
type LiquidationInput struct {
	Salary    Num       // employees.salary (`sal`)
	StartDate time.Time // employees.start_date
	ExitDate  time.Time // #liq-date
	Reason    Reason

	SalPend   Num // #liq-sal-pend -- item 1, "salario pendiente"
	Otros     Num // #liq-otros    -- item 6
	VacDays   Num // #liq-vac      -- fallback input for item 3
	DecMonths Num // #liq-dec      -- fallback input for item 4

	AcumVac   Num // #liq-acum-vac   (11-month accumulation)
	AcumDec   Num // #liq-acum-dec   (12-month accumulation)
	AcumPrima Num // #liq-acum-prima (60-month accumulation)
	Acum6m    Num // #liq-acum-6m    (6-month accumulation)
	Sal30Raw  Num // #liq-sal30

	DeductionQuotaTotal Num // sum of the checked .liq-ded-check quotas
}

// LiquidationResult mirrors calcLiq's full computed breakdown, byte-faithful
// to hr_admin_panel.html:3577-3766.
type LiquidationResult struct {
	Years        Num // continuous: days / 365.25
	TotalMonths  int // (exitY*12+exitM) - (startY*12+startM)
	RoundedYears Num // = Math.round(totalMonths/12). NAMED to break hazard 2
	// ("floorYears" in the JS despite being a round, not a floor);
	// serialized as "floorYears" (slice 3g) for print/JSON compatibility.

	Salario, Preaviso, VacProp, DecProp, Parcial Num

	PrimaMonths, PrimaTotal, PrimaMensual, PrimaSemanal Num
	AntigSem, PrimaDeduccion, AntigSemNeta              Num

	Indem6mMensual, Sal30, IndemSemanalFav                    Num
	IndemWeeks, IndemBase, RecargoPct, Recargo, Indemnizacion Num

	Total Num // parcial + antigSemNeta + indemnizacion (pre-deduction)

	CSSBase, CSS91, SE92, ISRAnual, ISRRate, ISR93 Num
	Art701Base, Art701Sujeta, ISR94                Num
	TotalLegal, TotalDed, NetTotal                 Num

	Branches Branches
}

// CalculateLiquidation is a byte-faithful port of calcLiq
// (hr_admin_panel.html:3577-3766). It carries all four calcLiq-side porting
// hazards documented in doc.go: the two distinct weeks-per-month divisors
// (hazard 1: weeksPerMonthPreaviso for preaviso only, weeksPerMonthPrima for
// prima de antigüedad and indemnización only), RoundedYears' round-not-floor
// semantics (hazard 2), the isrRate zero-annual guard -- which the real JS
// source already implements as `annual>0 ? isrAnual/annual : 0`, so there is
// no NaN to reproduce (hazard 3, corrected from the design draft's claim) --
// and the primaMonths minimum-1 clamp (hazard 4).
func CalculateLiquidation(in LiquidationInput) (LiquidationResult, error) {
	if in.ExitDate.Before(in.StartDate) {
		return LiquidationResult{}, ErrExitBeforeStart
	}

	days := int64(in.ExitDate.Sub(in.StartDate).Hours() / 24)
	years, err := FromInt(days).Div(daysPerYear)
	if err != nil {
		// Unreachable: daysPerYear is the fixed nonzero constant "365.25".
		return LiquidationResult{}, err
	}

	exitY, exitM, _ := in.ExitDate.Date()
	startY, startM, _ := in.StartDate.Date()
	totalMonths := (exitY*12 + int(exitM)) - (startY*12 + int(startM))

	roundedYears, err := FromInt(int64(totalMonths)).Div(FromInt(12))
	if err != nil {
		// Unreachable: 12 is a fixed nonzero constant.
		return LiquidationResult{}, err
	}
	roundedYears = roundedYears.RoundHalfUp()

	// 1. Salario pendiente.
	salario := in.SalPend

	// 2. Preaviso -- only for despido_preaviso / pension; uses the
	// CONTINUOUS `years` value (not roundedYears) for its bracket
	// thresholds, and HAZARD 1's weeksPerMonthPreaviso (4.333) -- NEVER
	// weeksPerMonthPrima (4.3333), which belongs exclusively to steps 7/8.
	preaviso := Zero()
	if in.Reason == ReasonDespidoPreaviso || in.Reason == ReasonPension {
		switch {
		case years.Cmp(preavisoYearsThreshold) < 0:
			preaviso, err = in.Salary.Div(weeksPerMonthPreaviso)
			if err != nil {
				// Unreachable: weeksPerMonthPreaviso is the fixed nonzero
				// constant "4.333".
				return LiquidationResult{}, err
			}
		case years.Cmp(FromInt(2)) < 0:
			half, err := in.Salary.Div(weeksPerMonthPreaviso)
			if err != nil {
				return LiquidationResult{}, err
			}
			preaviso = half.Mul(FromInt(2))
		case years.Cmp(FromInt(5)) < 0:
			preaviso = in.Salary
		default:
			preaviso = in.Salary.Mul(FromInt(2))
		}
	}

	// The five INDEPENDENT acumulados branch flags (fired once, reused by
	// every one of the five checks below -- never a single shared flag).
	branches := Branches{
		VacProp: in.AcumVac.IsPositive(),
		DecProp: in.AcumDec.IsPositive(),
		Prima:   in.AcumPrima.IsPositive(),
		Indem6m: in.Acum6m.IsPositive(),
		Sal30:   in.Sal30Raw.IsPositive(),
	}

	// 3. Vacaciones proporcionales -- ACUMULADOS BRANCH.
	var vacProp Num
	if branches.VacProp {
		vacProp, err = in.AcumVac.Add(in.SalPend).Add(in.Otros).Div(vacDivisor)
		if err != nil {
			// Unreachable: vacDivisor is the fixed nonzero constant "11".
			return LiquidationResult{}, err
		}
	} else {
		perDay, err := in.Salary.Div(daysPerMonth)
		if err != nil {
			// Unreachable: daysPerMonth is the fixed nonzero constant "30".
			return LiquidationResult{}, err
		}
		vacProp = in.VacDays.Mul(perDay)
	}

	// 4. Décimo tercer mes proporcional -- ACUMULADOS BRANCH.
	var decProp Num
	if branches.DecProp {
		decProp, err = in.AcumDec.Add(vacProp).Add(in.SalPend).Add(in.Otros).Div(decDivisor)
		if err != nil {
			// Unreachable: decDivisor is the fixed nonzero constant "12".
			return LiquidationResult{}, err
		}
	} else {
		perMonth, err := in.Salary.Div(decDivisor)
		if err != nil {
			return LiquidationResult{}, err
		}
		decProp = perMonth.Mul(in.DecMonths)
	}

	// Parcial 1-4+6.
	parcial := salario.Add(preaviso).Add(vacProp).Add(decProp).Add(in.Otros)

	// 7. Prima de antigüedad -- ACUMULADOS BRANCH; HAZARD 4's minimum-1
	// clamp is applied AFTER the branch-specific min(…,60) cap, to both
	// branches, because primaMonths is a divisor two lines below.
	var primaMonths Num
	if branches.Prima {
		primaMonths = years.Mul(FromInt(12)).RoundHalfUp().Min(FromInt(primaMonthsCap))
	} else {
		primaMonths = roundedYears.Mul(FromInt(12)).Min(FromInt(primaMonthsCap))
	}
	primaMonths = primaMonths.Max(FromInt(primaMonthsMin))

	var primaTotal Num
	if branches.Prima {
		primaTotal = in.AcumPrima
	} else {
		primaTotal = in.Salary.Mul(FromInt(12)).Mul(roundedYears.Min(FromInt(primaYearsCap)))
	}

	primaMensual, err := primaTotal.Div(primaMonths)
	if err != nil {
		// Unreachable: primaMonths is clamped to a minimum of 1 above.
		return LiquidationResult{}, err
	}
	primaSemanal, err := primaMensual.Div(weeksPerMonthPrima)
	if err != nil {
		// Unreachable: weeksPerMonthPrima is the fixed nonzero constant
		// "4.3333".
		return LiquidationResult{}, err
	}
	antigSem := primaSemanal.Mul(roundedYears)

	primaDeduccion := Zero()
	if in.Reason == ReasonSinPrev {
		// Art. 222 preaviso offset: a "sin preaviso" exit deducts the
		// weekly prima rate from the seniority prima itself.
		primaDeduccion = primaSemanal
	}
	antigSemNeta := antigSem.Sub(primaDeduccion)

	// 8. Indemnización -- TWO independent ACUMULADOS BRANCHES.
	indem6mMensual := in.Salary
	if branches.Indem6m {
		indem6mMensual, err = in.Acum6m.Div(FromInt(6))
		if err != nil {
			return LiquidationResult{}, err
		}
	}
	sal30 := in.Salary
	if branches.Sal30 {
		sal30 = in.Sal30Raw
	}
	indemSemanalFav, err := indem6mMensual.Max(sal30).Div(weeksPerMonthPrima)
	if err != nil {
		return LiquidationResult{}, err
	}

	var indemWeeks Num
	if roundedYears.Cmp(FromInt(indemWeekThreshold)) <= 0 {
		indemWeeks = roundedYears
	} else {
		indemWeeks = FromInt(indemWeekThreshold).Add(
			roundedYears.Sub(FromInt(indemWeekThreshold)).Mul(FromInt(indemWeekMultiplier)))
	}
	indemBase := indemSemanalFav.Mul(indemWeeks)

	recargoPct := Zero()
	switch in.Reason {
	case ReasonIndem25:
		recargoPct = recargo25
	case ReasonIndem50:
		recargoPct = recargo50
	}
	recargo := indemBase.Mul(recargoPct)

	// Only injustificada/acuerdo (full indemBase, no recargo) and
	// indem_25/indem_50 (indemBase+recargo) produce a nonzero
	// indemnización; the other six reasons (voluntaria, sin_prev,
	// justificada, despido_preaviso, pension, vencimiento) fall through to
	// the zero default -- exactly as the live JS's if/else-if chain does.
	indemnizacion := Zero()
	switch in.Reason {
	case ReasonInjustificada, ReasonAcuerdo:
		indemnizacion = indemBase
	case ReasonIndem25, ReasonIndem50:
		indemnizacion = indemBase.Add(recargo)
	}

	total := parcial.Add(antigSemNeta).Add(indemnizacion)

	// 9. Descuentos legales.
	cssBase := in.SalPend.Add(vacProp).Add(decProp).Add(preaviso).Add(in.Otros)
	css91 := cssBase.Mul(cssEmployee)
	se92 := cssBase.Mul(seEmployee)

	annual := in.Salary.Mul(annualizeMonths)
	isrAnual := AnnualISR(annual)
	// HAZARD 3 (corrected): the live JS already guards this division with
	// `annual>0 ? isrAnual/annual : 0` (hr_admin_panel.html:3669, confirmed
	// against the vendored legacy source), so a zero-salary liquidación
	// returns isrRate=0, never NaN. There is no deviation to encode here --
	// see doc.go for the full correction of the design draft's claim.
	isrRate := Zero()
	if annual.IsPositive() {
		isrRate, err = isrAnual.Div(annual)
		if err != nil {
			return LiquidationResult{}, err
		}
	}
	isr93 := cssBase.Mul(isrRate)

	// Art. 701 -- the severance/prima portion is taxed separately, against
	// its own 3-bracket application of AnnualISR (not re-annualized; the
	// art701Sujeta base is used directly as the bracket input, exactly as
	// the JS's inline isr94 ternary chain does).
	art701Base := antigSemNeta.Add(indemnizacion)
	roundedYearsPct, err := roundedYears.Div(art701PctDiv)
	if err != nil {
		// Unreachable: art701PctDiv is the fixed nonzero constant "100".
		return LiquidationResult{}, err
	}
	art701Sujeta := art701Base.Sub(art701Base.Mul(roundedYearsPct)).Sub(art701Exemption).Max(Zero())
	isr94 := AnnualISR(art701Sujeta)

	totalLegal := css91.Add(se92).Add(isr93).Add(isr94)

	netTotal := total.Sub(totalLegal).Sub(in.DeductionQuotaTotal)

	return LiquidationResult{
		Years: years, TotalMonths: totalMonths, RoundedYears: roundedYears,

		Salario: salario, Preaviso: preaviso, VacProp: vacProp, DecProp: decProp, Parcial: parcial,

		PrimaMonths: primaMonths, PrimaTotal: primaTotal, PrimaMensual: primaMensual, PrimaSemanal: primaSemanal,
		AntigSem: antigSem, PrimaDeduccion: primaDeduccion, AntigSemNeta: antigSemNeta,

		Indem6mMensual: indem6mMensual, Sal30: sal30, IndemSemanalFav: indemSemanalFav,
		IndemWeeks: indemWeeks, IndemBase: indemBase, RecargoPct: recargoPct, Recargo: recargo, Indemnizacion: indemnizacion,

		Total: total,

		CSSBase: cssBase, CSS91: css91, SE92: se92, ISRAnual: isrAnual, ISRRate: isrRate, ISR93: isr93,
		Art701Base: art701Base, Art701Sujeta: art701Sujeta, ISR94: isr94,
		TotalLegal: totalLegal, TotalDed: in.DeductionQuotaTotal, NetTotal: netTotal,

		Branches: branches,
	}, nil
}
