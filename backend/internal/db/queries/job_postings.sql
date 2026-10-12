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
