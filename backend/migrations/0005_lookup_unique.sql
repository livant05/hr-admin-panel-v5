-- ============================================================
-- Phase 1 (employee-core) — lookup-table name uniqueness.
--
-- departments/positions/branches/roles hold no id-based reference from
-- employees; the relationship is a denormalized TEXT name match
-- (employees.department/.position/.branch, users.role — see
-- sdd/rrhh-go-table-migration/phase1-design P6.1). A per-tenant unique
-- index on lower(name) is the minimum addition needed to make the
-- handler-level RESTRICT-on-delete and propagate-on-rename behavior
-- unambiguous: without it, two same-named rows in one company would
-- make "how many employees reference this name" and "which row is
-- being renamed" both ill-defined.
--
-- Safe to add now: the Go-owned Postgres holds no lookup rows yet
-- (0003_seed.sql inserts only a company and a user).
-- ============================================================

CREATE UNIQUE INDEX IF NOT EXISTS idx_dept_co_name_lower
  ON departments (company_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS idx_pos_co_name_lower
  ON positions (company_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS idx_branch_co_name_lower
  ON branches (company_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS idx_role_co_name_lower
  ON roles (company_id, lower(name));
