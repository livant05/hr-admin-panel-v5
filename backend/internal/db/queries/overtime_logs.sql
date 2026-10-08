-- name: ListOvertimeLogs :many
SELECT id, company_id, employee_id, employee_name, date, hours, type, hourly_rate, amount, notes, created_at
FROM overtime_logs
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('date')::date IS NULL OR date = sqlc.narg('date'))
ORDER BY date DESC
LIMIT $3 OFFSET $4;

-- name: GetOvertimeLog :one
SELECT id, company_id, employee_id, employee_name, date, hours, type, hourly_rate, amount, notes, created_at
FROM overtime_logs
WHERE id = $2 AND company_id = $1;

-- name: CreateOvertimeLog :one
-- A1 rule 6 (design Q2, worked example in attendance_logs.sql/leave_requests.sql):
-- the tenant check on the client-supplied employee_id IS the insert -- a
-- foreign employee_id selects zero rows from `employees`, so a cross-tenant
-- create becomes pgx.ErrNoRows -> 404, never a row written under either
-- tenant. employee_name is derived from the employees row, never trusted
-- from the request body. amount is computed here as
-- hourly_rate * rate * hours using exact Postgres NUMERIC arithmetic
-- (design Q6) -- rate is the statutory recargo resolved server-side from the
-- otRates map (overtime.go) and passed in as sqlc.arg('rate'); the
-- client-submitted amount is never trusted (declared in the request struct
-- only so DisallowUnknownFields accepts the existing payload). hourly_rate
-- itself stays client-supplied in Phase 2 -- design Q6 explicitly defers
-- deriving it server-side from salary/weekly_hours to Phase 3's prCalc port.
INSERT INTO overtime_logs (company_id, employee_id, employee_name, date, hours, type, hourly_rate, amount, notes)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, $3, $4, $5, $6,
       $6 * sqlc.arg(rate)::numeric * $4, $7
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, date, hours, type, hourly_rate, amount, notes, created_at;

-- name: UpdateOvertimeLog :one
-- PATCH keeps employee_id/date immutable, mirroring attendance_logs' and
-- leave_requests' precedent of restricting PATCH to the
-- operationally-editable fields rather than a full-column replace. amount
-- is recomputed from the (possibly changed) hours/hourly_rate/rate, never
-- accepted as-is from the client -- same server-authority rule as create.
UPDATE overtime_logs
SET hours = $3, type = $4, hourly_rate = $5, amount = $5 * sqlc.arg(rate)::numeric * $3, notes = $6
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, date, hours, type, hourly_rate, amount, notes, created_at;

-- name: DeleteOvertimeLog :execrows
-- Hard delete (design Q3): the payroll amount is snapshotted into
-- employee_pay_records at run time (Part A B2), so deleting the log cannot
-- retro-alter a paid slip. overtime_logs is not an FK target
-- (rg 'REFERENCES overtime_logs' migrations/ -> zero matches).
DELETE FROM overtime_logs
WHERE id = $2 AND company_id = $1;
