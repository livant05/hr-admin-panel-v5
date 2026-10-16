-- name: ListTrainings :many
SELECT id, company_id, name, category, hours, start_date, end_date, mode,
  instructor, description, status, created_at
FROM trainings
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: GetTraining :one
SELECT id, company_id, name, category, hours, start_date, end_date, mode,
  instructor, description, status, created_at
FROM trainings
WHERE id = $2 AND company_id = $1;

-- name: CreateTraining :one
INSERT INTO trainings (
  company_id, name, category, hours, start_date, end_date, mode, instructor,
  description, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, company_id, name, category, hours, start_date, end_date, mode,
  instructor, description, status, created_at;

-- name: GetTrainingForMerge :one
-- PATCH's merge read. FOR NO KEY UPDATE serializes concurrent PATCHes (and a
-- PATCH against DELETE) without blocking an enrollment insert, which only takes
-- FOR KEY SHARE on the course.
SELECT id, company_id, name, category, hours, start_date, end_date, mode,
  instructor, description, status, created_at
FROM trainings
WHERE id = $2 AND company_id = $1
FOR NO KEY UPDATE;

-- name: UpdateTraining :one
UPDATE trainings SET
  name = $3, category = $4, hours = $5, start_date = $6, end_date = $7,
  mode = $8, instructor = $9, description = $10, status = $11
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, name, category, hours, start_date, end_date, mode,
  instructor, description, status, created_at;

-- name: DeleteTraining :execrows
-- The course's enrollments go with it (ON DELETE CASCADE): the panel removes
-- them together, and an enrollment means nothing without its course.
DELETE FROM trainings
WHERE id = $2 AND company_id = $1;
