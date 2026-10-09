-- name: CreateLiquidationHistoryWithEmployee :one
-- A1 rule 6 (design R3, task 7.2): the tenant check on the client-supplied
-- employee_id IS the insert -- a foreign employee_id selects zero rows from
-- `employees`, so a cross-tenant create becomes pgx.ErrNoRows -> 404 (same
-- shape as CreateEmployeePayRecordWithEmployee / UpsertPayrollRunPayRecord).
-- Kept STRUCTURALLY even though the caller already read the employee via
-- GetEmployeeForLiquidation (slice 3e) to run the calculation -- the same
-- "kept structurally" precedent UpsertPayrollRunPayRecord's own comment
-- states (design R4c).
-- employee_name is DERIVED from the employees row here too, never trusted
-- from the request body -- saveLiqHistory's own call signature sends a name
-- argument, but it is declared-and-ignored by the handler (Q6 precedent).
-- total_amount keeps meaning netTotal (design R6, verified against
-- saveLiqHistory's call site and loadLiqHistory's render site) -- it is
-- NOT available for repurposing, and it is ALWAYS the server-recomputed
-- CalculateLiquidation.NetTotal, never the client-sent value.
-- start_date/breakdown/inputs/calc_version are the decision-#2/#3 audit
-- payload (design R6, migration 0007): breakdown holds the full calculated
-- LiquidationResult, inputs holds the raw acumulados inputs plus which of
-- the five independent branches fired.
INSERT INTO liquidation_history (
  company_id, employee_id, employee_name, reason, exit_date, total_amount, notes,
  start_date, breakdown, inputs, calc_version
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, $3, $4, $5, $6,
  $7, $8, $9, $10
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, reason, exit_date, total_amount, notes,
  created_at, start_date, breakdown, inputs, calc_version;

-- name: ListLiquidationHistory :many
-- Filterable by employee_id (spec "List filtered by employee returns only
-- that employee's liquidaciones") and id (same symmetry every other List
-- query in this codebase keeps). ORDER BY created_at DESC (design R7 -- no
-- PATCH exists for this resource, so there is no "last edited" concept to
-- order by, only newest-first).
SELECT id, company_id, employee_id, employee_name, reason, exit_date, total_amount, notes,
  created_at, start_date, breakdown, inputs, calc_version
FROM liquidation_history
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountLiquidationHistory :one
SELECT count(*) FROM liquidation_history
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'));

-- name: GetLiquidationHistory :one
SELECT id, company_id, employee_id, employee_name, reason, exit_date, total_amount, notes,
  created_at, start_date, breakdown, inputs, calc_version
FROM liquidation_history
WHERE id = $2 AND company_id = $1;

-- name: DeleteLiquidationHistory :execrows
-- Hard delete (spec "List, get, delete" -- matches delLiq's existing
-- behavior; no PATCH exists for this resource -- immutable historical
-- record, design R7). There is nothing to cascade: liquidation_history has
-- no FK relationship to employee_pay_records at all (unlike payroll_history,
-- design R5's no-cascade decision is not even a design choice here, it is
-- structurally the only possible behavior).
DELETE FROM liquidation_history
WHERE id = $2 AND company_id = $1;
