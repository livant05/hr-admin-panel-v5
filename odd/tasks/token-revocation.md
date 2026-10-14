# Feature: token revocation and server-side logout

## Objective
A deactivated or demoted user loses access on the next request, and "Cerrar sesión" invalidates the user's tokens on the server instead of only clearing the browser.

## Problem / Why
Audit 2026-10-13 (H2): `auth.Signer.Middleware` only checks the JWT signature and `exp` (24h TTL). Role and company come from the token claims, nothing is checked against `users`, and there is no logout route. A deactivated or demoted user keeps the old access for up to 24h; a copied token stays valid after logout.

## Scope (authorized)
Backend (`internal/auth`, `internal/handlers`, sqlc queries, new migration 0013), frontend `doLogout`, tests, README. Stacked on `fix/server-permissions` (branch `fix/token-revocation`).

## Decisions
- Per-request session check (orchestrator default, always applied): the middleware loads the user by the token's user id; the request is 401 unless the user exists and `status = 'active'`. `role` and `company_id` put in the request context come from the DB row, not from the token claims, so a demotion applies on the next request.
- User decision (2026-10-13): global logout via `users.token_version` (INT NOT NULL DEFAULT 0, migration 0013). The token carries the version (claim `tv`); a token whose version differs from the DB is 401. A token with no `tv` claim counts as version 0 (tokens issued before this change stay valid until expiry or the first bump).
- `POST /api/auth/logout` (authenticated) increments the caller's `token_version` and returns 204: every token of that user, on every device, stops working.
- The version is also incremented when an admin changes a user's role or status (`PATCH /api/users/{id}`) or deactivates it (`DELETE /api/users/{id}`), so the affected user must sign in again and the frontend's cached role (`hr_meta`) is refreshed.
- 401 responses keep the existing envelope so the frontend's `onUnauthorized` shows the single "Sesión expirada" flow.

## Constraints
- Strict TDD. Backend runner: `cd backend && TEST_DB_URL=postgres://rrhh_go:<POSTGRES_PASSWORD from backend/.env>@127.0.0.1:5434/rrhh_go_test?sslmode=disable go test ./... -count=1`. Frontend runner: node harness under `tools/` (style of `tools/session/harness.mjs`). RED before implementation, GREEN, REFACTOR.
- sqlc: `~/go/bin/sqlc generate` from `backend/internal/db` (config `sqlc.yaml`); commit generated code with the query change.
- Dev DB needs migration 0013 applied manually (initdb runs only on a fresh volume); the orchestrator does that for live verification.
- One work-unit commit per task, Conventional Commits, no AI attribution. Push/PR are the user's decision. ~400 authored lines per task is advisory only.

## Tasks
- [x] T1 — migration 0013 `users.token_version`; `tv` claim issued at login/register; middleware per-request check (exists, active, version match; role/company from DB); tests: deactivated user 401, demoted role applied on next request, version mismatch 401, missing `tv` treated as 0, unknown user 401. (route: delegated writer; trigger: 2+ non-trivial files)
- [ ] T2 — `POST /api/auth/logout` bumps version (204); bump on role/status change and user delete; route classification test updated; tests for each bump path and that other users' tokens are unaffected. (route: same delegated writer)
- [ ] T3 — frontend `doLogout` calls `POST /api/auth/logout` best-effort (local clear always happens, no error toast if offline), harness test; README auth section. (route: same delegated writer)

## Acceptance criteria
- Deactivating, re-roling or logging out a user invalidates that user's existing tokens on the next request; other users unaffected.
- Demoted user (if the token still matches the version) is evaluated with the DB role.
- Full backend suite, `go vet`, existing node harnesses and the new harness pass.

## Delivery
Forecast ~400-600 authored lines (mostly tests). Strategy `ask-on-risk`; stacked on `fix/server-permissions`.

## Progress / evidence
### T1 (commit hash recorded in the T2 entry; a commit cannot contain its own hash)
- Route: delegated writer. TDD: on (source: orchestrator brief), runner `go test`.
- Design: `auth.Signer.Middleware(next, lookup auth.SessionLookup)`; `SessionLookup func(ctx, userID) (auth.Session, error)` is injected, nil panics at construction (no JWT-only mode). `handlers.API.SessionLookup` implements it with sqlc `GetSessionUser` (PK lookup: status, role, company_id, token_version) and is wired in `Routes`. Not found / non-UUID subject -> 401, lookup error -> 500 (fail closed). Token without `tv` decodes as 0.
- `Signer.Sign` now takes the token version; login uses `users.token_version`, register 0. `testutil.TokenAs(t, pool, signer, role)` now inserts a fresh user holding the role (role comes from the DB row).
- Existing tests adapted: `TestUsers_PatchRefusesToRemoveTheLastActiveAdmin` (the caller deactivated itself, so the survivor uses its own token) and `TestUsers_ConcurrentDemotionOfTwoAdminsKeepsOneActive` (a caller that is one of the two admins can now legitimately lose the race with 403/401; asserts one 200 + one refusal and that the guard 409 path is reached in at least one round).
- Runbook updated: migration 0013 must be applied before the new binary.
- RED: `go test ./internal/auth ./internal/handlers` failed to compile: `undefined: auth.Session`, `undefined: auth.SessionLookup`, `undefined: auth.ErrSessionNotFound`, `too many arguments in call to signer.Sign`, `too many arguments in call to ...Middleware` (new tests: TestMiddleware_SessionChecks, TestSessionCheck_*).
- GREEN: `go test ./... -count=1` all packages ok; `go vet ./...` clean.

## Next step
T2.
