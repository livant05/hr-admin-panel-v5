// tools/zk-register/harness.mjs
//
// Drives the REAL registerZKDevice of hr_admin_panel.html in a node:vm sandbox
// against a fake POST /api/zkteco_devices. Devices are pre-registered: ADMS
// refuses a serial with no row, so the panel must send exactly the serial the
// device reports and show the server's answer.
//
// Run: node tools/zk-register/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.ZK_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');
const start = html.indexOf('async function registerZKDevice');
const end = html.indexOf('async function claimZKDevice');
assert.ok(start > 0 && end > start, 'registerZKDevice not found before claimZKDevice in hr_admin_panel.html');
const src = html.slice(start, end);

// The panel's real error mapper, so the messages are the ones users see.
const errStart = html.indexOf('var API_CONFLICT_ES');
const errEnd = html.indexOf('function apiErrMsg');
const errFnEnd = html.indexOf('\n}\n', errEnd) + 3;
assert.ok(errStart > 0 && errEnd > errStart, 'apiErrMsg block not found');
const errSrc = html.slice(errStart, errEnd) + html.slice(errEnd, errFnEnd);

function makeEnv({ demo = false, reply } = {}) {
  const inputs = { 'zk-new-serial': '', 'zk-new-name': '', 'zk-new-location': '' };
  const calls = [];
  const toasts = [];
  let reloads = 0;
  const sandbox = {
    A: { demo },
    Object, Array, String, RegExp, JSON, Promise,
    toast: (msg, type) => toasts.push({ msg, type: type || 'success' }),
    loadZKTeco: () => { reloads++; },
    recRequest: async (key, btn, call) => call(),
    DEMO_CALC_MSG: 'Cálculo no disponible en modo demo',
    document: { getElementById: (id) => (id in inputs ? { get value() { return inputs[id]; }, set value(v) { inputs[id] = v; } } : null) },
    Supa: {
      ins: async (tbl, data) => {
        calls.push({ tbl, data });
        return reply ? reply(data) : [Object.assign({ id: 'dev-1', company_id: 'co-1' }, data)];
      },
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(errSrc, sandbox);
  vm.runInContext(src, sandbox);
  return { inputs, calls, toasts, reloads: () => reloads, run: () => vm.runInContext('registerZKDevice()', sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('a valid serial is sent as typed (trimmed) with null empty labels, then the list reloads', async () => {
  const env = makeEnv();
  env.inputs['zk-new-serial'] = '  CJDE193560100  ';
  env.inputs['zk-new-location'] = ' Lobby ';
  await env.run();
  // JSON round-trip: objects built inside the vm realm have another prototype.
  assert.deepEqual(JSON.parse(JSON.stringify(env.calls)), [{ tbl: 'zkteco_devices', data: { serial_number: 'CJDE193560100', name: null, location: 'Lobby' } }]);
  assert.equal(env.toasts.at(-1).type, 'success');
  assert.equal(env.reloads(), 1);
  assert.equal(env.inputs['zk-new-serial'], '', 'the form is cleared after a registration');
  assert.equal(env.inputs['zk-new-location'], '');
});

test('an empty or malformed serial is refused before any request', async () => {
  for (const bad of ['', '   ', 'AB CD', 'AB/CD', 'S' + '9'.repeat(64)]) {
    const env = makeEnv();
    env.inputs['zk-new-serial'] = bad;
    await env.run();
    assert.equal(env.calls.length, 0, `serial ${JSON.stringify(bad)} must not be sent`);
    assert.equal(env.toasts.at(-1).type, 'warning');
    assert.equal(env.reloads(), 0);
  }
});

test('a 64 character serial is accepted (same rule as the server)', async () => {
  const env = makeEnv();
  env.inputs['zk-new-serial'] = 'S' + '9'.repeat(63);
  await env.run();
  assert.equal(env.calls.length, 1);
});

test('a duplicate serial shows the server message and keeps the form', async () => {
  const env = makeEnv({ reply: () => ({ error: { code: 'conflict', message: 'Ese número de serie ya está registrado' } }) });
  env.inputs['zk-new-serial'] = 'DUP1';
  await env.run();
  assert.deepEqual(env.toasts.at(-1), { msg: 'Ese número de serie ya está registrado', type: 'danger' });
  assert.equal(env.inputs['zk-new-serial'], 'DUP1', 'the typed serial stays for a correction');
  assert.equal(env.reloads(), 0);
});

test('a non-admin sees "Solo administradores"', async () => {
  const env = makeEnv({ reply: () => ({ error: { code: 'forbidden', message: 'admin only' } }) });
  env.inputs['zk-new-serial'] = 'X1';
  await env.run();
  assert.deepEqual(env.toasts.at(-1), { msg: 'Solo administradores', type: 'danger' });
});

test('demo mode sends nothing', async () => {
  const env = makeEnv({ demo: true });
  env.inputs['zk-new-serial'] = 'X1';
  await env.run();
  assert.equal(env.calls.length, 0);
  assert.equal(env.toasts.at(-1).type, 'info');
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (e) { failed++; console.log('FAIL ' + name + '\n     ' + (e && e.message)); }
}
console.log(`\n${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
