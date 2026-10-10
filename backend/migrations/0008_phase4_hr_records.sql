-- ============================================================
-- RRHH-Go Table Migration -- Phase 4 (hr-records)
-- (a) medical_records: persist the server-computed incapacity cost
--     together with the salary it was computed from, so the figure is
--     reproducible (salary_basis snapshot).
-- (b) A dates CHECK backstopping the handler's end >= start validation.
-- (c) The company_id indexes none of the five Phase 4 tables has, plus
--     the generated_documents.template_id index the NO ACTION FK check
--     needs on every template DELETE.
-- Purely additive: no column narrowed, no existing row rewritten, no
-- backfill (pre-existing rows keep NULL = "not computed by the server").
-- Idempotent: safe to apply more than once.
--
-- Rollback: DROP INDEX x6 (idx_tpl_co, idx_gdoc_co, idx_gdoc_tpl,
-- idx_mr_co, idx_eval_co, idx_unif_co); ALTER TABLE medical_records
-- DROP CONSTRAINT mr_dates_chk; DROP COLUMN salary_basis, DROP COLUMN cost.
-- ============================================================

ALTER TABLE medical_records
  ADD COLUMN IF NOT EXISTS salary_basis NUMERIC(12,2),  -- employees.salary at compute time
  ADD COLUMN IF NOT EXISTS cost         NUMERIC(12,2);  -- employer + CSS-subsidised cost

-- ADD CONSTRAINT has no IF NOT EXISTS; guard it so the file stays idempotent.
-- NULL dates pass a CHECK, so this cannot fail on any pre-existing row shape.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mr_dates_chk') THEN
    ALTER TABLE medical_records
      ADD CONSTRAINT mr_dates_chk CHECK (end_date >= start_date);
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_tpl_co   ON document_templates(company_id);
CREATE INDEX IF NOT EXISTS idx_gdoc_co  ON generated_documents(company_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_gdoc_tpl ON generated_documents(template_id);
CREATE INDEX IF NOT EXISTS idx_mr_co    ON medical_records(company_id, start_date DESC);
CREATE INDEX IF NOT EXISTS idx_eval_co  ON evaluations(company_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_unif_co  ON uniforms(company_id, date DESC);
