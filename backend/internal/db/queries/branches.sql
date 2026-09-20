-- name: ListBranches :many
SELECT id, company_id, name, created_at
FROM branches
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
ORDER BY name
LIMIT $3 OFFSET $4;

-- name: CountBranches :one
SELECT count(*) FROM branches
WHERE company_id = $1 AND ($2::uuid IS NULL OR id = $2);

-- name: GetBranch :one
SELECT id, company_id, name, created_at
FROM branches
WHERE id = $2 AND company_id = $1;

-- name: CreateBranch :one
INSERT INTO branches (company_id, name)
VALUES ($1, $2)
RETURNING id, company_id, name, created_at;

-- name: UpdateBranch :one
UPDATE branches SET name = $3
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, created_at;

-- name: DeleteBranch :execrows
DELETE FROM branches
WHERE id = $2 AND company_id = $1;

-- name: CountEmployeesInBranch :one
SELECT count(*) FROM employees
WHERE company_id = $1 AND branch = $2;

-- name: RenameEmployeeBranch :execrows
UPDATE employees SET branch = $3, updated_at = NOW()
WHERE company_id = $1 AND branch = $2;
