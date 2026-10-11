-- ============================================================
-- RRHH-Go Table Migration -- medical permission
-- Gates medical_records behind a dedicated `medical` role permission.
-- Until now the Incapacidades page was gated by `attendance` in the UI
-- only. To avoid removing access from anyone who has it today, every
-- existing role that lacks the key gets an explicit `medical` value that
-- mirrors its attendance access, using the frontend's semantics
-- (applyPermissions): attendance is on when it is JSON true, a non-zero
-- number, a non-empty string, or an object with at least one sub-key whose
-- value is true / non-zero / non-empty. Anything else (false, absent,
-- empty or all-false object) yields medical = false.
--
-- Rows whose permissions is not a JSON object (JSON null, array or scalar;
-- the column is NOT NULL so SQL NULL cannot occur) carry no attendance
-- access and the backend already fails closed on them; they are replaced
-- by {"medical": false} so the row is a valid permissions object.
-- Rows that already have a `medical` key are never touched.
-- Idempotent: after the first run no row matches the WHERE clauses.
-- ============================================================

UPDATE roles
SET permissions = jsonb_build_object('medical', false)
WHERE jsonb_typeof(permissions) IS DISTINCT FROM 'object';

UPDATE roles
SET permissions = permissions || jsonb_build_object('medical', (
  CASE jsonb_typeof(permissions->'attendance')
    WHEN 'boolean' THEN (permissions->'attendance') = 'true'::jsonb
    WHEN 'number'  THEN (permissions->'attendance') <> '0'::jsonb
    WHEN 'string'  THEN (permissions->>'attendance') <> ''
    WHEN 'object'  THEN EXISTS (
      SELECT 1 FROM jsonb_each(permissions->'attendance') AS sub(k, v)
      WHERE CASE jsonb_typeof(sub.v)
        WHEN 'boolean' THEN sub.v = 'true'::jsonb
        WHEN 'number'  THEN sub.v <> '0'::jsonb
        WHEN 'string'  THEN (sub.v #>> '{}') <> ''
        ELSE false
      END
    )
    ELSE false
  END
))
WHERE jsonb_typeof(permissions) = 'object'
  AND NOT (permissions ? 'medical');
