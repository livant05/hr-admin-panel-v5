// tools/recruitment/harness.mjs
//
// Drives the REAL loadRecruitment of hr_admin_panel.html (with recErrMsg and
// apiErrMsg) in a node:vm sandbox, real mode, with a stubbed Supa.
//
// Run: node tools/recruitment/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.RECRUITMENT_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

const objectLiteral = (header) => (html.includes(header) ? extractFunction(html, header) + ';' : '');

const srcs = [
  objectLiteral('var API_FIELD_ES='),
  objectLiteral('var API_CONFLICT_ES='),
  extractFunction(html, 'function apiErrMsg('),
  extractFunction(html, 'function recErrMsg('),
  extractFunction(html, 'async function loadRecruitment('),
  extractFunction(html, 'async function saveApplication('),
].join('\n');

const escHtml = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);

function makeEnv(results) {
  const toasts = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, innerHTML: 'stale', style: {} });
  const sandbox = {
    A: { demo: false, companyId: 'c1' },
    DS: { job_postings: [], candidates: [], applications: [] },
    REC_JOB_STATUS: {}, REC_STAGES: [], REC_STAGE_BADGE: {}, REC_STAGE_LABEL: {},
    escHtml, Object, String, Array, Math, Promise, Error,
    Supa: { selAll: async (tbl) => results[tbl] },
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    TS: '2026-10-14',
    gv: (id) => (els[id] ? els[id].value : ''),
    $: () => ({ modal: () => {} }),
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { sandbox, toasts, el, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('a null list result is a load failure: danger toast, tables cleared, no crash', async () => {
  const e = makeEnv({ job_postings: [], candidates: null, applications: [] });
  await e.run('loadRecruitment()');
  assert.deepEqual(e.toasts.map((t) => t.type), ['danger']);
  assert.match(e.toasts[0].msg, /^No se pudo cargar reclutamiento/);
  for (const id of ['rec-jobs-tbody', 'rec-candidates-tbody', 'rec-pipeline-tbody']) assert.equal(e.el(id).innerHTML, '');
  assert.equal(e.sandbox.DS.candidates.length, 0);
});

test('an error envelope still shows its Spanish reason (unchanged)', async () => {
  const e = makeEnv({ job_postings: [], candidates: { error: { code: 'forbidden', message: 'forbidden' } }, applications: [] });
  await e.run('loadRecruitment()');
  assert.deepEqual(e.toasts, [{ msg: 'No tienes permiso para esta acción', type: 'danger' }]);
  assert.equal(e.el('rec-jobs-tbody').innerHTML, '');
});

test('three lists load into the cache with no toast (unchanged)', async () => {
  const job = { id: 'j1', title: 'Dev', status: 'open', created_at: '2026-10-01' };
  const e = makeEnv({ job_postings: [job], candidates: [], applications: [] });
  await e.run('loadRecruitment()');
  assert.equal(e.toasts.length, 0);
  assert.equal(e.sandbox.DS.job_postings.length, 1);
  assert.match(e.el('rec-jobs-tbody').innerHTML, /<strong>Dev<\/strong>/);
});

test('demo mode: a second application for the same candidate and posting is refused, nothing added', async () => {
  const e = makeEnv({});
  e.sandbox.A.demo = true;
  e.sandbox.DS.candidates = [{ id: 'c1', company_id: 'c1', full_name: 'Ana' }];
  e.sandbox.DS.job_postings = [{ id: 'j1', company_id: 'c1', title: 'Cajero' }];
  e.sandbox.DS.applications = [{ id: 'ap-1', company_id: 'c1', job_posting_id: 'j1', candidate_id: 'c1', stage: 'applied' }];
  Object.assign(e.el('ra-candidate'), { value: 'c1' });
  Object.assign(e.el('ra-job'), { value: 'j1' });
  Object.assign(e.el('ra-id'), { value: '' });
  await e.run('saveApplication()');
  assert.deepEqual(e.toasts, [{ msg: 'El candidato ya tiene una postulación para esta vacante', type: 'danger' }]);
  assert.equal(e.sandbox.DS.applications.length, 1);
});

test('demo mode: editing the same application keeps its own pair (unchanged)', async () => {
  const e = makeEnv({});
  e.sandbox.A.demo = true;
  e.sandbox.DS.candidates = [{ id: 'c1', company_id: 'c1', full_name: 'Ana' }];
  e.sandbox.DS.job_postings = [{ id: 'j1', company_id: 'c1', title: 'Cajero' }];
  e.sandbox.DS.applications = [{ id: 'ap-1', company_id: 'c1', job_posting_id: 'j1', candidate_id: 'c1', stage: 'applied' }];
  Object.assign(e.el('ra-candidate'), { value: 'c1' });
  Object.assign(e.el('ra-job'), { value: 'j1' });
  Object.assign(e.el('ra-id'), { value: 'ap-1' });
  Object.assign(e.el('ra-stage'), { value: 'interview' });
  await e.run('saveApplication()');
  assert.equal(e.toasts[0].msg, '✅ Postulación guardada');
  assert.equal(e.sandbox.DS.applications[0].stage, 'interview');
});

let pass = 0;
for (const [name, fn] of tests) {
  try { await fn(); pass++; console.log('ok  ', name); }
  catch (err) { console.log('FAIL', name, '\n    ', err.message.split('\n').slice(0, 4).join('\n     ')); }
}
console.log(`${pass}/${tests.length} passed`);
process.exitCode = pass === tests.length ? 0 : 1;
