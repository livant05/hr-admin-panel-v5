-- name: ListJobPostings :many
SELECT id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at
FROM job_postings
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetJobPosting :one
SELECT id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at
FROM job_postings
WHERE id = $2 AND company_id = $1;

-- name: CreateJobPosting :one
INSERT INTO job_postings (
  company_id, title, department, location, description, requirements,
  salary_min, salary_max, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at;

-- name: GetJobPostingForUpdate :one
-- DELETE's row lock. FOR UPDATE conflicts with the FOR KEY SHARE an application
-- insert takes on this row (FK check), so a delete waits for an in-flight insert
-- and then counts it instead of cascading it away.
SELECT id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at
FROM job_postings
WHERE id = $2 AND company_id = $1
FOR UPDATE;

-- name: GetJobPostingForMerge :one
-- PATCH's merge read. FOR NO KEY UPDATE still serializes concurrent PATCHes and a
-- PATCH against DELETE (both conflict with FOR UPDATE), but unlike FOR UPDATE it
-- does not block application inserts: the PATCH never changes the key.
SELECT id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at
FROM job_postings
WHERE id = $2 AND company_id = $1
FOR NO KEY UPDATE;

-- name: UpdateJobPosting :one
UPDATE job_postings SET
  title = $3, department = $4, location = $5, description = $6,
  requirements = $7, salary_min = $8, salary_max = $9, status = $10
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at;

-- name: CountApplicationsForJobPosting :one
-- Counts by posting only: any referencing row, whoever owns it, blocks the delete.
SELECT count(*) FROM applications WHERE job_posting_id = $1;

-- name: DeleteJobPosting :execrows
DELETE FROM job_postings
WHERE id = $2 AND company_id = $1;
