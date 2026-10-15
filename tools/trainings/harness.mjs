// tools/trainings/harness.mjs
//
// Drives the REAL Capacitaciones functions of hr_admin_panel.html (loadTraining,
// saveTraining, openEnrollModal, setTrainingStatus, openGradeModal, saveGrade,
// delTraining, delEnrollment) in a node:vm sandbox with a stubbed Supa.
//
// Run: node tools/trainings/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.TRAININGS_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

const objectLiteral = (header) => (html.includes(header) ? extractFunction(html, header) + ';' : '');
const optional = (header) => (html.includes(header) ? extractFunction(html, header) : '');

const srcs = [
  objectLiteral('var API_FIELD_ES='),
  objectLiteral('var API_CONFLICT_ES='),
  extractFunction(html, 'function apiErrMsg('),
  objectLiteral('var TR_STATUS_LABEL='),
  optional('function trainingPayload('),
  extractFunction(html, 'async function loadTraining('),
  extractFunction(html, 'async function saveTraining('),
  html.includes('async function openEnrollModal(') ? extractFunction(html, 'async function openEnrollModal(') : extractFunction(html, 'function openEnrollModal('),
  html.includes('async function delTraining(') ? extractFunction(html, 'async function delTraining(') : extractFunction(html, 'function delTraining('),
  html.includes('async function delEnrollment(') ? extractFunction(html, 'async function delEnrollment(') : extractFunction(html, 'function delEnrollment('),
  optional('async function setTrainingStatus('),
  optional('function openGradeModal('),
  optional('async function saveGrade('),
  'var chTr=null;',
].join('\n');

const escHtml = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
const plain = (x) => JSON.parse(JSON.stringify(x));

const TR = { id: 't1', company_id: 'c1', name: 'Excel', category: 'tecnico', hours: 8, start_date: '2026-11-02', end_date: '2026-11-03', mode: 'virtual', instructor: 'Ana', description: 'Hojas', status: 'programado' };
const EN = { id: 'e1', company_id: 'c1', training_id: 't1', training_name: 'Excel', employee_id: 'p1', employee_name: 'Luis Pinto', status: 'completado', score: 92.5, notes: null };
const EMPS = [{ id: 'p1', first_name: 'Luis', last_name: 'Pinto', status: 'active' }, { id: 'p2', first_name: 'Rosa', last_name: 'Mena', status: 'active' }];

function makeEnv({ demo = false, lists = {}, res = {} } = {}) {
  const toasts = [], calls = [], modals = [];
  const els = {};
  const checks = [];
  const el = (id) => (els[id] ??= { id, value: '', innerHTML: '', style: {}, checked: false, getAttribute: () => '' });
  const sandbox = {
    A: { demo, companyId: 'c1', companyName: 'Acme' },
    DS: { trainings: [], enrollments: [], employees: [] },
    escHtml, Object, String, Array, Number, Math, Promise, Date, JSON, parseInt, parseFloat, isNaN, Error,
    Supa: {
      sel: async (t) => { calls.push(['sel', t]); return t in lists ? lists[t] : []; },
      selAll: async (t) => { calls.push(['selAll', t]); return t in lists ? lists[t] : []; },
      ins: async (t, d) => { calls.push(['ins', t, plain(d)]); const r = res['ins:' + t]; return typeof r === 'function' ? r(d) : (r ?? [{ id: t === 'trainings' ? 't-new' : 'e-new', ...d }]); },
      upd: async (t, id, d) => { calls.push(['upd', t, id, plain(d)]); return res['upd:' + t] ?? [{ id, ...d }]; },
      del: async (t, id) => { calls.push(['del', t, id]); return res['del:' + t] ?? null; },
    },
    gv: (id) => String(el(id).value || '').trim(),
    document: { getElementById: el, querySelectorAll: () => checks },
    $: (sel) => ({ modal: (a) => modals.push([sel, a]) }),
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    confirm: () => true,
    TS: '2026-10-15',
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  const check = (id, name) => checks.push({ id: 'tr-emp-' + id, value: id, checked: true, getAttribute: () => name });
  return { sandbox, toasts, calls, modals, el, check, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('loadTraining (real) renders the API courses, enrollments and certificates and fills the cache', async () => {
  const e = makeEnv({ lists: { trainings: [TR], enrollments: [EN], employees: EMPS } });
  await e.run('loadTraining()');
  assert.equal(e.toasts.length, 0);
  assert.equal(e.sandbox.DS.trainings.length, 1);
  assert.equal(e.sandbox.DS.enrollments.length, 1);
  assert.match(e.el('training-cards').innerHTML, /Excel/);
  assert.match(e.el('enrollment-tbody').innerHTML, /Luis Pinto/);
  assert.match(e.el('enrollment-tbody').innerHTML, /openGradeModal\('e1'\)/);
  assert.match(e.el('certificate-cards').innerHTML, /printCertificate\('e1'\)/);
  assert.match(e.el('tr-emp-wrap').innerHTML, /Rosa Mena/);
});

test('loadTraining (real): a failed list clears the page and shows a Spanish toast', async () => {
  const e = makeEnv({ lists: { trainings: { error: { code: 'forbidden', message: 'insufficient permissions' } }, enrollments: [], employees: [] } });
  await e.run('loadTraining()');
  assert.deepEqual(e.toasts, [{ msg: 'No tienes permiso para esta acción', type: 'danger' }]);
  assert.equal(e.el('training-cards').innerHTML, '');
});

test('saveTraining (real) posts the course (empty fields as null) and enrolls the checked employees', async () => {
  const e = makeEnv({ lists: { trainings: [], enrollments: [], employees: EMPS } });
  Object.assign(e.el('tr-name'), { value: ' Excel ' });
  Object.assign(e.el('tr-hours'), { value: '8' });
  Object.assign(e.el('tr-cat'), { value: 'tecnico' });
  Object.assign(e.el('tr-mode'), { value: 'virtual' });
  e.check('p1', 'Luis Pinto');
  await e.run('saveTraining()');
  const ins = e.calls.filter((c) => c[0] === 'ins');
  assert.deepEqual(ins[0], ['ins', 'trainings', { name: 'Excel', category: 'tecnico', hours: 8, start_date: null, end_date: null, mode: 'virtual', instructor: null, description: null }]);
  assert.deepEqual(ins[1], ['ins', 'enrollments', { training_id: 't-new', employee_id: 'p1' }]);
  assert.equal(e.toasts.at(-1).msg, '✅ Capacitación guardada');
});

test('saveTraining (real): a server error keeps the modal open and enrolls nobody', async () => {
  const e = makeEnv({ res: { 'ins:trainings': { error: { code: 'validation_failed', message: 'validation failed', fields: { end_date: 'must not be before start_date' } } } } });
  Object.assign(e.el('tr-name'), { value: 'Excel' });
  e.check('p1', 'Luis Pinto');
  await e.run('saveTraining()');
  assert.equal(e.calls.filter((c) => c[0] === 'ins').length, 1);
  assert.equal(e.toasts.at(-1).type, 'danger');
  assert.match(e.toasts.at(-1).msg, /^Datos inválidos/);
  assert.equal(e.modals.filter((m) => m[1] === 'hide').length, 0);
});

test('saveTraining (real): a failed enrollment is reported, not hidden behind the success toast', async () => {
  const e = makeEnv({ res: { 'ins:enrollments': { error: { code: 'conflict', message: 'El empleado ya está inscrito en esta capacitación' } } } });
  Object.assign(e.el('tr-name'), { value: 'Excel' });
  e.check('p1', 'Luis Pinto');
  await e.run('saveTraining()');
  assert.equal(e.toasts.at(-1).type, 'warning');
  assert.match(e.toasts.at(-1).msg, /1 inscripción no se pudo guardar/);
});

test('openEnrollModal (real) offers the API employees not yet enrolled, not the demo store', async () => {
  const e = makeEnv({ lists: { trainings: [TR], enrollments: [EN], employees: EMPS } });
  await e.run('loadTraining()');
  await e.run("openEnrollModal('t1')");
  const ins = e.calls.filter((c) => c[0] === 'ins');
  assert.deepEqual(ins, [['ins', 'enrollments', { training_id: 't1', employee_id: 'p2' }]]);
});

test('setTrainingStatus (real) patches only the status', async () => {
  const e = makeEnv({ lists: { trainings: [TR], enrollments: [], employees: [] } });
  await e.run("setTrainingStatus('t1','en_progreso')");
  assert.deepEqual(e.calls.find((c) => c[0] === 'upd'), ['upd', 'trainings', 't1', { status: 'en_progreso' }]);
  assert.equal(e.toasts.at(-1).type, 'success');
});

test('Calificar (real): saveGrade patches status, score and notes; an empty score is null', async () => {
  const e = makeEnv({ lists: { trainings: [TR], enrollments: [EN], employees: [] } });
  await e.run('loadTraining()');
  e.run("openGradeModal('e1')");
  assert.equal(e.el('gr-score').value, '92.5');
  Object.assign(e.el('gr-status'), { value: 'completado' });
  Object.assign(e.el('gr-score'), { value: '' });
  Object.assign(e.el('gr-notes'), { value: 'Aprobado' });
  await e.run('saveGrade()');
  assert.deepEqual(e.calls.find((c) => c[0] === 'upd'), ['upd', 'enrollments', 'e1', { status: 'completado', score: null, notes: 'Aprobado' }]);
  assert.equal(e.toasts.at(-1).msg, '✅ Calificación guardada');
});

test('Calificar: a score outside 0-100 is refused before calling the API', async () => {
  const e = makeEnv({ lists: { trainings: [TR], enrollments: [EN], employees: [] } });
  await e.run('loadTraining()');
  e.run("openGradeModal('e1')");
  Object.assign(e.el('gr-score'), { value: '120' });
  await e.run('saveGrade()');
  assert.equal(e.calls.filter((c) => c[0] === 'upd').length, 0);
  assert.deepEqual(e.toasts.at(-1), { msg: 'La nota debe estar entre 0 y 100', type: 'warning' });
});

test('Calificar (demo) updates the demo store', async () => {
  const e = makeEnv({ demo: true });
  e.sandbox.DS.trainings = [TR];
  e.sandbox.DS.enrollments = [{ ...EN, status: 'programado', score: null }];
  e.run("openGradeModal('e1')");
  Object.assign(e.el('gr-status'), { value: 'completado' });
  Object.assign(e.el('gr-score'), { value: '80' });
  await e.run('saveGrade()');
  assert.equal(e.sandbox.DS.enrollments[0].status, 'completado');
  assert.equal(e.sandbox.DS.enrollments[0].score, 80);
  assert.equal(e.calls.filter((c) => c[0] === 'upd').length, 0);
});

test('delTraining / delEnrollment (real) call the API and report a failure', async () => {
  const e = makeEnv({ res: { 'del:enrollments': { error: { code: 'not_found', message: 'not found' } } } });
  await e.run("delTraining('t1')");
  assert.deepEqual(e.calls.find((c) => c[0] === 'del'), ['del', 'trainings', 't1']);
  await e.run("delEnrollment('e1')");
  assert.deepEqual(e.toasts.at(-1), { msg: 'No se pudo eliminar la inscripción: el registro no existe', type: 'danger' });
});

let pass = 0;
for (const [name, fn] of tests) {
  try { await fn(); pass++; console.log('ok  ', name); }
  catch (err) { console.log('FAIL', name, '\n    ', err.message.split('\n').slice(0, 4).join('\n     ')); }
}
console.log(`${pass}/${tests.length} passed`);
process.exitCode = pass === tests.length ? 0 : 1;
