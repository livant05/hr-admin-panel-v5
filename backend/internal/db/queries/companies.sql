-- name: GetCompanyByID :one
SELECT id, name, ruc, address, phone, created_at
FROM companies
WHERE id = $1
LIMIT 1;
