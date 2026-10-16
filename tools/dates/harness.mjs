// tools/dates/harness.mjs
//
// Drives the REAL "today" helpers of hr_admin_panel.html (TD, TS, fy and, once
// it exists, panamaDate) in a node:vm sandbox with a fixed clock. Sources are
// sliced out of the HTML and executed as written.
//
// TS ("today" as YYYY-MM-DD) and fy() feed the attendance export, default form
// dates and export file names. They must be Panama dates (UTC-5, no DST, the
// zone the backend scheduler uses), not UTC dates: from 19:00 in Panama the
// UTC date is already tomorrow.
//
// Run: node tools/dates/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { extractFunction } from '../lib/extract.mjs';

const html = readFileSync(process.env.DATES_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

// The `var TD = ...` line, the fy() helper on the next line, and panamaDate()
// when the page defines it.
const lineOf = (prefix) => {
  const at = html.indexOf('\n' + prefix);
  assert.ok(at >= 0, `${prefix} not found in hr_admin_panel.html`);
  return html.slice(at + 1, html.indexOf('\n', at + 1));
};
const srcs = [
  html.includes('\nfunction panamaDate(') ? extractFunction(html, 'function panamaDate(') : '',
  lineOf('var TD = '),
  lineOf('function fy('),
].join('\n');

// A Date whose "now" is fixed; every other constructor form behaves normally.
function clockAt(iso) {
  const now = new Date(iso).getTime();
  return class FixedDate extends Date {
    constructor(...a) { if (a.length) super(...a); else super(now); }
    static now() { return now; }
  };
}

function run(iso) {
  const sandbox = { Date: clockAt(iso), Intl, String, Number, Math };
  vm.createContext(sandbox);
  vm.runInContext(srcs, sandbox);
  return { TS: vm.runInContext('TS', sandbox), fy: (d) => vm.runInContext(`fy(${d})`, sandbox) };
}

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('TS is the Panama date at 21:00 Panama (02:00 UTC next day)', () => {
  assert.equal(run('2026-10-15T02:00:00Z').TS, '2026-10-14');
});

test('TS at midday Panama is the same day (unchanged)', () => {
  assert.equal(run('2026-10-14T17:00:00Z').TS, '2026-10-14');
});

test('TS just after midnight Panama is the new day', () => {
  assert.equal(run('2026-10-15T05:01:00Z').TS, '2026-10-15');
});

test('fy() counts days from the Panama date, also across a month end at night', () => {
  const t = run('2026-11-01T02:30:00Z'); // Oct 31, 21:30 in Panama
  assert.equal(t.fy(0), '2026-10-31');
  assert.equal(t.fy(1), '2026-11-01');
  assert.equal(t.fy(-30), '2026-10-01');
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
