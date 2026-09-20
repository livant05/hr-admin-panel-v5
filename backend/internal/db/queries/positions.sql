-- name: ListPositions :many
SELECT id, company_id, name, created_at
FROM positions
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
ORDER BY name
LIMIT $3 OFFSET $4;

-- name: CountPositions :one
SELECT count(*) FROM positions
WHERE company_id = $1 AND ($2::uuid IS NULL OR id = $2);

-- name: GetPosition :one
SELECT id, company_id, name, created_at
FROM positions
WHERE id = $2 AND company_id = $1;

-- name: CreatePosition :one
INSERT INTO positions (company_id, name)
VALUES ($1, $2)
RETURNING id, company_id, name, created_at;

-- name: UpdatePosition :one
UPDATE positions SET name = $3
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, created_at;

-- name: DeletePosition :execrows
DELETE FROM positions
WHERE id = $2 AND company_id = $1;

-- name: CountEmployeesInPosition :one
SELECT count(*) FROM employees
WHERE company_id = $1 AND position = $2;

-- name: RenameEmployeePosition :execrows
UPDATE employees SET position = $3, updated_at = NOW()
WHERE company_id = $1 AND position = $2;
