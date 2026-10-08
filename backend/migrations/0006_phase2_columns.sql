-- Phase 2 schema catch-up (design Q1, sdd/rrhh-go-table-migration/phase2-design).
-- attendance_logs and deductions are stale relative to what hr_admin_panel.html
-- already reads/writes -- the same drift pattern Part A B2/B5 already found and
-- fixed once for employee_pay_records.cedula.
--
-- Additive only: ADD COLUMN IF NOT EXISTS plus one DROP NOT NULL that only
-- widens what the column already accepts. Rollback is DROP COLUMN; the
-- DROP NOT NULL is not worth reverting and cannot break a reader.

-- attendance_logs.work_type: the PAYDAY_CODES code (hr_admin_panel.html
-- 1818-1853), written by saveAttendance (2772), read by wtLabel (2756) and
-- the absence counter (2951, l.work_type===28). Absent from both schema.sql
-- and 0001_init.sql.
ALTER TABLE attendance_logs ADD COLUMN IF NOT EXISTS work_type INT;

-- deductions: nine columns saveDed (3419-3434) and importDedCSV
-- (3511-3523) already write and renderDedTable (3440-3453) /
-- exportDedCSV (3460-3464) already read back.
ALTER TABLE deductions
  ADD COLUMN IF NOT EXISTS cedula          TEXT,
  ADD COLUMN IF NOT EXISTS acreedor_nombre TEXT,
  ADD COLUMN IF NOT EXISTS acreedor_codigo TEXT,
  ADD COLUMN IF NOT EXISTS tipo_pago       TEXT DEFAULT 'Cheque',
  ADD COLUMN IF NOT EXISTS centro_costo    TEXT,
  ADD COLUMN IF NOT EXISTS prioridad       INT DEFAULT 4,
  ADD COLUMN IF NOT EXISTS end_date        DATE,
  ADD COLUMN IF NOT EXISTS periodo         TEXT,
  ADD COLUMN IF NOT EXISTS numero_planilla TEXT;

-- deductions.employee_id must accept NULL: importDedCSV sends
-- employee_id: null (3512) for a name/cedula that matches no employee.
-- CSV import of an unknown employee fails today without this.
ALTER TABLE deductions ALTER COLUMN employee_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_ded_emp     ON deductions(employee_id);
CREATE INDEX IF NOT EXISTS idx_att_co_date ON attendance_logs(company_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_ot_co_date  ON overtime_logs(company_id, date DESC);
