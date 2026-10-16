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

-- name: GetRoleForUpdate :one
-- Locks the role row for a rename or delete. User writes that assign this
-- role take LockRoleForShare first, so they wait for this transaction and
-- never leave a user on a name renamed or deleted meanwhile.
SELECT id, company_id, name, permissions, created_at
FROM roles
WHERE id = $2 AND company_id = $1
FOR UPDATE;

-- name: RenameUsersRole :execrows
-- users.role holds the role NAME (permissions are looked up by it), so a
-- role rename carries the company's users along in the same transaction.
UPDATE users SET role = sqlc.arg('new_name')
WHERE company_id = $1 AND role = sqlc.arg('old_name');

-- name: CountUsersWithRole :one
SELECT count(*) FROM users WHERE company_id = $1 AND role = sqlc.arg('name');

-- name: LockRoleForShare :one
-- A user write that assigns a custom role takes this shared lock first, before
-- any user row: it then waits for a rename or delete of the role in flight
-- (GetRoleForUpdate) and re-reads it, so a user never ends up on a name that
-- was renamed or deleted meanwhile. Same order everywhere (role, then users),
-- so the two never deadlock.
SELECT id FROM roles
WHERE company_id = $1 AND name = $2
FOR SHARE;
