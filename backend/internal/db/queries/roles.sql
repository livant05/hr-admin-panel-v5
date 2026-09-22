-- name: ListRoles :many
SELECT id, company_id, name, permissions, created_at
FROM roles
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
ORDER BY name
LIMIT $3 OFFSET $4;

-- name: CountRoles :one
SELECT count(*) FROM roles
WHERE company_id = $1 AND ($2::uuid IS NULL OR id = $2);

-- name: GetRole :one
SELECT id, company_id, name, permissions, created_at
FROM roles
WHERE id = $2 AND company_id = $1;

-- name: CreateRole :one
INSERT INTO roles (company_id, name, permissions)
VALUES ($1, $2, $3)
RETURNING id, company_id, name, permissions, created_at;

-- name: UpdateRole :one
UPDATE roles SET name = $3, permissions = $4
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, permissions, created_at;

-- name: DeleteRole :execrows
DELETE FROM roles
WHERE id = $2 AND company_id = $1;

-- name: GetRoleByName :one
-- Used by requirePermission (Q4, Phase 2 design/authz.go) to look up the
-- JWT's role by name within the caller's own tenant -- never by id, since
-- the JWT carries only a role name, not a roles.id.
SELECT id, company_id, name, permissions, created_at
FROM roles
WHERE company_id = $1 AND name = $2;
