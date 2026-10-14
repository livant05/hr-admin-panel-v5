# Feature: server-side module permissions

## Objective
Enforce on the Go API the module permissions that today only the frontend hides (`applyPermissions`), so a non-admin role can only read and write the modules its `roles.permissions` grants.

## Problem / Why
Audit 2026-10-13 (H1): `requirePermission` / `requireAdmin` guard only users, roles writes, medical, leave and ZKTeco routes. Any authenticated role can read every salary/cedula/SS number, run or delete payroll, delete liquidations and pay records, and edit deductions. The UI menu hiding is the only barrier.

## Scope (authorized)
Backend only: `backend/internal/handlers` (routes, authz, employees responses) plus tests and README authz notes. No frontend change, no schema change.

## Decisions
- User decision (2026-10-13): shared reads stay open but redact sensitive fields. `GET /api/employees` and `GET /api/employees/{id}` remain available to every authenticated user; for roles without `employees` or `payroll` the response omits `salary`, `hourly_rate`, `cedula`, `ss_number`, `address`, `emergency_contact`, `emergency_phone`, `emergency_relation` (JSON null). `phone` and `birth_date` stay (directory, birthday calendar).
- Admin keeps the existing bypass (`requirePermission` returns true for JWT role `admin`). Fail-closed for every other role (missing role row, missing/falsy key -> 403).
- Module-level keys only (a module whose sub-keys are all off is denied, as `isTruthy` already does).

## Route -> permission map
| Resource | Read | Write |
|---|---|---|
| me, companies, departments, positions, branches, roles (GET) | any authenticated | departments/positions/branches: `settings`; roles: `requireAdmin` (unchanged) |
| users, zkteco_*, unknown pins | unchanged (`requireAdmin` / existing) | unchanged |
| employees | any authenticated, redacted unless `employees` or `payroll` | `employees` |
| evaluations, uniforms | `employees` | `employees` |
| attendance_logs | `attendance` | `attendance` |
| leave_balances, leave_requests | `vacations` | `vacations` (existing PATCH/accrue checks kept) |
| medical_records | `medical` (existing) | `medical` (existing) |
| overtime_logs | `overtime` | `overtime` |
| deductions | `deductions` | `deductions` |
| payroll_history (incl. calculate) | `payroll` | `payroll` |
| liquidation_history (incl. calculate) | `liquidation` | `liquidation` |
| employee_pay_records (incl. bases) | `payroll` or `liquidation` | `liquidation` |
| document_templates, generated_documents | `documents` | `documents` |
| job_postings, candidates, applications | `recruitment` | `recruitment` |

Known consequence: cross-module widgets (dashboard "presentes hoy", "resumen planilla", notifications) show empty/0 for roles lacking that module; the frontend already treats failed loads as empty.

## Constraints
- Strict TDD (session config). Runner: `cd backend && TEST_DB_URL=postgres://rrhh_go:<POSTGRES_PASSWORD from backend/.env>@127.0.0.1:5434/rrhh_go_test?sslmode=disable go test ./... -count=1`. RED before implementation, then GREEN, then REFACTOR.
- One work-unit commit per task on `fix/server-permissions` (branched from `feat/rrhh-go-auth-cutover`), Conventional Commits, no AI attribution. Push/PR are the user's decision.
- ~400 authored changed lines per task is advisory only.

## Tasks
- [x] T1 — route-level permission gate: wrapper usable in `routes.go` (`a.perm(key, h)` / any-of variant), apply the map above to every protected route except employees reads; a route-table test that fails if a protected route has no classification (gated / open-by-design / admin). Table-driven tests: non-admin without key -> 403, with key -> not 403, admin -> not 403, for each module. (route: delegated writer; trigger: 2+ non-trivial files + preparation reading)
- [x] T2 — employees read redaction for roles without `employees`/`payroll` (list and get), tests for redacted vs full vs admin. (route: delegated writer, same worker context)
- [x] T3 — README authz section updated with the map; full suite + `go vet`. (route: inline with T2 writer)

## Acceptance criteria
- Every protected route is classified; the classification test fails on an unclassified new route.
- Non-admin role without the module gets 403 on that module's routes; with it, behaviour is unchanged.
- Employees list/get redact the listed fields for roles without `employees`/`payroll`; admin and permitted roles see everything.
- Full backend suite and `go vet` pass.

## Delivery
Forecast ~550-700 authored lines (mostly tests). Strategy: `ask-on-risk` (default); chain strategy asked when the running count passes ~400.

## Progress / evidence
### T1 (commit subject: `fix(api): enforce module permissions on protected routes`; hash 0b8582c)
- Authored lines: +573 / -179 (routes.go diff is inflated by the table re-indent; ~265 are tests).
- Design: `protectedRoutes()` is the single registry of `routeSpec{pattern, gate, handler}`; `Routes` registers only that list and wraps `gatePermission` routes with `a.perm(keys, h)` (tenant, then any-of `permissionAllows`; admin bypass, fail-closed). `requirePermission` now delegates to `permissionAllows` (same semantics). Medical, users, roles writes and ZKTeco stay `viaHandler`; leave PATCH/accrue keep their in-handler check (same 403 envelope, so double gating is invisible).
- RED (stubbed `ProtectedRouteGates` returning nil, original routes.go): `TestRoutes_EveryProtectedRouteIsClassified` failed with `map entry without a registered route: DELETE /api/applications/{id}` (and the rest); `TestRoutes_ModulePermissionGate` failed 83 subtests, e.g. `role without the key: expected 403, got 400` (POST /api/departments), `got 404` (GET /api/payroll_history/{id}).
- GREEN: both new tests pass; `go build ./... && go vet ./...` clean; `go test ./... -count=1` all ok.
- Existing tests changed (decision-driven): the three "any authenticated role may read" recruitment subtests (job_postings, candidates, applications) now seed an `empleado` role with `recruitment: true`.

### T2 (commit subject: `fix(api): redact sensitive employee fields for roles without pay access`)
- Authored lines: +166 / -0 (employees.go +43, employees_redaction_test.go +123).
- Design: `canSeeEmployeePay` = `permissionAllows` for `employees` or `payroll` (admin bypass); otherwise list/get marshal the `db.Employee` row and null the 8 fields, keeping every key. Writes were already gated by `employees` in T1.
- RED: `TestEmployees_ReadRedactsSensitiveFields/list_redacts...` and `/get_redacts...` failed, e.g. `list as nokey: salary = 1500.00, want null`, `cedula = "8-123-456", want null` (all 8 fields, roles nokey/empfalse/ghost); the "shows everything" subtests passed on the base as expected.
- GREEN: all 4 subtests pass; full suite `go test ./... -count=1` ok, `go vet` clean.

### T2 hash: a279676. T3 (commit subject: `docs(api): document server-side module permissions`)
- README: new "Module permissions (server-side)" section with the route map and employees redaction; recruitment sections no longer say "any authenticated role".
- Verification: `go build ./... && go vet ./...` clean; `go test ./... -count=1` all packages ok (see final report).
- T3 hash: 5a10511.

### Orchestrator checks (2026-10-13)
- Spot check: `go test ./internal/handlers/ -count=1` ok (37s).
- RDD assess `--base-ref 429ed0f --committed-only`: risk `medium`, `review_due: false` (`under_budget`); no native review started. Writer self-verification + parent spot check (medium tier, non-small writer).
- Delivery: running count ~1040 changed lines (mostly tests and route-table re-indent), above the ~400 budget; chain strategy pending user choice before any PR.
- Live E2E on the dev stack (2026-10-13, backend rebuilt from 66f2999, nginx restarted): temporary role `qa-perm-test` `{vacations:true}` + user. As that user: leave_requests/leave_balances/departments/employees 200; payroll_history (list, calculate, DELETE), liquidation_history (list, DELETE), employee_pay_records, deductions (GET, POST), overtime_logs, attendance_logs, POST employees, POST departments, evaluations, job_postings, document_templates, medical_records, users, POST roles all 403. Employees list: salary/cedula/ss_number/address/emergency_contact null, phone and birth_date present; admin sees all. Granting `payroll` via PATCH /api/roles took effect immediately (payroll 200, salaries visible, deductions still 403); revoking returned 403. UI as that user: 12 pages, 0 JS errors, 0 non-403 failures. Test user and role deleted afterwards (users: only the admin remains; roles back to 1).
- Observed, out of scope: the frontend menu still shows nearly every module to that role (`hasPerm` is fail-open on undefined keys; audit finding #20). Denied screens render empty.

## Next step
User review; push / PR is the user's decision.
