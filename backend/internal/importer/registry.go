package importer

import "context"

// Row is one decoded export row: column name to decoded JSON value (numbers
// are json.Number so no precision is lost).
type Row map[string]any

// tableSpec describes how one table is loaded.
type tableSpec struct {
	Name string
	// Transform validates and rewrites a row before it is inserted, mirroring
	// the server-side invariants of the matching API handler. It returns an
	// *invalidError to reject the row. It runs inside the row's savepoint.
	Transform func(ctx context.Context, q querier, row Row, st *runState) error
	// Unique lists column sets that must be unique within the export; the
	// database enforces them too, this gives a clearer reason and does not
	// depend on insertion order against rows already present.
	Unique [][]string
	// After runs once the table's rows are loaded (inside the transaction).
	After func(ctx context.Context, q querier, tr TableReport) error
}

// tables is the load order, by FK tier: companies first, then every table
// that only depends on companies, then employees, then everything that
// depends on employees (or on a tier-1 table such as trainings/surveys).
var tables = []tableSpec{
	// tier 0
	{Name: "companies"},
	// tier 1
	{Name: "departments"},
	{Name: "positions"},
	{Name: "branches"},
	{Name: "roles", After: seedMedicalPermission},
	{Name: "users", Transform: transformUser},
	{Name: "document_templates"},
	{Name: "trainings"},
	{Name: "surveys"},
	{Name: "announcements"},
	{Name: "job_postings"},
	{Name: "candidates"},
	{Name: "payroll_history"},
	{Name: "zkteco_devices"},
	{Name: "zkteco_unknown_pins"},
	// tier 2
	{Name: "employees"},
	// tier 3
	{Name: "attendance_logs", Unique: [][]string{{"employee_id", "date"}}},
	{Name: "leave_balances", Unique: [][]string{{"employee_id", "year"}}},
	{Name: "leave_requests"},
	{Name: "overtime_logs", Transform: transformOvertimeLog},
	{Name: "deductions"},
	{Name: "employee_pay_records"},
	{Name: "liquidation_history"},
	{Name: "generated_documents"},
	{Name: "medical_records", Transform: transformMedicalRecord},
	{Name: "evaluations", Transform: transformEvaluation},
	{Name: "uniforms"},
	{Name: "enrollments"},
	{Name: "survey_responses"},
	{Name: "applications", Unique: [][]string{{"job_posting_id", "candidate_id"}}},
}
