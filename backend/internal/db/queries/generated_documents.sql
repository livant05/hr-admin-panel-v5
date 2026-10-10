-- name: ListGeneratedDocuments :many
SELECT id, company_id, employee_id, employee_name, template_id, template_name,
  document_name, content, created_at
FROM generated_documents
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('template_id')::uuid IS NULL OR template_id = sqlc.narg('template_id'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountGeneratedDocuments :one
SELECT count(*) FROM generated_documents
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
  AND (sqlc.narg('template_id')::uuid IS NULL OR template_id = sqlc.narg('template_id'));

-- name: GetGeneratedDocument :one
SELECT id, company_id, employee_id, employee_name, template_id, template_name,
  document_name, content, created_at
FROM generated_documents
WHERE id = $2 AND company_id = $1;

-- name: CreateGeneratedDocument :one
-- A1 rule 6 for TWO independent nullable FKs (design H5). The FKs check
-- existence, not tenancy, so each SUPPLIED id must match a row in the
-- caller's tenant or the statement selects zero rows -> pgx.ErrNoRows -> 404.
-- employee_name/template_name are DERIVED when the id matched and stored AS
-- SENT only when that id is null. content is opaque: the client already ran
-- applyVars; the server performs no {{VAR}} substitution.
INSERT INTO generated_documents (
  company_id, employee_id, employee_name, template_id, template_name, document_name, content
)
SELECT $1,
       e.id,
       CASE WHEN e.id IS NULL THEN sqlc.narg('employee_name')::text
            ELSE e.first_name || ' ' || e.last_name END,
       t.id,
       CASE WHEN t.id IS NULL THEN sqlc.narg('template_name')::text ELSE t.name END,
       sqlc.narg('document_name')::text,
       sqlc.narg('content')::text
FROM (SELECT 1) AS one
LEFT JOIN employees e
       ON e.id = sqlc.narg('employee_id')::uuid AND e.company_id = $1
LEFT JOIN document_templates t
       ON t.id = sqlc.narg('template_id')::uuid AND t.company_id = $1
WHERE (sqlc.narg('employee_id')::uuid IS NULL OR e.id IS NOT NULL)
  AND (sqlc.narg('template_id')::uuid IS NULL OR t.id IS NOT NULL)
RETURNING id, company_id, employee_id, employee_name, template_id, template_name,
  document_name, content, created_at;
