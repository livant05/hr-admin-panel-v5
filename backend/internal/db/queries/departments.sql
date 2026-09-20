-- name: ListDepartments :many
SELECT id, company_id, name, created_at
FROM departments
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
ORDER BY name
LIMIT $3 OFFSET $4;

-- name: CountDepartments :one
SELECT count(*) FROM departments
WHERE company_id = $1 AND ($2::uuid IS NULL OR id = $2);

-- name: GetDepartment :one
SELECT id, company_id, name, created_at
FROM departments
WHERE id = $2 AND company_id = $1;

-- name: CreateDepartment :one
INSERT INTO departments (company_id, name)
VALUES ($1, $2)
RETURNING id, company_id, name, created_at;

-- name: UpdateDepartment :one
UPDATE departments SET name = $3
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, created_at;

-- name: DeleteDepartment :execrows
DELETE FROM departments
WHERE id = $2 AND company_id = $1;

-- name: CountEmployeesInDepartment :one
SELECT count(*) FROM employees
WHERE company_id = $1 AND department = $2;

-- name: RenameEmployeeDepartment :execrows
UPDATE employees SET department = $3, updated_at = NOW()
WHERE company_id = $1 AND department = $2;
