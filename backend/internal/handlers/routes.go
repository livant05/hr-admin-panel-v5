package handlers

import (
	"net/http"

	"github.com/livant05/rrhh-go/internal/auth"
)

// Routes builds the full API mux. Every tenant-scoped route lives on one
// `protected` sub-mux mounted once behind signer.Middleware: omitting a
// route from `protected` yields a 404 (fail-closed), instead of the
// fail-open risk of forgetting to wrap an individual route in middleware.
func (a *API) Routes(signer *auth.Signer) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/me", a.Me)

	protected.HandleFunc("GET /api/departments", a.ListDepartments)
	protected.HandleFunc("POST /api/departments", a.CreateDepartment)
	protected.HandleFunc("GET /api/departments/{id}", a.GetDepartment)
	protected.HandleFunc("PATCH /api/departments/{id}", a.UpdateDepartment)
	protected.HandleFunc("DELETE /api/departments/{id}", a.DeleteDepartment)

	protected.HandleFunc("GET /api/positions", a.ListPositions)
	protected.HandleFunc("POST /api/positions", a.CreatePosition)
	protected.HandleFunc("GET /api/positions/{id}", a.GetPosition)
	protected.HandleFunc("PATCH /api/positions/{id}", a.UpdatePosition)
	protected.HandleFunc("DELETE /api/positions/{id}", a.DeletePosition)

	protected.HandleFunc("GET /api/branches", a.ListBranches)
	protected.HandleFunc("POST /api/branches", a.CreateBranch)
	protected.HandleFunc("GET /api/branches/{id}", a.GetBranch)
	protected.HandleFunc("PATCH /api/branches/{id}", a.UpdateBranch)
	protected.HandleFunc("DELETE /api/branches/{id}", a.DeleteBranch)

	protected.HandleFunc("GET /api/roles", a.ListRoles)
	protected.HandleFunc("POST /api/roles", a.CreateRole)
	protected.HandleFunc("GET /api/roles/{id}", a.GetRole)
	protected.HandleFunc("PATCH /api/roles/{id}", a.UpdateRole)
	protected.HandleFunc("DELETE /api/roles/{id}", a.DeleteRole)

	protected.HandleFunc("GET /api/employees", a.ListEmployees)
	protected.HandleFunc("POST /api/employees", a.CreateEmployee)
	protected.HandleFunc("GET /api/employees/{id}", a.GetEmployee)
	protected.HandleFunc("PATCH /api/employees/{id}", a.UpdateEmployee)
	protected.HandleFunc("DELETE /api/employees/{id}", a.DeleteEmployee) // soft delete (P6.2)

	protected.HandleFunc("GET /api/attendance_logs", a.ListAttendanceLogs)
	protected.HandleFunc("POST /api/attendance_logs", a.CreateAttendanceLog) // upsert on (employee_id,date)
	protected.HandleFunc("GET /api/attendance_logs/{id}", a.GetAttendanceLog)
	protected.HandleFunc("PATCH /api/attendance_logs/{id}", a.UpdateAttendanceLog)
	protected.HandleFunc("DELETE /api/attendance_logs/{id}", a.DeleteAttendanceLog) // hard delete (Q3)

	// leave_balances: no generic POST, no DELETE (design Q3) — rows are owned
	// by the accrual scheduler. The one exception is the admin-gated manual
	// accrual trigger below; Go 1.22 ServeMux ranks the literal
	// "/accrue" path above the "/{id}" pattern, so there is no registration
	// conflict (design Q5c).
	protected.HandleFunc("GET /api/leave_balances", a.ListLeaveBalances)
	protected.HandleFunc("GET /api/leave_balances/{id}", a.GetLeaveBalance)
	protected.HandleFunc("PATCH /api/leave_balances/{id}", a.UpdateLeaveBalance)   // {used_days} ONLY
	protected.HandleFunc("POST /api/leave_balances/accrue", a.AccrueLeaveBalances) // requirePermission("vacations")

	protected.HandleFunc("GET /api/leave_requests", a.ListLeaveRequests)
	protected.HandleFunc("POST /api/leave_requests", a.CreateLeaveRequest)
	protected.HandleFunc("GET /api/leave_requests/{id}", a.GetLeaveRequest)
	protected.HandleFunc("PATCH /api/leave_requests/{id}", a.UpdateLeaveRequest)  // approve/reject, requirePermission("vacations")
	protected.HandleFunc("DELETE /api/leave_requests/{id}", a.DeleteLeaveRequest) // hard delete while pending only, 409 once decided (design Q3 addendum)

	protected.HandleFunc("GET /api/overtime_logs", a.ListOvertimeLogs)
	protected.HandleFunc("POST /api/overtime_logs", a.CreateOvertimeLog) // amount server-recomputed (Q6)
	protected.HandleFunc("GET /api/overtime_logs/{id}", a.GetOvertimeLog)
	protected.HandleFunc("PATCH /api/overtime_logs/{id}", a.UpdateOvertimeLog)
	protected.HandleFunc("DELETE /api/overtime_logs/{id}", a.DeleteOvertimeLog) // hard delete (Q3)

	protected.HandleFunc("GET /api/deductions", a.ListDeductions)
	protected.HandleFunc("POST /api/deductions", a.CreateDeduction) // employee_id MAY be null (CSV import)
	protected.HandleFunc("GET /api/deductions/{id}", a.GetDeduction)
	protected.HandleFunc("PATCH /api/deductions/{id}", a.UpdateDeduction)
	protected.HandleFunc("DELETE /api/deductions/{id}", a.DeleteDeduction) // soft delete: status='cancelled' (Q3)

	protected.HandleFunc("GET /api/employee_pay_records", a.ListEmployeePayRecords)
	protected.HandleFunc("POST /api/employee_pay_records", a.CreateEmployeePayRecord)  // employee_id MAY be null (CSV import)
	protected.HandleFunc("GET /api/employee_pay_records/bases", a.GetEmployeePayBases) // literal > /{id} (Q5c)
	protected.HandleFunc("GET /api/employee_pay_records/{id}", a.GetEmployeePayRecord)
	protected.HandleFunc("PATCH /api/employee_pay_records/{id}", a.UpdateEmployeePayRecord)
	protected.HandleFunc("DELETE /api/employee_pay_records/{id}", a.DeleteEmployeePayRecord) // hard delete

	// payroll_history: the committing run (slice 3f). No PATCH -- immutable
	// historical record (design R7). No ledger cascade on delete (design
	// R5). Literal "/calculate" ranks above "/{id}" under Go 1.22 ServeMux
	// (Q5c).
	protected.HandleFunc("GET /api/payroll_history", a.ListPayrollHistory)
	protected.HandleFunc("GET /api/payroll_history/calculate", a.CalculatePayroll) // preview, writes nothing
	protected.HandleFunc("POST /api/payroll_history", a.CreatePayrollRun)          // tx: 1 aggregate + N ledger rows
	protected.HandleFunc("GET /api/payroll_history/{id}", a.GetPayrollHistory)
	protected.HandleFunc("DELETE /api/payroll_history/{id}", a.DeletePayrollHistory) // hard; no ledger cascade

	// liquidation_history: no PATCH -- immutable historical record (design
	// R7). No cascade on delete -- no FK to employee_pay_records exists at
	// all. Literal "/calculate" ranks above "/{id}" under Go 1.22 ServeMux
	// (Q5c).
	protected.HandleFunc("GET /api/liquidation_history", a.ListLiquidationHistory)
	protected.HandleFunc("GET /api/liquidation_history/calculate", a.CalculateLiquidation) // preview, writes nothing
	protected.HandleFunc("POST /api/liquidation_history", a.CreateLiquidation)             // recomputes server-side
	protected.HandleFunc("GET /api/liquidation_history/{id}", a.GetLiquidationHistory)
	protected.HandleFunc("DELETE /api/liquidation_history/{id}", a.DeleteLiquidationHistory) // hard

	// document_templates: opaque CRUD, full-replace PATCH. DELETE answers 409
	// while generated_documents still reference the template (slice 4b).
	protected.HandleFunc("GET /api/document_templates", a.ListDocumentTemplates)
	protected.HandleFunc("POST /api/document_templates", a.CreateDocumentTemplate)
	protected.HandleFunc("GET /api/document_templates/{id}", a.GetDocumentTemplate)
	protected.HandleFunc("PATCH /api/document_templates/{id}", a.UpdateDocumentTemplate)
	protected.HandleFunc("DELETE /api/document_templates/{id}", a.DeleteDocumentTemplate) // hard; 409 when referenced

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health)     // public — more specific than /api/
	mux.HandleFunc("POST /api/auth/login", a.Login) // public — more specific than /api/
	mux.Handle("/api/", signer.Middleware(protected))
	return mux
}
