-- Public register (design D14): one company plus its first admin, created in a
-- single transaction by the handler. Neither query returns password_hash, so
-- the generated row types cannot carry it (XC-2).

-- name: CreateCompany :one
INSERT INTO companies (name)
VALUES ($1)
RETURNING id, name;

-- name: CreateAdminUser :one
INSERT INTO users (company_id, email, role, first_name, last_name, status, password_hash)
VALUES ($1, $2, 'admin', $3, $4, 'active', $5)
RETURNING id, company_id, email, role, first_name, last_name;
