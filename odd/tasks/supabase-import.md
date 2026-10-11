# Feature: offline Supabase → Go data import tool

## Objective
A Go CLI (`backend/cmd/import`) that loads a Supabase data export into the Go Postgres schema, preserving original UUIDs, so the Go backend can replace Supabase with real data (evaluations included).

## Problem / Why
No data was ever migrated: the Go DB only has the seed company/admin. Evaluations (and every other table) reference Supabase employee UUIDs that do not exist in Go, so a per-table import is impossible without employees first. User decisions (2026-10-10): full import tool from a JSON export the user provides (no direct Supabase access by the agent); imported users get temporary random passwords written to a local CSV.

## Input contract
`--dir <path>`: one `<table>.json` per table, each a JSON array of row objects as returned by PostgREST `GET /rest/v1/<table>?select=*`. Missing files = table skipped (reported). `--db-url` or env `DB_URL` (same as `cmd/api`).

## Scope (authorized)
- Load order by FK tiers: companies → departments/positions/branches/roles/users/document_templates/trainings/surveys/announcements/job_postings/candidates/payroll_history/zkteco_devices/zkteco_unknown_pins → employees → all employee-dependent tables (attendance_logs, leave_balances, leave_requests, overtime_logs, deductions, employee_pay_records, liquidation_history, generated_documents, medical_records, evaluations, uniforms, enrollments, survey_responses, applications).
- Generic insert by SQL preserving `id`: columns = intersection of JSON keys and the live table columns (from `information_schema`); unknown JSON keys dropped and reported per table; never insert generated columns (`leave_balances.remaining_days`). `ON CONFLICT (id) DO NOTHING` → idempotent re-runs, seed company/admin untouched. Insert-only (no UPDATE path, the `update_timestamp` trigger must not rewrite `updated_at`).
- One transaction for the whole import; any error rolls back everything.
- `--dry-run`: full validation + transforms + per-table report (read / would insert / already present / invalid / dropped keys), rolls back.
- Invalid rows abort by default; `--skip-invalid` skips them and lists them (table, id, reason) in the report.
- Transforms mirroring server invariants (reuse the pure Go packages, never re-implement):
  - evaluations: unwrap legacy JSON-string `scores`, `evaluation.ParseScores` + `Evaluate`, store `CanonicalScores()`, recomputed `avg`/`category`, status default `completada`. Non-integer/out-of-range scores → invalid (no silent rounding).
  - medical_records: `payroll.CalculateIncapacity` with the imported employee salary → days/employer_days/css_days/salary_basis/cost; `end < start` or unknown type → invalid.
  - overtime_logs: recompute `amount` with the same rate table/caps as `handlers/overtime.go` (reuse or extract, don't duplicate constants); unknown type → invalid.
  - roles: after insert, apply migration 0009's `medical` seeding to imported roles lacking the key.
  - attendance_logs/leave_balances: unique-key duplicates inside the export → invalid.
  - payroll_history, liquidation_history, employee_pay_records: imported as-is (immutable history; `origin` default `manual`).
- users: every imported user (not already present) gets a random temporary password (crypto/rand, ≥16 chars), bcrypt via `auth.HashPassword`/existing helper; `--passwords-out <file>` (required when users.json has new users) writes CSV `email,temporary_password` with mode 0600, refuses to overwrite an existing file; passwords never printed to stdout/logs. Email already used by a different user id → invalid.
- Docs: `backend/README.md` section (how to export from Supabase, run dry-run, run, distribute passwords); `.gitignore` pattern for the passwords CSV.

## Out of scope
- Connecting to Supabase (user exports).
- User management UI/route.
- Re-validating tenant scoping beyond FK integrity (company ids come from the export).

## Constraints
- Strict TDD: RED observed before GREEN. Runner: `cd backend && set -a && . ./.env && set +a && TEST_DB_URL="postgres://rrhh_go:$POSTGRES_PASSWORD@localhost:5434/rrhh_go_test?sslmode=disable" go test ./...`.
- DB tests use the `testutil` pattern; fixtures are small synthetic JSON exports under `backend/cmd/import/testdata/` (no real personal data).
- Work-unit commits on `feat/supabase-import`, Conventional Commits, no AI attribution. Push/PR/delivery strategy are the user's call (forecast > 400 lines; chain strategy asked at delivery).
- ~400 authored lines per task is advisory only.

## Tasks
- [x] T1 — CLI skeleton: flags, loader, table registry/tier order, generic id-preserving insert with column intersection, single tx, dry-run, report (route: delegated writer; trigger: 2+ non-trivial files)
- [x] T2 — transforms + validation: evaluations, medical_records, overtime_logs, roles medical seeding, unique-key duplicates, `--skip-invalid` (route: delegated writer)
- [x] T3 — users: temporary passwords, bcrypt, 0600 CSV, no overwrite, email collision (route: delegated writer)
- [x] T4 — docs + .gitignore (88f20b2); live dry-run on the dev DB with synthetic exports (parent, inline)

## Acceptance criteria
- Importing a synthetic export into an empty schema reproduces ids and relations; re-running inserts nothing new.
- Legacy evaluation string scores become canonical objects with server avg/category; invalid rows abort (or are skipped and reported with `--skip-invalid`).
- Medical cost/salary_basis and overtime amount match what the API would compute.
- Imported roles carry `medical`; imported users can log in with the CSV password; seed admin unchanged.
- Dry-run writes nothing.
- Full `go test ./...` green; `go vet`, `gofmt -l` clean.

## Progress / evidence
Route: delegated writer (single), TDD strict (runner: go test, source: task brief).
- T1 745ff2d (1100 ins): RED = all importer tests failed against a `not implemented` stub (e.g. `TestRun_InsertsInTierOrderPreservingIDs: Run: not implemented`); cmd/import RED against a stub run() returning 99 (`missing --dir: code=99 ... want 2`). GREEN after implementation. Files: cmd/import/*, internal/importer/{importer,insert,load,registry,report}.go, testutil.IsolatedSchema.
- T2 4e08902 (755 ins / 49 del): RED = behavioral (`avg=1.00 category=Deficiente`, `Inserted:6 want 1 inserted 5 invalid`, parity `api "45.83" != import "1.00"`, dup reason `duplicate key value violates unique constraint`). Overtime table/caps extracted to internal/overtime, medical caps/status to payroll; handlers use them. 0009 embedded via backend/migrations/embed.go.
- T3 ae446b0 (467 ins / 12 del): RED = `UsersCreated = 0, want 2`, `err = <nil>, want ErrPasswordsOutRequired`, `expected an error when the passwords file exists`, email collisions inserted 5 invalid 0. CLI flag wiring RED was a build failure only.
- T4 docs 88f20b2 (105 ins): backend/README.md section + .gitignore `*passwords*.csv`.
- Verification: full `go test ./... -count=1` green (scheduler flake FK 23503 seen once, green on re-run); `go vet ./...` and `gofmt -l .` clean; `go build ./cmd/import` ok.
- Test isolation: each DB test uses a fresh schema (testutil.IsolatedSchema, search_path) with all migrations + seed; dropped on cleanup. 0007's pg_constraint-by-name guard skips epr_origin_chk in these schemas.

- T4 live dry-run (parent): `go run ./cmd/import --dry-run` against live `rrhh_go` with testdata/export_users (2 users would be created, no CSV written), testdata/export (seed company reported already present; `supabase_only_column` and `remaining_days` dropped) and testdata/export_invalid (1 invalid row reported, exit 1). Row counts employees/evaluations/users/roles identical before and after (2/0/1/0).

## Review follow-ups (user-approved WARNINGs, strict TDD)
- R4-ambiguous-commit-deletes-passwords c87121a: on Commit error the CSV is deleted only after a fresh-context (10s) probe confirms no created user exists; present => kept + `Report.CommitWarning`; undeterminable => kept + `ErrCommitOutcomeUnknown`. Seams `Options.Commit`/`Options.Probe`. RED: `TestUsers_AmbiguousCommitThatLandedKeepsPasswordsAndWarns` (no CommitWarning), `...FailedCommitThatDidNotLand...` (err nil), `...UnverifiableCommitOutcome...` (err nil). GREEN after fix.
- R3-unique-key-claimed-by-rejected-row + R3-trailing-json-silently-ignored 8f13675: `uniqueTracker` split into `check` (no claim) and `claim` (after processRow succeeds); `readTable` requires EOF after the array. RED: `TestRun_RejectedRowDoesNotClaimItsUniqueKey` (valid row rejected as duplicate of rejected row), `TestRun_TrailingJSONInATableFileFailsTheImport` (err nil). GREEN after fix.
- R2-ot-type-list-dup c04d787: `overtime.Types()`/`TypesList()`; importer and `handlers/overtime_logs.go` messages derive from it (text unchanged). RED: `TestOvertime_UnknownTypeMessageListsTheOvertimePackageTypes` (stub Types returned nil), `TestTypes_...` (undefined symbol). GREEN after fix.
- Re-verification: `go test ./...` green, `go vet`, `gofmt -l`, `go build ./cmd/import` clean.

## Next step
Delivery: ~2400 authored lines > 400 budget; user picks the delivery strategy.
