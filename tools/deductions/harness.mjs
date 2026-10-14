// tools/deductions/harness.mjs
//
// Drives the REAL deductions code of hr_admin_panel.html (renderDedTable for
// the Deducciones table, saveDed/saldaDed/delDed for its actions, loadLiqEmp
// for the installments and the vacation balance offered in a liquidation) in a
// node:vm sandbox with fake Supa/DOM. Sources are sliced out
// of the HTML by name and executed as written.
//
// Outside demo mode DS is emptied at sign-in and never refilled, so these
// screens must read deductions and leave balances from the API (Supa), not DS.
//
// Run: node tools/deductions/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.DEDUCTIONS_HTML ||
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

const srcs = [
  extractFunction(html, 'async function renderDedTable('),
  extractFunction(html, 'async function loadLiqEmp('),
  extractFunction(html, 'function vacTotalsByEmployee('),
  extractFunction(html, 'async function saveDed('),
  extractFunction(html, 'async function saldaDed('),
  extractFunction(html, 'async function delDed('),
  extractFunction(html, 'function apiErrMsg('),
].join('\n');

const fmt = (v) => 'B/. ' + Number(v || 0).toFixed(2); // as the page's fmt
const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const EMP = { id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Diaz', salary: 1000, status: 'active', start_date: '2020-01-01' };
const API_DEDS = [
  { id: 'd1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', acreedor_nombre: 'Banco Uno', quota: 100, remaining: 900, status: 'active', description: 'Prestamo' },
  { id: 'd2', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', acreedor_nombre: 'Banco Dos', quota: 50, remaining: 0, status: 'paid', description: 'Viejo' },
  { id: 'd3', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', acreedor_nombre: 'Banco Tres', quota: 70, remaining: 700, status: 'cancelled', description: 'Borrada' },
];
// One leave_balances row per employee and year; leftovers are not carried over.
const API_BALS = [
  { id: 'b1', company_id: 'c1', employee_id: 'e1', year: 2025, earned_days: 30, used_days: 25, remaining_days: 5 },
  { id: 'b2', company_id: 'c1', employee_id: 'e1', year: 2026, earned_days: 22.5, used_days: 15, remaining_days: 7.5 },
  { id: 'b3', company_id: 'c1', employee_id: 'e2', year: 2026, earned_days: 30, used_days: 0, remaining_days: 30 },
];
// The PATCH body fields the API accepts (deductionRequest, DisallowUnknownFields).
const PATCH_FIELDS = ['employee_id', 'employee_name', 'type', 'description', 'total_amount', 'quota', 'remaining', 'start_date', 'status', 'cedula',
  'acreedor_nombre', 'acreedor_codigo', 'tipo_pago', 'centro_costo', 'prioridad', 'end_date', 'periodo', 'numero_planilla'];

function makeEnv({ deds = API_DEDS, bals = API_BALS, insResult = null, updResult = null, delResult = null } = {}) {
  const calls = [], toasts = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', style: { display: 'none' }, classList: { add() {} } });
  el('liq-emp').value = 'e1';
  const pick = (rows, flt) => !Array.isArray(rows) ? rows : rows.filter((r) => Object.entries(flt || {}).every(([k, v]) => r[k] === v));
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { deductions: [], leave_balances: [] },
    fmt, escHtml,
    seniStr: () => '1 año',
    gv: (id) => el(id).value,
    getEmpPayBases: async () => ({ months: 0 }),
    calcLiq: () => {},
    TS: '2026-10-14',
    API_FIELD_ES: { quota: 'cuota' },
    API_CONFLICT_ES: {},
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => { calls.push({ fn: 'sel', tbl, flt }); return pick({ employees: [EMP], deductions: deds, leave_balances: bals }[tbl] || [], flt); },
      selAll: async (tbl, flt) => { calls.push({ fn: 'selAll', tbl, flt }); return pick({ employees: [EMP], deductions: deds, leave_balances: bals }[tbl] || [], flt); },
      ins: async (tbl, data) => { calls.push({ fn: 'ins', tbl, data }); return insResult || [Object.assign({ id: 'new' }, data)]; },
      upd: async (tbl, id, data) => { calls.push({ fn: 'upd', tbl, id, data }); return updResult || [Object.assign({}, data, { id })]; },
      del: async (tbl, id) => { calls.push({ fn: 'del', tbl, id }); return delResult; },
    },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    confirm: () => true,
    document: { getElementById: el },
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { sandbox, calls, toasts, el, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('renderDedTable lists the deductions from the API (DS is empty outside demo mode)', async () => {
  const e = makeEnv();
  await e.run('renderDedTable()');
  const tb = e.el('ded-tbody').innerHTML;
  assert.match(tb, /Banco Uno/);
  assert.match(tb, /Banco Dos/);
  assert.match(tb, /B\/\. 100\.00/);
  assert.ok(e.calls.some((c) => c.tbl === 'deductions'), 'deductions must be requested from the API');
});

test('renderDedTable with no deductions shows the empty-state row (unchanged)', async () => {
  const e = makeEnv({ deds: [] });
  await e.run('renderDedTable()');
  assert.match(e.el('ded-tbody').innerHTML, /Sin deducciones/);
});

test('renderDedTable leaves out deleted (cancelled) deductions', async () => {
  const e = makeEnv();
  await e.run('renderDedTable()');
  assert.doesNotMatch(e.el('ded-tbody').innerHTML, /Banco Tres/);
});

test('saldaDed sends the whole editable row (the PATCH replaces every column) with status paid and nothing left', async () => {
  const e = makeEnv();
  await e.run("saldaDed('d1')");
  const upd = e.calls.find((c) => c.fn === 'upd');
  assert.ok(upd, 'no PATCH sent');
  assert.equal(upd.id, 'd1');
  assert.deepEqual(Object.keys(upd.data).filter((k) => !PATCH_FIELDS.includes(k)), [], 'unknown fields are a 400');
  assert.equal(upd.data.status, 'paid');
  assert.equal(upd.data.remaining, 0);
  assert.equal(upd.data.quota, 100);
  assert.equal(upd.data.acreedor_nombre, 'Banco Uno');
  assert.deepEqual(e.toasts, [{ msg: 'Marcada como saldada', type: 'success' }]);
});

test('saldaDed rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ updResult: { error: { code: 'validation_failed', message: 'validation failed' } } });
  await e.run("saldaDed('d1')");
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('delDed rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ delResult: { error: { code: 'not_found', message: 'not found' } } });
  await e.run("delDed('d1')");
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('delDed success keeps the info toast (unchanged)', async () => {
  const e = makeEnv();
  await e.run("delDed('d1')");
  assert.deepEqual(e.toasts, [{ msg: 'Eliminada', type: 'info' }]);
});

const fillDed = (e) => {
  e.el('ded-emp').value = 'e1'; e.el('ded-quota').value = '100'; e.el('ded-acreedor').value = 'Banco Uno';
  e.el('ded-total').value = '1200'; e.el('ded-start').value = '2026-10-01';
};

test('saveDed rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ insResult: { error: { code: 'validation_failed', message: 'validation failed', fields: { quota: 'required' } } } });
  fillDed(e);
  await e.run('saveDed()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('saveDed success keeps its toast and sends the deduction (unchanged)', async () => {
  const e = makeEnv();
  fillDed(e);
  await e.run('saveDed()');
  const ins = e.calls.find((c) => c.fn === 'ins');
  assert.ok(ins, 'no POST sent');
  assert.deepEqual(Object.keys(ins.data).filter((k) => !PATCH_FIELDS.includes(k)), [], 'unknown fields are a 400');
  assert.equal(ins.data.quota, 100);
  assert.equal(ins.data.remaining, 1200);
  assert.equal(ins.data.status, 'active');
  assert.deepEqual(e.toasts, [{ msg: 'Deducción guardada', type: 'success' }]);
});

test('loadLiqEmp offers the employee active installments from the API', async () => {
  const e = makeEnv();
  await e.run('loadLiqEmp()');
  const list = e.el('liq-ded-list').innerHTML;
  assert.match(list, /value="d1"/);
  assert.match(list, /data-quota="100"/);
  assert.doesNotMatch(list, /B\/\. B\/\./, 'currency prefix printed twice');
  assert.doesNotMatch(list, /value="d2"/);
  assert.equal(e.el('liq-ded-wrap').style.display, '');
});

test('loadLiqEmp proposes the unused vacation days of every year from the API', async () => {
  const e = makeEnv();
  await e.run('loadLiqEmp()');
  assert.equal(Number(e.el('liq-vac').value), 12.5);
});

test('loadLiqEmp leaves the vacation field alone when the employee has no balance (unchanged)', async () => {
  const e = makeEnv({ bals: [] });
  e.el('liq-vac').value = '3';
  await e.run('loadLiqEmp()');
  assert.equal(e.el('liq-vac').value, '3');
});

test('loadLiqEmp hides the installments box when the employee has none (unchanged)', async () => {
  const e = makeEnv({ deds: [] });
  await e.run('loadLiqEmp()');
  assert.equal(e.el('liq-ded-wrap').style.display, 'none');
});

test('renderDedTable shows a failed read as an error, not as "Sin deducciones"', async () => {
  const e = makeEnv({ deds: { error: { code: 'internal', message: 'internal error' } } });
  await e.run('renderDedTable()');
  const tb = e.el('ded-tbody').innerHTML;
  assert.doesNotMatch(tb, /Sin deducciones/);
  assert.match(tb, /No se pudieron cargar las deducciones: error interno del servidor/);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
