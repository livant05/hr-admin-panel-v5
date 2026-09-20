-- ============================================================
-- RRHH-Go Table Migration — Phase 0 (Schema Only)
-- Adds employee_pay_records, zkteco_devices, zkteco_unknown_pins.
-- Column shapes verified directly against production Supabase
-- via information_schema.columns (see sdd/rrhh-go-table-migration
-- spec for the full verification record). Purely additive: no
-- existing table is altered or dropped.
--
-- Deliberate deviations from the live Supabase defaults, kept
-- consistent with this repo's existing migration convention
-- (0001_init.sql): uuid_generate_v4() via the uuid-ossp extension
-- instead of gen_random_uuid(), and explicit NUMERIC(10,2)
-- precision instead of bare NUMERIC.
--
-- CORRECTIVE FOLLOW-UP (sdd-verify CRITICAL-1 / design revision 3,
-- section B5): employee_pay_records.company_id is NOT NULL here.
-- This table is created empty in the Go-owned Postgres 16 (no
-- backfill risk), every other tenant table in 0001_init.sql already
-- enforces NOT NULL, and every employee_pay_records row is created
-- under an authenticated context (design A1 rule 4/5, B5.2) — there
-- is no "unassigned" pay record. ON DELETE CASCADE remains
-- compatible with NOT NULL.
--
-- employee_pay_records.cedula is REQUIRED here (design B5.1). The
-- live Supabase schema now includes cedula (added via ALTER TABLE
-- during this change and verified with a live insert), it is read
-- back by exportPayRecordsCSV (hr_admin_panel.html:3190), and
-- CSV-imported rows with employee_id IS NULL are identified only by
-- employee_name + cedula. The insert-failure symptom this column
-- was once thought to mask was already fixed independently by
-- commit 1b62f23 (surfaced result-shape errors in both call sites).
--
-- zkteco_devices.company_id is nullable and carried forward from
-- live production. It exists but is currently unpopulated by any
-- code path; schema lands now, enforcement (tenant-scoped reads,
-- claim endpoint) is Phase 6 work — see design B3b.
-- ============================================================

-- EMPLOYEE PAY RECORDS
CREATE TABLE IF NOT EXISTS employee_pay_records (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id       UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id      UUID REFERENCES employees(id) ON DELETE SET NULL,
  employee_name    TEXT,
  cedula           TEXT,
  numero_planilla  TEXT,
  centro_costo     TEXT,
  periodo          TEXT,
  period_year      INT NOT NULL,
  period_month     INT NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  gross_salary     NUMERIC(10,2) DEFAULT 0,
  overtime_amount  NUMERIC(10,2) DEFAULT 0,
  commissions      NUMERIC(10,2) DEFAULT 0,
  bonuses          NUMERIC(10,2) DEFAULT 0,
  vacations_paid   NUMERIC(10,2) DEFAULT 0,
  other_income     NUMERIC(10,2) DEFAULT 0,
  total_earned     NUMERIC(10,2) DEFAULT 0,
  css_employee     NUMERIC(10,2) DEFAULT 0,
  se_employee      NUMERIC(10,2) DEFAULT 0,
  isr              NUMERIC(10,2) DEFAULT 0,
  other_deductions NUMERIC(10,2) DEFAULT 0,
  net_salary       NUMERIC(10,2) DEFAULT 0,
  notes            TEXT,
  created_at       TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_epr_co  ON employee_pay_records(company_id);
CREATE INDEX IF NOT EXISTS idx_epr_emp ON employee_pay_records(employee_id, period_year DESC, period_month DESC);

-- ZKTECO DEVICES
CREATE TABLE IF NOT EXISTS zkteco_devices (
  id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id       UUID REFERENCES companies(id) ON DELETE SET NULL,
  serial_number    TEXT NOT NULL UNIQUE,
  name             TEXT,
  location         TEXT,
  status           TEXT DEFAULT 'offline',
  last_seen        TIMESTAMPTZ,
  firmware_version TEXT,
  created_at       TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_zkd_co        ON zkteco_devices(company_id);
CREATE INDEX IF NOT EXISTS idx_zkd_last_seen ON zkteco_devices(last_seen DESC);

-- Guarded single-company backfill: only assigns pre-existing
-- unassigned devices when exactly one company exists. No-op on a
-- fresh database (no rows yet) and safe to re-run.
UPDATE zkteco_devices
SET company_id = (SELECT id FROM companies)
WHERE company_id IS NULL
  AND (SELECT count(*) FROM companies) = 1;

-- ZKTECO UNKNOWN PINS
-- No company_id: an unresolved PIN has no matched employee, hence
-- no company to infer (confirmed absent from live schema too).
CREATE TABLE IF NOT EXISTS zkteco_unknown_pins (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  pin           TEXT NOT NULL UNIQUE,
  serial_number TEXT,
  attempts      INT NOT NULL DEFAULT 1,
  resolved      BOOLEAN NOT NULL DEFAULT FALSE,
  last_seen     TIMESTAMPTZ,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);
