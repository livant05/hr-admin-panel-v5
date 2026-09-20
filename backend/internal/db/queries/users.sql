-- name: GetUserByEmail :one
SELECT id, company_id, email, role, first_name, last_name, status, password_hash, created_at
FROM users
WHERE email = $1
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
