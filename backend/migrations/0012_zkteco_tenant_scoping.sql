-- ============================================================
-- RRHH-Go migration 0012 -- ZKTeco tenant scoping and cedula lookup index
-- (S5a review carry-overs CO-S5a-8 and CO-S5a-3)
--
-- zkteco_unknown_pins used to be keyed by UNIQUE(pin) alone. Two companies
-- whose devices both see the same unresolved PIN (cedula) then collided on one
-- global row, and the row carried whichever serial wrote last. The key becomes
-- (serial_number, pin): one row per device and PIN, so a PIN is attributed to
-- the tenant that owns the device (serial_number -> zkteco_devices.company_id)
-- and never shared across tenants (CO-S5a-8).
--
-- The table still has NO company_id column: an unenforced tenant column is
-- prohibited (parent design A1 rule 4). Rows with a NULL serial_number (legacy
-- imports) match no device and are therefore visible to no tenant.
--
-- Idempotent: every statement is guarded, so it is safe to re-run on an
-- existing volume and the test harness re-applies it on every run. Existing
-- volumes do not run new migrations automatically: apply it with psql.
-- ============================================================

-- 0004 declared `pin TEXT NOT NULL UNIQUE`, whose constraint is auto-named
-- <table>_<column>_key.
ALTER TABLE zkteco_unknown_pins DROP CONSTRAINT IF EXISTS zkteco_unknown_pins_pin_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_zkteco_unknown_pins_serial_pin
  ON zkteco_unknown_pins (serial_number, pin);

-- CedulaExistsElsewhere asks "does this cedula exist in any other company",
-- filtering on cedula alone; idx_emp_co_cedula (company_id leading) can not
-- serve that and the query would scan employees once per unresolved punch,
-- up to 10,000 per batch (CO-S5a-3).
CREATE INDEX IF NOT EXISTS idx_emp_cedula ON employees(cedula);
