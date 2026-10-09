-- name: ListEmployeePayRecords :many
-- Fixed ORDER BY period_year DESC, period_month DESC, created_at DESC
-- (design R7) -- newest period first, ties broken by insertion order.
SELECT id, company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes,
  origin, created_at
FROM employee_pay_records
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('period_year')::int IS NULL OR period_year = sqlc.narg('period_year'))
  AND (sqlc.narg('period_month')::int IS NULL OR period_month = sqlc.narg('period_month'))
ORDER BY period_year DESC, period_month DESC, created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountEmployeePayRecords :one
SELECT count(*) FROM employee_pay_records
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('period_year')::int IS NULL OR period_year = sqlc.narg('period_year'))
  AND (sqlc.narg('period_month')::int IS NULL OR period_month = sqlc.narg('period_month'));

-- name: GetEmployeePayRecord :one
SELECT id, company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes,
  origin, created_at
FROM employee_pay_records
WHERE id = $2 AND company_id = $1;

-- name: CreateEmployeePayRecordWithEmployee :one
-- A1 rule 6 (manual-entry path, design R4a/spec "Manual entry with
-- employee_id succeeds"): the tenant check on the client-supplied
-- employee_id IS the insert -- a foreign employee_id selects zero rows
-- from `employees`, so a cross-tenant create becomes pgx.ErrNoRows -> 404,
-- never a row written under the wrong (or any) company. employee_name and
-- cedula are DERIVED from the employees row, never trusted from the
-- request body (spoofing + rename-drift defense; unlike deductions.go,
-- which trusts a client-supplied cedula even on its WithEmployee path --
-- employee_pay_records' spec requires both derived here).
-- total_earned is RECOMPUTED server-side in NUMERIC (spec: no
-- authoritative total_earned column exists anywhere upstream); net_salary
-- is NOT recomputed and is stored exactly as submitted (design R4f: manual
-- entry derives it arithmetically client-side, but the server does not
-- re-derive it, leaving room for an operator correction).
-- origin is hard 'manual' -- server-owned, never client-supplied (design
-- R4a); this is the ONLY path besides CreateEmployeePayRecordWithoutEmployee
-- that can ever write this column, and both write 'manual'. origin='run'
-- is written exclusively by UpsertPayrollRunPayRecord (a later slice).
INSERT INTO employee_pay_records (
  company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes, origin
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, e.cedula, $3, $4, $5,
  $6, $7, $8, $9, $10, $11, $12,
  $13, ($8::numeric + $9::numeric + $10::numeric + $11::numeric + $12::numeric + $13::numeric),
  $14, $15, $16, $17, $18, $19, 'manual'
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes,
  origin, created_at;

-- name: CreateEmployeePayRecordWithoutEmployee :one
-- CSV-import dual path (spec "CSV row imported successfully"): CSV rows
-- never carry an authoritative employee_id (resolved only by
-- employee_name + cedula in the source file), so there is no
-- client-supplied FK to tenant-check here -- a plain company_id-scoped
-- insert (A1 rules 1-4). employee_name/cedula are stored EXACTLY as the
-- client sent them (the deliberate exception, scoped to this
-- employee_id-absent path only, same shape as
-- CreateDeductionWithoutEmployee). total_earned is still recomputed;
-- net_salary is still trusted as sent (design R4f: CSV import's net_salary
-- is authoritative historical data from the PayDay "Salario Neto" column).
INSERT INTO employee_pay_records (
  company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes, origin
) VALUES (
  $1, NULL, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11, $12, $13,
  $14, ($9::numeric + $10::numeric + $11::numeric + $12::numeric + $13::numeric + $14::numeric),
  $15, $16, $17, $18, $19, $20, 'manual'
)
RETURNING id, company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes,
  origin, created_at;

-- name: UpdateEmployeePayRecord :one
-- Full-column replace of every editable business field (deductions.go's
-- UpdateDeduction precedent -- employee_pay_records keeps PATCH, unlike
-- payroll_history/liquidation_history, per design R7's "employee_pay_records
-- DOES keep PATCH -- loadPayRecords is an editable operator ledger").
-- employee_id, company_id, origin, created_at stay immutable: the FK root,
-- the tenant, and the write-path provenance are set at creation and never
-- reassigned by edit. total_earned is RECOMPUTED here too (same rule as
-- create -- it is a derived column regardless of which verb wrote it);
-- net_salary is NOT recomputed, matching create.
UPDATE employee_pay_records
SET employee_name = $3, cedula = $4, numero_planilla = $5, centro_costo = $6, periodo = $7,
    period_year = $8, period_month = $9, gross_salary = $10, overtime_amount = $11, commissions = $12,
    bonuses = $13, vacations_paid = $14, other_income = $15,
    total_earned = ($10::numeric + $11::numeric + $12::numeric + $13::numeric + $14::numeric + $15::numeric),
    css_employee = $16, se_employee = $17, isr = $18, other_deductions = $19, net_salary = $20, notes = $21
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, cedula, numero_planilla, centro_costo, periodo,
  period_year, period_month, gross_salary, overtime_amount, commissions, bonuses, vacations_paid,
  other_income, total_earned, css_employee, se_employee, isr, other_deductions, net_salary, notes,
  origin, created_at;

-- name: DeleteEmployeePayRecord :execrows
-- Hard delete (spec "List and delete with filters" -- matches delPayRecord's
-- existing behavior; no soft-delete semantics apply to this table).
DELETE FROM employee_pay_records
WHERE id = $2 AND company_id = $1;
