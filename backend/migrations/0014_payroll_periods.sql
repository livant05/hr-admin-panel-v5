-- Payroll periods: one ledger row and one history row per period.
--
-- A month can be paid as one 'mensual' run or as two quincenas
-- ('quincenal1' = days 1-15, 'quincenal2' = day 16 to month end).
--
--  * employee_pay_records.period lets the run-origin natural key tell the two
--    halves apart. Manual and CSV rows keep the default 'mensual'.
--  * The run-scoped unique index is widened to include the period; the old
--    index (company, employee, year, month) is replaced.
--  * payroll_history gets a unique key (company, year, month, period) so a
--    re-run or a double submit cannot duplicate the aggregate row.
--
-- PRE-CHECK before applying on a database that already has payroll runs:
--   SELECT company_id, year, month, period, count(*) FROM payroll_history
--   GROUP BY 1,2,3,4 HAVING count(*) > 1;
-- Creating the unique index fails if this returns rows; delete the older
-- duplicates first (the newest row per key is the one the app would keep).
-- Rows written before this migration with period 'quincenal' (the retired
-- value) are tolerated: the period CHECK below is NOT VALID, so it only
-- constrains new and updated rows.
--
-- Idempotent (rule for every migration numbered 0010 or higher); existing
-- volumes do not run new migrations automatically: apply it with psql.

ALTER TABLE employee_pay_records
  ADD COLUMN IF NOT EXISTS period TEXT NOT NULL DEFAULT 'mensual';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'epr_period_chk') THEN
    ALTER TABLE employee_pay_records
      ADD CONSTRAINT epr_period_chk CHECK (period IN ('mensual','quincenal1','quincenal2'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payroll_history_period_chk') THEN
    ALTER TABLE payroll_history
      ADD CONSTRAINT payroll_history_period_chk
      CHECK (period IN ('mensual','quincenal1','quincenal2')) NOT VALID;
  END IF;
END $$;

DROP INDEX IF EXISTS uq_epr_run_period;
CREATE UNIQUE INDEX IF NOT EXISTS uq_epr_run_period_v2
  ON employee_pay_records (company_id, employee_id, period_year, period_month, period)
  WHERE origin = 'run';

CREATE UNIQUE INDEX IF NOT EXISTS uq_payroll_history_period
  ON payroll_history (company_id, year, month, period);
