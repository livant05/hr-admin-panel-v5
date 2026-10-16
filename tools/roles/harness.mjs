// tools/roles/harness.mjs
//
// Drives the REAL role writes of hr_admin_panel.html (saveRole, updateRole,
// delRole, with apiErrMsg) in a node:vm sandbox with a stubbed Supa.
//
// Run: node tools/roles/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.ROLES_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

const objectLiteral = (header) => (html.includes(header) ? extractFunction(html, header) + ';' : '');
const optional = (header) => (html.includes(header) ? extractFunction(html, header) : '');

const srcs = [
  objectLiteral('var API_FIELD_ES='),
  objectLiteral('var API_CONFLICT_ES='),
  extractFunction(html, 'function apiErrMsg('),
  optional('function roleErrMsg('),
  optional('function reservedRoleName('),
  extractFunction(html, 'async function saveRole('),
  extractFunction(html, 'async function delRole('),
  extractFunction(html, 'async function updateRole('),
].join('\n');

const RESERVED = 'El nombre "admin" está reservado para el administrador';

function makeEnv({ demo = false, res = {} } = {}) {
  const toasts = [], calls = [];
  const els = {};
  const el = (id) => (els[id] ??= { id, value: '' });
  const sandbox = {
    A: { demo, companyId: 'c1' },
    DS: { roles: [{ id: 'r1', company_id: 'c1', name: 'Ventas' }] },
    Object, String, Array,
    Supa: {
      ins: async (t, d) => { calls.push(['ins', t, d]); return res.ins ?? [{ id: 'r2' }]; },
      upd: async (t, id, d) => { calls.push(['upd', t, id, d]); return res.upd ?? [{ id }]; },
      del: async (t, id) => { calls.push(['del', t, id]); return res.del ?? null; },
    },
    gv: (id) => el(id).value.trim(),
    document: { getElementById: el },
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    confirm: () => true,
    loadRoleMgmt: () => {},
  };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { sandbox, toasts, calls, el, run: (c) => vm.runInContext(c, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('saveRole refuses "admin" in any case before calling the API', async () => {
  for (const name of ['admin', ' Admin ']) {
    const e = makeEnv();
    e.el('new-role-name').value = name;
    await e.run('saveRole()');
    assert.equal(e.calls.length, 0);
    assert.deepEqual(e.toasts, [{ msg: RESERVED, type: 'warning' }]);
  }
});

test('updateRole refuses renaming to "admin", in real and demo mode', async () => {
  for (const demo of [false, true]) {
    const e = makeEnv({ demo });
    e.el('role-edit-r1').value = 'ADMIN';
    await e.run("updateRole('r1')");
    assert.equal(e.calls.length, 0);
    assert.deepEqual(e.toasts, [{ msg: RESERVED, type: 'warning' }]);
    assert.equal(e.sandbox.DS.roles[0].name, 'Ventas');
  }
});

test('a server reserved-name answer is shown in Spanish, not "validation failed"', async () => {
  const e = makeEnv({ res: { ins: { error: { code: 'validation_failed', message: 'validation failed', fields: { name: 'reserved name' } } } } });
  e.el('new-role-name').value = 'Supervisor';
  await e.run('saveRole()');
  assert.deepEqual(e.toasts, [{ msg: RESERVED, type: 'danger' }]);
});

test('delRole of a role in use shows the server count (409)', async () => {
  const e = makeEnv({ res: { del: { error: { code: 'conflict', message: '2 usuario(s) tienen este rol' } } } });
  await e.run("delRole('r1')");
  assert.deepEqual(e.toasts, [{ msg: 'No se pudo eliminar: 2 usuario(s) tienen este rol', type: 'danger' }]);
});

test('a duplicate name (409 already exists) is Spanish', async () => {
  const e = makeEnv({ res: { upd: { error: { code: 'conflict', message: 'already exists' } } } });
  e.el('role-edit-r1').value = 'Cajero';
  await e.run("updateRole('r1')");
  assert.deepEqual(e.toasts, [{ msg: 'Ya existe un rol con ese nombre', type: 'danger' }]);
});

test('saveRole with a normal name creates it (unchanged)', async () => {
  const e = makeEnv();
  e.el('new-role-name').value = 'Supervisor';
  await e.run('saveRole()');
  assert.deepEqual(JSON.parse(JSON.stringify(e.calls)), [['ins', 'roles', { name: 'Supervisor', permissions: {} }]]);
  assert.deepEqual(e.toasts, [{ msg: '✅ Rol agregado', type: 'success' }]);
});

let pass = 0;
for (const [name, fn] of tests) {
  try { await fn(); pass++; console.log('ok  ', name); }
  catch (err) { console.log('FAIL', name, '\n    ', err.message.split('\n').slice(0, 4).join('\n     ')); }
}
console.log(`${pass}/${tests.length} passed`);
process.exitCode = pass === tests.length ? 0 : 1;
