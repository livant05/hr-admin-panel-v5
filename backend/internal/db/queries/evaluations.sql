-- name: ListEvaluations :many
SELECT id, company_id, employee_id, employee_name, period, evaluator, scores,
  avg, category, comments, status, created_at
FROM evaluations
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'))
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: CountEvaluations :one
SELECT count(*) FROM evaluations
WHERE company_id = $1
  AND ($2::uuid IS NULL OR id = $2)
  AND (sqlc.narg('employee_id')::uuid IS NULL OR employee_id = sqlc.narg('employee_id'));

-- name: GetEvaluation :one
SELECT id, company_id, employee_id, employee_name, period, evaluator, scores,
  avg, category, comments, status, created_at
FROM evaluations
WHERE id = $2 AND company_id = $1;

-- name: CreateEvaluation :one
-- A1 rule 6: the tenant check on the client-supplied employee_id IS the
-- insert -- a foreign employee_id selects zero rows -> pgx.ErrNoRows -> 404.
-- employee_name is DERIVED from the employees row. scores/avg/category are
-- computed in Go by internal/evaluation (design D1); client avg/category are
-- never read. There is deliberately no Update query (no PATCH route).
INSERT INTO evaluations (
  company_id, employee_id, employee_name, period, evaluator, scores, avg, category, comments, status
)
SELECT $1, e.id, e.first_name || ' ' || e.last_name,
  $3, $4, $5, $6, $7, $8, $9
FROM employees e
WHERE e.id = $2 AND e.company_id = $1
RETURNING id, company_id, employee_id, employee_name, period, evaluator, scores,
  avg, category, comments, status, created_at;

-- name: DeleteEvaluation :execrows
-- Hard delete (Phase 2 Q3 precedent): evaluations is not an FK target.
DELETE FROM evaluations
WHERE id = $2 AND company_id = $1;
