-- name: ListApplications :many
SELECT id, company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at, created_at
FROM applications
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
  AND (sqlc.narg('job_posting_id')::uuid IS NULL OR job_posting_id = sqlc.narg('job_posting_id'))
  AND (sqlc.narg('candidate_id')::uuid IS NULL OR candidate_id = sqlc.narg('candidate_id'))
  AND (sqlc.narg('stage')::text IS NULL OR stage = sqlc.narg('stage'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetApplication :one
SELECT id, company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at, created_at
FROM applications
WHERE id = $2 AND company_id = $1;

-- name: CreateApplication :one
-- The tenant check of both foreign keys IS the insert: the row is built from the
-- posting and the candidate of the caller's company, so another company's id (or
-- a nonexistent one) yields zero rows (pgx.ErrNoRows -> 404) and nothing is
-- written. job_title and candidate_name are derived here, never taken from input.
-- FOR KEY SHARE OF jp, c takes the locks the foreign key check would take later,
-- up front: a guarded delete of either row (FOR UPDATE) then waits for this insert
-- and counts it, or finishes first and leaves zero rows here instead of a 23503.
INSERT INTO applications (
  company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at
)
SELECT sqlc.arg('company_id')::uuid, jp.id, jp.title, c.id, c.full_name,
  sqlc.arg('stage')::text, sqlc.narg('notes')::text,
  COALESCE(sqlc.narg('applied_at')::date, CURRENT_DATE)
FROM job_postings jp, candidates c
WHERE jp.id = sqlc.arg('job_posting_id')::uuid AND jp.company_id = sqlc.arg('company_id')::uuid
  AND c.id = sqlc.arg('candidate_id')::uuid AND c.company_id = sqlc.arg('company_id')::uuid
FOR KEY SHARE OF jp, c
RETURNING id, company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at, created_at;

-- name: GetApplicationForMerge :one
-- PATCH's merge read. FOR NO KEY UPDATE serializes concurrent PATCHes and a PATCH
-- against DELETE, and keeps a concurrent committed write from being overwritten
-- with a stale copy.
SELECT id, company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at, created_at
FROM applications
WHERE id = $2 AND company_id = $1
FOR NO KEY UPDATE;

-- name: LockJobPostingForShare :one
-- The tenant check of a re-point to a new posting, with the lock the foreign key
-- check would take later: while the PATCH is in flight a guarded delete of the
-- posting (FOR UPDATE) waits and then counts the re-pointed application; a posting
-- deleted first leaves no row here (404) instead of a 23503.
SELECT id, title FROM job_postings
WHERE id = $2 AND company_id = $1
FOR KEY SHARE;

-- name: LockCandidateForShare :one
-- Same as LockJobPostingForShare, for a re-point to a new candidate.
SELECT id, full_name FROM candidates
WHERE id = $2 AND company_id = $1
FOR KEY SHARE;

-- name: UpdateApplication :one
UPDATE applications SET
  job_posting_id = $3, job_title = $4, candidate_id = $5, candidate_name = $6,
  stage = $7, notes = $8, applied_at = $9
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, job_posting_id, job_title, candidate_id, candidate_name,
  stage, notes, applied_at, created_at;

-- name: DeleteApplication :execrows
DELETE FROM applications
WHERE id = $2 AND company_id = $1;
