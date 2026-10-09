// tools/goldens/cases/prcalc.mjs
//
// Fixed input matrix for `prCalc(e, factor, attData, dedData)`
// (hr_admin_panel.html:1870-1890). `prCalc` is a pure function — no DOM, no
// Supa, no vm stubs needed; generate.mjs calls it directly inside the vm
// context that evaluated the extracted PR constant + prCalc source.
//
// `e` only ever needs `.salary` inside prCalc's own body (see the source);
// other employee fields are irrelevant to this function and are omitted.

export const prcalcCases = [
  {
    name: 'factor1-no-attendance',
    note:
      'Mensual (factor=1), no attendance record for the period at all ' +
      '(attData=null) — the common "Calcular" path for an employee with a ' +
      'clean period and no deductions.',
    e: { salary: 1200 },
    factor: 1,
    attData: null,
    dedData: null,
  },
  {
    name: 'factor1-attendance-wt28',
    note:
      'Mensual, attendance present with 2 unpaid-absence days ' +
      '(work_type=28, "Ausencia NO pagada") already reduced into ' +
      'attData.absentDays by the caller (loadPayroll/3e), plus overtime pay.',
    e: { salary: 1200 },
    factor: 1,
    attData: { hasData: true, absentDays: 2, otAmount: 45.5 },
    dedData: null,
  },
  {
    name: 'factor1-worktype-zero-falls-back-to-status',
    note:
      'Hazard 5 anchor (ported to SQL in slice 3e, hr_admin_panel.html:2957: ' +
      "l.work_type?l.work_type===28:(l.status==='absent')). This fixture " +
      'pins prCalc\'s output for the already-correctly-reduced attData that ' +
      '3e\'s SQL layer must independently produce when a raw attendance row ' +
      'has work_type=0 (falsy in JS) and status=\'absent\' — i.e. one day ' +
      'counted via the legacy-status fallback, not via a work_type===28 ' +
      'match. prCalc itself never sees work_type; this fixture is the ' +
      'downstream half of the hazard-5 cross-check, not a test of the SQL ' +
      'reduction itself.',
    e: { salary: 900 },
    factor: 1,
    attData: { hasData: true, absentDays: 1, otAmount: 0 },
    dedData: null,
  },
  {
    name: 'factor05-quincenal',
    note: 'Quincenal (factor=0.5) — the biweekly pay-period path.',
    e: { salary: 1200 },
    factor: 0.5,
    attData: null,
    dedData: null,
  },
  {
    name: 'with-active-deductions',
    note:
      'Mensual with a non-zero dedData.quotaTotal (sum of the employee\'s ' +
      "active deductions' quotas) — exercises the dedQuota/net subtraction.",
    e: { salary: 1500 },
    factor: 1,
    attData: null,
    dedData: { quotaTotal: 120.75 },
  },
  {
    name: 'isr-boundary-11000',
    note:
      'Annualized base (b/factor*13) lands exactly on 11000 — the boundary ' +
      'belongs to the <=11000 bracket (isrAnnual=0), not the next one. ' +
      'salary chosen so salBase=b=11000/13≈846.1538... exactly: using ' +
      'salary=11000/13 keeps b==salBase (no attendance/OT adjustment).',
    e: { salary: 11000 / 13 },
    factor: 1,
    attData: null,
    dedData: null,
  },
  {
    name: 'isr-boundary-50000',
    note:
      'Annualized base lands exactly on 50000 — boundary belongs to the ' +
      '11000-50000 bracket: isrAnnual=(50000-11000)*0.15=5850.',
    e: { salary: 50000 / 13 },
    factor: 1,
    attData: null,
    dedData: null,
  },
  {
    name: 'isr-above-50000',
    note:
      'Annualized base above 50000 — exercises the top bracket: ' +
      'isrAnnual=5850+(annual-50000)*0.25.',
    e: { salary: 60000 / 13 },
    factor: 1,
    attData: null,
    dedData: null,
  },
  {
    name: 'absent-all-days-b-clamped-to-zero',
    note:
      'attDed (salary/30 * absentDays) exceeds salBase+otAmt, so ' +
      'b=max(0, salBase-attDed+otAmt) must clamp to exactly 0, not go ' +
      'negative — exercises the Math.max(0, ...) clamp.',
    e: { salary: 900 },
    factor: 1,
    attData: { hasData: true, absentDays: 31, otAmount: 0 },
    dedData: null,
  },
  {
    name: 'zero-salary',
    note:
      'salary=0 — defensive case for prCalc\'s own arithmetic (the real ' +
      'divide-by-zero hazards named in the design, 1/2/3/4, all belong to ' +
      'calcLiq, not prCalc; this fixture only confirms prCalc itself does ' +
      'not panic/NaN at salary=0: b=0, every derived field is 0, and ' +
      'annual=(0/1)*13=0 so isrAnnual=0 (0<=11000), never a division).',
    e: { salary: 0 },
    factor: 1,
    attData: null,
    dedData: null,
  },
];
