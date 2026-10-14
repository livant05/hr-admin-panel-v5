// tools/reports/harness.mjs
//
// Drives the REAL vacation pieces of hr_admin_panel.html: the "Vacaciones"
// report (loadReport with rpt-type=vac) and the "días de vacaciones sin usar"
// alert (calcAlerts), in a node:vm sandbox with fake Supa/DOM. Sources are
// sliced out of the HTML by name and executed as written.
//
// Outside demo mode DS.leave_balances is emptied at sign-in and never
// refilled, so both must read the balances from the API. leave_balances holds
// one row per employee and year with no carry-over, so both show one total per
// employee summed over every year (the rule used by the liquidation and the
// profile).
//
// Run: node tools/reports/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.REPORTS_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

// Source of `header ... { ... }` by brace matching that skips strings/comments.
// Only used on functions without regex literals.
function extractFunction(src, header) {
  const at = src.indexOf(header);
  assert.ok(at >= 0, `${header} not found in hr_admin_panel.html`);
  let i = src.indexOf('{', at);
  let depth = 0;
  for (; i < src.length; i++) {
    const c = src[i];
    if (c === '"' || c === "'" || c === '`') {
      for (i++; src[i] !== c; i++) if (src[i] === '\\') i++;
    } else if (c === '/' && src[i + 1] === '/') {
      i = src.indexOf('\n', i);
    } else if (c === '/' && src[i + 1] === '*') {
      i = src.indexOf('*/', i) + 1;
    } else if (c === '{') depth++;
    else if (c === '}' && --depth === 0) return src.slice(at, i + 1);
  }
  throw new Error(`unbalanced braces after ${header}`);
}
const optional = (header) => (html.includes(header) ? extractFunction(html, header) : '');

const srcs = [
  optional('function vacTotalsByEmployee('),
  extractFunction(html, 'async function loadReport('),
  extractFunction(html, 'async function calcAlerts('),
].join('\n');

const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const BALS = [
  { id: 'b1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', year: 2025, earned_days: 30, used_days: 18, remaining_days: 12 },
  { id: 'b2', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', year: 2026, earned_days: 15, used_days: 4, remaining_days: 11 },
  { id: 'b3', company_id: 'c1', employee_id: 'e2', employee_name: 'Luis Gomez', year: 2026, earned_days: 15, used_days: 7, remaining_days: 8 },
];

function makeEnv({ bals = BALS } = {}) {
  const charts = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', textContent: '' });
  el('rpt-type').value = 'vac';
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { leave_balances: [], overtime_logs: [] },
    escHtml,
    daysTo: () => null,
    renderRptChart: (labels, data, label) => charts.push({ labels, data, label }),
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl) => ({ employees: [], leave_requests: [], leave_balances: bals }[tbl] || []),
      selAll: async (tbl) => ({ employees: [], leave_requests: [], leave_balances: bals }[tbl] || []),
    },
    document: { getElementById: el },
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { el, charts, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('Vacaciones report lists one row per employee with the earned and unused days of every year, from the API', async () => {
  const e = makeEnv();
  await e.run('loadReport()');
  const rows = e.el('rpt-tbody').innerHTML.split('</tr>').filter(Boolean);
  assert.equal(rows.length, 2);
  assert.match(rows[0], /<td>Ana Diaz<\/td><td>45<\/td><td><span class="badge badge-success">23<\/span>/);
  assert.match(rows[1], /<td>Luis Gomez<\/td><td>15<\/td><td><span class="badge badge-warning">8<\/span>/);
  assert.deepEqual(JSON.parse(JSON.stringify(e.charts.at(-1))), { labels: ['Ana Diaz', 'Luis Gomez'], data: [23, 8], label: 'Días Disponibles' });
});

test('Vacaciones report with no balances renders an empty table (unchanged)', async () => {
  const e = makeEnv({ bals: [] });
  await e.run('loadReport()');
  assert.equal(e.el('rpt-tbody').innerHTML, '');
});

test('alerts flag an employee whose unused days over every year exceed 20, from the API', async () => {
  const e = makeEnv();
  const alerts = await e.run('calcAlerts()');
  const vac = alerts.filter((a) => a.cat === 'vacbal');
  assert.equal(vac.length, 1);
  assert.match(vac[0].msg, /<strong>Ana Diaz<\/strong> tiene <strong>23 días<\/strong>/);
});

test('alerts stay quiet when nobody exceeds 20 unused days (unchanged)', async () => {
  const e = makeEnv({ bals: [BALS[2]] });
  const alerts = await e.run('calcAlerts()');
  assert.equal(alerts.filter((a) => a.cat === 'vacbal').length, 0);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
