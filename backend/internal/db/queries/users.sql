-- name: GetUserForLogin :one
SELECT u.id, u.company_id, u.email, u.role, u.first_name, u.last_name, u.status,
       u.password_hash, u.token_version, c.name AS company_name
FROM users u
JOIN companies c ON c.id = u.company_id
WHERE lower(u.email) = lower(sqlc.arg(email)::text)
LIMIT 1;

-- name: GetUserByID :one
SELECT id, company_id, email, role, first_name, last_name, status, password_hash, created_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: GetSessionUser :one
-- Per-request session check (auth middleware): primary-key lookup of only the
-- columns that decide whether a token is still honoured.
SELECT status, role, company_id, token_version
FROM users
WHERE id = $1;

-- The users API selects explicit columns and never password_hash, so the
-- generated row types cannot carry the hash (XC-2 holds structurally).

-- name: ListUsers :many
SELECT id, company_id, email, role, first_name, last_name, status, created_at
FROM users
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
  AND (sqlc.narg('role')::text IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at, id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetUser :one
SELECT id, company_id, email, role, first_name, last_name, status, created_at
FROM users
WHERE id = $2 AND company_id = $1;

-- name: CreateUser :one
INSERT INTO users (company_id, email, role, first_name, last_name, status, password_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, company_id, email, role, first_name, last_name, status, created_at;

-- The write path below locks rows so the last-active-admin guard holds under
-- concurrency (design D6). Callers MUST run LockActiveAdmins first and
-- GetUserForUpdate second, inside one transaction: every transaction then takes
-- the admin rows in id order before any other user row, so two requests can
-- never wait on each other.

-- name: LockActiveAdmins :many
SELECT id
FROM users
WHERE company_id = $1 AND role = 'admin' AND status = 'active'
ORDER BY id
FOR UPDATE;

-- name: GetUserForUpdate :one
SELECT id, company_id, email, role, first_name, last_name, status, created_at
FROM users
WHERE id = $2 AND company_id = $1
FOR UPDATE;

-- name: UpdateUser :one
UPDATE users
SET email = $3, role = $4, first_name = $5, last_name = $6, status = $7
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, email, role, first_name, last_name, status, created_at;

-- name: DeactivateUser :one
UPDATE users
SET status = 'inactive'
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, email, role, first_name, last_name, status, created_at;

-- name: BumpUserTokenVersion :exec
-- Invalidates every token issued to the user (logout, role or status change).
UPDATE users
SET token_version = token_version + 1
WHERE id = $1;
