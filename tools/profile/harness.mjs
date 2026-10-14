// tools/profile/harness.mjs
//
// Drives the REAL showEmpProfile of hr_admin_panel.html (employee profile
// modal) in a node:vm sandbox with fake Supa/DOM/jQuery. The source is sliced
// out of the HTML by name and executed as written.
//
// Covers the "Vacaciones saldo" line: outside demo mode DS.leave_balances is
// emptied at sign-in and never refilled, so the balance must come from the API.
// leave_balances holds one row per employee and year with no carry-over, so
// the profile shows the sum over all years (the same rule as the liquidation).
//
// Run: node tools/profile/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.PROFILE_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

// Source of `header ... { ... }` by brace matching that skips strings/comments.
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

const src = extractFunction(html, 'async function showEmpProfile(');

const fmt = (v) => 'B/. ' + Number(v || 0).toFixed(2);
const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const EMP = { id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Diaz', salary: 1000, status: 'inactive', start_date: '2020-01-01' };
const API_BALS = [
  { id: 'b1', company_id: 'c1', employee_id: 'e1', year: 2025, earned_days: 30, used_days: 25, remaining_days: 5 },
  { id: 'b2', company_id: 'c1', employee_id: 'e1', year: 2026, earned_days: 15, used_days: 5, remaining_days: 10 },
  { id: 'b3', company_id: 'c1', employee_id: 'e2', year: 2026, earned_days: 30, used_days: 0, remaining_days: 30 },
];

function makeEnv({ bals = API_BALS } = {}) {
  const els = {};
  const el = (id) => (els[id] ??= { id, innerHTML: '', onclick: null });
  const pick = (rows, flt) => rows.filter((r) => Object.entries(flt || {}).every(([k, v]) => r[k] === v));
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { leave_balances: [] },
    fmt, escHtml,
    TD: new Date('2026-10-14T12:00:00Z'),
    seniStr: () => '6 años', daysTo: () => null, empAvatar: () => '<i></i>',
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => pick({ employees: [EMP], leave_balances: bals }[tbl] || [], flt),
      goFetch: async () => [],
      gq: () => '',
    },
    document: { getElementById: el },
    $: () => ({ modal() {} }),
  };
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox);
  return { el, run: (c) => vm.runInContext(c, sandbox) };
}

const vacLine = (body) => (body.match(/Vacaciones saldo<\/small>(.*?)<\/div><\/div><\/div>/) || [])[1] || '';

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('profile shows the unused and earned vacation days of every year from the API', async () => {
  const e = makeEnv();
  await e.run("showEmpProfile('e1')");
  const line = vacLine(e.el('emp-modal-body').innerHTML);
  assert.match(line, /<strong>15 días de 45 ganados<\/strong>/);
  assert.match(line, /width:33%/);
});

test('profile with nothing earned yet shows 0% instead of NaN%', async () => {
  const e = makeEnv({ bals: [{ id: 'b1', company_id: 'c1', employee_id: 'e1', year: 2026, earned_days: 0, used_days: 0, remaining_days: 0 }] });
  await e.run("showEmpProfile('e1')");
  const line = vacLine(e.el('emp-modal-body').innerHTML);
  assert.match(line, /<strong>0 días de 0 ganados<\/strong>/);
  assert.match(line, /width:0%/);
});

test('profile without balance rows shows "Sin registro" (unchanged)', async () => {
  const e = makeEnv({ bals: [] });
  await e.run("showEmpProfile('e1')");
  assert.match(vacLine(e.el('emp-modal-body').innerHTML), /<strong>Sin registro<\/strong>/);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
