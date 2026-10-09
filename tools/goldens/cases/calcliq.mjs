// tools/goldens/cases/calcliq.mjs
//
// Fixed input matrix for `calcLiq()` (hr_admin_panel.html:3580-3771).
// Each case supplies the exact DOM element values calcLiq reads via
// `gv`/`document.getElementById`, the employee row `Supa.sel('employees')`
// returns, and any checked `.liq-ded-check` deduction rows. generate.mjs
// drives these through tools/goldens/stubs.mjs's node:vm context and
// captures printLiq's complete breakdown object — no DOM, no browser.
//
// Element id reference (all read by the real, unmodified calcLiq source):
//   liq-emp, liq-date, liq-reason, liq-sal-pend, liq-otros, liq-vac,
//   liq-dec, liq-acum-vac, liq-acum-dec, liq-acum-prima, liq-sal30,
//   liq-acum-6m

const EMP_ID = 'emp-golden-1';

function employee(overrides) {
  return {
    id: EMP_ID,
    first_name: 'Ana',
    last_name: 'Gómez',
    position: 'Analista',
    department: 'Operaciones',
    ...overrides,
  };
}

function elements({
  date,
  reason,
  salPend = 0,
  otros = 0,
  vac = 0,
  dec = 0,
  acumVac = 0,
  acumDec = 0,
  acumPrima = 0,
  sal30 = 0,
  acum6m = 0,
}) {
  return {
    'liq-emp': EMP_ID,
    'liq-date': date,
    'liq-reason': reason,
    'liq-sal-pend': String(salPend),
    'liq-otros': String(otros),
    'liq-vac': String(vac),
    'liq-dec': String(dec),
    'liq-acum-vac': String(acumVac),
    'liq-acum-dec': String(acumDec),
    'liq-acum-prima': String(acumPrima),
    'liq-sal30': String(sal30),
    'liq-acum-6m': String(acum6m),
  };
}

// ── Matrix 1: all 10 termination reasons, acumulados PRESENT ──────────────
// 6 years of service (2018-03-10 -> 2024-03-10), salary=1500, every acum*
// field and sal30Raw populated so every one of the 5 independent acumulados
// branches takes its precise (non-fallback) path.
const TEN_REASONS = [
  'voluntaria',
  'sin_prev',
  'acuerdo',
  'justificada',
  'injustificada',
  'despido_preaviso',
  'indem_25',
  'indem_50',
  'pension',
  'vencimiento',
];

const acumuladosPresentCases = TEN_REASONS.map((reason) => ({
  name: `reason-${reason}-acumulados-present`,
  note:
    `Termination reason "${reason}", all five acumulados inputs populated ` +
    '(acumVac/acumDec/acumPrima/acum6m/sal30Raw all > 0) so vacProp, ' +
    'decProp, primaTotal, indem6mMensual and sal30 all take their precise ' +
    'acumulados-branch formula, not the statutory-default fallback.',
  employee: employee({ salary: 1500, start_date: '2018-03-10' }),
  elements: elements({
    date: '2024-03-10',
    reason,
    salPend: 1500,
    otros: 50,
    acumVac: 1200,
    acumDec: 1400,
    acumPrima: 9000,
    sal30: 1550,
    acum6m: 7500,
  }),
  dedChecks:
    reason === 'acuerdo'
      ? [
          { id: 'd1', desc: 'Préstamo personal', quota: 35.0, checked: true },
          { id: 'd2', desc: 'Avance no activo', quota: 15.0, checked: false },
        ]
      : [],
}));

// ── Matrix 2: 5 distinct-path reasons, acumulados ABSENT ───────────────────
// 3 years of service (2021-07-01 -> 2024-07-01), salary=900, every acum*
// field and sal30Raw at 0 so all five branches take their statutory-default
// fallback formula instead.
const FIVE_ABSENT_REASONS = [
  'voluntaria',
  'despido_preaviso',
  'sin_prev',
  'injustificada',
  'indem_25',
];

const acumuladosAbsentCases = FIVE_ABSENT_REASONS.map((reason) => ({
  name: `reason-${reason}-acumulados-absent`,
  note:
    `Termination reason "${reason}", all five acumulados inputs at 0 so ` +
    'vacProp/decProp/primaTotal/indem6mMensual/sal30 all take their ' +
    'statutory-default fallback formula.',
  employee: employee({ salary: 900, start_date: '2021-07-01' }),
  elements: elements({
    date: '2024-07-01',
    reason,
    salPend: 900,
    otros: 0,
    vac: 8,
    dec: 1,
  }),
  dedChecks: [],
}));

// ── Matrix 3: five single-branch cases ──────────────────────────────────
// Exactly ONE acumulados field > 0 per case, proving the five checks are
// independent — not one shared "acumulados present" flag.
const sharedSingleBranchEmployee = employee({ salary: 1000, start_date: '2020-01-01' });
const sharedSingleBranchElements = {
  date: '2024-01-01', // 4 years of service, floorYears=4
  reason: 'voluntaria',
  salPend: 500,
  otros: 0,
  vac: 10,
  dec: 1,
};

const singleBranchCases = [
  {
    name: 'only-acum-vac',
    note: 'Only acumVac > 0 — vacProp uses the acumulados formula; decProp/primaTotal/indem6mMensual/sal30 all use their fallback.',
    acum: { acumVac: 5000 },
  },
  {
    name: 'only-acum-dec',
    note: 'Only acumDec > 0 — decProp uses the acumulados formula; the other four branches fall back.',
    acum: { acumDec: 6000 },
  },
  {
    name: 'only-acum-prima',
    note: 'Only acumPrima > 0 — primaTotal uses the acumulados formula; the other four branches fall back.',
    acum: { acumPrima: 20000 },
  },
  {
    name: 'only-acum-6m',
    note: 'Only acum6m > 0 — indem6mMensual uses the acumulados formula (acum6m/6); the other four branches fall back.',
    acum: { acum6m: 5400 },
  },
  {
    name: 'only-sal30',
    note: 'Only sal30Raw > 0 — sal30 uses sal30Raw directly; the other four branches fall back (including indem6mMensual, which still falls back to `sal`).',
    acum: { sal30: 1050 },
  },
].map(({ name, note, acum }) => ({
  name,
  note,
  employee: sharedSingleBranchEmployee,
  elements: elements({ ...sharedSingleBranchElements, ...acum }),
  dedChecks: [],
}));

// ── indemWeeks boundary (floorYears<=10 ? floorYears : 10+(floorYears-10)*2) ─
const indemWeeksCases = [
  {
    name: 'indem-weeks-under-10',
    note: 'floorYears=8 (<=10) — indemWeeks=floorYears=8, the 1-week-per-year bracket.',
    employee: employee({ salary: 1000, start_date: '2016-01-01' }),
    elements: elements({ date: '2024-01-01', reason: 'injustificada', salPend: 1000, vac: 5, dec: 1 }),
    dedChecks: [],
  },
  {
    name: 'indem-weeks-over-10',
    note: 'floorYears=15 (>10) — indemWeeks=10+(15-10)*2=20, the 2-weeks-per-year-beyond-10 bracket.',
    employee: employee({ salary: 1000, start_date: '2009-01-01' }),
    elements: elements({ date: '2024-01-01', reason: 'indem_50', salPend: 1000, vac: 5, dec: 1 }),
    dedChecks: [],
  },
];

// ── Art. 701 discount clamp: art701Sujeta = max(0, base - base*(floorYears/100) - 5000) ─
const art701Case = {
  name: 'art701-sujeta-clamped-to-zero',
  note:
    'Low salary (400) + short service (floorYears=3) keeps art701Base ' +
    '(antigSemNeta+indemnizacion, both small here since reason=voluntaria ' +
    'has indemnizacion=0) well under the 5000 exemption, so ' +
    'art701Sujeta=max(0, base - base*0.03 - 5000) clamps to exactly 0.',
  employee: employee({ salary: 400, start_date: '2021-02-01' }),
  elements: elements({ date: '2024-02-01', reason: 'voluntaria', salPend: 400 }),
  dedChecks: [],
};

// ── Hazard 3 anchor: isrRate at annual=0 ──────────────────────────────────
// IMPORTANT FINDING (see tools/goldens/README or apply-progress notes):
// the design/spec's hazard-3 claim ("isrRate = isrAnual/annual produces
// NaN at salary=0, and that NaN is persisted") does NOT reproduce against
// the actual current source. hr_admin_panel.html:3669 already reads
// `var isrRate = annual>0 ? isrAnual/annual : 0;` — an explicit zero-guard
// that has been in place since this feature's introducing commit
// (739ac1d, "feat(liquidation): add CSS/SE/ISR legal deductions"). At
// salary=0, annual=0, isrAnual=0 (0<=11000 bracket) and isrRate=0 — no NaN
// is ever computed or persisted. This fixture is kept (per the task's
// explicit instruction) and still pins the real, verified JS behavior —
// but `expected_deviation` is intentionally empty because there is no
// deviation: Go returning 0 here matches the JS exactly, it does not
// diverge from it. 3b/3c's implementer should NOT treat this as a hazard
// requiring special handling; `Num.Div` by zero still returns an error for
// defensive safety, but this exact code path in calcLiq never reaches it.
const zeroSalaryCase = {
  name: 'zero-salary-nan',
  note:
    'salary=0, acumulados absent, floorYears=2. Real JS output: isrRate=0, ' +
    'NOT NaN (see the long-form explanation above and in js_source_sha ' +
    'provenance). Every derived field is exactly 0.',
  employee: employee({ salary: 0, start_date: '2022-01-01' }),
  elements: elements({ date: '2024-01-01', reason: 'voluntaria', salPend: 0, otros: 0 }),
  dedChecks: [],
  expectedDeviation: {},
  designClaimMismatch:
    'design doc R1d / spec hazard 3 claims isrRate=NaN at salary=0; the ' +
    'actual source at hr_admin_panel.html:3669 already guards this and ' +
    'returns 0. No NaN is ever produced by the real, current JS.',
};

// ── Hazard 4 anchor: primaMonths >= 1 clamp (acumulados-absent branch) ────
const oneMonthServiceCase = {
  name: 'one-month-service',
  note:
    '1 month of service (2025-05-15 -> 2025-06-15): totalMonths=1, ' +
    'floorYears=round(1/12)=0, so the acumulados-absent primaMonths ' +
    'formula min(floorYears*12,60)=min(0,60)=0 BEFORE the `if(primaMonths<1) ' +
    'primaMonths=1` clamp fires. Without the clamp, primaMensual=primaTotal/0 ' +
    'would divide by zero.',
  employee: employee({ salary: 600, start_date: '2025-05-15' }),
  elements: elements({ date: '2025-06-15', reason: 'voluntaria', salPend: 0, otros: 0 }),
  dedChecks: [],
};

export const calcliqCases = [
  ...acumuladosPresentCases,
  ...acumuladosAbsentCases,
  ...singleBranchCases,
  ...indemWeeksCases,
  art701Case,
  zeroSalaryCase,
  oneMonthServiceCase,
];
