// tools/attendance/harness.mjs
//
// Drives the REAL attendance screen code of hr_admin_panel.html (saveAttendance,
// delAtt, plus apiErrMsg) in a node:vm sandbox with fake Supa/DOM/toast. Sources
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

const srcs = [
  extractFunction(html, 'async function saveAttendance('),
  extractFunction(html, 'async function delAtt('),
  extractFunction(html, 'function apiErrMsg('),
].join('\n');

const LOGS = [{ id: 'a1', company_id: 'c1', employee_id: 'e1', date: '2026-10-14', status: 'present' }];

function makeEnv({ logs = LOGS, insResult = null, updResult = null, delResult = null } = {}) {
  const calls = [], toasts = [], reloads = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '' });
  el('att-emp').value = 'e1';
  el('att-emp').selectedIndex = 0;
  el('att-emp').options = [{ getAttribute: (k) => ({ 'data-name': 'Ana Diaz', 'data-dept': 'Ventas' }[k]) }];
  el('att-date').value = '2026-10-14';
  el('att-in').value = '08:00'; el('att-out').value = '17:00'; el('att-st').value = 'present';
  const pick = (rows, flt) => rows.filter((r) => Object.entries(flt || {}).every(([k, v]) => r[k] === v));
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { attendance_logs: [] },
    API_FIELD_ES: {},
    gv: (id) => el(id).value,
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => { calls.push({ fn: 'sel', tbl, flt }); return tbl === 'attendance_logs' ? pick(logs, flt) : []; },
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

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
