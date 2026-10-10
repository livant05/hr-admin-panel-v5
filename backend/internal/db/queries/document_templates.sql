-- name: ListDocumentTemplates :many
SELECT id, company_id, name, type, content, created_at, updated_at
FROM document_templates
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
ORDER BY name
LIMIT $3 OFFSET $4;

-- name: CountDocumentTemplates :one
SELECT count(*) FROM document_templates
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'));

-- name: GetDocumentTemplate :one
SELECT id, company_id, name, type, content, created_at, updated_at
FROM document_templates
WHERE id = $2 AND company_id = $1;

-- name: CreateDocumentTemplate :one
INSERT INTO document_templates (company_id, name, type, content)
VALUES ($1, $2, COALESCE(sqlc.narg('type')::text, 'contract'), $3)
RETURNING id, company_id, name, type, content, created_at, updated_at;

-- updated_at is maintained by trigger trg_tpl_upd (0001_init.sql); do not set it here.
-- name: UpdateDocumentTemplate :one
UPDATE document_templates SET name = $3, type = $4, content = $5
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, type, content, created_at, updated_at;

-- name: DeleteDocumentTemplate :execrows
DELETE FROM document_templates
WHERE id = $2 AND company_id = $1;
