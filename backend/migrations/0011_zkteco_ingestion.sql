-- ============================================================
-- RRHH-Go migration 0011 -- ZKTeco ADMS ingestion support
-- Adds the rejection counters ADMS ingestion writes (ING-9) and the
-- employees(company_id, cedula) lookup index used by tenant-scoped PIN
-- resolution (ING-4).
--
-- zkteco_ingest_rejections deliberately has NO company_id column: an
-- unenforced tenant column is prohibited (parent design A1 rule 4), and a
-- rejected punch may come from a device that no company owns. Operators join
-- through serial_number -> zkteco_devices. One row per (device serial,
-- reason); `count` accumulates events across batches.
--
-- Idempotent: every statement is guarded, so it is safe to re-run on an
-- existing volume and the test harness re-applies it on every run.
-- ============================================================

CREATE TABLE IF NOT EXISTS zkteco_ingest_rejections (
  serial_number TEXT        NOT NULL,
  reason        TEXT        NOT NULL CHECK (reason IN (
                  'device_unclaimed', 'not_found', 'tenant_mismatch',
                  'ambiguous', 'no_checkin', 'malformed', 'unsupported_code')),
  count         BIGINT      NOT NULL DEFAULT 0,
  last_pin      TEXT,
  last_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (serial_number, reason)
);

CREATE INDEX IF NOT EXISTS idx_emp_co_cedula ON employees(company_id, cedula);
