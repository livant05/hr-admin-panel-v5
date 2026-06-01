-- ============================================================
-- HR Admin Panel v5.0 — SQL Schema para Supabase (PostgreSQL)
-- Panamá · Código de Trabajo · Ley 51/2005
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 1. EMPRESAS (Multitenancy)
CREATE TABLE IF NOT EXISTS companies (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name       TEXT NOT NULL,
  ruc        TEXT,
  address    TEXT,
  phone      TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 2. DEPARTAMENTOS
CREATE TABLE IF NOT EXISTS departments (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_dept_co ON departments(company_id);

-- 3. CARGOS
CREATE TABLE IF NOT EXISTS positions (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pos_co ON positions(company_id);

-- 4. SUCURSALES
CREATE TABLE IF NOT EXISTS branches (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_branch_co ON branches(company_id);

-- 5. ROLES Y PERMISOS
CREATE TABLE IF NOT EXISTS roles (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  permissions JSONB NOT NULL DEFAULT '{}',
  created_at  TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_role_co ON roles(company_id);

-- 6. USUARIOS DE APLICACIÓN
-- El id coincide con auth.users cuando el usuario existe en Supabase Auth.
-- El email es el vínculo entre ambas tablas.
CREATE TABLE IF NOT EXISTS users (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  email      TEXT,
  role       TEXT NOT NULL DEFAULT 'empleado',
  first_name TEXT,
  last_name  TEXT,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_usr_co ON users(company_id);

-- 7. EMPLEADOS
-- Nota: age es calculado dinámicamente desde birth_date en la app.
CREATE TABLE IF NOT EXISTS employees (
  id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id          UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  first_name          TEXT NOT NULL,
  last_name           TEXT NOT NULL,
  cedula              TEXT,
  ss_number           TEXT,
  dv                  TEXT,
  birth_date          DATE,
  age                 INT,
  sex                 CHAR(1),
  marital_status      TEXT,
  blood_type          TEXT,
  nationality         TEXT,
  address             TEXT,
  phone               TEXT,
  email               TEXT,
  license_number      TEXT,
  license_type        TEXT,
  license_expiry      DATE,
  position            TEXT,
  department          TEXT,
  branch              TEXT,
  salary              NUMERIC(12,2) DEFAULT 0,
  weekly_hours        NUMERIC(5,1)  DEFAULT 44,
  monthly_hours       NUMERIC(7,1),
  hourly_rate         NUMERIC(10,4),
  contract_type       TEXT DEFAULT 'indefinido',
  start_date          DATE,
  status              TEXT DEFAULT 'active',
  contract_expiry     DATE,
  work_permit_expiry  DATE,
  emergency_contact   TEXT,
  emergency_phone     TEXT,
  emergency_relation  TEXT,
  created_at          TIMESTAMPTZ DEFAULT NOW(),
  updated_at          TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_emp_co ON employees(company_id);
CREATE INDEX IF NOT EXISTS idx_emp_st ON employees(status);
CREATE INDEX IF NOT EXISTS idx_emp_dp ON employees(department);

-- 8. ASISTENCIA
CREATE TABLE IF NOT EXISTS attendance_logs (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  department    TEXT,
  date          DATE NOT NULL,
  time_in       TIME,
  time_out      TIME,
  status        TEXT DEFAULT 'present',
  notes         TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_att_emp_date ON attendance_logs(employee_id, date);

-- 9. SALDOS VACACIONES
CREATE TABLE IF NOT EXISTS leave_balances (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id    UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name  TEXT,
  year           INT NOT NULL,
  earned_days    NUMERIC(6,1) DEFAULT 0,
  used_days      NUMERIC(6,1) DEFAULT 0,
  remaining_days NUMERIC(6,1) GENERATED ALWAYS AS (earned_days - used_days) STORED,
  UNIQUE(employee_id, year)
);

-- 10. SOLICITUDES VACACIONES / LICENCIAS
CREATE TABLE IF NOT EXISTS leave_requests (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  type          TEXT DEFAULT 'vacaciones',
  start_date    DATE NOT NULL,
  end_date      DATE NOT NULL,
  days          INT,
  status        TEXT DEFAULT 'pending',
  notes         TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 11. HORAS EXTRAS
CREATE TABLE IF NOT EXISTS overtime_logs (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  date          DATE NOT NULL,
  hours         NUMERIC(5,1) NOT NULL,
  type          TEXT DEFAULT 'regular',
  hourly_rate   NUMERIC(10,4),
  amount        NUMERIC(12,2),
  notes         TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 12. DEDUCCIONES
CREATE TABLE IF NOT EXISTS deductions (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  type          TEXT DEFAULT 'prestamo',
  description   TEXT,
  total_amount  NUMERIC(12,2) DEFAULT 0,
  quota         NUMERIC(12,2) NOT NULL,
  remaining     NUMERIC(12,2) DEFAULT 0,
  start_date    DATE,
  status        TEXT DEFAULT 'active',
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 13. HISTORIAL PLANILLAS
CREATE TABLE IF NOT EXISTS payroll_history (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  period         TEXT NOT NULL,
  month          INT NOT NULL,
  year           INT NOT NULL,
  month_name     TEXT,
  employee_count INT DEFAULT 0,
  total_bruto    NUMERIC(14,2) DEFAULT 0,
  total_isr      NUMERIC(14,2) DEFAULT 0,
  total_neto     NUMERIC(14,2) DEFAULT 0,
  total_empresa  NUMERIC(14,2) DEFAULT 0,
  created_at     TIMESTAMPTZ DEFAULT NOW()
);

-- 14. LIQUIDACIONES
CREATE TABLE IF NOT EXISTS liquidation_history (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID REFERENCES employees(id),
  employee_name TEXT,
  reason        TEXT,
  exit_date     DATE,
  total_amount  NUMERIC(14,2) DEFAULT 0,
  notes         TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 15. PLANTILLAS DE DOCUMENTOS
CREATE TABLE IF NOT EXISTS document_templates (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  type       TEXT DEFAULT 'contract',
  content    TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- 16. DOCUMENTOS GENERADOS
CREATE TABLE IF NOT EXISTS generated_documents (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID REFERENCES employees(id),
  employee_name TEXT,
  template_id   UUID REFERENCES document_templates(id),
  template_name TEXT,
  document_name TEXT,
  content       TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 17. INCAPACIDADES MÉDICAS
CREATE TABLE IF NOT EXISTS medical_records (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  type          TEXT,
  diagnosis     TEXT,
  start_date    DATE,
  end_date      DATE,
  days          INT,
  employer_days INT,
  css_days      INT,
  cert_number   TEXT,
  notes         TEXT,
  status        TEXT DEFAULT 'activa',
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 18. EVALUACIONES DE DESEMPEÑO
CREATE TABLE IF NOT EXISTS evaluations (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  period        TEXT,
  evaluator     TEXT,
  scores        JSONB,
  avg           NUMERIC(4,2),
  category      TEXT,
  comments      TEXT,
  status        TEXT DEFAULT 'completada',
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 19. UNIFORMES / EQUIPOS
CREATE TABLE IF NOT EXISTS uniforms (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  item          TEXT NOT NULL,
  category      TEXT,
  size          TEXT,
  quantity      INT DEFAULT 1,
  date          DATE,
  value         NUMERIC(10,2),
  notes         TEXT,
  status        TEXT DEFAULT 'activo',
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 20. CAPACITACIONES
CREATE TABLE IF NOT EXISTS trainings (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  category    TEXT,
  hours       INT,
  start_date  DATE,
  end_date    DATE,
  mode        TEXT,
  instructor  TEXT,
  description TEXT,
  status      TEXT DEFAULT 'programado',
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 21. INSCRIPCIONES A CAPACITACIONES
CREATE TABLE IF NOT EXISTS enrollments (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  training_id   UUID REFERENCES trainings(id) ON DELETE CASCADE,
  training_name TEXT,
  employee_id   UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
  employee_name TEXT,
  status        TEXT DEFAULT 'programado',
  score         NUMERIC(5,2),
  notes         TEXT,
  created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- 22. ENCUESTAS
CREATE TABLE IF NOT EXISTS surveys (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  description TEXT,
  audience    TEXT DEFAULT 'todos',
  close_date  DATE,
  status      TEXT DEFAULT 'activa',
  questions   JSONB,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 23. RESPUESTAS ENCUESTAS
CREATE TABLE IF NOT EXISTS survey_responses (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  survey_id  UUID REFERENCES surveys(id) ON DELETE CASCADE,
  answers    JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 24. COMUNICADOS
CREATE TABLE IF NOT EXISTS announcements (
  id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  type       TEXT DEFAULT 'general',
  audience   TEXT DEFAULT 'todos',
  content    TEXT,
  priority   TEXT DEFAULT 'normal',
  expires    DATE,
  views      INT DEFAULT 0,
  status     TEXT DEFAULT 'activo',
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 25. VACANTES
CREATE TABLE IF NOT EXISTS job_postings (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  department   TEXT,
  location     TEXT,
  description  TEXT,
  requirements TEXT,
  salary_min   NUMERIC(10,2),
  salary_max   NUMERIC(10,2),
  status       TEXT DEFAULT 'open',
  created_at   TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_job_co ON job_postings(company_id);

-- 26. CANDIDATOS
CREATE TABLE IF NOT EXISTS candidates (
  id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  full_name    TEXT NOT NULL,
  email        TEXT,
  phone        TEXT,
  nationality  TEXT DEFAULT 'Panameña',
  id_number    TEXT,
  resume_notes TEXT,
  created_at   TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cand_co ON candidates(company_id);

-- 27. POSTULACIONES
CREATE TABLE IF NOT EXISTS applications (
  id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  job_posting_id UUID REFERENCES job_postings(id) ON DELETE CASCADE,
  job_title      TEXT,
  candidate_id   UUID REFERENCES candidates(id) ON DELETE CASCADE,
  candidate_name TEXT,
  stage          TEXT DEFAULT 'applied',
  notes          TEXT,
  applied_at     DATE DEFAULT CURRENT_DATE,
  created_at     TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_app_co ON applications(company_id);

-- ══════════════════════════════════════════
-- ROW LEVEL SECURITY (Multiempresa)
-- ══════════════════════════════════════════
CREATE OR REPLACE FUNCTION get_company_id() RETURNS UUID AS $$
  SELECT (auth.jwt() -> 'user_metadata' ->> 'company_id')::UUID;
$$ LANGUAGE SQL STABLE;

-- companies: cada usuario solo accede a su empresa
ALTER TABLE companies ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_iso ON companies FOR ALL USING (id = get_company_id());

-- resto de tablas: aislamiento por company_id
DO $$ DECLARE t TEXT;
BEGIN FOR t IN SELECT unnest(ARRAY[
  'branches','roles','users',
  'departments','positions','employees','attendance_logs',
  'leave_balances','leave_requests','overtime_logs','deductions',
  'payroll_history','liquidation_history','document_templates',
  'generated_documents','medical_records','evaluations','uniforms',
  'trainings','enrollments','surveys','survey_responses','announcements',
  'job_postings','candidates','applications'
]) LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
  EXECUTE format('CREATE POLICY tenant_iso ON %I FOR ALL USING (company_id = get_company_id())', t);
END LOOP; END $$;

-- ══════════════════════════════════════════
-- FUNCIÓN: Acumular vacaciones (Art. 54 C. Laboral Panamá)
-- 1 día por cada 11 días trabajados
-- ══════════════════════════════════════════
CREATE OR REPLACE FUNCTION accrue_vacation_days() RETURNS void AS $$
DECLARE emp RECORD; days_worked INT; earned NUMERIC;
BEGIN
  FOR emp IN SELECT * FROM employees WHERE status = 'active' LOOP
    SELECT COUNT(*) INTO days_worked FROM attendance_logs
    WHERE employee_id = emp.id AND status IN ('present','late')
      AND EXTRACT(YEAR FROM date) = EXTRACT(YEAR FROM NOW());
    earned := FLOOR(days_worked / 11.0);
    INSERT INTO leave_balances(company_id,employee_id,employee_name,year,earned_days,used_days)
    VALUES(emp.company_id, emp.id, emp.first_name||' '||emp.last_name,
      EXTRACT(YEAR FROM NOW())::INT, earned, 0)
    ON CONFLICT (employee_id, year) DO UPDATE SET earned_days = EXCLUDED.earned_days;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Programar con pg_cron (opcional):
-- SELECT cron.schedule('accrue-vacations','0 2 * * 0','SELECT accrue_vacation_days()');

-- ══════════════════════════════════════════
-- TRIGGER: updated_at automático
-- ══════════════════════════════════════════
CREATE OR REPLACE FUNCTION update_timestamp() RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_emp_upd BEFORE UPDATE ON employees
  FOR EACH ROW EXECUTE FUNCTION update_timestamp();
CREATE TRIGGER trg_tpl_upd BEFORE UPDATE ON document_templates
  FOR EACH ROW EXECUTE FUNCTION update_timestamp();

-- FIN DEL SCHEMA
-- Total tablas: 27 | RLS: Habilitado en todas | Panamá
