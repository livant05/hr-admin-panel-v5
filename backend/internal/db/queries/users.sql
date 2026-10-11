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
