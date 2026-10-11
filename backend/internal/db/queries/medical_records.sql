-- name: ListMedicalRecords :many
SELECT id, company_id, employee_id, employee_name, type, diagnosis, start_date,
  end_date, days, employer_days, css_days, cert_number, notes, status,
  created_at, salary_basis, cost
FROM medical_records
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY start_date DESC, created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: CountMedicalRecords :one
SELECT count(*) FROM medical_records
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'));

-- name: GetMedicalRecord :one
SELECT id, company_id, employee_id, employee_name, type, diagnosis, start_date,
  end_date, days, employer_days, css_days, cert_number, notes, status,
  created_at, salary_basis, cost
FROM medical_records
WHERE id = $2 AND company_id = $1;

-- name: CreateMedicalRecord :one
-- A1 rule 6: the tenant check on the client-supplied employee_id IS the
-- insert -- a foreign employee_id selects zero rows -> pgx.ErrNoRows -> 404.
-- employee_name is DERIVED from the employees row. days/employer_days/
-- css_days/salary_basis/cost are computed in Go by payroll.CalculateIncapacity
-- (design D2); client values are never read.
INSERT INTO medical_records (
  company_id, employee_id, employee_name, type, diagnosis, start_date, end_date,
  days, employer_days, css_days, cert_number, notes, status, salary_basis, cost
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name,
  $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, type, diagnosis, start_date,
  end_date, days, employer_days, css_days, cert_number, notes, status,
  created_at, salary_basis, cost;

-- name: GetMedicalRecordForUpdate :one
-- Read-modify-write source for PATCH. Tenant-scoped (a foreign id ->
-- ErrNoRows -> 404) and row-locked so two concurrent PATCHes cannot
-- interleave their recompute. effective_salary is the stored snapshot or --
-- only for a row that never had one -- the employee's current salary, which
-- the UPDATE then persists as salary_basis (self-healing).
SELECT m.id, m.company_id, m.employee_id, m.employee_name, m.type, m.diagnosis,
  m.start_date, m.end_date, m.days, m.employer_days, m.css_days,
  m.cert_number, m.notes, m.status, m.salary_basis, m.cost, m.created_at,
  COALESCE(m.salary_basis, e.salary)::numeric AS effective_salary
FROM medical_records m
JOIN employees e ON e.id = m.employee_id
WHERE m.id = $2 AND m.company_id = $1
FOR UPDATE OF m;

-- name: UpdateMedicalRecord :one
-- Full write of the merged row, including the recomputed fields. employee_id,
-- employee_name, company_id and created_at are immutable.
UPDATE medical_records SET
  type = $3, diagnosis = $4, start_date = $5, end_date = $6,
  days = $7, employer_days = $8, css_days = $9,
  cert_number = $10, notes = $11, status = $12,
  salary_basis = $13, cost = $14
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, type, diagnosis, start_date,
  end_date, days, employer_days, css_days, cert_number, notes, status,
  created_at, salary_basis, cost;

-- name: DeleteMedicalRecord :execrows
-- Hard delete (Phase 2 Q3 precedent): medical_records is not an FK target.
DELETE FROM medical_records
WHERE id = $2 AND company_id = $1;
