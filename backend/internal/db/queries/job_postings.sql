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
-- Row lock: serializes PATCH merges, and DELETE against concurrent application
-- inserts (their FK check takes FOR KEY SHARE on this row, which this lock blocks).
SELECT id, company_id, title, department, location, description, requirements,
  salary_min, salary_max, status, created_at
FROM job_postings
WHERE id = $2 AND company_id = $1
FOR UPDATE;

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
