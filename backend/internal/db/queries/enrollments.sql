-- name: ListEnrollments :many
SELECT id, company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes, created_at
FROM enrollments
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
  AND (sqlc.narg('training_id')::uuid IS NULL OR training_id = sqlc.narg('training_id'))
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetEnrollment :one
SELECT id, company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes, created_at
FROM enrollments
WHERE id = $2 AND company_id = $1;

-- name: CreateEnrollment :one
-- The tenant check of both foreign keys IS the insert: the row is built from
-- the course and the employee of the caller's company, so another company's id
-- (or a nonexistent one) yields zero rows (pgx.ErrNoRows -> 404) and nothing
-- is written. training_name and employee_name are derived here, never taken
-- from input. FOR KEY SHARE takes the foreign key locks up front, so a course
-- deleted meanwhile gives 404 instead of a 23503.
INSERT INTO enrollments (
  company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes
)
SELECT sqlc.arg('company_id')::uuid, t.id, t.name, e.id,
  trim(concat_ws(' ', e.first_name, e.last_name)),
  sqlc.arg('status')::text, sqlc.narg('score')::numeric, sqlc.narg('notes')::text
FROM trainings t, employees e
WHERE t.id = sqlc.arg('training_id')::uuid AND t.company_id = sqlc.arg('company_id')::uuid
  AND e.id = sqlc.arg('employee_id')::uuid AND e.company_id = sqlc.arg('company_id')::uuid
FOR KEY SHARE OF t, e
RETURNING id, company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes, created_at;

-- name: GetEnrollmentForMerge :one
-- PATCH's merge read; FOR NO KEY UPDATE serializes concurrent grades.
SELECT id, company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes, created_at
FROM enrollments
WHERE id = $2 AND company_id = $1
FOR NO KEY UPDATE;

-- name: UpdateEnrollment :one
-- Grading only: status, score and notes. The course and the employee are fixed
-- at creation (delete and enroll again to change them).
UPDATE enrollments SET status = $3, score = $4, notes = $5
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, training_id, training_name, employee_id, employee_name,
  status, score, notes, created_at;

-- name: DeleteEnrollment :execrows
DELETE FROM enrollments
WHERE id = $2 AND company_id = $1;
