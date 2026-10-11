package importer

// Row is one decoded export row: column name to decoded JSON value (numbers
// are json.Number so no precision is lost).
type Row map[string]any

// tableSpec describes how one table is loaded.
type tableSpec struct {
	Name string
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
	{Name: "roles"},
	{Name: "users"},
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
	{Name: "attendance_logs"},
	{Name: "leave_balances"},
	{Name: "leave_requests"},
	{Name: "overtime_logs"},
	{Name: "deductions"},
	{Name: "employee_pay_records"},
	{Name: "liquidation_history"},
	{Name: "generated_documents"},
	{Name: "medical_records"},
	{Name: "evaluations"},
	{Name: "uniforms"},
	{Name: "enrollments"},
	{Name: "survey_responses"},
	{Name: "applications"},
}
