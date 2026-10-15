// tools/payroll/harness.mjs
//
// Drives the REAL payroll screen code of hr_admin_panel.html (period helpers,
// loadPayroll, printPayslip, savePayrollRun, loadPayrollHistory) in a node:vm
// sandbox with fake Supa/DOM/toast. Nothing is reimplemented: sources are
// sliced out of the HTML by name and executed as written.
//
// Covers the quincena work: attendance/overtime badges and the payslip use
// only the selected half (1-15 / 16-end), the payslip quota is the halved
// quota the server charged, Save cannot be double submitted, server errors
// (e.g. 409 mensual-vs-quincena) are shown instead of a fake success, and the
// history shows readable period labels.
//
// Run: node tools/payroll/harness.mjs   (exit code 1 on any failed assertion)
// Mutation runs point PAYROLL_HTML at a modified copy of the page.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.PAYROLL_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

function between(startMarker, endMarker) {
  const s = html.indexOf(startMarker);
  assert.ok(s >= 0, `${startMarker} not found`);
  const e = html.indexOf(endMarker, s);
  assert.ok(e > s, `${endMarker} not found after ${startMarker}`);
  return html.slice(s, e);
}

const srcs = [
  between('var PR_PERIODS', '\n\n'),
  extractFunction(html, 'function prFactor('),
  extractFunction(html, 'function prPeriodLabel('),
  extractFunction(html, 'function prPeriodRange('),
  extractFunction(html, 'function prInPeriod('),
  extractFunction(html, 'function prQuota('),
  extractFunction(html, 'async function loadPayroll('),
  extractFunction(html, 'async function printPayslip('),
  extractFunction(html, 'async function savePayrollRun('),
  extractFunction(html, 'async function loadPayrollHistory('),
  extractFunction(html, 'function apiErrMsg('),
  'var _prRows = [];', 'var _prSaving=false;',
].join('\n');

const MONTHS = ['Enero', 'Febrero', 'Marzo', 'Abril', 'Mayo', 'Junio', 'Julio', 'Agosto', 'Septiembre', 'Octubre', 'Noviembre', 'Diciembre'];
const fmt = (v) => Number(v || 0).toFixed(2);
const escHtml = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const EMP = { id: 'e1', company_id: 'c1', first_name: 'Ana', last_name: 'Diaz', salary: 3000, status: 'active' };
const ATT = [
  { employee_id: 'e1', date: '2025-06-02', status: 'present' },
  { employee_id: 'e1', date: '2025-06-15', status: 'absent' },
  { employee_id: 'e1', date: '2025-06-16', status: 'present' },
  { employee_id: 'e1', date: '2025-06-17', status: 'present' },
  { employee_id: 'e1', date: '2025-06-30', status: 'absent' },
];
const OT = [
  { employee_id: 'e1', company_id: 'c1', date: '2025-06-10', hours: 2, amount: 20, type: 'regular' },
  { employee_id: 'e1', company_id: 'c1', date: '2025-06-20', hours: 3, amount: 30, type: 'nocturno' },
];
const DEDS = [{ employee_id: 'e1', company_id: 'c1', status: 'active', quota: 80, type: 'prestamo', description: 'Prestamo' }];

function makeEnv({ period = 'quincenal1', previewRow = {} } = {}) {
  const toasts = [], inserts = [], written = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', disabled: false });
  el('pr-period').value = period; el('pr-month').value = '6'; el('pr-year').value = '2025';
  const ctl = { insResult: async () => [{ id: 'h1' }] };
  const sandbox = {
    A: { companyId: 'c1', companyName: 'Acme', demo: false },
    DS: { overtime_logs: OT, deductions: DEDS },
    MONTHS, fmt, escHtml,
    API_FIELD_ES: {}, API_CONFLICT_ES: {},
    TD: new Date('2025-06-20T12:00:00Z'),
    seniStr: () => '1 año',
    getWorkingDays: () => 22,
    JSON, Object, Array, Number, String, Math, Promise, parseInt, Error, Date,
    Supa: {
      gq: (o) => '?' + Object.entries(o).map(([k, v]) => `${k}=${v}`).join('&'),
      sel: async (tbl) => ({ employees: [EMP], attendance_logs: ATT, overtime_logs: OT, deductions: DEDS }[tbl] || []),
      selAll: async (tbl) => (tbl === 'overtime_logs' ? OT : []),
      goFetch: async () => [{ employee_id: 'e1', b: 1500, css: 146.25, se: 18.75, isr: 0, ded: 165, ded_quota: 40, net: 1295, pcss: 0, pse: 0, dec: 0, dec_cssp: 0, tot: 1500, ...previewRow }],
      ins: async (tbl, data) => { inserts.push({ tbl, data }); return ctl.insResult(); },
      del: async () => [],
    },
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    window: { open: () => ({ document: { write: (h) => written.push(h), close: () => {} }, onload: null }) },
    confirm: () => true,
  };
  sandbox.window.open = () => ({ document: { write: (h) => written.push(h), close: () => {} } });
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { sandbox, toasts, inserts, written, el, ctl, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('prFactor / prQuota: quincena halves the quota, mensual keeps it', () => {
  const e = makeEnv();
  assert.equal(e.run("prFactor('mensual')"), 1);
  assert.equal(e.run("prFactor('quincenal1')"), 0.5);
  assert.equal(e.run("prFactor('quincenal2')"), 0.5);
  assert.equal(e.run("prQuota(80,'quincenal2')"), 40);
  assert.equal(e.run("prQuota(80,'mensual')"), 80);
});

test('prInPeriod: day 15 closes the 1st half, day 16 opens the 2nd, mensual takes the month only', () => {
  const e = makeEnv();
  const f = (d, p) => e.run(`prInPeriod('${d}','${p}',2025,6)`);
  assert.equal(f('2025-06-01', 'quincenal1'), true);
  assert.equal(f('2025-06-15', 'quincenal1'), true);
  assert.equal(f('2025-06-16', 'quincenal1'), false);
  assert.equal(f('2025-06-16', 'quincenal2'), true);
  assert.equal(f('2025-06-30', 'quincenal2'), true);
  assert.equal(f('2025-06-15', 'quincenal2'), false);
  assert.equal(f('2025-06-15', 'mensual'), true);
  assert.equal(f('2025-07-01', 'mensual'), false);
  assert.equal(f('2025-07-01', 'quincenal2'), false);
  assert.equal(e.run("prInPeriod(null,'mensual',2025,6)"), false);
});

test('prPeriodRange: inclusive bounds, month end follows the calendar (leap February)', () => {
  const e = makeEnv();
  const r = (p, y, m) => ({ ...e.run(`prPeriodRange('${p}',${y},${m})`) });
  assert.deepEqual(r('quincenal1', 2025, 6), { from: '2025-06-01', to: '2025-06-15' });
  assert.deepEqual(r('quincenal2', 2025, 6), { from: '2025-06-16', to: '2025-06-30' });
  assert.deepEqual(r('mensual', 2024, 2), { from: '2024-02-01', to: '2024-02-29' });
  assert.deepEqual(r('quincenal2', 2025, 12), { from: '2025-12-16', to: '2025-12-31' });
});

test('prPeriodLabel: readable labels for the history badge', () => {
  const e = makeEnv();
  assert.equal(e.run("prPeriodLabel('mensual')"), 'Mensual');
  assert.equal(e.run("prPeriodLabel('quincenal1')"), '1ra Quincena');
  assert.equal(e.run("prPeriodLabel('quincenal2')"), '2da Quincena');
  assert.equal(e.run("prPeriodLabel('quincenal')"), 'Quincenal');
});

test('loadPayroll quincenal1: badge counts only cover days 1-15', async () => {
  const e = makeEnv({ period: 'quincenal1' });
  await e.run('loadPayroll()');
  const att = e.run('_prRows')[0].att;
  assert.equal(att.workedDays, 1);
  assert.equal(att.absentDays, 1);
  assert.equal(att.otHours, 2);
});

test('loadPayroll quincenal2: badge counts only cover day 16 to month end', async () => {
  const e = makeEnv({ period: 'quincenal2' });
  await e.run('loadPayroll()');
  const att = e.run('_prRows')[0].att;
  assert.equal(att.workedDays, 2);
  assert.equal(att.absentDays, 1);
  assert.equal(att.otHours, 3);
});

test('loadPayroll mensual: whole month', async () => {
  const e = makeEnv({ period: 'mensual' });
  await e.run('loadPayroll()');
  const att = e.run('_prRows')[0].att;
  assert.equal(att.workedDays, 3);
  assert.equal(att.otHours, 5);
});

test('printPayslip quincenal2 (no cache): only 2nd-half overtime, halved quota, net not double-charged', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: { ot_amt: 30 } });
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /2da Quincena/);
  assert.match(doc, /Horas Extras nocturno \(3 hrs\)/);
  assert.doesNotMatch(doc, /Horas Extras regular/);
  assert.match(doc, /Prestamo \(cuota\)<\/td><td class="text-right text-danger">- 40\.00/);
  assert.doesNotMatch(doc, /- 80\.00/);
});

test('printPayslip net comes from the server row (quota already inside), not minus the quota again', async () => {
  const e = makeEnv({ period: 'quincenal1', previewRow: { net: 1295 } });
  e.run('_prRows = []');
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /SALARIO NETO A PAGAR<\/small><div class="val">1295\.00</);
});

// The server's b already includes ot_amt (b = base - absences + overtime) and
// css/se/isr/net are computed on that b, so the payslip only itemizes it.
const OT_ROW = { b: 1500, ot_amt: 30, css: 146.25, se: 18.75, isr: 0, ded: 165, ded_quota: 40, net: 1295, tot: 1900 };

test('printPayslip HH.EE. (no cache): overtime itemized inside the server gross, never added twice', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /<td>Salario Base<\/td><td>1470\.00</);
  assert.match(doc, /Total Horas Extras<\/td><td style="color:#28a745">30\.00</);
  assert.match(doc, /TOTAL DEVENGADO<\/td><td style="color:#007bff">1500\.00</);
  assert.match(doc, /CSS empleado \(9\.75%\)<\/td><td style="color:#dc3545">- 146\.25</);
  assert.match(doc, /TOTAL DEDUCCIONES<\/td><td style="color:#dc3545">- 205\.00</);
  assert.match(doc, /SALARIO NETO A PAGAR<\/small><div class="val">1295\.00</);
});

test('printPayslip HH.EE. (cached row from Calcular): shows the server overtime total, not 0', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  await e.run('loadPayroll()');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Total Horas Extras<\/td><td style="color:#28a745">30\.00</);
  assert.match(doc, /<td>Salario Base<\/td><td>1470\.00</);
  assert.match(doc, /SALARIO NETO A PAGAR<\/small><div class="val">1295\.00</);
});

test('printPayslip HH.EE. lists the overtime from the API, not the in-memory demo store', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  e.sandbox.DS.overtime_logs = [];
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Horas Extras nocturno \(3 hrs\)/);
  assert.doesNotMatch(doc, /Horas Extras regular/);
});

test('printPayslip HH.EE. reads every overtime page, so an older period is not cut off by the 200-row default page', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  // The default page (newest first) no longer reaches June 2025; only the whole list does
  const sel = e.sandbox.Supa.sel;
  e.sandbox.Supa.sel = async (tbl, flt) => (tbl === 'overtime_logs' ? [] : sel(tbl, flt));
  e.sandbox.Supa.selAll = async (tbl) => (tbl === 'overtime_logs' ? OT : []);
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Horas Extras nocturno \(3 hrs\)<\/td><td class="text-right text-success">30\.00</);
  assert.doesNotMatch(doc, /Horas Extras regular/);
  assert.match(doc, /Total Horas Extras<\/td><td style="color:#28a745">30\.00</);
});

test('printPayslip (no cache) lists the installments from the API and totals the server quota', async () => {
  const e = makeEnv({ period: 'quincenal1', previewRow: { ded: 165, ded_quota: 40, net: 1295 } });
  e.sandbox.DS.deductions = [];
  e.run('_prRows = []');
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Prestamo \(cuota\)<\/td><td class="text-right text-danger">- 40\.00/);
  assert.match(doc, /TOTAL DEDUCCIONES<\/td><td style="color:#dc3545">- 205\.00/);
});

test('printPayslip (cached row from Calcular) lists the installments too, not only their total', async () => {
  const e = makeEnv({ period: 'quincenal1', previewRow: { ded: 165, ded_quota: 40, net: 1295 } });
  e.sandbox.DS.deductions = [];
  await e.run('loadPayroll()');
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Prestamo \(cuota\)<\/td><td class="text-right text-danger">- 40\.00/);
  assert.match(doc, /TOTAL DEDUCCIONES<\/td><td style="color:#dc3545">- 205\.00/);
});

test('printPayslip without HH.EE.: the gross (overtime included) is labelled Salario Bruto, no overtime rows', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  e.run('_prRows = []');
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /<td>Salario Bruto<\/td><td>1500\.00</);
  assert.match(doc, /<small>Salario Bruto<\/small><div class="val">1500\.00</);
  assert.doesNotMatch(doc, /Salario Base/, 'the gross includes overtime, so it is not the base salary');
  assert.doesNotMatch(doc, /Horas Extras nocturno/);
  assert.match(doc, /SALARIO NETO A PAGAR<\/small><div class="val">1295\.00</);
});

test('printPayslip with HH.EE. and Aportes Patronales: Costo Total Empresa is the server total, overtime not added twice', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  e.el('slip-show-pat').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /<small>Salario Base<\/small><div class="val">1470\.00</);
  assert.match(doc, /Costo Total Empresa<\/td><td style="color:#e65100">1900\.00</);
});

test('printPayslip asks the server only for the period\'s overtime (employee and date range)', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  const calls = [];
  e.sandbox.Supa.selAll = async (tbl, flt) => { calls.push({ tbl, flt }); return tbl === 'overtime_logs' ? OT : []; };
  e.run('_prRows = []');
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const ot = calls.filter((c) => c.tbl === 'overtime_logs');
  assert.equal(ot.length, 1);
  assert.deepEqual({ ...ot[0].flt }, { employee_id: 'e1', date_from: '2025-06-16', date_to: '2025-06-30' });
  assert.match(e.written.join(''), /Horas Extras nocturno \(3 hrs\)/);
});

test('printPayslip after overtime was logged past "Calcular": the totals are recomputed, not the stale table row', async () => {
  const e = makeEnv({ period: 'quincenal2', previewRow: OT_ROW });
  await e.run('loadPayroll()'); // table computed with 30.00 of overtime
  // A new 20.00 overtime log arrives; the server now computes 50.00
  e.sandbox.Supa.goFetch = async () => [{ employee_id: 'e1', b: 1520, ot_amt: 50, css: 148.2, se: 19, isr: 0, ded: 167.2, ded_quota: 40, net: 1312.8, pcss: 0, pse: 0, dec: 0, dec_cssp: 0, tot: 1520 }];
  e.el('slip-show-ot').checked = true;
  await e.run("printPayslip('e1')");
  const doc = e.written.join('');
  assert.match(doc, /Total Horas Extras<\/td><td style="color:#28a745">50\.00</);
  assert.match(doc, /TOTAL DEVENGADO<\/td><td style="color:#007bff">1520\.00</);
  assert.match(doc, /SALARIO NETO A PAGAR<\/small><div class="val">1312\.80</);
});

test('printPayslip when the preview fails: Spanish danger toast, no slip window', async () => {
  const e = makeEnv({ period: 'quincenal2' });
  e.sandbox.Supa.goFetch = async () => ({ error: { code: 'internal', message: 'internal error' } });
  await e.run("printPayslip('e1')");
  assert.equal(e.written.length, 0);
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
  assert.match(e.toasts[0].msg, /^No se pudo calcular el recibo/);
  assert.doesNotMatch(e.toasts[0].msg, /internal error/);
});

test('savePayrollRun: a double click sends ONE request and the button is disabled meanwhile', async () => {
  const e = makeEnv();
  e.run("_prRows = [{b:100,isr:1,net:90,tot:110}]");
  const releases = [];
  e.ctl.insResult = () => new Promise((r) => { releases.push(() => r([{ id: 'h1' }])); });
  const first = e.run('savePayrollRun()');
  const second = e.run('savePayrollRun()');
  assert.equal(e.el('pr-save-btn').disabled, true);
  releases.forEach((r) => r());
  await Promise.all([first, second]);
  assert.equal(e.inserts.length, 1);
  assert.equal(e.inserts[0].data.period, 'quincenal1');
  assert.equal(e.el('pr-save-btn').disabled, false);
  assert.equal(e.toasts.filter((t) => t.type === 'danger').length, 0);
});

test('savePayrollRun: server 409 -> Spanish danger toast (not the English server text), no success toast, button re-enabled', async () => {
  const e = makeEnv();
  e.run("_prRows = [{b:100,isr:1,net:90,tot:110}]");
  e.ctl.insResult = async () => ({ error: { code: 'conflict', message: 'this month already has payroll in a different period type' } });
  await e.run('savePayrollRun()');
  assert.equal(e.toasts.length, 1);
  assert.equal(e.toasts[0].type, 'danger');
  assert.match(e.toasts[0].msg, /Este mes ya tiene planilla/);
  assert.doesNotMatch(e.toasts[0].msg, /different period type/);
  assert.equal(e.el('pr-save-btn').disabled, false);
  // and a later click works again
  e.ctl.insResult = async () => [{ id: 'h2' }];
  await e.run('savePayrollRun()');
  assert.equal(e.inserts.length, 2);
});

test('savePayrollRun without calculated rows: warns and sends nothing', async () => {
  const e = makeEnv();
  e.run('_prRows = []');
  await e.run('savePayrollRun()');
  assert.equal(e.inserts.length, 0);
  assert.equal(e.toasts[0].type, 'warning');
});

test('loadPayrollHistory: period badge uses the readable label', async () => {
  const e = makeEnv();
  e.sandbox.Supa.sel = async () => [{ id: 'h1', period: 'quincenal2', month_name: 'Junio', year: 2025, employee_count: 1, total_bruto: 1, total_neto: 1, total_empresa: 1, created_at: '2025-06-30T00:00:00Z' }];
  await e.run('loadPayrollHistory()');
  assert.match(e.el('pr-hist-tbody').innerHTML, />2da Quincena</);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
