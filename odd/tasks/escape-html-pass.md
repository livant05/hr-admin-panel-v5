# Feature: app-wide HTML escaping pass

## Objective
Escape every DB- or user-sourced string interpolated into HTML (`innerHTML`, `insertAdjacentHTML`, `document.write`, `.html()`) in `hr_admin_panel.html`, using the existing `escHtml` helper (added in PR #27), without changing what users see.

## Problem / Why
PR #27 fixed the documents module only. About 135 `innerHTML` uses remain; many concatenate fields such as employee names, notes, titles, descriptions, candidate data or survey answers raw. A stored value with markup runs script in the app origin (session and Go JWT exposure). Print popups opened with `window.open('')` share the origin too.

## Scope (authorized)
Every module section of `hr_admin_panel.html` (DASHBOARD through RECRUITMENT, plus NOTIFICATIONS, ZKTECO, SETTINGS, USER MANAGEMENT, CSV IMPORT and print/export popups such as liquidation and payroll slips).

## Rules
- Escape text-like values from the DB, API, CSV import, localStorage or user input in HTML text and attribute contexts (`value=`, `title=`, `data-*`).
- Do NOT escape: numbers already formatted by `fmt`/`fL`/`toFixed`, static markup, values that are trusted constants (enum label maps, icon class maps keyed by a constrained value — but escape the raw fallback when the key is unknown).
- JS-string-inside-attribute contexts (`onclick="fn('+id+')"`): `escHtml` is NOT sufficient there (HTML entities decode before JS). Ids are server UUIDs (or demo ids) — leave id-only handlers as they are; if any handler embeds free text, switch it to an id lookup or a `data-*` attribute + escHtml, and record it.
- If a field is intentionally rendered as HTML (rich text), do not break it: stop and report it as a decision gap.
- Visible output must stay the same for ordinary values (plain text, accents, `&` displayed correctly).

## Constraints
- Strict TDD (frontend has no automated harness): per task, a node harness that extracts the touched render functions, stubs DOM/Supa, feeds a `<img src=x onerror=alert(1)>` payload in every text field, and asserts no raw `<img` and the escaped text present. RED before the change, GREEN after. Plus `node --check` of the inline script.
- One work-unit commit per task on `fix/escape-html-pass`, Conventional Commits, no AI attribution. Push/PR are the user's call.
- ~400 authored changed lines per task is advisory only.

## Tasks
- [x] T1 — people: DASHBOARD, EMPLOYEES, DIRECTORY, ORG CHART, USER MANAGEMENT, NOTIFICATIONS (route: delegated writer; trigger: preparation + 2+ non-trivial changes)
- [x] T2 — time & leave: ATTENDANCE, VACATIONS, OVERTIME, DEDUCTIONS, ZKTECO, CALENDAR (route: delegated writer)
- [x] T3 — money & print: PAYROLL, LIQUIDATION, REPORTS, payroll/liquidation print popups, CSV IMPORT previews (route: delegated writer)
- [x] T4 — HR records & settings: SETTINGS (departments, positions, branches, roles), EVALUATIONS, UNIFORMS, MEDICAL, SURVEYS, TRAINING, ANNOUNCEMENTS, RECRUITMENT (route: delegated writer)

## Acceptance criteria
- Every interpolated text-like DB/user field in HTML sinks goes through `escHtml` (or is a documented safe exception).
- Harness RED->GREEN per task; `node --check` OK.
- No visible change for ordinary data.

## Progress / evidence
Route: one delegated writer for T1-T4 (mapping + 2+ non-trivial changes). Harnesses live in the session scratchpad (not committed): esc_base.js, esc_t1..t4.js. Each: RED on the pre-change file (raw `<img src=x` in the sinks, no `&lt;img`), GREEN after. `node --check` of the inline script OK after every task. `cd backend && go build ./...` OK.

- T1 `15d8203` (45+/45-): toast, empAvatar, setPhotoPreview, DASHBOARD, EMPLOYEES (list tabs, form options, profile modal), DIRECTORY, ORG CHART, NOTIFICATIONS alerts (msg built once with escaped values), USER MANAGEMENT. RED: 44 failures; GREEN: pass.
- T2 `8bf64b1` (28+/28-): ATTENDANCE (today, history, report, selects incl. `data-name`/`data-dept`), VACATIONS, OVERTIME, DEDUCTIONS, CALENDAR (labels escaped once at creation, used in text and `title=`), ZKTECO. RED: 33 failures; GREEN: pass.
- T3 `37a3dc3` (39+/39-): PAYROLL table, payslip popup, payroll print popup, payroll history, PAY RECORDS (table, selects, import skipped list), LIQUIDATION (select, info, deduction list incl. `data-quota`/`data-desc`, preview, history, print popup), REPORTS, CSV IMPORT preview. RED: 38 failures; GREEN: pass.
- T4 `8734cf9` (54+/53-): SETTINGS (entity lists, roles, `editEntity` input value), EVALUATIONS, UNIFORMS, MEDICAL, SURVEYS, TRAINING (+ certificate popup), ANNOUNCEMENTS, RECRUITMENT. RED: 55 failures; GREEN: pass.

### Exceptions and JS-attribute cases
- `toast(msg, t, raw)`: message is now escaped by default; the only caller passing markup (ZKTECO live poll, fingerprint icon) passes `raw=true` and escapes its own values. Server error messages in all other toasts are now escaped.
- ZKTECO `editZKDevice(id, encodeURIComponent(name), ...)` onclick: `encodeURIComponent` does not encode `'`, so a name containing `'` broke out of the JS string. Fixed by also replacing `'` with `%27` (decoded back by the existing `decodeURIComponent`).
- TRAINING `openEnrollModal(id, name)` embedded free text in an onclick JS string. Changed to `openEnrollModal(id)` with a lookup of the name in `DS.trainings`.
- `editEntity` input value: `.replace(/"/g,'&quot;')` replaced by `escHtml(name.trim())` (name comes from `textContent`, so no double escaping).
- `<option data-name=... data-dept=...>` (attendance, evaluations, training checkboxes): escaped; `getAttribute` decodes, so saved values are unchanged.
- Left as-is on purpose: id-only handlers, `fmt`/`fL`/`toFixed` numbers, enum label/class maps (fallback to the raw value is escaped), CSV exports, `confirm()` strings (not HTML), `textContent` assignments, chart labels (canvas), `printPayroll` reuse of the already-escaped `#pr-tbody` HTML.

### Decision gaps
None found: no seed or UI field carries intentional HTML (announcement content, survey descriptions, document notes are plain text from inputs/textareas).

### Not covered by harness
Static HTML pages in the markup (outside the inline script) and `openRolePermsModal` (labels are static constants). Sidebar `hdr.innerHTML = label` uses the static nav label text.

## Next step
Review the 4 work-unit commits, then push and open the PR (user's call). Native review per commit only if RDD is on.
