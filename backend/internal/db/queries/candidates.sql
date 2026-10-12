-- name: ListCandidates :many
SELECT id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at
FROM candidates
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetCandidate :one
SELECT id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at
FROM candidates
WHERE id = $2 AND company_id = $1;

-- name: CreateCandidate :one
INSERT INTO candidates (
  company_id, full_name, email, phone, nationality, id_number, resume_notes
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at;
