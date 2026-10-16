-- One application per candidate and posting.
--
-- POST /api/applications has no idempotency key: a client or proxy that
-- retries after a timeout (the INSERT already committed) used to write a
-- second identical row. The unique index makes the retry a 409 instead.
-- Rows with a NULL job_posting_id or candidate_id (imported data whose
-- posting or candidate is gone) never collide: NULLs are distinct.
--
-- PRE-CHECK before applying on a database that already has applications:
--   SELECT job_posting_id, candidate_id, count(*) FROM applications
--   WHERE job_posting_id IS NOT NULL AND candidate_id IS NOT NULL
--   GROUP BY 1,2 HAVING count(*) > 1;
-- Creating the index fails if this returns rows; keep one row per pair
-- (normally the most advanced stage) and delete the others first.
--
-- Idempotent: safe to apply again.

CREATE UNIQUE INDEX IF NOT EXISTS uq_applications_posting_candidate
  ON applications (job_posting_id, candidate_id);
