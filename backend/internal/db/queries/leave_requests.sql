-- name: ListLeaveRequests :many
SELECT id, company_id, employee_id, employee_name, type, start_date, end_date, days, status, notes, created_at
FROM leave_requests
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: GetLeaveRequest :one
SELECT id, company_id, employee_id, employee_name, type, start_date, end_date, days, status, notes, created_at
FROM leave_requests
WHERE id = $2 AND company_id = $1;

-- name: CreateLeaveRequest :one
-- A1 rule 6 (design Q2, worked example in attendance_logs.sql): the tenant
-- check on the client-supplied employee_id IS the insert -- a foreign
-- employee_id selects zero rows from `employees`, so a cross-tenant create
-- becomes pgx.ErrNoRows -> 404, never a row written under either tenant.
-- employee_name is derived from the employees row, never trusted from the
-- request body. days is accepted verbatim from the client (spec: "days is
-- computed client-side and passed through, not recomputed server-side,
-- matches saveLeaveReq") -- this table has no financial amount to protect
-- the way overtime_logs does.
INSERT INTO leave_requests (company_id, employee_id, employee_name, type, start_date, end_date, days, notes)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, $3, $4, $5, $6, $7
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, type, start_date, end_date, days, status, notes, created_at;

-- name: UpdateLeaveRequestStatus :one
-- Q4 state machine: PATCH accepts only {status,notes}; start_date/end_date/
-- type/employee_id are immutable after creation. The `status = 'pending'`
-- predicate closes the approve-twice race in SQL, not just in the handler's
-- pre-check Get: a concurrent PATCH that already won sets status away from
-- 'pending', so this UPDATE affects zero rows and returns pgx.ErrNoRows,
-- which the handler maps to 409 (not 404 -- existence was already confirmed
-- by the handler's own Get before calling this).
UPDATE leave_requests
SET status = $3, notes = $4
WHERE id = $2 AND company_id = $1 AND status = 'pending'
RETURNING id, company_id, employee_id, employee_name, type, start_date, end_date, days, status, notes, created_at;
