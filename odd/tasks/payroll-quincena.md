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
- [ ] T2 — persistence: migration 0014 (`employee_pay_records.period` + widened run unique index; `payroll_history` unique (company, year, month, period) + CHECK on period), per-period ledger upsert and `periodo` label, history replace-on-rerun, mensual/quincena exclusivity (409), advisory lock, `GetEmployeePayBases` monthly aggregation; tests: two quincenas -> two ledger rows, re-run replaces (1 history row), double concurrent submit -> 1 history row, mensual after quincena -> 409, bases sum quincenas into one month. (route: same writer)
- [ ] T3 — frontend + docs: disable Save during the request, payslip lines per half range and halved quota, history/pay-records labels; node harness; README payroll section + runbook note for 0014. (route: same writer)

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
- Commit: T1_HASH

## Next step
T2.
