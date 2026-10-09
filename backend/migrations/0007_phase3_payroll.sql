-- ============================================================
-- RRHH-Go Table Migration -- Phase 3
-- Adds: (a) employee_pay_records.origin + the run-scoped natural
-- key that settled decision #1's upsert needs (design R4a), and
-- (b) liquidation_history's breakdown/inputs audit payload for
-- settled decision #3 (design R6). Purely additive: no existing
-- column is altered or narrowed, no existing row is rewritten.
-- ============================================================

-- -- employee_pay_records: run-origin provenance --------------
-- NOT NULL DEFAULT on a constant: Postgres 11+ does not rewrite
-- the table, so this is safe regardless of row count.
ALTER TABLE employee_pay_records
  ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual';

-- ADD CONSTRAINT has no IF NOT EXISTS; guard it so the migration
-- stays idempotent (the harness applies every file twice).
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'epr_origin_chk') THEN
    ALTER TABLE employee_pay_records
      ADD CONSTRAINT epr_origin_chk CHECK (origin IN ('manual','run'));
  END IF;
END $$;

-- PARTIAL unique index: scopes the natural key to run-origin rows
-- ONLY, so Part A B2's "no natural key" stays true for manual entry
-- and CSV re-import. The predicate is exactly `origin = 'run'` --
-- deliberately NOT also `employee_id IS NOT NULL`, because run rows
-- always carry one (the INSERT...SELECT FROM employees guarantees
-- it) and the shorter predicate is what ON CONFLICT must restate
-- verbatim for arbiter inference to succeed.
CREATE UNIQUE INDEX IF NOT EXISTS uq_epr_run_period
  ON employee_pay_records (company_id, employee_id, period_year, period_month)
  WHERE origin = 'run';

-- -- liquidation_history: the decision-#3 audit payload (R6) ---
ALTER TABLE liquidation_history
  ADD COLUMN IF NOT EXISTS start_date   DATE,
  ADD COLUMN IF NOT EXISTS breakdown    JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS inputs       JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS calc_version TEXT;

CREATE INDEX IF NOT EXISTS idx_liq_co_exit ON liquidation_history(company_id, exit_date DESC);
CREATE INDEX IF NOT EXISTS idx_liq_emp     ON liquidation_history(employee_id);
