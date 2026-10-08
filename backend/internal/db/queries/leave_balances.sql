-- name: ListLeaveBalances :many
SELECT id, company_id, employee_id, employee_name, year, earned_days, used_days, remaining_days
FROM leave_balances
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('year')::int IS NULL OR year = sqlc.narg('year'))
ORDER BY year DESC, employee_name
LIMIT $3 OFFSET $4;

-- name: GetLeaveBalance :one
SELECT id, company_id, employee_id, employee_name, year, earned_days, used_days, remaining_days
FROM leave_balances
WHERE id = $2 AND company_id = $1;

-- name: UpdateLeaveBalance :one
-- PATCH restricted to used_days only (design Q3): earned_days is owned by
-- the accrual job (AccrueVacationDays below) and would be silently reverted
-- on the next scheduled run, and remaining_days is a GENERATED column that
-- recomputes itself from earned_days - used_days.
UPDATE leave_balances
SET used_days = $3
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, year, earned_days, used_days, remaining_days;

-- name: AccrueVacationDays :many
-- Design Q5a/Q5b, ported from accrue_vacation_days() (0001_init.sql:419-433).
-- One set-based statement replaces the plpgsql FOR loop over every active
-- employee. The LEFT JOIN preserves the original behavior of inserting a
-- zero row for an employee with no attendance. The ON CONFLICT clause
-- touches only earned_days -- a recompute of the absolute value, never an
-- increment -- which is what makes repeated runs idempotent by construction
-- (running this a hundred times in a row yields byte-identical rows).
--
-- This query is created in slice 2c1 because it is a leave_balances query
-- (design's own file-changes table assigns it here), but its only caller --
-- the accrual scheduler's Run/AccrueAll/AccrueCompany (design Q5c) -- is
-- slice 2c2's job, not implemented yet. sqlc.narg('company_id') is the
-- scheduler's two-method split: AccrueAll passes NULL (no handler can reach
-- it), AccrueCompany passes a non-nullable tenant.
INSERT INTO leave_balances (company_id, employee_id, employee_name, year, earned_days, used_days)
SELECT e.company_id, e.id, e.first_name || ' ' || e.last_name,
       sqlc.arg('year')::int, FLOOR(COALESCE(a.days, 0) / 11.0), 0
FROM employees e
LEFT JOIN (
  SELECT employee_id, COUNT(*) AS days FROM attendance_logs
  WHERE status IN ('present', 'late') AND EXTRACT(YEAR FROM date) = sqlc.arg('year')::int
  GROUP BY employee_id
) a ON a.employee_id = e.id
WHERE e.status = 'active'
  AND (sqlc.narg('company_id')::uuid IS NULL OR e.company_id = sqlc.narg('company_id'))
ON CONFLICT (employee_id, year) DO UPDATE SET earned_days = EXCLUDED.earned_days
RETURNING id, company_id, employee_id, employee_name, year, earned_days, used_days, remaining_days;
