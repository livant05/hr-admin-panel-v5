// tools/overtime/harness.mjs
//
// Drives the REAL overtime screen code of hr_admin_panel.html (renderOTTable,
// saveOT, delOT, plus apiErrMsg) in a node:vm sandbox with fake Supa/DOM/toast.
// Sources are sliced out of the HTML by name and executed as written.
//
// Outside demo mode DS.overtime_logs is emptied at sign-in and never refilled,
// so the table must read the logs from the API; saving and deleting must report
// the server's outcome instead of a fake success.
//
// Run: node tools/overtime/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.OVERTIME_HTML ||
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
  extractMaybeAsync(html, 'function renderOTTable('),
  extractFunction(html, 'async function saveOT('),
  extractFunction(html, 'async function delOT('),
  extractFunction(html, 'function apiErrMsg('),
].join('\n');

const fmt = (v) => 'B/. ' + Number(v || 0).toFixed(2);
const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const OTS = [
  { id: 'o1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', date: '2026-09-02', hours: 2, type: 'regular', amount: 20 },
  { id: 'o2', company_id: 'c1', employee_id: 'e2', employee_name: 'Luis Gomez', date: '2026-10-01', hours: 1.5, type: 'night', amount: 30.5 },
];

function makeEnv({ ots = OTS, insResult = null, delResult = null } = {}) {
  const calls = [], toasts = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', textContent: '' });
  el('ot-emp').value = 'e1';
  el('ot-emp').selectedIndex = 0;
  el('ot-emp').options = [{ text: 'Ana Diaz', getAttribute: (k) => ({ 'data-sal': '1000', 'data-wh': '48' }[k]) }];
  el('ot-hrs').value = '2';
  el('ot-type').value = 'regular';
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { overtime_logs: [] },
    TS: '2026-10-14',
    OT_RATES: { regular: 1.25, mixed: 1.5, night: 1.75, sunday: 1.5, holiday: 2.5 },
    API_FIELD_ES: {},
    API_CONFLICT_ES: {},
    fmt, escHtml,
    gv: (id) => el(id).value,
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => { calls.push({ fn: 'sel', tbl, flt }); return tbl === 'overtime_logs' ? ots : []; },
      selAll: async (tbl, flt) => { calls.push({ fn: 'selAll', tbl, flt }); return tbl === 'overtime_logs' ? ots : []; },
      ins: async (tbl, data) => { calls.push({ fn: 'ins', tbl, data }); return insResult || [Object.assign({ id: 'new' }, data)]; },
      del: async (tbl, id) => { calls.push({ fn: 'del', tbl, id }); return delResult; },
    },
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { el, calls, toasts, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('renderOTTable lists the overtime logs from the API, newest first, with the total', async () => {
  const e = makeEnv();
  await e.run('renderOTTable()');
  const rows = e.el('ot-tbody').innerHTML.split('</tr>').filter(Boolean);
  assert.equal(rows.length, 2);
  assert.match(rows[0], /Luis Gomez/);
  assert.match(rows[1], /Ana Diaz/);
  assert.match(e.el('ot-tfoot').innerHTML, /B\/\. 50\.50/);
});

test('renderOTTable with no logs leaves the footer empty (unchanged)', async () => {
  const e = makeEnv({ ots: [] });
  await e.run('renderOTTable()');
  assert.equal(e.el('ot-tfoot').innerHTML, '');
});

test('saveOT rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ insResult: { error: { code: 'validation_failed', message: 'validation failed' } } });
  await e.run('saveOT()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('saveOT success keeps its toast (unchanged)', async () => {
  const e = makeEnv();
  await e.run('saveOT()');
  assert.deepEqual(e.toasts, [{ msg: '✅ Horas extras guardadas', type: 'success' }]);
});

test('delOT rejected by the server shows the error, never a fake success', async () => {
  const e = makeEnv({ delResult: { error: { code: 'not_found', message: 'not found' } } });
  await e.run("delOT('o1')");
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
});

test('delOT success keeps the info toast (unchanged)', async () => {
  const e = makeEnv();
  await e.run("delOT('o1')");
  assert.deepEqual(e.toasts, [{ msg: 'Eliminado', type: 'info' }]);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
