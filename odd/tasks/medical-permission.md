# Feature: dedicated `medical` permission for medical_records

## Objective
Gate `medical_records` (health data incl. `diagnosis`) behind a dedicated role permission `medical`, enforced server-side, without removing access from anyone who has it today.

## Problem / Why
- Backend: the 5 `/api/medical_records` routes have no permission check; any authenticated user of the tenant can read diagnoses via the API.
- Frontend: the Incapacidades page/menu is gated by `attendance`, so every role with attendance (e.g. `supervisor`) sees health data.
- Phase 4 open item #4. User decision (2026-10-10): dedicated `medical` key, not reuse of `attendance`.

## Scope (authorized)
- Migration `0009`: add `medical` to every existing role that lacks it; value = attendance truthiness by frontend semantics (boolean true, or object with at least one truthy sub-key) else `false`. Idempotent (only rows without the key).
- Backend: `requirePermission(..., "medical")` on all 5 medical routes (list, get, create, PATCH, delete). Admin JWT bypass and fail-closed semantics unchanged (existing `authz.go`).
- Frontend `hr_admin_panel.html`: `PERM_MODULES` gets a flat `medical` module ("Incapacidades"); `SIDEBAR_PERM_MAP` `m-medical` and `PAGE_PERM_MAP` `medical` use `medical`; demo role seeds get `medical`; medical screen surfaces a 403 as an error toast; payroll preview keeps working (no badge) without the permission.

## Out of scope
- Changing `isTruthy` object semantics for other keys (backend treats any object as truthy, frontend requires a truthy sub-key) — noted as follow-up.
- Gating other modules.

## Constraints
- Strict TDD: RED observed before GREEN. Runner: `cd backend && set -a && . ./.env && set +a && TEST_DB_URL="postgres://rrhh_go:$POSTGRES_PASSWORD@localhost:5434/rrhh_go_test?sslmode=disable" go test ./...` (source: project memory / Phase 2-4 practice).
- Frontend has no automated harness: node harness or `node --check` of inline script + live check.
- Work-unit commits on `feat/medical-permission`, Conventional Commits, no AI attribution. Push/PR are the user's call.
- ~400 authored lines per task is advisory only.

## Tasks
- [x] T1 — migration `0009_medical_permission.sql` + DB test (route: delegated writer; trigger: 2+ non-trivial files across T1-T3)
- [x] T2 — backend gating of 5 medical routes + tests (403 non-admin without key, 403 with `medical:false`, allowed with `medical:true`, admin bypass) (route: delegated writer)
- [x] T3 — frontend permission wiring + 403 handling (route: delegated writer)
- [ ] T4 — apply 0009 to live dev DB, rebuild backend, live smoke (route: inline)

## Acceptance criteria
- Non-admin role without truthy `medical` gets 403 on all 5 medical routes; with `medical:true` gets normal behavior; admin always passes.
- After 0009, every pre-existing role has an explicit `medical` matching its attendance access.
- Role permissions modal shows and saves "Incapacidades"; menu/page follow `medical`.
- Full `go test ./...` green.

## Progress / evidence
- T1 (633952c, +159): RED `TestMigration0009_SeedsMedicalPermission` failed "read 0009 migration: ... no such file"; GREEN after `0009_medical_permission.sql` (13 role cases + idempotent re-run). Non-object permissions (JSON null/array/scalar) become `{"medical": false}`; numbers/strings truthy for attendance too.
- T2 (8d4a668, +107/-5): RED `TestMedicalRecords_RequiresMedicalPermission` "list as nokey: expected 403, got 200"; GREEN after `requirePermission(..., "medical")` in 5 handlers; covers no key, medical:false, no roles row (403, no side effects), medical:true (normal), admin.
- T3 (7aa871e, +12/-7): RED node harness (isPageAllowed('medical') followed attendance; 3 FAIL); GREEN after map/PERM_MODULES/seeds/loadMedical toast; `node --check` of inline script OK. Payroll preview already guards non-array (no change).
- Verification: `go vet ./...` clean, `gofmt -l .` clean, `go test -count=1 ./...` green.

## Next step
T4: apply 0009 to live dev DB, rebuild backend, live smoke.
