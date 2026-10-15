# Trainings backend
Objective: move the Capacitaciones module (trainings + enrollments) from the in-memory demo store to the Go API, and add editing (course status, grading an enrollment) so certificates work with real data.
Branch: `feat/trainings-backend` (on `fix/role-rename`). Delivery: feature-branch-chain (user's choice on tracker #33), one PR per task slice. Tests: Go `go test ./...` with `TEST_DB_URL`; frontend node harness `tools/trainings/harness.mjs`.

## Specs
- S1 — Trainings API: `/api/trainings` list (filter `id`), get, create, partial PATCH, delete; tenant-scoped like the recruitment resources. Fields from the existing table: `name` (required), `category`, `hours`, `start_date`, `end_date`, `mode`, `instructor`, `description`, `status`. Deleting a course deletes its enrollments (the schema cascades, and the panel already removes them together). Assumption: routes are gated by the `employees` permission, the key the panel already uses for the Capacitaciones page.
- S2 — Enrollments API: `/api/enrollments` list (filters `id`, `training_id`, `employee_id`), get, create, PATCH, delete. Create takes `training_id` and `employee_id`, both checked against the caller's company in the insert itself; `training_name` and `employee_name` are derived by the server. Assumption: one enrollment per employee and course (migration `0016`, unique index, 409 in Spanish), since the panel already hides employees who are enrolled.
- S3 — From the recommended option the user accepted: "cambiar el estado del curso". A course's `status` is one of `programado`, `en_progreso`, `completado` and can be changed from the panel.
- S4 — From the same option: "calificar una inscripción (estado + nota). Agrega una acción "Calificar" en la tabla de inscripciones, y con eso los certificados funcionan." An enrollment PATCH sets `status` (`programado`, `en_progreso`, `completado`), `score` (0-100, nullable) and `notes`; the panel's enrollments table gets a "Calificar" action; certificates list completed enrollments with a score, from API data.
- S5 — Panel cutover: in real mode `loadTraining`, `saveTraining`, `openEnrollModal`, `delTraining`, `delEnrollment` and `printCertificate` use the API (no more success toast for an unsaved change); demo mode keeps working on `DS`.
- S6 — Docs: backend README sections for both resources; runbook entry for `0016`.

## Tasks
| ID | Specs | Route | Status | Commit / evidence |
|---|---|---|---|---|
| T1 | S1, S3 (API), S6 | inline | done | `6edd527`; RED 5/5 (routes 404) -> GREEN; full suite ok |
| T2 | S2, S4 (API), S6 | inline | pending | |
| T3 | S3, S4, S5 (panel) | inline | pending | |

Route evidence: no Writer trigger (units are sequential: T2 depends on T1's resource, T3 on both), so the parent writes inline following this logbook.

## Log
- L1 (2026-10-15, user, verbatim): "arranca con los dos primeros" — the two remaining items were the trainings backend and the permission (role) renames; the role renames are done on `fix/role-rename`.
- L2 (2026-10-15, user, verbatim): "dale lo recomendado" — answering the scope question; the recommended option was: "Lo anterior más edición: cambiar el estado del curso y calificar una inscripción (estado + nota). Agrega una acción "Calificar" en la tabla de inscripciones, y con eso los certificados funcionan."
- L3: exploration. Tables `trainings` and `enrollments` exist since `0001_init.sql` (enrollments: `training_id` FK cascade, `employee_id` FK, `status` default `programado`, `score NUMERIC(5,2)`, `notes`). No API route uses them; the importer already loads both. The panel stores everything in `DS` and toasts success; `openEnrollModal` reads `DS.employees`, which is never filled outside demo mode. No UI changes a course's status or grades an enrollment, so completed counts and certificates are always 0 with real data.
- L4: forecast ~1300 authored changed lines (T1 ~450, T2 ~600, T3 ~250) — over the ~400 budget; strategy feature-branch-chain is already the user's choice for this tracker, so each task becomes its own chained PR when delivered.
- L5 (T1): `/api/trainings` CRUD in `backend/internal/handlers/trainings.go` (queries `trainings.sql`), routes gated `need("employees")` and classified in `routes_permissions_test.go`. Validation: name 1-200, hours 0-10000 whole, dates YYYY-MM-DD with end >= start (empty string rejected; the panel must send null), status programado|en_progreso|completado, exact PATCH keys (shared decoder). Delete cascades enrollments. Tests `trainings_test.go` (create/defaults/list, 13 validation cases, partial PATCH incl. status and null-clears, delete cascade + 404, tenant isolation, permission 403/201). README "Trainings (Capacitaciones)" + permissions table. Full `go test ./...` ok.
