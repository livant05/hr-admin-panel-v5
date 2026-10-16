-- Case-insensitive unique user emails (AUTH-3).
--
-- RULE: every migration numbered 0010 or higher MUST be idempotent. The test
-- harness (internal/testutil) re-applies them on every run against an
-- already-initialized database.
--
-- Idempotent. Fails loudly, naming the offending addresses, if existing rows
-- collide once lowercased, so the unique index is never silently skipped.
-- Stored emails are not rewritten: lookups use lower(email).
DO $$
DECLARE dups TEXT;
BEGIN
  SELECT string_agg(e, ', ' ORDER BY e) INTO dups
  FROM (SELECT lower(email) AS e FROM users WHERE email IS NOT NULL
        GROUP BY lower(email) HAVING count(*) > 1) d;
  IF dups IS NOT NULL THEN
    RAISE EXCEPTION 'migration 0010: duplicate user emails (case-insensitive): %', dups;
  END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (lower(email));
