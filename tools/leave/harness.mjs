// tools/leave/harness.mjs
//
// Drives the REAL saveLeaveReq / approveLeave of hr_admin_panel.html (plus the
// Supa adapter, gv and apiErrMsg) in a node:vm sandbox with a fake fetch, DOM
// and toast. Nothing is reimplemented: sources are sliced out of the HTML and
// executed as written.
//
// Regression guard for the silent data loss where saveLeaveReq sent
// `status:'pending'` (rejected by the Go handler's DisallowUnknownFields with a
// 400) and still toasted success. Also covers approveLeave's error handling,
// demo mode (local only, zero network calls), and the demo-mode Supa.sel
// date_from/date_to range that the attendance history filter relies on.
//
// Run: node tools/leave/harness.mjs   (exit code 1 on any failed assertion)
// Mutation runs point LEAVE_HTML at a modified copy of the page.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.LEAVE_HTML ||
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
function between(startMarker, endMarker) {
  const s = html.indexOf(startMarker);
  assert.ok(s >= 0, `${startMarker} not found`);
  const e = html.indexOf(endMarker, s);
  assert.ok(e > s, `${endMarker} not found after ${startMarker}`);
  return html.slice(s, e);
}

// exportVacCSV may or may not be declared async; keep the keyword if present.
function extractMaybeAsync(src, header) {
  const fn = extractFunction(src, header);
  const at = src.indexOf(header);
  return src.slice(Math.max(0, at - 6), at) === 'async ' ? 'async ' + fn : fn;
}
const supaSrc = between('var Supa = {', '\n};\n') + '\n};\n';
const fieldsSrc = between('var API_FIELD_ES=', '};') + '};';
const conflictsSrc = between('var API_CONFLICT_ES=', '};') + '};';
const srcs = [
  supaSrc, fieldsSrc, conflictsSrc,
  extractFunction(html, 'function gv('),
  extractFunction(html, 'function apiErrMsg('),
  extractFunction(html, 'async function saveLeaveReq('),
  extractFunction(html, 'async function approveLeave('),
  extractMaybeAsync(html, 'function exportVacCSV('),
  extractFunction(html, 'function csvCell('),
  extractFunction(html, 'function csvText('),
].join('\n');

const res = (status, body) => ({
  status, ok: status >= 200 && status < 300,
  headers: { get: () => null },
  json: async () => { if (body === undefined) throw new Error('no body'); return body; },
});

function makeEnv({ demo = false } = {}) {
  const calls = [], toasts = [], reloads = [], downloads = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '' });
  const server = { respond: () => { throw new Error('unexpected fetch'); } };
  const sandbox = {
    A: { session: { access_token: 't' }, demo, companyId: 'c1' },
    DS: { employees: [{ id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Pérez' }], leave_requests: [], leave_balances: [] },
    downCSV: (csv, name) => downloads.push({ csv, name }),
    A_goUrl: '',
    JSON, Object, Array, Number, String, Math, Promise, parseInt, Error, Date,
    fetch: async (url, opts = {}) => {
      calls.push({ url, method: opts.method || 'GET', body: opts.body ? JSON.parse(opts.body) : undefined });
      return server.respond(url, opts);
    },
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    loadVacations: () => { reloads.push(1); },
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  const fill = () => {
    el('vn-emp').value = 'e1'; el('vn-start').value = '2026-07-01'; el('vn-end').value = '2026-07-05';
    el('vn-days').value = '5'; el('vn-type').value = 'vacaciones'; el('vn-notes').value = 'n';
  };
  return {
    sandbox, calls, toasts, reloads, downloads, fill,
    run: (code) => vm.runInContext(code, sandbox),
    respondWith: (fn) => { server.respond = fn; },
  };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const SENT = '✅ Solicitud enviada';
const post = (e) => e.calls.filter((c) => c.method === 'POST' && c.url === '/api/leave_requests');

test('real mode: POST body has no status key and carries the Go contract fields', async () => {
  const e = makeEnv();
  e.fill();
  e.respondWith(async (url, o) => (o.method === 'POST' ? res(201, [{ id: 'r1' }]) : res(200, [{ id: 'e1', first_name: 'Ana', last_name: 'Pérez' }])));
  await e.run('saveLeaveReq()');
  const posts = post(e);
  assert.equal(posts.length, 1);
  const b = posts[0].body;
  assert.equal('status' in b, false, 'status must not be sent: Go rejects unknown fields');
  assert.equal(b.employee_id, 'e1');
  assert.equal(b.start_date, '2026-07-01');
  assert.equal(b.end_date, '2026-07-05');
  assert.equal(b.days, 5);
  assert.equal(b.type, 'vacaciones');
});

test('real mode: server 400 -> one danger toast, no success toast, no reload', async () => {
  const e = makeEnv();
  e.fill();
  e.respondWith(async (url, o) => (o.method === 'POST'
    ? res(400, { error: { code: 'bad_request', message: 'invalid body' } })
    : res(200, [{ id: 'e1', first_name: 'Ana', last_name: 'Pérez' }])));
  await e.run('saveLeaveReq()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
  assert.match(e.toasts[0].msg, /^No se pudo enviar la solicitud/);
  assert.equal(e.toasts.some((t) => t.msg === SENT), false);
  assert.equal(e.reloads.length, 0);
});

test('real mode: success -> "Solicitud enviada" once, then reload', async () => {
  const e = makeEnv();
  e.fill();
  e.respondWith(async (url, o) => (o.method === 'POST' ? res(201, [{ id: 'r1' }]) : res(200, [{ id: 'e1', first_name: 'Ana', last_name: 'Pérez' }])));
  await e.run('saveLeaveReq()');
  assert.deepEqual(e.toasts, [{ msg: SENT, type: 'success' }]);
  assert.equal(e.reloads.length, 1);
});

test('approveLeave real mode: server error -> danger toast, no success toast', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(409, { error: { code: 'conflict', message: 'leave request already decided' } }));
  await e.run("approveLeave('r1', true)");
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
  assert.notEqual(e.toasts[0].msg, '✅ Aprobada');
  assert.equal(e.reloads.length, 0);
  assert.deepEqual(e.calls.map((c) => [c.method, c.body]), [['PATCH', { status: 'approved' }]]);
});

test('approveLeave real mode: generic failure uses the fallback wording', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(500, { error: { code: 'internal', message: 'boom' } }));
  await e.run("approveLeave('r1', false)");
  assert.equal(e.toasts[0].type, 'danger');
  assert.match(e.toasts[0].msg, /^No se pudo actualizar la solicitud/);
});

test('approveLeave real mode: success -> success toast and reload', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(200, [{ id: 'r1', status: 'approved' }]));
  await e.run("approveLeave('r1', true)");
  assert.deepEqual(e.toasts, [{ msg: '✅ Aprobada', type: 'success' }]);
  assert.equal(e.reloads.length, 1);
  const r = makeEnv();
  r.respondWith(async () => res(200, [{ id: 'r1', status: 'rejected' }]));
  await r.run("approveLeave('r1', false)");
  assert.deepEqual(r.toasts, [{ msg: '❌ Rechazada', type: 'danger' }]);
});

test('demo mode: request is stored locally as pending with zero network calls', async () => {
  const e = makeEnv({ demo: true });
  e.fill();
  await e.run('saveLeaveReq()');
  assert.equal(e.calls.length, 0);
  const rows = e.sandbox.DS.leave_requests;
  assert.equal(rows.length, 1);
  assert.equal(rows[0].status, 'pending');
  assert.equal(rows[0].employee_id, 'e1');
  assert.deepEqual(e.toasts, [{ msg: SENT, type: 'success' }]);
});

const BAL_ROWS = [
  { id: 'b1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Pérez', year: 2025, earned_days: 30, used_days: 25, remaining_days: 5 },
  { id: 'b2', company_id: 'c1', employee_id: 'e2', employee_name: 'Luis "Lucho" Gómez', year: 2026, earned_days: 15, used_days: 0, remaining_days: null },
];

test('exportVacCSV (real mode) exports the balances from the API, not the empty DS', async () => {
  const e = makeEnv();
  e.respondWith(async (url) => (url.startsWith('/api/leave_balances') ? res(200, BAL_ROWS) : res(404, {})));
  await e.run('exportVacCSV()');
  assert.equal(e.downloads.length, 1);
  const { csv, name } = e.downloads[0];
  assert.equal(name, 'vacaciones.csv');
  const lines = csv.split('\n');
  assert.equal(lines[0], '"Empleado","Año","Ganados","Usados","Saldo"');
  assert.equal(lines[1], '"Ana Pérez","2025","30","25","5"');
  assert.equal(lines[2], '"Luis ""Lucho"" Gómez","2026","15","0",""', 'quotes doubled, null empty');
  assert.deepEqual(e.toasts, [{ msg: 'Exportado 📥', type: 'success' }]);
});

test('exportVacCSV with no balances warns and downloads nothing', async () => {
  const e = makeEnv();
  e.respondWith(async (url) => (url.startsWith('/api/leave_balances') ? res(200, []) : res(404, {})));
  await e.run('exportVacCSV()');
  assert.equal(e.downloads.length, 0);
  assert.deepEqual(e.toasts, [{ msg: 'Sin datos', type: 'warning' }]);
});

test('exportVacCSV in demo mode exports the local store (no network)', async () => {
  const e = makeEnv({ demo: true });
  e.sandbox.DS.leave_balances = [BAL_ROWS[0]];
  e.respondWith(() => { throw new Error('demo must not fetch'); });
  await e.run('exportVacCSV()');
  assert.equal(e.calls.length, 0);
  assert.equal(e.downloads[0].csv.split('\n')[1], '"Ana Pérez","2025","30","25","5"');
});

const DEMO_LOGS = [
  { id: 'l1', company_id: 'c1', employee_id: 'e1', date: '2026-09-30' },
  { id: 'l2', company_id: 'c1', employee_id: 'e1', date: '2026-10-01' },
  { id: 'l3', company_id: 'c1', employee_id: 'e2', date: '2026-10-15' },
  { id: 'l4', company_id: 'c1', employee_id: 'e1', date: '2026-10-31' },
  { id: 'l5', company_id: 'c1', employee_id: 'e1', date: '2026-11-01' },
  { id: 'l6', company_id: 'c2', employee_id: 'e1', date: '2026-10-10' },
];
const ids = (rows) => rows.map((r) => r.id).sort();

test('demo mode: Supa.sel treats date_from/date_to as an inclusive date range, like the API', async () => {
  const e = makeEnv({ demo: true });
  e.sandbox.DS.attendance_logs = DEMO_LOGS;
  e.respondWith(() => { throw new Error('demo must not fetch'); });
  e.sandbox.__r = await e.run("Supa.sel('attendance_logs',{date_from:'2026-10-01',date_to:'2026-10-31'})");
  assert.deepEqual(ids(e.sandbox.__r), ['l2', 'l3', 'l4']);
  e.sandbox.__r = await e.run("Supa.sel('attendance_logs',{employee_id:'e1',date_from:'2026-10-01'})");
  assert.deepEqual(ids(e.sandbox.__r), ['l2', 'l4', 'l5']);
  e.sandbox.__r = await e.run("Supa.sel('attendance_logs',{date_to:'2026-09-30'})");
  assert.deepEqual(ids(e.sandbox.__r), ['l1']);
  assert.equal(e.calls.length, 0);
});

test('demo mode: Supa.sel exact-match filters and company scoping are unchanged', async () => {
  const e = makeEnv({ demo: true });
  e.sandbox.DS.attendance_logs = DEMO_LOGS;
  e.respondWith(() => { throw new Error('demo must not fetch'); });
  e.sandbox.__r = await e.run("Supa.sel('attendance_logs',{employee_id:'e2'})");
  assert.deepEqual(ids(e.sandbox.__r), ['l3']);
  e.sandbox.__r = await e.run("Supa.sel('attendance_logs',{date:'2026-10-10'})");
  assert.deepEqual(ids(e.sandbox.__r), [], 'other companies stay out');
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
