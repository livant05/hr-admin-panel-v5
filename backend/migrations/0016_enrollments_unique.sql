-- One enrollment per employee and course.
--
-- The panel already hides employees who are enrolled, but nothing stopped a
-- second identical row (a retried POST, two tabs). The unique index makes it a
-- 409. Rows with a NULL training_id (imported data whose course is gone)
-- never collide: NULLs are distinct.
--
-- PRE-CHECK before applying on a database that already has enrollments:
--   SELECT training_id, employee_id, count(*) FROM enrollments
--   WHERE training_id IS NOT NULL
--   GROUP BY 1,2 HAVING count(*) > 1;
-- Creating the index fails if this returns rows; keep one row per pair
-- (normally the graded one) and delete the others first.
--
-- Idempotent: safe to apply again.

CREATE UNIQUE INDEX IF NOT EXISTS uq_enrollments_training_employee
  ON enrollments (training_id, employee_id);
