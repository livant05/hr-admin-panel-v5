-- name: ListEmployees :many
SELECT id, company_id, first_name, last_name, cedula, ss_number, dv, birth_date, age, sex,
  marital_status, blood_type, nationality, address, phone, email,
  license_number, license_type, license_expiry, position, department, branch,
  salary, weekly_hours, monthly_hours, hourly_rate, contract_type, start_date,
  status, contract_expiry, work_permit_expiry, emergency_contact, emergency_phone,
  emergency_relation, photo_url, created_at, updated_at
FROM employees
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY first_name, last_name
LIMIT $3 OFFSET $4;

-- name: CountEmployees :one
SELECT count(*) FROM employees
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'));

-- name: GetEmployee :one
SELECT id, company_id, first_name, last_name, cedula, ss_number, dv, birth_date, age, sex,
  marital_status, blood_type, nationality, address, phone, email,
  license_number, license_type, license_expiry, position, department, branch,
  salary, weekly_hours, monthly_hours, hourly_rate, contract_type, start_date,
  status, contract_expiry, work_permit_expiry, emergency_contact, emergency_phone,
  emergency_relation, photo_url, created_at, updated_at
FROM employees
WHERE id = $2 AND company_id = $1;

-- name: CreateEmployee :one
INSERT INTO employees (
  company_id, first_name, last_name, cedula, ss_number, dv, birth_date, age, sex,
  marital_status, blood_type, nationality, address, phone, email,
  license_number, license_type, license_expiry, position, department, branch,
  salary, weekly_hours, monthly_hours, hourly_rate, contract_type, start_date,
  status, contract_expiry, work_permit_expiry, emergency_contact, emergency_phone,
  emergency_relation, photo_url
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9,
  $10, $11, $12, $13, $14, $15,
  $16, $17, $18, $19, $20, $21,
  $22, $23, $24, $25, $26, $27,
  $28, $29, $30, $31, $32,
  $33, $34
)
RETURNING id, company_id, first_name, last_name, cedula, ss_number, dv, birth_date, age, sex,
  marital_status, blood_type, nationality, address, phone, email,
  license_number, license_type, license_expiry, position, department, branch,
  salary, weekly_hours, monthly_hours, hourly_rate, contract_type, start_date,
  status, contract_expiry, work_permit_expiry, emergency_contact, emergency_phone,
  emergency_relation, photo_url, created_at, updated_at;

-- name: UpdateEmployee :one
UPDATE employees SET
  first_name = $3, last_name = $4, cedula = $5, ss_number = $6, dv = $7,
  birth_date = $8, age = $9, sex = $10, marital_status = $11, blood_type = $12,
  nationality = $13, address = $14, phone = $15, email = $16,
  license_number = $17, license_type = $18, license_expiry = $19,
  position = $20, department = $21, branch = $22,
  salary = $23, weekly_hours = $24, monthly_hours = $25, hourly_rate = $26,
  contract_type = $27, start_date = $28, status = $29,
  contract_expiry = $30, work_permit_expiry = $31,
  emergency_contact = $32, emergency_phone = $33, emergency_relation = $34,
  photo_url = $35
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, first_name, last_name, cedula, ss_number, dv, birth_date, age, sex,
  marital_status, blood_type, nationality, address, phone, email,
  license_number, license_type, license_expiry, position, department, branch,
  salary, weekly_hours, monthly_hours, hourly_rate, contract_type, start_date,
  status, contract_expiry, work_permit_expiry, emergency_contact, emergency_phone,
  emergency_relation, photo_url, created_at, updated_at;

-- name: DeactivateEmployee :execrows
-- Soft delete only (design P6.2): employees has 9 CASCADE child tables
-- (attendance_logs, leave_balances, leave_requests, overtime_logs,
-- deductions, medical_records, evaluations, uniforms, enrollments) that a
-- real DELETE would silently destroy, plus 2 tables with no ON DELETE clause
-- (liquidation_history, generated_documents) that would block it with a raw
-- FK error. No handler ever issues DELETE FROM employees.
UPDATE employees SET status = 'inactive', updated_at = NOW()
WHERE id = $2 AND company_id = $1 AND status <> 'inactive';
