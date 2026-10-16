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

-- name: GetCandidateForUpdate :one
-- DELETE's row lock. FOR UPDATE conflicts with the FOR KEY SHARE an application
-- insert takes on this row (FK check), so a delete waits for an in-flight insert
-- and then counts it instead of cascading it away.
SELECT id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at
FROM candidates
WHERE id = $2 AND company_id = $1
FOR UPDATE;

-- name: GetCandidateForMerge :one
-- PATCH's merge read. FOR NO KEY UPDATE still serializes concurrent PATCHes and a
-- PATCH against DELETE (both conflict with FOR UPDATE), but unlike FOR UPDATE it
-- does not block application inserts: the PATCH never changes the key.
SELECT id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at
FROM candidates
WHERE id = $2 AND company_id = $1
FOR NO KEY UPDATE;

-- name: UpdateCandidate :one
UPDATE candidates SET
  full_name = $3, email = $4, phone = $5, nationality = $6,
  id_number = $7, resume_notes = $8
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, full_name, email, phone, nationality, id_number,
  resume_notes, created_at;

-- name: CountApplicationsForCandidate :one
-- Counts by candidate only: any referencing row, whoever owns it, blocks the delete.
SELECT count(*) FROM applications WHERE candidate_id = $1;

-- name: DeleteCandidate :execrows
DELETE FROM candidates
WHERE id = $2 AND company_id = $1;
