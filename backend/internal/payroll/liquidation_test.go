package payroll

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing date %q: %v", s, err)
	}
	return d
}

// TestParseReason_Allowlist (task 3.1) asserts all 10 valid reasons are
// accepted, and an unrecognized reason returns (_, false) rather than
// silently matching the JS's own fall-through default (any unrecognized
// `reason` value behaves identically to "voluntaria" in the live JS, with
// no warning -- this port rejects it instead, per the Q6 precedent).
func TestParseReason_Allowlist(t *testing.T) {
	valid := []Reason{
		ReasonVoluntaria, ReasonSinPrev, ReasonAcuerdo, ReasonJustificada,
		ReasonInjustificada, ReasonDespidoPreaviso, ReasonIndem25, ReasonIndem50,
		ReasonPension, ReasonVencimiento,
	}
	for _, r := range valid {
		got, ok := ParseReason(string(r))
		if !ok || got != r {
			t.Errorf("ParseReason(%q) = (%q, %v), want (%q, true)", r, got, ok, r)
		}
	}

	for _, bad := range []string{"", "Voluntaria", "despido_preaviso ", "renuncia", "indem_75"} {
		if _, ok := ParseReason(bad); ok {
			t.Errorf("ParseReason(%q) = (_, true), want (_, false) -- an unrecognized reason must never be silently accepted", bad)
		}
	}
}

// TestCalculateLiquidation_ExitBeforeStartRejected (task 3.1) asserts a
// negative service period is rejected rather than silently computing a
// negative totalMonths -- the live JS clamps both `years` and `totalMonths`
// to Math.max(0, ...) instead (design R1d explicitly accepts this as a
// deliberate divergence, since RoundHalfUp only matches JS Math.round for
// non-negative values).
func TestCalculateLiquidation_ExitBeforeStartRejected(t *testing.T) {
	in := LiquidationInput{
		Salary:    FromInt(1000),
		StartDate: mustDate(t, "2024-01-01"),
		ExitDate:  mustDate(t, "2023-01-01"),
		Reason:    ReasonVoluntaria,
	}
	_, err := CalculateLiquidation(in)
	if err != ErrExitBeforeStart {
		t.Fatalf("CalculateLiquidation with ExitDate before StartDate: err = %v, want ErrExitBeforeStart", err)
	}
}

// TestCalculateLiquidation_RoundedYearsTieRoundsUp (task 3.2, HAZARD 2)
// constructs a totalMonths that lands exactly on a .5 tie when divided by
// 12 (totalMonths=18 -> 18/12=1.5) and asserts RoundedYears rounds TOWARD
// +Infinity (JS Math.round semantics: 2), never away from zero in the
// Go-idiomatic sense (which would coincide here) and never floored (which
// would wrongly give 1). RoundedYears is deliberately NOT named floorYears
// in this port -- see the JSON serialization note in liquidation.go --
// specifically to prevent a translator assuming math.Floor from the name.
func TestCalculateLiquidation_RoundedYearsTieRoundsUp(t *testing.T) {
	// 2022-01-01 -> 2023-07-01 is 18 calendar months by the
	// (exitY*12+exitM)-(startY*12+startM) method: totalMonths=18,
	// 18/12=1.5 exactly -- a genuine halfway tie.
	in := LiquidationInput{
		Salary:    FromInt(1200),
		StartDate: mustDate(t, "2022-01-01"),
		ExitDate:  mustDate(t, "2023-07-01"),
		Reason:    ReasonVoluntaria,
	}
	got, err := CalculateLiquidation(in)
	if err != nil {
		t.Fatalf("CalculateLiquidation: unexpected error: %v", err)
	}
	if got.TotalMonths != 18 {
		t.Fatalf("TotalMonths = %d, want 18 (test construction error)", got.TotalMonths)
	}
	if want := "2"; got.RoundedYears.FloatString(0) != want {
		t.Errorf("RoundedYears = %s, want %s (an exact .5 tie must round toward +Infinity, matching JS Math.round)", got.RoundedYears.FloatString(0), want)
	}
}

// TestCalculateLiquidation_WeekDivisorsDiffer (task 3.3, HAZARD 1) asserts
// preaviso (weeksPerMonthPreaviso = 4.333) and the prima/indemnización
// chain (weeksPerMonthPrima = 4.3333) are never interchanged, by
// constructing a years<0.5 case -- the ONLY preaviso bracket that divides by
// 4.333 at all -- where none of the fixture inventory from slice 3a happens
// to land (every calcliq fixture's floorYears rounds to >= 2), so this
// hazard needs its own direct unit test rather than relying on the golden
// walk.
func TestCalculateLiquidation_WeekDivisorsDiffer(t *testing.T) {
	// 2024-01-01 -> 2024-03-01 is 2 calendar months: years = 60/365.25 =
	// 0.164... < 0.5, landing squarely in preaviso's first bracket
	// (sal/4.333). floorYears = round(2/12) = 0, so antigSem's chain
	// (primaSemanal, which divides by weeksPerMonthPrima=4.3333) also runs,
	// letting both divisors be exercised and compared in one case.
	in := LiquidationInput{
		Salary:    FromInt(1000),
		StartDate: mustDate(t, "2024-01-01"),
		ExitDate:  mustDate(t, "2024-03-01"),
		Reason:    ReasonDespidoPreaviso,
	}
	got, err := CalculateLiquidation(in)
	if err != nil {
		t.Fatalf("CalculateLiquidation: unexpected error: %v", err)
	}

	wantPreaviso, err := FromInt(1000).Div(weeksPerMonthPreaviso)
	if err != nil {
		t.Fatalf("building expected preaviso: %v", err)
	}
	if got.Preaviso.FloatString(6) != wantPreaviso.FloatString(6) {
		t.Errorf("Preaviso = %s, want sal/4.333 = %s", got.Preaviso.FloatString(6), wantPreaviso.FloatString(6))
	}

	// primaSemanal = primaMensual / weeksPerMonthPrima (4.3333, NOT 4.333).
	// primaMensual = primaTotal/primaMonths; with acumulados absent and
	// floorYears=0, primaTotal = sal*12*min(0,5) = 0, so primaMensual = 0
	// and primaSemanal = 0 regardless of which constant divided it here --
	// this case alone cannot distinguish the two divisors for primaSemanal,
	// so the distinguishing assertion is on preaviso itself: confirm it is
	// NOT computed using weeksPerMonthPrima (4.3333), which would produce a
	// visibly different value in the final cent.
	wrongPreaviso, err := FromInt(1000).Div(weeksPerMonthPrima)
	if err != nil {
		t.Fatalf("building wrong-divisor comparison value: %v", err)
	}
	if got.Preaviso.FloatString(2) == wrongPreaviso.FloatString(2) {
		t.Fatalf("Preaviso must differ in the final cent from sal/weeksPerMonthPrima -- otherwise hazard 1's two divisors are indistinguishable in this case (test construction error)")
	}
}

// TestCalculateLiquidation_PrimaMonthsMinClamp (task 3.4, HAZARD 4)
// constructs a zero-month-service input where, without the minimum-1
// clamp, primaMensual = primaTotal / primaMonths would divide by zero
// (primaMonths = min(floorYears*12, 60) = min(0, 60) = 0). Mirrors the
// slice 3a `one-month-service` fixture's own finding, asserted directly
// here rather than only through the golden walk.
func TestCalculateLiquidation_PrimaMonthsMinClamp(t *testing.T) {
	in := LiquidationInput{
		Salary:    FromInt(600),
		StartDate: mustDate(t, "2025-05-15"),
		ExitDate:  mustDate(t, "2025-06-15"),
		Reason:    ReasonVoluntaria,
	}
	got, err := CalculateLiquidation(in)
	if err != nil {
		t.Fatalf("CalculateLiquidation: unexpected error (the minimum-1 clamp must prevent a divide-by-zero): %v", err)
	}
	if got.PrimaMonths.FloatString(0) != "1" {
		t.Errorf("PrimaMonths = %s, want 1 (the HAZARD 4 clamp must fire before primaMensual's division)", got.PrimaMonths.FloatString(0))
	}
}

// TestCalculateLiquidation_ISRRateZeroAtZeroAnnual (task 3.5, HAZARD 3,
// corrected) asserts that at salary=0 (annual=sal*13=0), ISRRate is exactly
// 0 -- matching the REAL current JS source
// (`annual>0?isrAnual/annual:0`, hr_admin_panel.html:3669, confirmed against
// the vendored tools/goldens/legacy copy), not a NaN. The design draft's
// claim that the JS produces NaN here was already corrected during slice
// 3a's fixture generation (see testdata/calcliq/zero-salary-nan.json's
// design_claim_mismatch field) -- this is a normal zero-guard matching real
// JS behavior, not a documented deviation from it.
func TestCalculateLiquidation_ISRRateZeroAtZeroAnnual(t *testing.T) {
	in := LiquidationInput{
		Salary:    Zero(),
		StartDate: mustDate(t, "2022-01-01"),
		ExitDate:  mustDate(t, "2024-01-01"),
		Reason:    ReasonVoluntaria,
	}
	got, err := CalculateLiquidation(in)
	if err != nil {
		t.Fatalf("CalculateLiquidation at salary=0: unexpected error (must not panic or error on a zero annual base): %v", err)
	}
	if !got.ISRRate.IsZero() {
		t.Errorf("ISRRate = %s, want exactly 0 at annual=0 (matching the real JS's own zero-guard, not a NaN)", got.ISRRate.FloatString(6))
	}
	if !got.ISR93.IsZero() {
		t.Errorf("ISR93 = %s, want exactly 0 (cssBase * isrRate, and isrRate=0)", got.ISR93.FloatString(2))
	}
}

// TestCalculateLiquidation_AcumuladosBranchesIndependent (task 3.6) asserts
// the five acumulados checks fire INDEPENDENTLY -- setting exactly one
// AcumX > 0 at a time flips exactly that one Branches flag, never the other
// four. This is the mechanical enforcement of the spec's Implementation
// Note: "not a single shared flag."
func TestCalculateLiquidation_AcumuladosBranchesIndependent(t *testing.T) {
	base := LiquidationInput{
		Salary:    FromInt(1000),
		StartDate: mustDate(t, "2020-01-01"),
		ExitDate:  mustDate(t, "2024-01-01"),
		Reason:    ReasonVoluntaria,
		SalPend:   FromInt(500),
		VacDays:   FromInt(10),
		DecMonths: FromInt(1),
	}

	cases := []struct {
		name      string
		mutate    func(in *LiquidationInput)
		wantFlags Branches
	}{
		{"only-acum-vac", func(in *LiquidationInput) { in.AcumVac = FromInt(5000) }, Branches{VacProp: true}},
		{"only-acum-dec", func(in *LiquidationInput) { in.AcumDec = FromInt(6000) }, Branches{DecProp: true}},
		{"only-acum-prima", func(in *LiquidationInput) { in.AcumPrima = FromInt(20000) }, Branches{Prima: true}},
		{"only-acum-6m", func(in *LiquidationInput) { in.Acum6m = FromInt(5400) }, Branches{Indem6m: true}},
		{"only-sal30", func(in *LiquidationInput) { in.Sal30Raw = FromInt(1050) }, Branches{Sal30: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			got, err := CalculateLiquidation(in)
			if err != nil {
				t.Fatalf("CalculateLiquidation: unexpected error: %v", err)
			}
			if got.Branches != tc.wantFlags {
				t.Errorf("Branches = %+v, want %+v (exactly one flag true, the other four false)", got.Branches, tc.wantFlags)
			}
		})
	}
}

// TestCalculateLiquidation_TerminationReasons (task 3.7) exercises a
// representative sample of the 10 termination reasons directly (not via the
// golden fixtures), confirming each reason's distinct calculation path.
func TestCalculateLiquidation_TerminationReasons(t *testing.T) {
	// 2021-07-01 -> 2024-07-01: totalMonths=36, floorYears=3 (the years<5
	// preaviso bracket; indemWeeks=3, the floorYears<=10 bracket).
	base := LiquidationInput{
		Salary:    FromInt(900),
		StartDate: mustDate(t, "2021-07-01"),
		ExitDate:  mustDate(t, "2024-07-01"),
		SalPend:   FromInt(900),
		VacDays:   FromInt(8),
		DecMonths: FromInt(1),
	}

	t.Run("voluntaria: no preaviso, no indemnizacion", func(t *testing.T) {
		in := base
		in.Reason = ReasonVoluntaria
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.Preaviso.IsZero() || !got.Indemnizacion.IsZero() {
			t.Errorf("voluntaria: Preaviso=%s Indemnizacion=%s, want both 0", got.Preaviso, got.Indemnizacion)
		}
	})

	t.Run("despido_preaviso: preaviso=sal (years<5 bracket), no indemnizacion", func(t *testing.T) {
		in := base
		in.Reason = ReasonDespidoPreaviso
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Preaviso.FloatString(2) != in.Salary.FloatString(2) {
			t.Errorf("despido_preaviso: Preaviso = %s, want sal = %s (the years<5 bracket)", got.Preaviso.FloatString(2), in.Salary.FloatString(2))
		}
		if !got.Indemnizacion.IsZero() {
			t.Errorf("despido_preaviso: Indemnizacion = %s, want 0", got.Indemnizacion.FloatString(2))
		}
	})

	t.Run("sin_prev: prima reduced by the Art. 222 preaviso offset", func(t *testing.T) {
		in := base
		in.Reason = ReasonSinPrev
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.PrimaDeduccion.FloatString(6) != got.PrimaSemanal.FloatString(6) {
			t.Errorf("sin_prev: PrimaDeduccion = %s, want PrimaSemanal = %s", got.PrimaDeduccion.FloatString(6), got.PrimaSemanal.FloatString(6))
		}
		if got.AntigSemNeta.Cmp(got.AntigSem) >= 0 {
			t.Errorf("sin_prev: AntigSemNeta (%s) must be strictly less than AntigSem (%s) when PrimaSemanal > 0", got.AntigSemNeta, got.AntigSem)
		}
	})

	t.Run("injustificada: indemnizacion=indemBase, no recargo", func(t *testing.T) {
		in := base
		in.Reason = ReasonInjustificada
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Indemnizacion.FloatString(6) != got.IndemBase.FloatString(6) {
			t.Errorf("injustificada: Indemnizacion = %s, want IndemBase = %s", got.Indemnizacion.FloatString(6), got.IndemBase.FloatString(6))
		}
		if !got.Recargo.IsZero() {
			t.Errorf("injustificada: Recargo = %s, want 0 (no surcharge)", got.Recargo.FloatString(2))
		}
	})

	t.Run("acuerdo: indemnizacion=indemBase, no recargo (same as injustificada)", func(t *testing.T) {
		in := base
		in.Reason = ReasonAcuerdo
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Indemnizacion.FloatString(6) != got.IndemBase.FloatString(6) {
			t.Errorf("acuerdo: Indemnizacion = %s, want IndemBase = %s", got.Indemnizacion.FloatString(6), got.IndemBase.FloatString(6))
		}
	})

	t.Run("indem_25: indemnizacion=indemBase+25%% recargo", func(t *testing.T) {
		in := base
		in.Reason = ReasonIndem25
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantRecargo := got.IndemBase.Mul(recargo25)
		if got.Recargo.FloatString(6) != wantRecargo.FloatString(6) {
			t.Errorf("indem_25: Recargo = %s, want indemBase*0.25 = %s", got.Recargo.FloatString(6), wantRecargo.FloatString(6))
		}
		wantIndem := got.IndemBase.Add(wantRecargo)
		if got.Indemnizacion.FloatString(6) != wantIndem.FloatString(6) {
			t.Errorf("indem_25: Indemnizacion = %s, want indemBase+recargo = %s", got.Indemnizacion.FloatString(6), wantIndem.FloatString(6))
		}
	})

	t.Run("indem_50: indemnizacion=indemBase+50%% recargo", func(t *testing.T) {
		in := base
		in.Reason = ReasonIndem50
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantRecargo := got.IndemBase.Mul(recargo50)
		if got.Recargo.FloatString(6) != wantRecargo.FloatString(6) {
			t.Errorf("indem_50: Recargo = %s, want indemBase*0.50 = %s", got.Recargo.FloatString(6), wantRecargo.FloatString(6))
		}
	})

	t.Run("pension: same preaviso bracket as despido_preaviso, no indemnizacion", func(t *testing.T) {
		in := base
		in.Reason = ReasonPension
		got, err := CalculateLiquidation(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Preaviso.FloatString(2) != in.Salary.FloatString(2) {
			t.Errorf("pension: Preaviso = %s, want sal = %s", got.Preaviso.FloatString(2), in.Salary.FloatString(2))
		}
		if !got.Indemnizacion.IsZero() {
			t.Errorf("pension: Indemnizacion = %s, want 0", got.Indemnizacion.FloatString(2))
		}
	})
}

// TestCalculateLiquidation_Art701SujetaClampedToZero (task 3.8) asserts the
// Art. 701 discount clamps to exactly 0 rather than going negative when the
// base is small relative to the 5000 exemption.
func TestCalculateLiquidation_Art701SujetaClampedToZero(t *testing.T) {
	in := LiquidationInput{
		Salary:    FromInt(400),
		StartDate: mustDate(t, "2021-02-01"),
		ExitDate:  mustDate(t, "2024-02-01"),
		Reason:    ReasonVoluntaria,
		SalPend:   FromInt(400),
	}
	got, err := CalculateLiquidation(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Art701Sujeta.IsZero() {
		t.Errorf("Art701Sujeta = %s, want exactly 0 (base - base*floorYears/100 - 5000 is negative here)", got.Art701Sujeta.FloatString(2))
	}
	if !got.ISR94.IsZero() {
		t.Errorf("ISR94 = %s, want 0 (AnnualISR of a zero base)", got.ISR94.FloatString(2))
	}
}
