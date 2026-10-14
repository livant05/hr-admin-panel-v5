// tools/attendance/harness.mjs
//
// Drives the REAL attendance screen code of hr_admin_panel.html (saveAttendance,
// delAtt, the monthly report loadAttReport, plus apiErrMsg) in a node:vm
// sandbox with fake Supa/DOM/toast. Sources
// are sliced out of the HTML by name and executed as written.
//
// Outside demo mode DS.attendance_logs is emptied at sign-in and never
// refilled, so the "is there already a log for this employee and day" check
// must ask the API; saving and deleting must report the server's outcome
// instead of a fake success.
//
// Run: node tools/attendance/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.ATTENDANCE_HTML ||
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

// The function may or may not be declared async; keep the keyword if present.
function extractMaybeAsync(src, header) {
  const fn = extractFunction(src, header);
  const at = src.indexOf(header);
  return src.slice(Math.max(0, at - 6), at) === 'async ' ? 'async ' + fn : fn;
}

const srcs = [
  extractFunction(html, 'async function saveAttendance('),
  extractFunction(html, 'async function delAtt('),
  extractFunction(html, 'function apiErrMsg('),
  extractMaybeAsync(html, 'function loadAttReport('),
].join('\n');

const LOGS = [{ id: 'a1', company_id: 'c1', employee_id: 'e1', date: '2026-10-14', status: 'present' }];

function makeEnv({ logs = LOGS, emps = [], demo = false, insResult = null, updResult = null, delResult = null } = {}) {
  const calls = [], toasts = [], reloads = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '' });
  el('att-emp').value = 'e1';
  el('att-emp').selectedIndex = 0;
  el('att-emp').options = [{ getAttribute: (k) => ({ 'data-name': 'Ana Diaz', 'data-dept': 'Ventas' }[k]) }];
  el('att-date').value = '2026-10-14';
  el('att-in').value = '08:00'; el('att-out').value = '17:00'; el('att-st').value = 'present';
  // date_from/date_to behave like the API (inclusive range); every other key is an exact match
  const pick = (rows, flt) => rows.filter((r) => Object.entries(flt || {}).every(([k, v]) =>
    k === 'date_from' ? r.date >= v : k === 'date_to' ? r.date <= v : r[k] === v));
  const sandbox = {
    A: { companyId: 'c1', demo },
    DS: { attendance_logs: [], employees: [] },
    MONTHS: ['Enero','Febrero','Marzo','Abril','Mayo','Junio','Julio','Agosto','Septiembre','Octubre','Noviembre','Diciembre'],
    TD: new Date('2026-10-14T12:00:00Z'),
    escHtml: (v) => String(v ?? ''),
    API_FIELD_ES: {},
    gv: (id) => el(id).value,
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => { calls.push({ fn: 'sel', tbl, flt }); return pick({ attendance_logs: logs, employees: emps }[tbl] || [], flt); },
      selAll: async (tbl, flt) => { calls.push({ fn: 'selAll', tbl, flt }); return pick({ attendance_logs: logs, employees: emps }[tbl] || [], flt); },
      ins: async (tbl, data) => { calls.push({ fn: 'ins', tbl, data }); return insResult || [Object.assign({ id: 'new' }, data)]; },
      upd: async (tbl, id, data) => { calls.push({ fn: 'upd', tbl, id, data }); return updResult || [Object.assign({ id }, data)]; },
      del: async (tbl, id) => { calls.push({ fn: 'del', tbl, id }); return delResult; },
    },
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    loadAttendance: () => { reloads.push(1); },
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { el, calls, toasts, reloads, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('saveAttendance finds the existing log through the API and updates it', async () => {
  const e = makeEnv();
  await e.run('saveAttendance()');
  const upd = e.calls.find((c) => c.fn === 'upd');
  assert.ok(upd, 'the existing log must be updated, not inserted again');
  assert.equal(upd.id, 'a1');
  assert.equal(e.calls.filter((c) => c.fn === 'ins').length, 0);
  assert.deepEqual(e.toasts, [{ msg: '✅ Registro actualizado', type: 'success' }]);
});

test('saveAttendance with no log that day inserts it (unchanged)', async () => {
  const e = makeEnv({ logs: [] });
  await e.run('saveAttendance()');
  assert.equal(e.calls.filter((c) => c.fn === 'ins').length, 1);
  assert.deepEqual(e.toasts, [{ msg: '✅ Asistencia registrada', type: 'success' }]);
});

test('saveAttendance rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ logs: [], insResult: { error: { code: 'validation_failed', message: 'validation failed' } } });
  await e.run('saveAttendance()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
  assert.equal(e.reloads.length, 0);
});

test('saveAttendance update rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ updResult: { error: { code: 'not_found', message: 'not found' } } });
  await e.run('saveAttendance()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('delAtt rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ delResult: { error: { code: 'not_found', message: 'not found' } } });
  await e.run("delAtt('a1')");
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('delAtt success keeps the info toast (unchanged)', async () => {
  const e = makeEnv();
  await e.run("delAtt('a1')");
  assert.deepEqual(e.toasts, [{ msg: 'Eliminado', type: 'info' }]);
});

const EMPS = [
  { id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Diaz', department: 'Ventas', status: 'active' },
  { id: 'e2', company_id: 'c1', first_name: 'Luis', last_name: 'Gomez', department: null, status: 'active' },
  { id: 'e3', company_id: 'c1', first_name: 'Old', last_name: 'Timer', department: 'X', status: 'inactive' },
];
const MONTH_LOGS = [
  { id: 'm1', company_id: 'c1', employee_id: 'e1', date: '2026-10-01', status: 'present' },
  { id: 'm2', company_id: 'c1', employee_id: 'e1', date: '2026-10-31', status: 'late' },
  { id: 'm3', company_id: 'c1', employee_id: 'e1', date: '2026-09-30', status: 'present' },
  { id: 'm4', company_id: 'c1', employee_id: 'e2', date: '2026-10-15', status: 'absent' },
  { id: 'm5', company_id: 'c1', employee_id: 'e2', date: '2026-11-01', status: 'absent' },
  { id: 'm6', company_id: 'c1', employee_id: 'e2', date: '2026-10-20', status: 'permission' },
];
const reportRows = (e) => e.el('att-rep-content').innerHTML.split('<tbody>')[1].split('</tr>').filter((r) => r.includes('<td>'));

test('monthly report counts each active employee from the API, first and last day of the month included', async () => {
  const e = makeEnv({ logs: MONTH_LOGS, emps: EMPS });
  e.el('att-r-month').value = '10'; e.el('att-r-year').value = '2026';
  await e.run('loadAttReport()');
  const rows = reportRows(e);
  assert.equal(rows.length, 2, 'inactive employees are left out');
  assert.match(rows[0], /<strong>Ana Diaz<\/strong>.*text-success">1<.*text-warning">1<.*text-danger">0<.*text-info">0</);
  assert.match(rows[1], /<strong>Luis Gomez<\/strong><\/td><td>—<.*text-success">0<.*text-warning">0<.*text-danger">1<.*text-info">1</);
  assert.match(e.el('att-rep-content').innerHTML, /Asistencia — Octubre 2026/);
});

test('monthly report asks the API for that month only', async () => {
  const e = makeEnv({ logs: MONTH_LOGS, emps: EMPS });
  e.el('att-r-month').value = '2'; e.el('att-r-year').value = '2028';
  await e.run('loadAttReport()');
  const q = e.calls.find((c) => c.tbl === 'attendance_logs');
  assert.deepEqual(JSON.parse(JSON.stringify(q.flt)), { date_from: '2028-02-01', date_to: '2028-02-29' });
});

test('monthly report in demo mode still works on the local store', async () => {
  const e = makeEnv({ logs: MONTH_LOGS, emps: EMPS, demo: true });
  e.el('att-r-month').value = '10'; e.el('att-r-year').value = '2026';
  await e.run('loadAttReport()');
  assert.equal(reportRows(e).length, 2);
  assert.match(reportRows(e)[0], /text-success">1</);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
