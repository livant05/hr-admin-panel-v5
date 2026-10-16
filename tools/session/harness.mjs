// tools/session/harness.mjs
//
// Drives the REAL session lifecycle code of hr_admin_panel.html (the Supa
// adapter, the AUTH section, gv and init) in a node:vm sandbox with a fake
// fetch, localStorage and DOM. Nothing is reimplemented here: the sources are
// sliced out of the HTML by name and executed as written.
//
// Covers AUTH-9 / AUTH-6 (UI) / XC-5 / SR-1 of rrhh-go-auth-cutover: Go sign-in
// without the global 401 hook, 429 wording, register auto-login, logout with
// a best-effort server logout, expiry on load, a single "Sesión expirada" on mid-session 401,
// and demo mode making no network request at all.
//
// Run: node tools/session/harness.mjs   (exit code 1 on any failed assertion)
// Mutation runs point SESSION_HTML at a modified copy of the page.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.SESSION_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

function between(startMarker, endMarker) {
  const s = html.indexOf(startMarker);
  assert.ok(s >= 0, `${startMarker} not found`);
  const e = html.indexOf(endMarker, s);
  assert.ok(e > s, `${endMarker} not found after ${startMarker}`);
  return html.slice(s, e);
}

const supaSrc = between('var Supa = {', '\n};\n') + '\n};\n';
const authSrc = between('// ════════════════ AUTH ', 'function loadUI(');
const gvSrc = extractFunction(html, 'function gv(');
const initSrc = extractFunction(html, 'async function init(');

const NOW = Date.parse('2026-06-01T12:00:00Z');
class FakeDate extends Date {
  constructor(...a) { if (a.length) super(...a); else super(NOW); }
  static now() { return NOW; }
}

const b64url = (o) => Buffer.from(JSON.stringify(o)).toString('base64url');
// Unsigned test token: the browser only reads the payload.
const jwt = (payload) => `${b64url({ alg: 'HS256', typ: 'JWT' })}.${b64url(payload)}.sig`;
const validToken = () => jwt({ sub: 'u1', exp: NOW / 1000 + 3600 });
const expiredToken = () => jwt({ sub: 'u1', exp: NOW / 1000 - 1 });

const loginPayload = (over = {}) => ({
  access_token: validToken(), token_type: 'bearer', expires_in: 86400,
  company_name: 'Ventatec',
  user: {
    id: 'u1', company_id: 'c1', email: 'ana@x.test', role: 'admin', first_name: 'Ana', last_name: 'Pérez',
    user_metadata: { role: 'admin', company_id: 'c1', first_name: 'Ana', last_name: 'Pérez', company_name: 'Ventatec' },
  },
  ...over,
});
const res = (status, body, headers = {}) => ({
  status, ok: status >= 200 && status < 300,
  headers: { get: (n) => headers[Object.keys(headers).find((k) => k.toLowerCase() === n.toLowerCase())] ?? null },
  json: async () => { if (body === undefined) throw new Error('no body'); return body; },
});
const errBody = (code, message = 'x', fields) => ({ error: { code, message, ...(fields ? { fields } : {}) } });

function makeEnv() {
  const calls = [];
  const toasts = [];
  const views = [];
  const log = { stopZK: 0, loadUI: [], go: [], rolePerms: 0 };
  const store = new Map();
  const els = {};
  const el = (id) => (els[id] ??= {
    id, value: '', textContent: '', innerHTML: '', disabled: false, style: {},
    _cls: new Set(),
    classList: {
      add: (...c) => c.forEach((x) => els[id]._cls.add(x)),
      remove: (...c) => c.forEach((x) => els[id]._cls.delete(x)),
      contains: (c) => els[id]._cls.has(c),
      toggle: (c, on) => { (on ?? !els[id]._cls.has(c)) ? els[id]._cls.add(c) : els[id]._cls.delete(c); },
    },
  });
  const server = { respond: () => { throw new Error('unexpected fetch'); } };
  const sandbox = {
    A: { session: null, role: 'admin', companyId: '', companyName: '', theme: 'light', demo: false, perms: null },
    DS: {
      companies: [{ id: 'co1', name: 'Empresa Demo S.A.' }],
      users: [{ id: 'u1', email: 'admin@demo.com', company_id: 'co1', role: 'admin', first_name: 'Admin', last_name: 'Demo', status: 'active' }],
      employees: [{ id: 'e1' }], departments: [{ id: 'd1' }],
    },
    A_goUrl: '', DEMO_CALC_MSG: 'demo',
    Date: FakeDate, JSON, Object, Array, Number, String, Math, Promise, parseInt, isFinite, atob: globalThis.atob,
    Error, setTimeout: () => 0,
    crypto: { randomUUID: () => 'uuid-fixed' },
    fetch: async (url, opts = {}) => {
      calls.push({ url, method: opts.method || 'GET', headers: opts.headers || {}, body: opts.body ? JSON.parse(opts.body) : undefined });
      return server.respond(url, opts);
    },
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    },
    document: { getElementById: el, body: { classList: { add() {}, remove() {} }, className: '' } },
    toast: (msg, type) => toasts.push({ msg, type }),
    showV: (v) => views.push(v),
    go: (p) => log.go.push(p),
    loadUI: (m) => log.loadUI.push(m),
    loadRolePerms: () => { log.rolePerms++; },
    loadNotifications: () => {},
    stopZKPolling: () => { log.stopZK++; },
  };
  vm.createContext(sandbox);
  vm.runInContext([supaSrc, gvSrc, authSrc, initSrc].join('\n'), sandbox);
  const run = (code) => vm.runInContext(code, sandbox);
  const A = sandbox.A;
  return {
    A, calls, toasts, views, log, store, el, server, run,
    respondWith: (fn) => { server.respond = fn; },
    fillLogin: (em, pw) => { el('l-email').value = em; el('l-pwd').value = pw; },
    fillRegister: (o = {}) => {
      const v = { 'rg-co': 'Acme', 'rg-fn': 'Ana', 'rg-ln': 'Pérez', 'rg-em': 'ana@x.test', 'rg-pw': 'secret123', 'rg-c2': 'secret123', ...o };
      Object.entries(v).forEach(([k, x]) => { el(k).value = x; });
    },
    lastView: () => views[views.length - 1],
    expiredToasts: () => toasts.filter((t) => t.msg === 'Sesión expirada').length,
  };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// ── sign-in ─────────────────────────────────────────────────────────────
test('login success: one POST to /api/auth/login, session stored, no companies lookup', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(200, loginPayload()));
  e.fillLogin('ana@x.test', 'secret123');
  await e.run('doLogin()');
  assert.equal(e.calls.length, 1);
  assert.equal(e.calls[0].url, '/api/auth/login');
  assert.equal(e.calls[0].method, 'POST');
  assert.deepEqual(e.calls[0].body, { email: 'ana@x.test', password: 'secret123' });
  assert.ok(!e.calls.some((c) => /companies|supabase|auth\/v1|rest\/v1/.test(c.url)));
  assert.equal(e.A.session.access_token, validToken());
  assert.equal(e.A.role, 'admin');
  assert.equal(e.A.companyId, 'c1');
  assert.equal(e.A.companyName, 'Ventatec');
  assert.equal(JSON.parse(e.store.get('hr_sess')).access_token, validToken());
  assert.deepEqual(JSON.parse(e.store.get('hr_meta')), { role: 'admin', companyId: 'c1', companyName: 'Ventatec', email: 'ana@x.test', name: 'Ana Pérez' });
  assert.equal(e.lastView(), 'app');
  assert.deepEqual(e.log.go, ['dashboard']);
  assert.equal(e.log.loadUI.length, 1);
  assert.equal(e.log.rolePerms, 1);
  assert.equal(e.toasts.length, 1);
  assert.equal(e.el('btn-l').disabled, false);
});

test('company name comes from the login response: top-level, then user_metadata, then the default', async () => {
  for (const [over, want] of [
    [{ company_name: 'Top Co' }, 'Top Co'],
    [{ company_name: undefined }, 'Ventatec'], // user_metadata.company_name
    [{ company_name: undefined, user: { ...loginPayload().user, user_metadata: { role: 'admin', company_id: 'c1' } } }, 'Mi Empresa'],
  ]) {
    const e = makeEnv();
    e.respondWith(async () => res(200, loginPayload(over)));
    e.fillLogin('ana@x.test', 'secret123');
    await e.run('doLogin()');
    assert.equal(e.A.companyName, want);
    assert.equal(e.calls.length, 1);
  }
});

test('login 401: "Credenciales inválidas", and the global 401 hook is NOT triggered', async () => {
  const e = makeEnv();
  e.A.session = { access_token: 'stale' }; // a session exists: gres would treat a 401 as expiry
  e.respondWith(async () => res(401, errBody('unauthorized', 'invalid credentials')));
  e.fillLogin('ana@x.test', 'wrong');
  await e.run('doLogin()');
  assert.equal(e.el('l-err').textContent, 'Credenciales inválidas');
  assert.equal(e.el('l-err').classList.contains('d-none'), false);
  assert.equal(e.expiredToasts(), 0);
  assert.equal(e.toasts.length, 0);
  assert.equal(e.log.stopZK, 0, 'clearSession must not run');
  assert.equal(e.A.session.access_token, 'stale');
  assert.equal(e.views.length, 0);
  assert.equal(e.store.size, 0);
  assert.equal(e.el('btn-l').disabled, false);
});

test('login 429: message carries the Retry-After seconds, with a fallback when the header is absent', async () => {
  for (const [headers, want] of [
    [{ 'Retry-After': '37' }, 'Demasiados intentos, intenta en 37 s'],
    [{ 'Retry-After': '5' }, 'Demasiados intentos, intenta en 5 s'],
    [{}, 'Demasiados intentos, intenta más tarde'],
    [{ 'Retry-After': 'soon' }, 'Demasiados intentos, intenta más tarde'],
  ]) {
    const e = makeEnv();
    e.A.session = { access_token: 'stale' };
    e.respondWith(async () => res(429, errBody('rate_limited', 'too many requests, retry later'), headers));
    e.fillLogin('ana@x.test', 'x');
    await e.run('doLogin()');
    assert.equal(e.el('l-err').textContent, want);
    assert.equal(e.expiredToasts(), 0);
    assert.equal(e.A.session.access_token, 'stale');
  }
});

test('login: network failure and unexpected server answers show a message and re-enable the button', async () => {
  for (const respond of [
    async () => { throw new TypeError('Failed to fetch'); },
    async () => res(500, errBody('internal', 'internal error')),
    async () => res(502, undefined),
    async () => res(200, { nope: true }), // 200 without a token is not a session
  ]) {
    const e = makeEnv();
    e.respondWith(respond);
    e.fillLogin('ana@x.test', 'secret123');
    await e.run('doLogin()');
    assert.notEqual(e.el('l-err').textContent, '');
    assert.equal(e.el('l-err').classList.contains('d-none'), false);
    assert.equal(e.A.session, null);
    assert.equal(e.store.size, 0);
    assert.equal(e.views.length, 0);
    assert.equal(e.el('btn-l').disabled, false);
  }
});

test('login with empty fields makes no request', async () => {
  const e = makeEnv();
  await e.run('doLogin()');
  assert.equal(e.calls.length, 0);
  assert.equal(e.el('l-err').textContent, 'Completa todos los campos');
});

// ── register ────────────────────────────────────────────────────────────
test('register: POST /api/auth/register with exactly the Go fields, then auto-login with no second request', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(201, loginPayload({ company_name: 'Acme' })));
  e.fillRegister();
  await e.run('doReg()');
  assert.equal(e.calls.length, 1);
  assert.equal(e.calls[0].url, '/api/auth/register');
  assert.equal(e.calls[0].method, 'POST');
  assert.deepEqual(Object.keys(e.calls[0].body).sort(), ['company_name', 'email', 'first_name', 'last_name', 'password']);
  assert.deepEqual(e.calls[0].body, { company_name: 'Acme', first_name: 'Ana', last_name: 'Pérez', email: 'ana@x.test', password: 'secret123' });
  assert.equal(e.A.session.access_token, validToken());
  assert.equal(e.A.companyName, 'Acme');
  assert.equal(JSON.parse(e.store.get('hr_sess')).access_token, validToken());
  assert.equal(e.lastView(), 'app');
  assert.deepEqual(e.log.go, ['dashboard']);
  assert.equal(e.el('btn-rg').disabled, false);
});

test('register failures: 409 and 400 show Spanish messages and create no session', async () => {
  for (const [r, want] of [
    [res(409, errBody('conflict', 'already exists')), /Ya existe una cuenta con ese correo/],
    [res(400, errBody('validation_failed', 'validation failed', { password: 'too short', company_name: 'required' })), /Datos inválidos.*contraseña.*empresa|Datos inválidos.*empresa.*contraseña/],
    [res(429, errBody('rate_limited'), { 'Retry-After': '12' }), /Demasiados intentos, intenta en 12 s/],
  ]) {
    const e = makeEnv();
    e.respondWith(async () => r);
    e.fillRegister();
    await e.run('doReg()');
    assert.match(e.el('rg-err').textContent, want);
    assert.equal(e.el('rg-err').classList.contains('d-none'), false);
    assert.equal(e.A.session, null);
    assert.equal(e.store.size, 0);
    assert.equal(e.views.length, 0);
    assert.equal(e.expiredToasts(), 0);
    assert.equal(e.el('btn-rg').disabled, false);
  }
});

test('register client-side checks (mismatch, short password, empty) make no request', async () => {
  for (const over of [{ 'rg-c2': 'other' }, { 'rg-pw': 'short', 'rg-c2': 'short' }, { 'rg-co': '' }]) {
    const e = makeEnv();
    e.fillRegister(over);
    await e.run('doReg()');
    assert.equal(e.calls.length, 0);
    assert.notEqual(e.el('rg-err').textContent, '');
  }
});

// ── logout ──────────────────────────────────────────────────────────────
const settle = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); };
const loginFor = async (e) => {
  e.respondWith(async () => res(200, loginPayload()));
  e.fillLogin('ana@x.test', 'secret123');
  await e.run('doLogin()');
  e.calls.length = 0; e.toasts.length = 0; e.views.length = 0;
  e.A.perms = { dashboard: true };
};
const assertSignedOutLocally = (e) => {
  assert.equal(e.A.session, null);
  assert.equal(e.A.perms, null);
  assert.equal(e.store.has('hr_sess'), false);
  assert.equal(e.store.has('hr_meta'), false);
  assert.equal(e.log.stopZK, 1);
  assert.equal(e.lastView(), 'login');
  assert.equal(e.run('DS.employees.length'), 0);
};

test('logout sends one POST /api/auth/logout with the bearer token, then clears the session, stops polling and shows login', async () => {
  const e = makeEnv();
  await loginFor(e);
  e.respondWith(async () => res(204));
  await e.run('doLogout()');
  await settle();
  assert.equal(e.calls.length, 1);
  assert.equal(e.calls[0].url, '/api/auth/logout');
  assert.equal(e.calls[0].method, 'POST');
  assert.equal(e.calls[0].headers.Authorization, 'Bearer ' + validToken());
  assertSignedOutLocally(e);
  assert.deepEqual(e.toasts, [{ msg: 'Sesión cerrada', type: 'info' }]);
});

test('logout still signs out locally, with no error toast, when the server is unreachable', async () => {
  const e = makeEnv();
  await loginFor(e);
  e.respondWith(async () => { throw new TypeError('Failed to fetch'); });
  await e.run('doLogout()');
  await settle();
  assert.equal(e.calls.length, 1);
  assertSignedOutLocally(e);
  assert.deepEqual(e.toasts, [{ msg: 'Sesión cerrada', type: 'info' }]);
});

test('logout answered 401 or 500 does not start the "Sesión expirada" flow nor toast an error', async () => {
  for (const status of [401, 500]) {
    const e = makeEnv();
    await loginFor(e);
    e.respondWith(async () => res(status, errBody(status === 401 ? 'unauthorized' : 'internal')));
    await e.run('doLogout()');
    await settle();
    assert.equal(e.calls.length, 1, String(status));
    assertSignedOutLocally(e);
    assert.equal(e.expiredToasts(), 0, String(status));
    assert.equal(e.run('Supa.sessionExpired'), false, String(status));
    assert.deepEqual(e.toasts, [{ msg: 'Sesión cerrada', type: 'info' }], String(status));
  }
});

test('logout with no session makes no request but still ends on login', async () => {
  const e = makeEnv();
  await e.run('doLogout()');
  await settle();
  assert.equal(e.calls.length, 0);
  assert.equal(e.lastView(), 'login');
});

// ── restore on load ─────────────────────────────────────────────────────
const seedSession = (e, token, meta = { role: 'admin', companyId: 'c1', companyName: 'Ventatec', email: 'ana@x.test', name: 'Ana' }) => {
  e.store.set('hr_sess', JSON.stringify({ access_token: token, user: {} }));
  e.store.set('hr_meta', JSON.stringify(meta));
};

test('init with a valid token restores the session without any request', async () => {
  const e = makeEnv();
  seedSession(e, validToken());
  await e.run('init()');
  assert.equal(e.calls.length, 0);
  assert.equal(e.A.session.access_token, validToken());
  assert.equal(e.A.companyId, 'c1');
  assert.equal(e.A.companyName, 'Ventatec');
  assert.equal(e.lastView(), 'app');
  assert.deepEqual(e.log.go, ['dashboard']);
  assert.equal(e.store.has('hr_sess'), true);
});

test('init with an expired, malformed or exp-less token goes to login with no request and drops the keys', async () => {
  const cases = {
    expired: expiredToken(),
    'exactly now (exp*1000 <= now)': jwt({ exp: NOW / 1000 }),
    'not a jwt': 'garbage',
    'two segments': 'a.b',
    'payload not base64': 'a.@@@@.c',
    'payload not json': `a.${Buffer.from('nope').toString('base64url')}.c`,
    'no exp claim': jwt({ sub: 'u1' }),
    'exp is a string': jwt({ exp: 'tomorrow' }),
    'empty token': '',
  };
  for (const [name, token] of Object.entries(cases)) {
    const e = makeEnv();
    seedSession(e, token);
    await e.run('init()');
    assert.equal(e.calls.length, 0, name);
    assert.equal(e.lastView(), 'login', name);
    assert.equal(e.A.session, null, name);
    assert.equal(e.store.has('hr_sess'), false, name);
    assert.equal(e.store.has('hr_meta'), false, name);
    assert.equal(e.log.loadUI.length, 0, name);
    assert.deepEqual(e.log.go, [], name);
  }
});

test('init: a token one second before expiry is still valid (boundary)', async () => {
  const e = makeEnv();
  seedSession(e, jwt({ exp: NOW / 1000 + 1 }));
  await e.run('init()');
  assert.equal(e.lastView(), 'app');
});

test('init with unreadable stored JSON shows login and removes the corrupt keys', async () => {
  const e = makeEnv();
  e.store.set('hr_sess', '{not json');
  e.store.set('hr_meta', '{}');
  await e.run('init()');
  assert.equal(e.calls.length, 0);
  assert.equal(e.lastView(), 'login');
  assert.equal(e.store.has('hr_sess'), false);
  assert.equal(e.store.has('hr_meta'), false);
});

test('init without a stored session shows login', async () => {
  const e = makeEnv();
  await e.run('init()');
  assert.equal(e.lastView(), 'login');
  assert.equal(e.calls.length, 0);
});

// ── mid-session 401 ─────────────────────────────────────────────────────
async function loggedIn() {
  const e = makeEnv();
  e.respondWith(async () => res(200, loginPayload()));
  e.fillLogin('ana@x.test', 'secret123');
  await e.run('doLogin()');
  e.calls.length = 0; e.toasts.length = 0; e.views.length = 0; e.log.stopZK = 0;
  return e;
}

test('a 401 from a Go call clears the session once: one toast, login view, keys removed', async () => {
  const e = await loggedIn();
  e.respondWith(async () => res(401, errBody('unauthorized', 'invalid token')));
  const out = await e.run("Supa.sel('employees')");
  assert.equal(out.error.code, 'unauthorized'); // callers still receive an error envelope
  assert.equal(e.expiredToasts(), 1);
  assert.equal(e.toasts.length, 1);
  assert.equal(e.log.stopZK, 1);
  assert.deepEqual(e.views, ['login']);
  assert.equal(e.A.session, null);
  assert.equal(e.store.has('hr_sess'), false);
  assert.equal(e.store.has('hr_meta'), false);
});

test('concurrent 401s still produce exactly one toast, one clearSession and one login view', async () => {
  const e = await loggedIn();
  e.respondWith(async () => res(401, errBody('unauthorized', 'invalid token')));
  await e.run("Promise.all([Supa.sel('employees'), Supa.sel('departments'), Supa.goFetch('/api/x'), Supa.ins('employees', {}), Supa.upd('employees', 'i', {}), Supa.del('employees', 'i')])");
  assert.equal(e.calls.length, 6, 'all six requests really went out');
  assert.equal(e.expiredToasts(), 1);
  assert.equal(e.toasts.length, 1);
  assert.equal(e.log.stopZK, 1);
  assert.deepEqual(e.views, ['login']);
});

test('onUnauthorized is re-entrancy safe on its own, and a new session can expire again', async () => {
  const e = await loggedIn();
  e.run('Supa.onUnauthorized(); Supa.onUnauthorized();');
  assert.equal(e.expiredToasts(), 1);
  assert.equal(e.log.stopZK, 1);
  // log in again: the flag is re-armed by the new session
  e.respondWith(async () => res(200, loginPayload()));
  e.fillLogin('ana@x.test', 'secret123');
  await e.run('doLogin()');
  e.respondWith(async () => res(401, errBody('unauthorized')));
  await e.run("Supa.sel('employees')");
  assert.equal(e.expiredToasts(), 2);
  assert.equal(e.log.stopZK, 2);
});

test('statuses other than 401 do not end the session', async () => {
  for (const status of [400, 403, 404, 409, 429, 500]) {
    const e = await loggedIn();
    e.respondWith(async () => res(status, errBody('x')));
    await e.run("Supa.sel('employees')");
    assert.equal(e.expiredToasts(), 0, String(status));
    assert.ok(e.A.session, String(status));
    assert.equal(e.store.has('hr_sess'), true, String(status));
  }
});

test('a 401 without a session, or in demo mode, does not trigger the hook', async () => {
  const e = makeEnv();
  e.respondWith(async () => res(401, errBody('unauthorized')));
  await e.run("Supa.sel('employees')"); // A.session is null
  assert.equal(e.expiredToasts(), 0);
  assert.equal(e.views.length, 0);
  const d = makeEnv();
  d.A.demo = true; d.A.session = { access_token: 'demo-1' };
  await d.run("Supa.gres({status: 401, ok: false, json: async () => ({error: {code: 'unauthorized'}})})");
  assert.equal(d.expiredToasts(), 0);
  assert.ok(d.A.session);
});

test('the Authorization header still carries the Go token on Go calls', async () => {
  const e = await loggedIn();
  e.respondWith(async () => res(200, []));
  await e.run("Supa.sel('employees')");
  assert.equal(e.calls[0].headers.Authorization, 'Bearer ' + validToken());
  assert.equal(e.calls[0].url, '/api/employees');
});

// ── demo mode (XC-5) ────────────────────────────────────────────────────
test('demoMode enters demo without inventing A.url / A.key and without a request', async () => {
  const e = makeEnv();
  await e.run('demoMode()');
  assert.equal(e.A.demo, true);
  assert.equal(e.A.companyId, 'co1');
  assert.equal('url' in e.A || 'key' in e.A, false);
  assert.equal(e.store.get('hr_demo'), '1');
  assert.equal(e.lastView(), 'login');
  assert.equal(e.calls.length, 0);
});

test('demo login, register and logout make zero network requests', async () => {
  const e = makeEnv();
  await e.run('demoMode()');
  // login
  e.fillLogin('admin@demo.com', 'abcd');
  await e.run('doLogin()');
  assert.ok(e.A.session.access_token.startsWith('demo-'));
  assert.equal(e.lastView(), 'app');
  assert.equal(e.A.companyName, 'Empresa Demo S.A.');
  // logout
  await e.run('doLogout()');
  assert.equal(e.lastView(), 'login');
  assert.equal(e.A.session, null);
  // wrong demo credentials
  e.fillLogin('nobody@demo.com', 'abcd');
  await e.run('doLogin()');
  assert.equal(e.el('l-err').textContent, 'Credenciales inválidas');
  // register: unchanged behaviour (creates the demo company and user, back to login)
  e.fillRegister();
  e.views.length = 0; e.toasts.length = 0;
  await e.run('doReg()');
  assert.equal(e.lastView(), 'login');
  assert.deepEqual(e.toasts.map((t) => t.msg), ['¡Empresa creada! Inicia sesión.']);
  assert.equal(e.run("DS.companies.some(function(c){ return c.name === 'Acme'; })"), true);
  assert.equal(e.run("DS.users.some(function(u){ return u.email === 'ana@x.test'; })"), true);
  assert.equal(e.calls.length, 0, 'no fetch at all in demo');
});

test('exitDemo removes hr_demo, leaves demo mode and makes no request', async () => {
  const e = makeEnv();
  await e.run('demoMode()');
  assert.equal(e.store.get('hr_demo'), '1');
  await e.run('exitDemo()');
  assert.equal(e.store.has('hr_demo'), false);
  assert.equal(e.A.demo, false);
  assert.equal(e.A.companyId, '');
  assert.equal(e.lastView(), 'login');
  assert.equal(e.calls.length, 0);
});

test('the login view offers "Modo Demo" in real mode and "Salir del modo demo" in demo mode', async () => {
  const e = makeEnv();
  await e.run('init()');
  assert.equal(e.el('btn-demo-enter').classList.contains('d-none'), false);
  assert.equal(e.el('btn-demo-exit').classList.contains('d-none'), true);
  await e.run('demoMode()');
  assert.equal(e.el('btn-demo-enter').classList.contains('d-none'), true);
  assert.equal(e.el('btn-demo-exit').classList.contains('d-none'), false);
  await e.run('exitDemo()');
  assert.equal(e.el('btn-demo-enter').classList.contains('d-none'), false);
  assert.equal(e.el('btn-demo-exit').classList.contains('d-none'), true);
  // a reload while in demo shows the exit button too
  const r = makeEnv();
  r.store.set('hr_demo', '1');
  await r.run('init()');
  assert.equal(r.el('btn-demo-exit').classList.contains('d-none'), false);
});

test('init in demo mode restores a demo session without the expiry check', async () => {
  const e = makeEnv();
  e.store.set('hr_demo', '1');
  seedSession(e, 'demo-1700000000000');
  await e.run('init()');
  assert.equal(e.lastView(), 'app');
  assert.equal(e.A.session.access_token, 'demo-1700000000000');
  assert.equal(e.calls.length, 0);
});

test('the login view markup carries both demo buttons (the setup view is going away)', () => {
  const login = between('<div id="v-login"', '<!-- REGISTER -->');
  assert.match(login, /id="btn-demo-enter"[^>]*onclick="demoMode\(\)"/);
  assert.match(login, /id="btn-demo-exit"[^>]*onclick="exitDemo\(\)"/);
  assert.match(login, /Salir del modo demo/);
});

// ── Supabase removal (SR-1, SR-2, XC-5) ─────────────────────────────────
test('real mode sends every table to /api/<table> on Go with only the Bearer header (no table list, no apikey)', async () => {
  const e = await loggedIn();
  e.respondWith(async (url, opts) => (opts.method === 'DELETE' ? res(204, undefined) : res(200, [])));
  // "job_postings" and "weird_table" were never in the sandbox's old route list.
  for (const tbl of ['job_postings', 'users', 'zkteco_devices', 'weird_table']) {
    e.calls.length = 0;
    await e.run(`Supa.sel('${tbl}', {id: 'x'})`);
    await e.run(`Supa.selAll('${tbl}')`);
    await e.run(`Supa.ins('${tbl}', {a: 1})`);
    await e.run(`Supa.upd('${tbl}', 'i1', {a: 2})`);
    await e.run(`Supa.del('${tbl}', 'i1')`);
    assert.deepEqual(e.calls.map((c) => `${c.method} ${c.url}`), [
      `GET /api/${tbl}?id=x`,
      `GET /api/${tbl}?_limit=1000&_offset=0`,
      `POST /api/${tbl}`,
      `PATCH /api/${tbl}/i1`,
      `DELETE /api/${tbl}/i1`,
    ]);
    for (const c of e.calls) {
      assert.equal(c.headers.Authorization, 'Bearer ' + validToken());
      assert.ok(!('apikey' in c.headers), 'no apikey header');
    }
  }
  assert.equal(e.calls.length, 5);
});

test('the Supa facade no longer exposes the Supabase routing or header helpers', async () => {
  const e = makeEnv();
  for (const name of ['hdr', 'route', 'goTables']) {
    assert.equal(e.run(`typeof Supa.${name}`), 'undefined', name);
  }
  for (const name of ['sel', 'selAll', 'ins', 'upd', 'del', 'goFetch', 'signIn', 'signUp', 'signOut', 'gres']) {
    assert.equal(e.run(`typeof Supa.${name}`), 'function', name);
  }
});

test('first load in a clean browser shows login directly: no setup view, no request, no config stored', async () => {
  const e = makeEnv();
  await e.run('init()');
  assert.deepEqual(e.views, ['login']);
  assert.equal(e.calls.length, 0);
  assert.equal(e.store.size, 0);
  assert.equal('url' in e.A || 'key' in e.A, false, 'init must not invent A.url / A.key');
  assert.equal(e.A.demo, false);
  // old browsers may still hold hr_url / hr_key from the setup view: they are simply ignored
  const old = makeEnv();
  old.store.set('hr_url', 'https://old.example'); old.store.set('hr_key', 'old');
  await old.run('init()');
  assert.deepEqual(old.views, ['login']);
  assert.equal('url' in old.A || 'key' in old.A, false);
});

test('the page has no setup view, no Supabase config and no Supabase endpoints left', () => {
  const banned = /supabase|\/rest\/v1|\/auth\/v1|apikey|hr_go_tables|swciybuhetkfrtpyjvgb|DEFAULT_URL|DEFAULT_KEY|saveConfig|showSQLSchema|id="v-setup"|id="s-url"|id="s-key"|\bA\.(url|key)\b/i;
  const hits = html.split('\n').map((l, i) => [i + 1, l]).filter(([, l]) => banned.test(l)).map(([n, l]) => `${n}: ${l.slice(0, 80)}`);
  assert.deepEqual(hits, []);
  assert.doesNotMatch(extractFunction(html, 'function showV('), /setup/);
  // the dev override of the Go base URL stays
  assert.match(html, /localStorage\.getItem\('hr_go_url'\)/);
});

test('settings shows "Servidor: este sitio" in real mode and "Modo Demo activo" in demo mode', () => {
  const src = between("'settings': function(){", '\n    }\n  };') + '\n    }';
  for (const [demo, want] of [[false, 'Servidor: este sitio'], [true, 'Modo Demo activo']]) {
    const e = makeEnv();
    e.A.demo = demo; e.A.companyName = 'Ventatec';
    e.run(`(${src.replace(/^'settings': /, '')})()`);
    assert.equal(e.el('st-url').textContent, want);
    assert.equal(e.el('st-coname').value, 'Ventatec');
  }
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
