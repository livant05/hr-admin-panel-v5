-- name: ListPayrollRunInputs :many
-- One row per active employee with the three aggregates prCalc needs
-- (design R4d). loadPayroll fetches four whole tables and reduces
-- client-side; this is one statement.
-- has_attendance mirrors attData.hasData (ANY row in the period, not just
-- absences -- loadPayroll:2963 `hasData: eAtt.length>0`).
-- absent_days reproduces loadPayroll:2957 EXACTLY, including HAZARD 5:
-- `l.work_type ? l.work_type===28 : (l.status==='absent')` treats
-- work_type=0 as FALSY, so 0 must fall back to the legacy status check. A
-- plain COALESCE(work_type, ...) port would handle NULL but silently drop
-- those rows.
-- Date-based inputs (attendance, overtime) are bounded by the half-open range
-- [start_date, end_date): the whole month for 'mensual', days 1-15 or 16-end
-- for a quincena. The handler computes the range from the period.
-- Deductions carry NO date filter, matching
-- Supa.sel('deductions',{status:'active'}) (loadPayroll:2941); the caller
-- scales the returned quota by the period factor.
-- No LIMIT/OFFSET, deliberately departing from the P2.1 rule-7 pagination
-- convention -- a partial payroll run is far worse than a slow one.
SELECT
  e.id, e.first_name, e.last_name, e.cedula, e.salary,
  COALESCE(att.has_data, false)      AS has_attendance,
  COALESCE(att.absent_days, 0)::int  AS absent_days,
  COALESCE(ot.amount, 0)::numeric    AS overtime_amount,
  COALESCE(ded.quota, 0)::numeric    AS deduction_quota
FROM employees e
LEFT JOIN (
  SELECT al.employee_id, true AS has_data,
         count(*) FILTER (
           WHERE CASE WHEN COALESCE(al.work_type, 0) <> 0
                      THEN al.work_type = 28
                      ELSE al.status = 'absent' END
         ) AS absent_days
  FROM attendance_logs al
  WHERE al.company_id = $1
    AND al.date >= sqlc.arg('start_date')::date
    AND al.date <  sqlc.arg('end_date')::date
  GROUP BY al.employee_id
) att ON att.employee_id = e.id
LEFT JOIN (
  SELECT ol.employee_id, SUM(COALESCE(ol.amount, 0)) AS amount
  FROM overtime_logs ol
  WHERE ol.company_id = $1
    AND ol.date >= sqlc.arg('start_date')::date
    AND ol.date <  sqlc.arg('end_date')::date
  GROUP BY ol.employee_id
) ot ON ot.employee_id = e.id
LEFT JOIN (
  SELECT d.employee_id, SUM(COALESCE(d.quota, 0)) AS quota
  FROM deductions d
  WHERE d.company_id = $1 AND d.status = 'active'
  GROUP BY d.employee_id
) ded ON ded.employee_id = e.id
WHERE e.company_id = $1 AND e.status = 'active'
ORDER BY e.first_name, e.last_name;

-- name: LockPayrollRun :exec
-- Serializes payroll runs for one (company, year, month): a transaction-scoped
-- advisory lock taken first inside the run's transaction, so a double submit
-- (or a mensual racing a quincena) waits for the other run to commit and then
-- sees its rows. The unique indexes remain the backstop.
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: CountConflictingPayrollPeriods :one
-- A month is paid either as ONE 'mensual' run or as quincenas, never both.
-- Counts history rows AND run-origin ledger rows of the same month whose kind
-- clashes with the requested period (history can be deleted while its ledger
-- rows survive, settled decision #2, so both sources are checked). The legacy
-- 'quincenal' history value counts as a quincena.
SELECT (
  SELECT count(*) FROM payroll_history ph
  WHERE ph.company_id = $1 AND ph.year = $2 AND ph.month = $3
    AND ph.period <> sqlc.arg('period')::text
    AND (sqlc.arg('period')::text = 'mensual' OR ph.period = 'mensual')
) + (
  SELECT count(*) FROM employee_pay_records epr
  WHERE epr.company_id = $1 AND epr.period_year = $2 AND epr.period_month = $3
    AND epr.origin = 'run'
    AND epr.period <> sqlc.arg('period')::text
    AND (sqlc.arg('period')::text = 'mensual' OR epr.period = 'mensual')
) AS conflicts;

-- name: UpsertPayrollHistory :one
-- The committing run's one aggregate row (design R4e, task 6.3/6.4). Written
-- FIRST inside the same transaction as the N UpsertPayrollRunPayRecord calls
-- (the P6.1 department-rename-propagation tx shape) -- a failure in any
-- later upsert rolls this back too (TestPayrollRun_IsAtomic).
-- Re-running a period REPLACES its row (unique key company, year, month,
-- period), the same replace semantics the ledger upsert already has.
INSERT INTO payroll_history (
  company_id, period, month, year, month_name, employee_count,
  total_bruto, total_isr, total_neto, total_empresa
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
ON CONFLICT (company_id, year, month, period) DO UPDATE SET
  month_name     = EXCLUDED.month_name,
  employee_count = EXCLUDED.employee_count,
  total_bruto    = EXCLUDED.total_bruto,
  total_isr      = EXCLUDED.total_isr,
  total_neto     = EXCLUDED.total_neto,
  total_empresa  = EXCLUDED.total_empresa,
  created_at     = NOW()
RETURNING id, company_id, period, month, year, month_name, employee_count,
  total_bruto, total_isr, total_neto, total_empresa, created_at;

-- name: ListPayrollHistory :many
-- Fixed ORDER BY year DESC, month DESC (design R7) -- replaces
-- loadPayrollHistory's client-side sort (hr_admin_panel.html:3131).
SELECT id, company_id, period, month, year, month_name, employee_count,
  total_bruto, total_isr, total_neto, total_empresa, created_at
FROM payroll_history
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('year')::int IS NULL OR year = sqlc.narg('year'))
  AND (sqlc.narg('month')::int IS NULL OR month = sqlc.narg('month'))
ORDER BY year DESC, month DESC
LIMIT $3 OFFSET $4;

-- name: CountPayrollHistory :one
SELECT count(*) FROM payroll_history
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('year')::int IS NULL OR year = sqlc.narg('year'))
  AND (sqlc.narg('month')::int IS NULL OR month = sqlc.narg('month'));

-- name: GetPayrollHistory :one
SELECT id, company_id, period, month, year, month_name, employee_count,
  total_bruto, total_isr, total_neto, total_empresa, created_at
FROM payroll_history
WHERE id = $2 AND company_id = $1;

-- name: DeletePayrollHistory :execrows
-- Hard delete, no cascade to employee_pay_records (design R5 -- no FK
-- exists between the two tables; TestPayrollHistory_DeleteLeavesPayRecords
-- pins this as the default, not an implementation).
DELETE FROM payroll_history
WHERE id = $2 AND company_id = $1;
