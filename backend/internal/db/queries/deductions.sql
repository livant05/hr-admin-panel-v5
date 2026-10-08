-- name: ListDeductions :many
SELECT id, company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, created_at, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo,
  prioridad, end_date, periodo, numero_planilla
FROM deductions
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: GetDeduction :one
SELECT id, company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, created_at, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo,
  prioridad, end_date, periodo, numero_planilla
FROM deductions
WHERE id = $2 AND company_id = $1;

-- name: CreateDeductionWithEmployee :one
-- A1 rule 6 (design Q2): the tenant check on the client-supplied employee_id
-- IS the insert, same as the other four Phase 2 tables -- a foreign
-- employee_id selects zero rows from `employees`, so a cross-tenant create
-- becomes pgx.ErrNoRows -> 404. employee_name is derived from the employees
-- row when employee_id IS provided (design Q2's "store as sent" exception
-- only covers the employee_id-absent CSV-import path in
-- CreateDeductionWithoutEmployee below).
INSERT INTO deductions (
  company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo, prioridad,
  end_date, periodo, numero_planilla
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
  $14, $15, $16, $17, $18
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, created_at, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo,
  prioridad, end_date, periodo, numero_planilla;

-- name: CreateDeductionWithoutEmployee :one
-- design Q1/Q2: deductions.employee_id is nullable (migration 0006) --
-- importDedCSV legitimately sends employee_id:null for a name/cedula that
-- matches no employee. There is no client-supplied employee_id to
-- tenant-check here, so this is a plain company_id-scoped insert (A1 rules
-- 1-4); employee_name/cedula are stored exactly as the client sent them --
-- the one deliberate exception to the "derive, never trust" rule, scoped to
-- this employee_id-absent path only (design Q1's "deductions is the
-- exception" note).
INSERT INTO deductions (
  company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo, prioridad,
  end_date, periodo, numero_planilla
) VALUES (
  $1, NULL, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
)
RETURNING id, company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, created_at, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo,
  prioridad, end_date, periodo, numero_planilla;

-- name: UpdateDeduction :one
-- PATCH is a full-column replace of every editable field -- deductions has
-- no state-machine PATCH restriction like leave_requests' Q4 (the design's
-- Interfaces/Contracts section states PATCH -> 200 [Deduction] with no
-- field restriction). employee_id/company_id/created_at stay immutable,
-- matching every other table's precedent: the FK root is set at creation,
-- never reassigned by edit.
UPDATE deductions
SET employee_name = $3, type = $4, description = $5, total_amount = $6, quota = $7, remaining = $8,
    start_date = $9, status = $10, cedula = $11, acreedor_nombre = $12, acreedor_codigo = $13,
    tipo_pago = $14, centro_costo = $15, prioridad = $16, end_date = $17, periodo = $18,
    numero_planilla = $19
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, type, description, total_amount, quota, remaining,
  start_date, status, created_at, cedula, acreedor_nombre, acreedor_codigo, tipo_pago, centro_costo,
  prioridad, end_date, periodo, numero_planilla;

-- name: CancelDeduction :execrows
-- Soft delete (design Q3): exportDedCSV already reads status==='cancelled'
-- -> "¿Cancelada? SI", and saldaDed already writes 'paid' -- the status
-- value already exists and is already consumed by the frontend. Mirrors
-- DeactivateEmployee's exact shape (P6.2): the `status <> 'cancelled'`
-- guard makes a second DELETE on an already-cancelled row report 404
-- (0 rows affected), consistent with that precedent.
UPDATE deductions
SET status = 'cancelled'
WHERE id = $2 AND company_id = $1 AND status <> 'cancelled';
