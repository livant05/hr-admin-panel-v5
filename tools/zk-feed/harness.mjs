// tools/zk-feed/harness.mjs
//
// Drives the REAL live-feed code of hr_admin_panel.html (the block between the
// "Live feed" marker and startZKPolling) in a node:vm sandbox against a fake
// attendance_logs endpoint that implements created_after, _order, _limit and
// _offset exactly like the Go handler (created_at then id, same direction).
//
// Run: node tools/zk-feed/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.ZK_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');
const start = html.indexOf('// ── Live feed ─');
const end = html.indexOf('// Realtime polling for attendance');
assert.ok(start > 0 && end > start, 'live feed block not found in hr_admin_panel.html');
const feedSrc = html.slice(start, end);

function makeEnv() {
  const server = { rows: [], calls: [], failAt: null, seq: 0 };
  const toasts = [];
  let attendanceReloads = 0;
  const sandbox = {
    A: { demo: false },
    Date, Object, Array, Number, isNaN, URLSearchParams,
    escHtml: (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
    toast: (msg, type) => toasts.push({ msg, type }),
    loadAttendance: () => { attendanceReloads++; },
    document: { getElementById: () => ({ classList: { contains: () => true } }) },
    Supa: {
      gq: (flt) => '?' + Object.keys(flt).map((k) => encodeURIComponent(k) + '=' + encodeURIComponent(flt[k])).join('&'),
      goFetch: async (p) => {
        const q = new URL('http://x' + p).searchParams;
        server.calls.push(Object.fromEntries(q));
        if (server.failAt !== null && server.calls.length === server.failAt) return { error: { code: 'internal', message: 'boom' } };
        let rows = server.rows.slice();
        if (q.has('created_after')) rows = rows.filter((r) => Date.parse(r.created_at) > Date.parse(q.get('created_after')));
        const dir = q.get('_order') === 'created_at.asc' ? 1 : -1;
        rows.sort((a, b) => dir * (Date.parse(a.created_at) - Date.parse(b.created_at) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)));
        const off = Number(q.get('_offset') || 0);
        return rows.slice(off, off + Number(q.get('_limit')));
      },
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(feedSrc, sandbox);
  const add = (iso, name) => { const r = { id: 'id-' + String(++server.seq).padStart(6, '0'), created_at: iso, employee_name: name || 'Emp ' + server.seq, time_in: '08:00' }; server.rows.push(r); return r; };
  return { sandbox, server, toasts, add, reloads: () => attendanceReloads, run: (code) => vm.runInContext(code, sandbox) };
}

const T0 = Date.parse('2026-05-01T10:00:00Z');
const iso = (ms) => new Date(T0 + ms).toISOString();
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('seed + silent prime: existing rows (including the overlap window) never toast', async () => {
  const e = makeEnv();
  e.add(iso(-3600_000)); e.add(iso(-60_000)); e.add(iso(-1_000));
  await e.run('zkFeedTick(false)');
  assert.equal(e.run('ZK_FEED.ready'), true);
  assert.equal(e.toasts.length, 0);
  assert.equal(e.run('ZK_FEED.maxSeen'), T0 - 1_000);
  assert.deepEqual({ ...e.server.calls[0] }, { _order: 'created_at.desc', _limit: '1' });
  assert.equal(e.server.calls[1]._order, 'created_at.asc');
  assert.equal(e.server.calls[1]._limit, '200');
  assert.equal(e.server.calls[1].created_after, iso(-1_000 - 120_000));
});

test('an empty table seeds at the epoch and the first row is delivered', async () => {
  const e = makeEnv();
  await e.run('zkFeedTick(false)');
  assert.equal(e.run('ZK_FEED.ready'), true);
  assert.equal(e.run('ZK_FEED.maxSeen'), 0);
  e.add(iso(5_000), 'Ana');
  await e.run('zkFeedTick(true)');
  assert.equal(e.toasts.length, 1);
  assert.match(e.toasts[0].msg, /Ana/);
});

test('a new row toasts once and is not repeated by the overlap re-read', async () => {
  const e = makeEnv();
  e.add(iso(-10_000));
  await e.run('zkFeedTick(false)');
  e.add(iso(1_000), 'Luis');
  await e.run('zkFeedTick(true)');
  await e.run('zkFeedTick(true)');
  await e.run('zkFeedTick(true)');
  assert.equal(e.toasts.length, 1);
  assert.match(e.toasts[0].msg, /Luis/);
  assert.equal(e.reloads(), 1);
});

test('a late commit with an older created_at (inside the overlap) is delivered', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  e.add(iso(60_000), 'Nueva');
  await e.run('zkFeedTick(true)');
  e.add(iso(30_000), 'Tarde'); // committed after Nueva but stamped 30s earlier
  await e.run('zkFeedTick(true)');
  assert.deepEqual(e.toasts.map((t) => /Nueva|Tarde/.exec(t.msg)[0]), ['Nueva', 'Tarde']);
});

test('a row stamped before the overlap window is outside the guarantee', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  e.add(iso(600_000));
  await e.run('zkFeedTick(true)');
  const before = e.toasts.length;
  e.add(iso(100_000), 'Muy tarde'); // 500s older than maxSeen, beyond the 120s overlap
  await e.run('zkFeedTick(true)');
  assert.equal(e.toasts.length, before);
});

test('paging: 450 new rows are read at offsets 0, 200, 400 and shown once (5 toasts + summary)', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  e.server.calls.length = 0;
  for (let i = 1; i <= 450; i++) e.add(iso(i * 100));
  await e.run('zkFeedTick(true)');
  assert.deepEqual(e.server.calls.map((c) => c._offset), ['0', '200', '400']);
  assert.equal(e.run('Object.keys(ZK_FEED.seen).length'), 451);
  assert.equal(e.toasts.length, 6);
  assert.match(e.toasts[0].msg, /\+445/);
  const shown = e.toasts.slice(1).map((t) => /Emp (\d+)/.exec(t.msg)[1]);
  assert.deepEqual(shown, ['447', '448', '449', '450', '451'].map(String));
  await e.run('zkFeedTick(true)');
  assert.equal(e.toasts.length, 6);
});

test('paging across a run of rows sharing one created_at loses and repeats nothing', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  for (let i = 0; i < 450; i++) e.add(iso(5_000));
  const got = await e.run('zkFeedRun(ZK_FEED, zkFeedFetchPage)');
  assert.equal(got.fresh.length, 450);
  assert.equal(new Set(got.fresh.map((r) => r.id)).size, 450);
});

test('a failed page keeps the cursor: rows already read are shown, the rest arrive next tick', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  e.server.calls.length = 0;
  for (let i = 1; i <= 250; i++) e.add(iso(i * 100));
  e.server.failAt = 2; // second page request fails
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('ZK_FEED.busy'), false);
  const afterFail = e.toasts.length; // 5 + "+195" summary from the first 200 rows
  assert.equal(afterFail, 6);
  e.server.failAt = null;
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('Object.keys(ZK_FEED.seen).length'), 251);
  assert.equal(e.toasts.length, afterFail + 6); // remaining 50 rows: 5 + summary "+45"
});

test('a seed failure leaves the feed not ready and the next tick retries it', async () => {
  const e = makeEnv();
  e.add(iso(0));
  e.server.failAt = 1;
  await e.run('zkFeedTick(false)');
  assert.equal(e.run('ZK_FEED.ready'), false);
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('ZK_FEED.ready'), true);
  assert.equal(e.toasts.length, 0);
});

test('the seen-set drops ids that fell out of the overlap window', async () => {
  const e = makeEnv();
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  for (let i = 1; i <= 10; i++) e.add(iso(i * 1_000));
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('Object.keys(ZK_FEED.seen).length'), 11);
  e.add(iso(600_000));
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('Object.keys(ZK_FEED.seen).length'), 1);
});

test('the seen-set respects its hard cap', async () => {
  const e = makeEnv();
  e.run('ZK_FEED_SEEN_CAP = 10');
  e.add(iso(0));
  await e.run('zkFeedTick(false)');
  for (let i = 1; i <= 30; i++) e.add(iso(i * 10));
  await e.run('zkFeedTick(true)');
  assert.equal(e.run('Object.keys(ZK_FEED.seen).length'), 10);
});

test('demo mode makes no request and shows nothing', async () => {
  const e = makeEnv();
  e.sandbox.A.demo = true;
  await e.run('zkFeedTick(false)');
  await e.run('zkFeedTick(true)');
  assert.equal(e.server.calls.length, 0);
  assert.equal(e.toasts.length, 0);
});

test('employee names are escaped in the toast markup', async () => {
  const e = makeEnv();
  await e.run('zkFeedTick(false)');
  e.add(iso(1_000), '<img src=x onerror=1>');
  await e.run('zkFeedTick(true)');
  assert.equal(e.toasts.length, 1);
  assert.ok(!e.toasts[0].msg.includes('<img'));
  assert.ok(e.toasts[0].msg.includes('&lt;img'));
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
