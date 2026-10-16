-- name: ListZktecoDevices :many
-- Rows with company_id NULL (the unclaimed pool) never match company_id = $1.
SELECT id, company_id, serial_number, name, location, status, last_seen,
  firmware_version, created_at
FROM zkteco_devices
WHERE company_id = $1
  AND (sqlc.narg('id')::uuid IS NULL OR id = sqlc.narg('id'))
ORDER BY last_seen DESC NULLS LAST, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: UpdateZktecoDevice :one
-- Only name and location are writable. A column is overwritten only when its
-- set_* flag is true, so an absent JSON key keeps the stored value and an
-- explicit null clears it.
UPDATE zkteco_devices SET
  name     = CASE WHEN sqlc.arg('set_name')::boolean THEN sqlc.narg('name')::text ELSE name END,
  location = CASE WHEN sqlc.arg('set_location')::boolean THEN sqlc.narg('location')::text ELSE location END
WHERE id = $2 AND company_id = $1
RETURNING id, company_id, serial_number, name, location, status, last_seen,
  firmware_version, created_at;

-- name: ListZktecoUnknownPins :many
-- zkteco_unknown_pins has no company_id (an unresolved PIN has no employee,
-- hence no company). Tenancy comes from the device that punched the PIN: only
-- rows whose serial_number belongs to a device of the caller's company are
-- returned, so pins of other tenants' devices and of unclaimed devices (or
-- with a NULL serial) never match. The caller must also be gated by
-- requireAdmin. zkteco_devices.serial_number is UNIQUE, so the join never
-- duplicates a pin.
SELECT p.id, p.pin, p.serial_number, p.attempts, p.resolved, p.last_seen, p.created_at
FROM zkteco_unknown_pins p
JOIN zkteco_devices d ON d.serial_number = p.serial_number
WHERE d.company_id = $1
  AND (sqlc.narg('resolved')::boolean IS NULL OR p.resolved = sqlc.narg('resolved'))
ORDER BY p.last_seen DESC NULLS LAST, p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListUnclaimedZktecoDevices :many
-- The unclaimed pool: devices with no company (loaded by the importer, or
-- self-registered before devices were pre-registered). The
-- caller must be gated by requireAdmin; the rows carry the serial number an
-- admin needs to recognize the physical device before claiming it.
SELECT id, company_id, serial_number, name, location, status, last_seen,
  firmware_version, created_at
FROM zkteco_devices
WHERE company_id IS NULL
ORDER BY last_seen DESC NULLS LAST, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: RegisterZktecoDevice :one
-- An admin registers a device for their company before pointing it at the
-- server; ADMS only accepts serials that have a row. serial_number is UNIQUE:
-- a serial already registered (by any company) or imported is a 23505.
INSERT INTO zkteco_devices (company_id, serial_number, name, location)
VALUES ($1, $2, $3, $4)
RETURNING id, company_id, serial_number, name, location, status, last_seen,
  firmware_version, created_at;

-- name: ClaimZktecoDevice :one
-- Atomic claim: the ownership check and the write are ONE statement. Two
-- concurrent claims serialize on the row lock; the loser re-evaluates
-- company_id IS NULL against the winner's committed row and gets no row back
-- (pgx.ErrNoRows -> 404, the same answer as a missing or foreign device).
UPDATE zkteco_devices SET company_id = $1
WHERE id = $2 AND company_id IS NULL
RETURNING id, company_id, serial_number, name, location, status, last_seen,
  firmware_version, created_at;
