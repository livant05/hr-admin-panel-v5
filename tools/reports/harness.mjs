// tools/reports/harness.mjs
//
// Drives the REAL report pieces of hr_admin_panel.html: the "Vacaciones" and
// "Horas Extras" reports (loadReport with rpt-type=vac / ot_summary) and the
// "días de vacaciones sin usar" alert (calcAlerts), and the overtime,
// attendance and full-employee CSV exports (exportOTCSV, exportAttCSV,
// exportAllCSV), in a node:vm sandbox with fake Supa/DOM. Sources are
// sliced out of the HTML by name and executed as written.
//
// Outside demo mode DS is emptied at sign-in and never refilled, so they must
// read leave balances and overtime logs from the API. leave_balances holds
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
import { extractFunction, extractMaybeAsync } from '../lib/extract.mjs';

const html = readFileSync(process.env.REPORTS_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

const optional = (header) => (html.includes(header) ? extractFunction(html, header) : '');

const srcs = [
  optional('function vacTotalsByEmployee('),
  extractFunction(html, 'async function loadReport('),
  extractFunction(html, 'async function calcAlerts('),
  extractMaybeAsync(html, 'function exportOTCSV('),
  extractMaybeAsync(html, 'function exportAttCSV('),
  extractMaybeAsync(html, 'function exportAllCSV('),
  optional('function csvCell('),
  optional('function csvText('),
  extractFunction(html, 'function apiErrMsg('),
  extractFunction(html, 'function hwCalc('),
].join('\n');

const fmt = (v) => 'B/. ' + Number(v || 0).toFixed(2);
const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const BALS = [
  { id: 'b1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', year: 2025, earned_days: 30, used_days: 18, remaining_days: 12 },
  { id: 'b2', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', year: 2026, earned_days: 15, used_days: 4, remaining_days: 11 },
  { id: 'b3', company_id: 'c1', employee_id: 'e2', employee_name: 'Luis Gomez', year: 2026, earned_days: 15, used_days: 7, remaining_days: 8 },
];

const OTS = [
  { id: 'o1', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', date: '2026-09-02', hours: 2.1, amount: 21.5 },
  { id: 'o2', company_id: 'c1', employee_id: 'e1', employee_name: 'Ana Diaz', date: '2026-09-20', hours: 5.2, amount: 52 },
  { id: 'o3', company_id: 'c1', employee_id: 'e2', employee_name: 'Luis Gomez', date: '2026-10-01', hours: 3, amount: 30 },
];

function makeEnv({ bals = BALS, ots = OTS, atts = [], emps = [], type = 'vac', fail = false } = {}) {
  const queries = [];
  const charts = [], toasts = [], downloads = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', textContent: '' });
  el('rpt-type').value = type;
  const sandbox = {
    A: { companyId: 'c1', demo: false },
    DS: { leave_balances: [], overtime_logs: [], attendance_logs: [], employees: [] },
    TS: '2026-10-14',
    API_FIELD_ES: {}, API_CONFLICT_ES: {},
    escHtml, fmt,
    daysTo: () => null,
    renderRptChart: (labels, data, label) => charts.push({ labels, data, label }),
    downCSV: (csv, name) => downloads.push({ csv, name }),
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    JSON, Object, Array, Number, String, Math, Promise, parseInt, parseFloat,
    Supa: {
      sel: async (tbl, flt) => { queries.push({ tbl, flt }); const rows = { employees: emps, leave_requests: [], leave_balances: bals, overtime_logs: ots, attendance_logs: atts }[tbl] || []; return rows.filter((r) => Object.entries(flt || {}).every(([k, v]) => r[k] === v)); },
      selAll: async (tbl, flt) => { queries.push({ tbl, flt }); if (fail) return { error: { code: 'internal', message: 'internal error' } }; const rows = { employees: emps, leave_requests: [], leave_balances: bals, overtime_logs: ots, attendance_logs: atts }[tbl] || []; return rows.filter((r) => Object.entries(flt || {}).every(([k, v]) => r[k] === v)); },
    },
    document: { getElementById: el },
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { el, charts, toasts, downloads, queries, run: (c) => vm.runInContext(c, sandbox) };
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

test('Horas Extras report totals hours and amount per employee from the API', async () => {
  const e = makeEnv({ type: 'ot_summary' });
  await e.run('loadReport()');
  const rows = e.el('rpt-tbody').innerHTML.split('</tr>').filter(Boolean);
  assert.equal(rows.length, 2);
  assert.match(rows[0], /<td>Ana Diaz<\/td><td>7\.3<\/td><td class="text-success">B\/\. 73\.50<\/td>/);
  assert.match(rows[1], /<td>Luis Gomez<\/td><td>3<\/td><td class="text-success">B\/\. 30\.00<\/td>/);
  assert.match(e.el('rpt-tfoot').innerHTML, /B\/\. 103\.50/);
  assert.deepEqual(JSON.parse(JSON.stringify(e.charts.at(-1))), { labels: ['Ana Diaz', 'Luis Gomez'], data: [74, 30], label: 'HE B/.' });
});

test('Horas Extras report with no overtime shows an empty table and a zero total (unchanged)', async () => {
  const e = makeEnv({ type: 'ot_summary', ots: [] });
  await e.run('loadReport()');
  assert.equal(e.el('rpt-tbody').innerHTML, '');
  assert.match(e.el('rpt-tfoot').innerHTML, /B\/\. 0\.00/);
});

test('exportOTCSV exports the overtime logs from the API, not the empty DS', async () => {
  const e = makeEnv({ ots: [OTS[0], { id: 'o4', company_id: 'c1', employee_name: 'Luis "Lucho" Gomez', date: '2026-10-02', hours: 0, type: 'nocturno', amount: null, notes: null }] });
  await e.run('exportOTCSV()');
  assert.equal(e.downloads.length, 1);
  const { csv, name } = e.downloads[0];
  assert.equal(name, 'horas_extras.csv');
  const lines = csv.split('\n');
  assert.equal(lines[0], '"Empleado","Fecha","Horas","Tipo","Valor","Notas"');
  assert.equal(lines[1], '"Ana Diaz","2026-09-02","2.1","","21.5",""');
  assert.equal(lines[2], '"Luis ""Lucho"" Gomez","2026-10-02","0","nocturno","",""', 'quotes doubled, 0 kept, null empty');
  assert.deepEqual(e.toasts, [{ msg: 'Exportado 📥', type: 'success' }]);
});

test('exportOTCSV with no overtime warns and downloads nothing', async () => {
  const e = makeEnv({ ots: [] });
  await e.run('exportOTCSV()');
  assert.equal(e.downloads.length, 0);
  assert.deepEqual(e.toasts, [{ msg: 'Sin datos', type: 'warning' }]);
});

const ATTS = [
  { id: 'a1', company_id: 'c1', employee_name: 'Ana Diaz', department: 'Ventas', date: '2026-10-14', time_in: '08:00', time_out: '17:00', status: 'present' },
  { id: 'a2', company_id: 'c1', employee_name: 'Luis "Lucho" Gomez', department: null, date: '2026-10-14', time_in: null, time_out: null, status: 'absent' },
  { id: 'a3', company_id: 'c1', employee_name: 'Ana Diaz', department: 'Ventas', date: '2026-10-13', time_in: '08:00', time_out: '12:00', status: 'present' },
];

test("exportAttCSV exports today's attendance from the API, not the empty DS", async () => {
  const e = makeEnv({ atts: ATTS });
  await e.run('exportAttCSV()');
  assert.ok(e.queries.some((q) => q.tbl === 'attendance_logs' && q.flt && q.flt.date === '2026-10-14'), 'asks the API for today only');
  assert.equal(e.downloads.length, 1);
  const { csv, name } = e.downloads[0];
  assert.equal(name, 'asistencia.csv');
  const lines = csv.split('\n');
  assert.equal(lines.length, 3);
  assert.equal(lines[0], '"Empleado","Departamento","Fecha","Entrada","Salida","Horas","Estado"');
  assert.match(lines[1], /^"Ana Diaz","Ventas","2026-10-14","08:00","17:00","[^"]+","present"$/);
  assert.equal(lines[2], '"Luis ""Lucho"" Gomez","","2026-10-14","","","0","absent"', 'quotes doubled, 0 hours kept, null empty');
  assert.deepEqual(e.toasts, [{ msg: 'Exportado 📥', type: 'success' }]);
});

test('exportAttCSV with no attendance today warns and downloads nothing', async () => {
  const e = makeEnv({ atts: [ATTS[2]] });
  await e.run('exportAttCSV()');
  assert.equal(e.downloads.length, 0);
  assert.deepEqual(e.toasts, [{ msg: 'Sin datos', type: 'warning' }]);
});

const EMP_ROWS = [
  { id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Diaz', cedula: '8-123-456', position: 'Cajera', department: 'Ventas', salary: 1000, start_date: '2020-01-01', status: 'active', email: 'ana@x.test', phone: '6000-0000', contract_type: 'indefinido', ss_number: '123' },
  // a role without employees/payroll gets the sensitive fields as null (server redaction)
  { id: 'e2', company_id: 'c1', first_name: 'Luis "Lucho"', last_name: 'Gomez', cedula: null, position: null, department: null, salary: null, start_date: '2021-05-03', status: 'inactive', email: null, phone: '6111-1111', contract_type: null, ss_number: null },
];

test('exportAllCSV exports every employee from the API, not the empty DS', async () => {
  const e = makeEnv({ emps: EMP_ROWS });
  await e.run('exportAllCSV()');
  assert.equal(e.downloads.length, 1);
  const { csv, name } = e.downloads[0];
  assert.equal(name, 'empleados_completo_2026-10-14.csv');
  const lines = csv.split('\n');
  assert.equal(lines[0], '"Nombre","Apellido","Cédula","Cargo","Departamento","Salario","Ingreso","Estado","Email","Teléfono","Contrato","SS"');
  assert.equal(lines[1], '"Ana","Diaz","8-123-456","Cajera","Ventas","1000","2020-01-01","active","ana@x.test","6000-0000","indefinido","123"');
  assert.equal(lines[2], '"Luis ""Lucho""","Gomez","","","","","2021-05-03","inactive","","6111-1111","",""', 'quotes doubled, redacted nulls empty');
  assert.deepEqual(e.toasts, [{ msg: '✅ CSV exportado', type: 'success' }]);
});

test('exportAllCSV with no employees warns and downloads nothing', async () => {
  const e = makeEnv({ emps: [] });
  await e.run('exportAllCSV()');
  assert.equal(e.downloads.length, 0);
  assert.deepEqual(e.toasts, [{ msg: 'Sin datos', type: 'warning' }]);
});

test('CSV exports neutralize text cells that a spreadsheet would run as a formula', async () => {
  const e = makeEnv({ ots: [
    { id: 'x1', company_id: 'c1', employee_name: '=HYPERLINK("http://evil","x")', date: '2026-10-02', hours: 1, type: 'regular', amount: 10, notes: '+cmd' },
    { id: 'x2', company_id: 'c1', employee_name: '@SUM(A1)', date: '2026-10-03', hours: 2, type: 'regular', amount: -5, notes: '-nota' },
  ] });
  await e.run('exportOTCSV()');
  const lines = e.downloads[0].csv.split('\n');
  assert.equal(lines[1], '"\'=HYPERLINK(""http://evil"",""x"")","2026-10-02","1","regular","10","\'+cmd"');
  assert.equal(lines[2], '"\'@SUM(A1)","2026-10-03","2","regular","-5","\'-nota"', 'a numeric -5 stays a number; text starting with - is neutralized');
});

test('exports report a failed read as an error, never as "Sin datos"', async () => {
  for (const fn of ['exportOTCSV', 'exportAttCSV', 'exportAllCSV']) {
    const e = makeEnv({ fail: true });
    await e.run(fn + '()');
    assert.equal(e.downloads.length, 0, fn);
    assert.equal(e.toasts.length, 1, fn);
    assert.equal(e.toasts[0].type, 'danger', fn);
    assert.match(e.toasts[0].msg, /error interno del servidor/, fn);
  }
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
