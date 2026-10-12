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
-- hence no company); the caller must be gated by requireAdmin.
SELECT id, pin, serial_number, attempts, resolved, last_seen, created_at
FROM zkteco_unknown_pins
WHERE (sqlc.narg('resolved')::boolean IS NULL OR resolved = sqlc.narg('resolved'))
ORDER BY last_seen DESC NULLS LAST, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
