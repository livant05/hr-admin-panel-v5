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
- [x] T2 — `POST /api/auth/logout` bumps version (204); bump on role/status change and user delete; route classification test updated; tests for each bump path and that other users' tokens are unaffected. (route: same delegated writer)
- [x] T3 — frontend `doLogout` calls `POST /api/auth/logout` best-effort (local clear always happens, no error toast if offline), harness test; README auth section. (route: same delegated writer)

## Acceptance criteria
- Deactivating, re-roling or logging out a user invalidates that user's existing tokens on the next request; other users unaffected.
- Demoted user (if the token still matches the version) is evaluated with the DB role.
- Full backend suite, `go vet`, existing node harnesses and the new harness pass.

## Delivery
Forecast ~400-600 authored lines (mostly tests). Strategy `ask-on-risk`; stacked on `fix/server-permissions`.

## Progress / evidence
### T1 — commit 306efda `fix(auth): check user status, role and token version on every request`
- Route: delegated writer. TDD: on (source: orchestrator brief), runner `go test`.
- Design: `auth.Signer.Middleware(next, lookup auth.SessionLookup)`; `SessionLookup func(ctx, userID) (auth.Session, error)` is injected, nil panics at construction (no JWT-only mode). `handlers.API.SessionLookup` implements it with sqlc `GetSessionUser` (PK lookup: status, role, company_id, token_version) and is wired in `Routes`. Not found / non-UUID subject -> 401, lookup error -> 500 (fail closed). Token without `tv` decodes as 0.
- `Signer.Sign` now takes the token version; login uses `users.token_version`, register 0. `testutil.TokenAs(t, pool, signer, role)` now inserts a fresh user holding the role (role comes from the DB row).
- Existing tests adapted: `TestUsers_PatchRefusesToRemoveTheLastActiveAdmin` (the caller deactivated itself, so the survivor uses its own token) and `TestUsers_ConcurrentDemotionOfTwoAdminsKeepsOneActive` (a caller that is one of the two admins can now legitimately lose the race with 403/401; asserts one 200 + one refusal and that the guard 409 path is reached in at least one round).
- Runbook updated: migration 0013 must be applied before the new binary.
- RED: `go test ./internal/auth ./internal/handlers` failed to compile: `undefined: auth.Session`, `undefined: auth.SessionLookup`, `undefined: auth.ErrSessionNotFound`, `too many arguments in call to signer.Sign`, `too many arguments in call to ...Middleware` (new tests: TestMiddleware_SessionChecks, TestSessionCheck_*).
- GREEN: `go test ./... -count=1` all packages ok; `go vet ./...` clean.

### T2 — commit b147637 `feat(auth): server-side logout and token invalidation on role/status change`
- Route: same delegated writer. `POST /api/auth/logout` is a protected `open` route (behind the middleware; login/register stay public), 204. `changeUser` bumps `token_version` (sqlc `BumpUserTokenVersion`) in the same transaction when a PATCH actually changes role or status, and on a DELETE that deactivates; renames, unchanged values and repeat deletes do not bump.
- Classification: `POST /api/auth/logout` added to `wantRouteClasses` as `open`.
- Test adaptations: survivor token signed with its current version; the concurrent-demotion test resets `token_version` each round.
- RED: `TestRoutes_EveryProtectedRouteIsClassified` (map entry without a registered route: POST /api/auth/logout); `TestLogout_InvalidatesEveryTokenOfTheCallerOnly` and `TestLogout_NonAdminRoleNeedsNoModulePermission` (got 404); `TestUserAdmin_BumpsTokenVersionOnRoleOrStatusChange` (role change: version = 0, want 1); `TestUserAdmin_DeleteBumpsTokenVersion` (delete: version = 0, want 1).
- GREEN: `go test ./... -count=1` all ok; `go vet ./...` clean; concurrent-demotion test stable over -count=8.

### T3 (commit hash: see `git log`; a commit cannot contain its own hash)
- Route: same delegated writer. `Supa.signOut` now fires `POST /api/auth/logout` (bearer, keepalive) directly via `fetch`, not `gres`, so a 401 never starts the "Sesión expirada" flow; errors are swallowed, no toast. `doLogout` calls it fire-and-forget and always clears locally. Demo mode and no-session make no request. Harness: `tools/session/harness.mjs` logout tests replaced/extended (request shape, offline, 401/500, no session; demo test unchanged and passing). README: "Sessions and revocation" replaces the AUTH-10 residual-risk note.
- RED (new tests against the unmodified page): FAIL "logout sends one POST /api/auth/logout ..." (0 !== 1), FAIL "logout still signs out locally ... unreachable" (0 !== 1), FAIL "logout answered 401 or 500 ..." (0 !== 1); 32/35 passed. Note: the page change was written before the first run and reverted with `git stash` to take this RED measurement.
- GREEN: session 35/35, permissions 13/13, leave 7/7, zk-feed 13/13; `node --check` of the main script ok.

### Orchestrator checks (2026-10-13)
- Spot check: `go test ./internal/auth/ ./internal/handlers/ -count=1` ok.
- Dev DB: migration 0013 applied with psql (before the new binary); backend rebuilt, nginx restarted.
- Live (curl): same user on two devices -> payroll 200 on both; logout from D1 204 -> D1 and D2 401, admin 200; re-login 200; admin PATCH role -> old token 401, re-login evaluated with the new role (payroll 403, vacations 200, /me role updated); DELETE user -> token 401 and login 401; admin unaffected. Versions after: test user 3, admin 0.
- Live (Chrome, cache-busted): "Cerrar sesión" sends `POST /api/auth/logout` 204, toast "Sesión cerrada", local session cleared, old token 401. An open tab whose token was revoked from another device shows a single "Sesión expirada" and returns to login. Test user and roles deleted.
- RDD: assess base 3a57996 -> high (hot_path, process_boundary), review due. Consent relayed, user granted. 4-lens native review (lineage review-d2306357519c5a0a): approved, acknowledged (`gentle-ai.review-acknowledged/v1`). Reviewed boundary advances to a32cede.
- Informational follow-ups from the review (not blocking): R4 session lookup failure returns 500 without logging the error (`internal/auth/auth.go:147-150`) — resolved, see below; R3 concurrent-demotion test depends on scheduling to reach the 409 guard (`users_test.go:848-850`), possible flakiness — still open.

### R4 follow-up (user-authorized, 2026-10-13) — commit adcd411 `fix(auth): log session lookup failures`
- Route: direct inline (one mechanical line + one test). `API.SessionLookup` logs `session lookup` with `user_id` and `err` before returning a DB error (the middleware still answers a generic 500).
- RED: `TestSessionCheck_LookupFailureIsLogged` (closed pool -> GET /api/me) got 500 but an empty log. GREEN after the change; `go build`, `go vet` clean; full suite ok.
- RDD assess base a32cede: medium, 63 lines, `review_due: false` (`under_budget`). Dev backend rebuilt.
- R3 (concurrent-demotion test timing) left open.
- Review (2026-10-14): `adcd411` got its own native review (resilience + reliability, lineage review-9275ece9ea098014), approved and acknowledged. Its advisories: canceled lookups were logged at ERROR (now WARN, commit `fix(auth): log canceled session lookups below error level`) and the test did not assert the level (now asserts `level=ERROR` and `err=` for a real failure).

## Delivery (2026-10-13)
User chose Feature Branch Chain on tracker #33 (`feat/rrhh-go-auth-cutover`). Slices, each targeting its parent; this document covers S9a/S9b, `odd/tasks/server-permissions.md` covers S8a-S8c:
| PR | Branch | Commits | +/- |
|---|---|---|---|
| #63 | `auth-cutover/s8a-route-permissions` | 0b8582c | +638/-179 (`size:exception`) |
| #64 | `auth-cutover/s8b-employee-redaction` | a279676..4dbf222 | +229/-8 |
| #65 | `auth-cutover/s8c-frontend-permissions` | da55313..3a57996 | +287/-20 |
| #66 | `auth-cutover/s9a-session-check` | 306efda | +613/-67 (`size:exception`) |
| #67 | `auth-cutover/s9b-server-logout` | b147637..ef2d881 + this record | +361/-27 |
No repository CI besides the skipped Supabase Preview. Nothing merged.

## Next step
Human review and merge down the chain into the tracker; R3 optional.
