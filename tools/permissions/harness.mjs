// tools/permissions/harness.mjs
//
// Drives the REAL client-side permission code of hr_admin_panel.html
// (SIDEBAR_PERM_MAP, applyPermissions, PAGE_PERM_MAP, isPageAllowed,
// loadRolePerms, startSession/enterApp, clearSession) in a node:vm sandbox with
// a fake sidebar DOM and a fake roles fetch. Nothing is reimplemented: the
// sources are sliced out of the HTML by name and executed as written.
//
// Covers the server-permissions fix (frontend half): role 'admin' is never
// restricted; every other role is fail-closed (undefined key, missing roles
// row, failed or rejected roles fetch => denied); sub-key semantics; unmapped pages stay
// allowed; a non-admin lands on the first allowed page after its permissions
// load (or gets one toast when it has none); logout + login does not leave a
// stale menu behind.
//
// Run: node tools/permissions/harness.mjs   (exit code 1 on any failed assertion)
// Mutation runs point PERMISSIONS_HTML at a modified copy of the page.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.PERMISSIONS_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

function between(startMarker, endMarker) {
  const s = html.indexOf(startMarker);
  assert.ok(s >= 0, `${startMarker} not found`);
  const e = html.indexOf(endMarker, s);
  assert.ok(e > s, `${endMarker} not found after ${startMarker}`);
  return html.slice(s, e);
}

const permSrc = between('var SIDEBAR_PERM_MAP=[', '// ════════════════ DASHBOARD');
const startSessionSrc = extractFunction(html, 'function startSession(');
// enterApp only exists after the fix; before it startSession navigates by itself.
const enterAppSrc = html.includes('function enterApp(') ? extractFunction(html, 'async function enterApp(') : '';
const clearSessionSrc = extractFunction(html, 'function clearSession(');

// Sidebar as the page lays it out: headers followed by items; the last item
// (notifications) has no PAGE_PERM_MAP / SIDEBAR_PERM_MAP entry.
const GROUPS = [
  ['h-main', ['m-dashboard']],
  ['h-people', ['m-employees', 'm-directory', 'm-org-chart', 'm-attendance', 'm-vacations', 'm-medical']],
  ['h-pay', ['m-payroll', 'm-overtime', 'm-deductions', 'm-liquidation', 'm-pay-records']],
  ['h-docs', ['m-documents', 'm-calendar', 'm-reports', 'm-announcements']],
  ['h-org', ['m-recruitment', 'm-evaluations', 'm-training', 'm-surveys', 'm-uniforms']],
  ['h-sys', ['m-settings', 'm-notifications']],
];

function makeEnv({ role = 'admin', roles = [], rolesFail = false, rolesReject = false } = {}) {
  const log = { go: [], toasts: [], rolesCalls: 0, timers: 0 };
  const siblings = [];
  const byId = {};
  const node = (cls) => {
    const n = { style: { display: '' }, cls, idx: siblings.length,
      classList: { contains: (c) => cls === c }, get nextElementSibling() { return siblings[n.idx + 1] || null; } };
    siblings.push(n);
    return n;
  };
  GROUPS.forEach(([h, items]) => {
    byId[h] = node('nav-header');
    items.forEach((id) => { const li = node('nav-item'); byId[id] = { id, parentElement: li }; });
  });
  const state = { roles, rolesFail, rolesReject };
  const sandbox = {
    A: { session: null, role, perms: null, demo: false },
    DS: {}, Object, Array, JSON, Promise,
    Supa: {
      sessionExpired: false,
      sel: async (t) => {
        assert.equal(t, 'roles'); log.rolesCalls++;
        if (state.rolesReject) throw new TypeError('Failed to fetch'); // fetch rejects when the network is down
        return state.rolesFail ? { error: { code: 'x' } } : state.roles;
      },
    },
    document: {
      getElementById: (id) => byId[id] || null,
      // honours the two selectors applyPermissions uses
      querySelectorAll: (sel) => siblings.filter((n) => (sel.includes('.nav-item') && n.cls === 'nav-item') || (sel.includes('.nav-header') && n.cls === 'nav-header')),
    },
    localStorage: { setItem() {}, removeItem() {} },
    toast: (msg, type) => { log.toasts.push({ msg, type }); },
    showV() {}, loadUI() {}, stopZKPolling() {}, loadNotifications() {},
    go: (p) => log.go.push(p),
    setTimeout: () => { log.timers++; return 0; },
  };
  vm.createContext(sandbox);
  vm.runInContext([permSrc, startSessionSrc, enterAppSrc, clearSessionSrc].join('\n'), sandbox);
  const visible = () => Object.entries(byId).filter(([id]) => id.startsWith('m-'))
    .filter(([, e]) => e.parentElement.style.display !== 'none').map(([id]) => id);
  const headerVisible = (h) => byId[h].style.display !== 'none';
  // startSession reads the role from user_metadata, like the Go login payload.
  const login = async (r) => {
    return sandbox.startSession({ access_token: 't', user: { user_metadata: { role: r, company_id: 'c1' } } }, 'u@x.test');
  };
  const run = (code) => vm.runInContext(code, sandbox);
  const settle = () => new Promise((r) => setImmediate(r));
  return { A: sandbox.A, log, state, visible, headerVisible, login, run, settle };
}
const row = (name, permissions) => ({ id: 'r-' + name, name, permissions });

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const ALL_MENU = GROUPS.flatMap(([, items]) => items);

test('admin with no roles row sees every menu item and page', async () => {
  const e = makeEnv({ role: 'admin', roles: [] });
  await e.run('loadRolePerms()');
  assert.deepEqual(e.visible(), ALL_MENU);
  for (const pg of ['payroll', 'settings', 'emp-new', 'notifications']) assert.equal(e.run(`isPageAllowed('${pg}')`), true, pg);
});

test('admin ignores a restrictive roles row too', async () => {
  const e = makeEnv({ role: 'admin', roles: [row('admin', { vacations: true })] });
  await e.run('loadRolePerms()');
  assert.deepEqual(e.visible(), ALL_MENU);
  assert.equal(e.run("isPageAllowed('payroll')"), true);
});

test('non-admin {vacations:true} sees only vacations (+ unmapped items); payroll page denied', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { vacations: true })] });
  await e.run('loadRolePerms()');
  assert.deepEqual(e.visible(), ['m-vacations', 'm-notifications']);
  assert.equal(e.run("isPageAllowed('vacations')"), true);
  assert.equal(e.run("isPageAllowed('payroll')"), false);
  assert.equal(e.run("isPageAllowed('dashboard')"), false);
  assert.equal(e.run("isPageAllowed('notifications')"), true);
  assert.equal(e.headerVisible('h-main'), false);
  assert.equal(e.headerVisible('h-pay'), false);
  assert.equal(e.headerVisible('h-people'), true);
});

test('non-admin: an undefined key is denied', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { dashboard: true })] });
  await e.run('loadRolePerms()');
  assert.equal(e.run("isPageAllowed('reports')"), false);
  assert.deepEqual(e.visible(), ['m-dashboard', 'm-notifications']);
});

test('non-admin: a null key is denied', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { dashboard: true, reports: null })] });
  await e.run('loadRolePerms()');
  assert.equal(e.run("isPageAllowed('reports')"), false);
});

test('non-admin with no roles row is denied everything mapped', async () => {
  const e = makeEnv({ role: 'ghost', roles: [row('other', { dashboard: true })] });
  await e.run('loadRolePerms()');
  assert.deepEqual(e.visible(), ['m-notifications']);
  assert.equal(e.run("isPageAllowed('dashboard')"), false);
  assert.equal(e.run("isPageAllowed('notifications')"), true);
});

test('non-admin with a failed roles fetch is denied everything mapped', async () => {
  const e = makeEnv({ role: 'staff', rolesFail: true });
  await e.run('loadRolePerms()');
  assert.deepEqual(e.visible(), ['m-notifications']);
  assert.equal(e.run("isPageAllowed('vacations')"), false);
});

test('sub-keys: payroll {calc:false,history:true} denies the payroll page', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { payroll: { calc: false, history: true } })] });
  await e.run('loadRolePerms()');
  assert.equal(e.run("isPageAllowed('payroll')"), false);
  assert.equal(e.visible().includes('m-payroll'), false);
});

test('sub-keys: employees true allows emp-list; object without the sub-key is allowed if any sub is truthy', async () => {
  let e = makeEnv({ role: 'staff', roles: [row('staff', { employees: true })] });
  await e.run('loadRolePerms()');
  assert.equal(e.run("isPageAllowed('emp-list')"), true);
  assert.equal(e.run("isPageAllowed('directory')"), true);
  e = makeEnv({ role: 'staff', roles: [row('staff', { employees: { list: false, org: true }, reports: { any: true } })] });
  await e.run('loadRolePerms()');
  assert.equal(e.run("isPageAllowed('emp-list')"), false);
  assert.equal(e.run("isPageAllowed('org-chart')"), true);
  assert.equal(e.run("isPageAllowed('evaluations')"), true); // 'employees' without sub-key, one sub truthy
  assert.equal(e.run("isPageAllowed('reports')"), true);
});

test('entry flow: {vacations:true} user lands on vacations, never on the denied dashboard', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { vacations: true })] });
  await e.login('staff');
  await e.settle();
  assert.deepEqual(e.log.go, ['vacations']);
  assert.deepEqual(e.visible(), ['m-vacations', 'm-notifications']);
});

test('entry flow: a user with no module gets one toast and no navigation', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { vacations: false })] });
  await e.login('staff');
  await e.settle();
  assert.deepEqual(e.log.go, []);
  assert.deepEqual(e.log.toasts.map((t) => t.msg), ['Tu rol no tiene módulos asignados. Contactá al administrador.']);
});

test('entry flow: admin goes to the dashboard immediately and keeps the full menu', async () => {
  const e = makeEnv({ role: 'admin', roles: [] });
  await e.login('admin');
  assert.deepEqual(e.log.go, ['dashboard'], 'navigation must not wait for the roles request');
  await e.settle();
  assert.deepEqual(e.visible(), ALL_MENU);
  assert.deepEqual(e.log.toasts, []);
});

test('entry flow: a roles request that rejects (network down) still ends in the fail-closed toast, not a blank app', async () => {
  const e = makeEnv({ role: 'staff', rolesReject: true });
  await e.login('staff');
  await e.settle();
  assert.deepEqual(e.log.go, []);
  assert.deepEqual(e.log.toasts.map((t) => t.msg), ['Tu rol no tiene módulos asignados. Contactá al administrador.']);
  assert.equal(e.log.timers, 1, 'notifications are still scheduled');
  assert.deepEqual(e.visible(), ['m-notifications'], 'every mapped module stays hidden');
});

test('entry flow: admin with a rejected roles request keeps the dashboard and leaves no unhandled rejection', async () => {
  const unhandled = [];
  const onRej = (r) => unhandled.push(r);
  process.on('unhandledRejection', onRej);
  try {
    const e = makeEnv({ role: 'admin', rolesReject: true });
    await e.login('admin');
    await e.settle(); await e.settle();
    assert.deepEqual(e.log.go, ['dashboard']);
    assert.deepEqual(e.visible(), ALL_MENU);
  } finally {
    await new Promise((r) => setImmediate(r));
    process.off('unhandledRejection', onRej);
  }
  assert.equal(unhandled.length, 0, 'loadRolePerms must not reject: ' + unhandled.map(String).join(', '));
});

test('logout then login as admin shows the whole menu again (no stale perms)', async () => {
  const e = makeEnv({ role: 'staff', roles: [row('staff', { vacations: true })] });
  await e.login('staff');
  await e.settle();
  assert.deepEqual(e.visible(), ['m-vacations', 'm-notifications']);
  e.run('clearSession()');
  assert.equal(e.A.perms, null);
  e.state.roles = []; // admin has no roles row
  e.log.go.length = 0;
  await e.login('admin');
  await e.settle();
  assert.deepEqual(e.visible(), ALL_MENU);
  assert.deepEqual(e.log.go, ['dashboard']);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log(`ok   - ${name}`); }
  catch (err) { failed++; console.log(`FAIL - ${name}\n       ${String(err.message).split('\n').slice(0, 40).join('\n       ')}`); }
}
console.log(`\n${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
