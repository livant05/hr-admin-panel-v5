-- name: ListUniforms :many
SELECT id, company_id, employee_id, employee_name, item, category, size,
  quantity, date, value, notes, status, created_at
FROM uniforms
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY date DESC NULLS LAST, created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: CountUniforms :one
SELECT count(*) FROM uniforms
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'));

-- name: GetUniform :one
SELECT id, company_id, employee_id, employee_name, item, category, size,
  quantity, date, value, notes, status, created_at
FROM uniforms
WHERE id = $2 AND company_id = $1;

-- name: CreateUniform :one
-- A1 rule 6: the tenant check on the client-supplied employee_id IS the
-- insert -- a foreign employee_id selects zero rows -> pgx.ErrNoRows -> 404.
-- employee_name is DERIVED from the employees row. date defaults to the
-- current date when the client sends none (legacy saveUniform behavior).
INSERT INTO uniforms (
  company_id, employee_id, employee_name, item, category, size,
  quantity, date, value, notes, status
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name,
  sqlc.arg('item')::text, sqlc.narg('category')::text, sqlc.narg('size')::text,
  sqlc.arg('quantity')::int, COALESCE(sqlc.narg('date')::date, CURRENT_DATE),
  sqlc.arg('value')::numeric, sqlc.narg('notes')::text, sqlc.arg('status')::text
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, item, category, size,
  quantity, date, value, notes, status, created_at;

-- name: UpdateUniform :one
-- Partial PATCH (design H1): absent/null keeps the stored value, so
-- returnUniform's {status:'devuelto'} touches only status. Every parameter
-- is explicitly cast (obs #849). employee_id/company_id/created_at immutable.
UPDATE uniforms SET
  item     = COALESCE(sqlc.narg('item')::text,     item),
  category = COALESCE(sqlc.narg('category')::text, category),
  size     = COALESCE(sqlc.narg('size')::text,     size),
  quantity = COALESCE(sqlc.narg('quantity')::int,  quantity),
  date     = COALESCE(sqlc.narg('date')::date,     date),
  value    = COALESCE(sqlc.narg('value')::numeric, value),
  notes    = COALESCE(sqlc.narg('notes')::text,    notes),
  status   = COALESCE(sqlc.narg('status')::text,   status)
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, employee_id, employee_name, item, category, size,
  quantity, date, value, notes, status, created_at;

-- name: DeleteUniform :execrows
-- Hard delete (Phase 2 Q3 precedent): uniforms is not an FK target.
DELETE FROM uniforms
WHERE id = $2 AND company_id = $1;
