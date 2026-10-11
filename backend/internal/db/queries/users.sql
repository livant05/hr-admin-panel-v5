-- name: GetUserForLogin :one
SELECT u.id, u.company_id, u.email, u.role, u.first_name, u.last_name, u.status,
       u.password_hash, c.name AS company_name
FROM users u
JOIN companies c ON c.id = u.company_id
WHERE lower(u.email) = lower(sqlc.arg(email)::text)
LIMIT 1;

-- name: GetUserByID :one
SELECT id, company_id, email, role, first_name, last_name, status, password_hash, created_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: GetCompanyByID :one
SELECT id, name, ruc, address, phone, created_at
FROM companies
WHERE id = $1
LIMIT 1;

-- The users API selects explicit columns and never password_hash, so the
-- generated row types cannot carry the hash (XC-2 holds structurally).

-- name: ListUsers :many
SELECT id, company_id, email, role, first_name, last_name, status, created_at
FROM users
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('role')::text IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at, id
LIMIT $3 OFFSET $4;

-- name: GetUser :one
SELECT id, company_id, email, role, first_name, last_name, status, created_at
FROM users
WHERE id = $2 AND company_id = $1;

-- name: CreateUser :one
INSERT INTO users (company_id, email, role, first_name, last_name, status, password_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, company_id, email, role, first_name, last_name, status, created_at;
