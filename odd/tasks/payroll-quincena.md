# Feature: correct quincenal payroll runs

## Objective
Make 1st/2nd-quincena payroll runs reachable and correct: each half uses only its own dates, deduction quotas are split, the ledger keeps one row per employee per period, and re-running or double-submitting a run never duplicates history.

## Problem / Why
Audit 2026-10-13 + mapping (same day):
- The UI sends `quincenal1`/`quincenal2` (`hr_admin_panel.html:655`) but `payrollFactor` (`backend/internal/handlers/payroll_history.go:47`) only accepts `mensual`/`quincenal`: every quincena preview/run is a 400 today.
- Once reachable: `ListPayrollRunInputs` (`payroll_history.sql:30-48`) applies the whole month of overtime and absences to each half, and the full deduction quota to each half (double-counted per month).
- `employee_pay_records` upsert key `uq_epr_run_period` (company, employee, year, month) has no period: the 2nd quincena overwrites the 1st.
- `CreatePayrollHistory` always inserts: a re-run or double click duplicates `payroll_history` rows (no unique key, no lock). The ledger upserts (replace) while history inserts (duplicate): the two disagree.

## Scope (authorized)
Backend handlers/queries/payroll calc, new migration 0014, frontend payroll screen, tests, README/runbook notes. Dev DB has 0 rows in `payroll_history` and `employee_pay_records` (no backfill needed in dev; production state must be checked before applying 0014).

## Decisions
- User decision (2026-10-13): deduction quotas are scaled by the period factor (50% in each quincena; 100% in mensual).
- Orchestrator defaults (technical, user informed):
  - Period vocabulary: API accepts exactly `mensual`, `quincenal1`, `quincenal2` (what the UI sends); `quincenal` is no longer accepted. Factor 1 / 0.5 / 0.5.
  - Date ranges for date-based inputs (attendance absences, overtime): mensual = whole month; quincenal1 = days 1-15; quincenal2 = day 16 to month end. Absence deduction stays `salary/30 * absent days in range` (per day, not scaled).
  - Ledger: one `origin='run'` row per employee per period (new `period` column on `employee_pay_records`, NOT NULL, default `mensual` for manual/CSV rows; the run unique index includes it). `periodo` text carries the half label (e.g. "Enero 2026 - 1ra Quincena"). `gross_salary` stays the factor-scaled base.
  - `GetEmployeePayBases` (liquidation) aggregates run/manual rows per (year, month) — summing the quincenas of a month — before ranking months, so its windows (60/12/11/6 months, `months`, `sal30`) keep meaning months.
  - Re-run of the same (company, year, month, period) replaces: the previous `payroll_history` row is replaced in the same transaction as the ledger upserts (unique index on payroll_history (company_id, year, month, period)). Mensual and quincena runs for the same month are mutually exclusive: 409 with a clear message.
  - Concurrency: a transaction-scoped advisory lock per (company, year, month) around the run, plus the unique indexes as backstop.
  - Delete semantics unchanged (history delete leaves ledger rows; settled decision #2).
  - Frontend: Save button disabled while saving; payslip overtime/deduction lines use the same half ranges and the halved quota.

## Constraints
- Strict TDD. Backend runner: `cd backend && TEST_DB_URL=postgres://rrhh_go:<POSTGRES_PASSWORD from backend/.env>@127.0.0.1:5434/rrhh_go_test?sslmode=disable go test ./... -count=1`. Frontend: node harness under `tools/`. RED, GREEN, REFACTOR.
- Goldens (`internal/payroll/testdata/prcalc`) are legacy-JS parity for the factor arithmetic: do not regenerate; new behaviour gets new Go tests. `make goldens-check` already fails on the base (marker not found) and is out of scope.
- sqlc: `~/go/bin/sqlc generate` from `backend/internal/db`. Migration 0014 idempotent (rule for 0010+), applied manually in dev by the orchestrator.
- Stacked on `fix/token-revocation` (branch `fix/payroll-quincena`). One work-unit commit per task, Conventional Commits, no AI attribution. Push/PR are the user's decision. ~400 authored lines per task is advisory only.

## Tasks
- [x] T1 — calculation: period vocabulary (`mensual|quincenal1|quincenal2`), date-ranged inputs in `ListPayrollRunInputs` (or a period-aware variant), quota scaled by factor; tests for each period (overtime/absences split by range, quota halved, mensual unchanged, unknown/legacy `quincenal` -> 400). (route: delegated writer)
- [x] T2 — persistence: migration 0014 (`employee_pay_records.period` + widened run unique index; `payroll_history` unique (company, year, month, period) + CHECK on period), per-period ledger upsert and `periodo` label, history replace-on-rerun, mensual/quincena exclusivity (409), advisory lock, `GetEmployeePayBases` monthly aggregation; tests: two quincenas -> two ledger rows, re-run replaces (1 history row), double concurrent submit -> 1 history row, mensual after quincena -> 409, bases sum quincenas into one month. (route: same writer)
- [x] T3 — frontend + docs: disable Save during the request, payslip lines per half range and halved quota, history/pay-records labels; node harness; README payroll section + runbook note for 0014. (route: same writer)

## Acceptance criteria
- Previewing and saving 1ra and 2da quincena works from the UI.
- Two quincenas of a month produce two history rows and two ledger rows per employee, with overtime/absences counted once per month in total and quotas summing to one full quota.
- Re-running a period leaves exactly one history row for it; concurrent double submit leaves one.
- Liquidation bases see one month for a month paid in quincenas, with the month's summed amounts.
- Full backend suite, vet, all node harnesses pass.

## Delivery
Forecast ~600-900 authored lines (mostly tests). Strategy: continue the Feature Branch Chain on tracker #33 after #67 (user chose it for this cycle); slicing per task.

## Progress / evidence
### T1 (delegated writer; TDD on, runner `go test` with TEST_DB_URL)
- RED: `go test ./internal/handlers -run TestCalculatePayroll` -> FAIL TestCalculatePayroll_PeriodVocabulary, _QuincenaSplitsAbsencesByRange, _QuincenaSplitsOvertimeByRange, _QuotaScaledByFactor; key line: `period quincenal1: expected 200, got 400 ... "must be 'mensual' or 'quincenal'"`.
- GREEN: `go test ./internal/handlers ./internal/payroll -count=1` ok (goldens untouched).
- Design: `payrollPeriodSpec` (factor + day range) replaces `payrollFactor`; `ListPayrollRunInputs` takes `start_date`/`end_date` (half-open) for attendance and overtime; quota scaled by factor in `calculatePayrollRun` (caller), `payroll.Calculate` untouched so legacy-parity goldens pass unchanged. Overtime still requires attendance data in the same range (legacy `hasData` semantics, now per range).
- Commit: 092ca2d

### T2 (same writer)
- RED: `go test ./internal/handlers -run 'TestPayrollRun_(TwoQ|RerunR|Conc|Mensual|Exclusivity)|TestPayrollHistory_Unique|TestPayBases_Aggregates'` -> all 7 FAIL: TwoQuincenasKeepTwoRows `expected 2 ledger rows ... got 1`; RerunReplacesHistory `got 1/2`; ConcurrentDoubleSubmit `expected exactly 1 history row, got 6`; MensualAndQuincenaAreExclusive `expected 409, got 201`; ExclusivityHoldsAfterHistoryDelete `got 201`; UniquePerPeriodBackstop `expected a unique violation`; PayBases_Aggregates `column "period" ... does not exist`.
- GREEN: `go build ./... && go vet ./...` clean; `go test ./... -count=1` all ok; `go test -race ./internal/handlers ./internal/payroll` ok; concurrent test -count=5 ok.
- Design: migration `0014_payroll_periods.sql` (period column + CHECKs, `uq_epr_run_period_v2`, `uq_payroll_history_period`); run tx = advisory lock -> exclusivity count (history + run ledger rows) -> history upsert -> ledger upserts; `GetEmployeePayBases` sums periods per month (run month uses run rows only) before ranking; `period` exposed on pay-record responses; runbook updated (0014 row, duplicate pre-check).
- Commit: 60c7f40 fix(payroll): keep one ledger and history row per period

### T3 (same writer)
- RED: `node tools/payroll/harness.mjs` -> `AssertionError: var PR_PERIODS not found` (helpers and guard did not exist; harness could not load the real functions).
- GREEN: `node tools/payroll/harness.mjs` 12/12; session 35/35, permissions 13/13, leave 7/7, zk-feed 13/13; `node --check` of the main script ok. Mutation: removing the `_prSaving` guard makes the double-click test fail (`2 !== 1`).
- Changes: `prFactor/prInPeriod/prQuota/prPeriodLabel`; Calcular badges and payslip use the half range; payslip quota halved; Save button `pr-save-btn` disabled while saving and server errors (409) toasted instead of a fake success; history badge labels; README payroll section.
- Fixed in passing (same lines): payslip fallback subtracted the quota again from the server net. Latent, not changed: with a cached row `cachedRow.att.otAmount` is undefined, so payslip overtime total is always 0.
- Commit: see git log (`fix(frontend): quincena payroll save guard and payslip ranges`).

### Orchestrator checks (2026-10-13)
- Spot check: `go test -race ./internal/payroll/ ./internal/handlers/ -run 'Payroll|Quincena|Bases|Liquidation'` ok; payroll harness 12/12.
- Dev DB: 0014 applied with psql; backend rebuilt, nginx restarted.
- Live (curl, Sept 2026 fixtures for Juan, salary 1000: present 5th, absent 8th, OT 10 on 5th and 30 on 20th, active quota 100): mensual b 1006.67 / quota 100; quincenal1 b 476.67 (absence only here), OT 10, quota 50; quincenal2 b 530.00, OT 30, quota 50 -> halves sum to the mensual base. Legacy `quincenal` -> 400 with the allowed values. Runs: q1 201, q2 201, re-run q1 201, parallel double q2 201+201 -> one history row per period; mensual same month -> 409. Ledger: 2 rows per employee labelled "Septiembre 2026 - 1ra/2da Quincena". Liquidation bases: months 1, sal30 1000 (halves summed).
- Live (Chrome): both quincenas preview (no 400); double `savePayrollRun()` -> one POST; 0 JS errors. The 409 toast showed the English server text -> fixed in 94216fd (`apiErrMsg` conflict override, Spanish text; harness RED 11/12 -> GREEN 12/12; other harnesses green; `node --check` ok); re-verified live after an nginx restart (bind-mount keeps the old inode after an editor replaces the file).
- Test fixtures, Sept runs and ledger rows deleted; dev DB back to its prior rows.
- Follow-up, needs a product decision: payslip overtime total is 0 when a cached row exists (`cachedRow.att.otAmount` undefined); showing it must not double-count the server `b`.

- RDD: assess base 3e7f3d5 -> high (process_boundary), review due; consent relayed, user granted; 4-lens native review (lineage review-1ec8c4652f23e695) approved and acknowledged (`gentle-ai.review-acknowledged/v1`). Reviewed boundary advances to c69c63e.
- Informational review findings (not blocking):
  - R1/R3: ledger run rows written by a legacy `quincenal` run would be backfilled to `mensual` by 0014, so history (`quincenal`) and ledger (`mensual`) disagree for that month and the exclusivity check can 409 unexpectedly. Practically unreachable from the UI (it never sent `quincenal`; it got 400), only from direct API callers; check production for `payroll_history.period='quincenal'` before applying 0014.
  - R2: `CountConflictingPayrollPeriods` uses positional `$4::text`, so sqlc names the field `Column4`; use `sqlc.arg('period')`.
  - R4: 0014 drops `uq_epr_run_period` before creating `uq_payroll_history_period` without one transaction; if the history unique index fails on existing duplicates, the old ledger index is already gone while the previous binary still upserts on it. Wrap in a transaction or create the new indexes before dropping the old one.

### R4 + R2 follow-ups (user-authorized 2026-10-14: "dale, arreglá R4 y R2")
- R4, commit 8b5e89f `fix(db): drop the old run index only after 0014's new indexes exist` (route: direct inline). 0014 now creates `uq_payroll_history_period`, then `uq_epr_run_period_v2`, and drops `uq_epr_run_period` last; runbook says a failed 0014 keeps the old index and can be re-applied after the cleanup.
  - RED: `TestMigration0014_FailureKeepsTheOldLedgerIndex` (isolated schema back to the pre-0014 layout, duplicate history rows, 0014 applied statement by statement like `psql -f` with ON_ERROR_STOP) -> `uq_epr_run_period was dropped before the failing statement (... SQLSTATE 23505)`. GREEN after the reorder.
- R2, commit 6430d46 `refactor(payroll): name the period parameter of the period conflict query`: `sqlc.arg('period')`, field `Period` (no `Column4`). No RED possible (rename only); the existing exclusivity tests cover it.
- Checks: `go build`, `go vet` clean; full suite `go test ./... -count=1` with TEST_DB_URL all ok. Dev: 0014 re-applied with psql, idempotent (notices only), indexes `uq_epr_run_period_v2` + `uq_payroll_history_period`.
- RDD assess base ae0e51b: medium, `review_due: false` (`under_budget`).

## Next step
Push/PR in the chain after #67 (user decision). Open: R1/R3 production pre-check for `period='quincenal'`; payslip overtime with cached row (product decision).
