-- ZKTeco ADMS ingestion (PROVISIONAL wire protocol; the tenancy and
-- idempotency guarantees below are binding). These queries are deliberately
-- separate from UpsertAttendanceLog, which overwrites time_out.

-- name: UpsertDeviceHeartbeat :one
-- Self-registers an unknown serial into the unclaimed pool (company_id NULL)
-- and refreshes a known one. company_id is NEVER part of the SET list, so a
-- heartbeat can not change ownership.
INSERT INTO zkteco_devices (serial_number, status, last_seen)
VALUES ($1, 'online', now())
ON CONFLICT (serial_number) DO UPDATE
SET status = 'online', last_seen = now()
RETURNING id, company_id;

-- name: ResolveEmployeesByCedula :many
-- Company-scoped PIN resolution. LIMIT 2 is enough to tell "exactly one" from
-- "ambiguous" without scanning every duplicate.
SELECT id FROM employees
WHERE company_id = sqlc.arg('company_id') AND cedula = sqlc.arg('cedula')::text
ORDER BY id
LIMIT 2;

-- name: CedulaExistsElsewhere :one
-- Distinguishes tenant_mismatch from not_found after a zero-row resolve. The
-- answer only picks the rejection reason; it never discloses a row.
SELECT EXISTS (
  SELECT 1 FROM employees
  WHERE cedula = sqlc.arg('cedula')::text AND company_id <> sqlc.arg('company_id')
) AS exists_elsewhere;

-- name: UpsertUnknownPin :exec
-- One row per (serial_number, pin) since migration 0012, so the same PIN on
-- two devices is two rows and a tenant never shares a counter with another.
-- attempts increments on every conflict (the Edge Function wrote a literal 1).
INSERT INTO zkteco_unknown_pins (pin, serial_number, attempts, last_seen)
VALUES (sqlc.arg('pin'), sqlc.arg('serial_number'), 1, now())
ON CONFLICT (serial_number, pin) DO UPDATE
SET attempts = zkteco_unknown_pins.attempts + 1,
    last_seen = now();

-- name: IngestCheckIn :execrows
-- The employee must belong to the company ($1) or nothing is written. The
-- earliest check-in of the day wins; time_out, work_type and notes are never
-- touched, so re-pushing a batch is idempotent.
INSERT INTO attendance_logs (company_id, employee_id, employee_name, department, date, time_in, status)
SELECT sqlc.arg('company_id'), e.id, e.first_name || ' ' || e.last_name, e.department,
  sqlc.arg('date')::date, sqlc.arg('time_in')::time, 'present'
FROM employees e
WHERE e.id = sqlc.arg('employee_id') AND e.company_id = sqlc.arg('company_id')
ON CONFLICT (employee_id, date) DO UPDATE
SET time_in = LEAST(attendance_logs.time_in, EXCLUDED.time_in),
    status = CASE WHEN attendance_logs.status = 'absent' THEN 'present' ELSE attendance_logs.status END;

-- name: IngestCheckOut :execrows
-- Sets time_out only on a row that has a check-in (time_in IS NOT NULL): an
-- absent or manually created row without time_in is not a punched day. Zero
-- rows affected means there was no check-in (the caller counts no_checkin).
-- Latest check-out wins.
UPDATE attendance_logs
SET time_out = GREATEST(time_out, sqlc.arg('time_out')::time)
WHERE company_id = sqlc.arg('company_id')
  AND employee_id = sqlc.arg('employee_id')
  AND date = sqlc.arg('date')::date
  AND time_in IS NOT NULL;

-- name: AddIngestRejections :exec
-- One call per (serial, reason) per batch. A NULL last_pin keeps the previous
-- value.
INSERT INTO zkteco_ingest_rejections (serial_number, reason, count, last_pin, last_seen)
VALUES (sqlc.arg('serial_number'), sqlc.arg('reason'), sqlc.arg('count'), sqlc.narg('last_pin'), now())
ON CONFLICT (serial_number, reason) DO UPDATE
SET count = zkteco_ingest_rejections.count + EXCLUDED.count,
    last_pin = COALESCE(EXCLUDED.last_pin, zkteco_ingest_rejections.last_pin),
    last_seen = now();

-- name: DeleteIngestRejectionsBySerial :exec
-- Run in the claim transaction: counters collected while a device was
-- unclaimed (or owned by someone else) must not pass to its new owner.
DELETE FROM zkteco_ingest_rejections WHERE serial_number = $1;

-- name: DeleteUnknownPinsBySerial :exec
-- Same as above for the unknown PINs punched on the device before the claim.
DELETE FROM zkteco_unknown_pins WHERE serial_number = $1;
