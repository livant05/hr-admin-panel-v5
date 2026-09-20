-- RRHH-Go owns credentials now — Supabase's GoTrue (auth.users) is not migrated.
ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
