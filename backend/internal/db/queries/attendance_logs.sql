-- name: ListAttendanceLogs :many
SELECT id, company_id, employee_id, employee_name, department, date, time_in,
  time_out, status, work_type, notes, created_at
FROM attendance_logs
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('date')::date IS NULL OR date = sqlc.narg('date'))
  AND (sqlc.narg('date_from')::date IS NULL OR date >= sqlc.narg('date_from'))
  AND (sqlc.narg('date_to')::date IS NULL OR date <= sqlc.narg('date_to'))
ORDER BY date DESC
LIMIT $3 OFFSET $4;

-- name: GetAttendanceLog :one
SELECT id, company_id, employee_id, employee_name, department, date, time_in,
  time_out, status, work_type, notes, created_at
FROM attendance_logs
WHERE id = $2 AND company_id = $1;

-- name: UpsertAttendanceLog :one
-- A1 rule 6 (design Q2, phase2-design): the tenant check on the
-- client-supplied employee_id IS the insert — a foreign employee_id selects
-- zero rows from `employees`, so a cross-tenant write becomes
-- pgx.ErrNoRows -> 404, never a row written under the wrong (or any)
-- company. employee_name/department are DERIVED from the employees row,
-- never trusted from the request body (spoofing + rename-drift defense).
-- POST is an upsert on (employee_id,date): saveAttendance's own
-- existing-row lookup, generateTestData's on_conflict=employee_id,date raw
-- fetch, and Phase 6's ZKTeco ingestion all rely on this being idempotent.
INSERT INTO attendance_logs (
  company_id, employee_id, employee_name, department, date, time_in, time_out, status, work_type, notes
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, e.department,
  $3, $4, $5, $6, $7, $8
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
ON CONFLICT (employee_id, date) DO UPDATE SET
  employee_name = EXCLUDED.employee_name,
  department    = EXCLUDED.department,
  time_in       = EXCLUDED.time_in,
  time_out      = EXCLUDED.time_out,
  status        = EXCLUDED.status,
  work_type     = EXCLUDED.work_type,
  notes         = EXCLUDED.notes
RETURNING id, company_id, employee_id, employee_name, department, date, time_in,
  time_out, status, work_type, notes, created_at;

-- name: UpdateAttendanceLog :one
-- PATCH keeps employee_id and date immutable (the upsert's own unique key)
-- -- changing which employee or day a punch belongs to is a new record, not
-- an edit, mirroring leave_requests' Q4 precedent of restricting PATCH to
-- the operationally-editable fields rather than a full-column replace.
UPDATE attendance_logs
SET time_in = $3, time_out = $4, status = $5, work_type = $6, notes = $7
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, department, date, time_in,
  time_out, status, work_type, notes, created_at;

-- name: DeleteAttendanceLog :execrows
-- Hard delete (design Q3): a mistyped punch is an operational correction,
-- not history worth preserving — attendance_logs is not an FK target
-- (rg 'REFERENCES attendance_logs' migrations/ -> zero matches).
DELETE FROM attendance_logs
WHERE id = $2 AND company_id = $1;
