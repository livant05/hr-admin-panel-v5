package handlers

import (
	"net/http"

	"github.com/livant05/rrhh-go/internal/auth"
)

// gateKind classifies how a protected route is authorized. Every route in
// protectedRoutes MUST carry one: routes_test.go pins the full classification
// table, so a route added without a deliberate decision fails the build.
type gateKind int

const (
	// gateUnset is the zero value; a spec carrying it is a classification bug.
	gateUnset gateKind = iota
	// gateOpen: any authenticated user (the JWT middleware is the only gate).
	// Handlers behind it may still shape the response (employees redaction).
	gateOpen
	// gateHandler: authorization is enforced inside the handler
	// (requireAdmin, or requirePermission for medical records).
	gateHandler
	// gatePermission: the route wrapper requires one of gate.keys via
	// requirePermission (admin bypass, fail-closed).
	gatePermission
)

// routeGate is a route's authorization classification.
type routeGate struct {
	kind gateKind
	keys []string // gatePermission only; any-of
}

var (
	open       = routeGate{kind: gateOpen}
	viaHandler = routeGate{kind: gateHandler}
)

// need gates a route on one module permission.
func need(key string) routeGate { return routeGate{kind: gatePermission, keys: []string{key}} }

// anyOf gates a route on at least one of the given module permissions.
func anyOf(keys ...string) routeGate { return routeGate{kind: gatePermission, keys: keys} }

// routeSpec is one protected route: its ServeMux pattern, its authorization
// classification and its handler.
type routeSpec struct {
	pattern string
	gate    routeGate
	handler http.HandlerFunc
}

// perm wraps h so it runs only when the caller holds at least one of keys.
// The tenant is resolved first (401 on a bad token), then the check, so a
// denied caller learns nothing from validation errors.
func (a *API) perm(keys []string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		companyID, ok := a.tenant(w, r)
		if !ok {
			return
		}
		for _, key := range keys {
			if a.permissionAllows(r, companyID, key) {
				h(w, r)
				return
			}
		}
		writeErrCode(w, http.StatusForbidden, codeForbidden, "insufficient permissions")
	}
}

// protectedRoutes is the single registry of tenant-scoped routes. Routes
// registers exactly this list, so nothing is reachable without a
// classification.
func (a *API) protectedRoutes() []routeSpec {
	return []routeSpec{
		{"GET /api/me", open, a.Me},

		{"GET /api/departments", open, a.ListDepartments},
		{"POST /api/departments", need("settings"), a.CreateDepartment},
		{"GET /api/departments/{id}", open, a.GetDepartment},
		{"PATCH /api/departments/{id}", need("settings"), a.UpdateDepartment},
		{"DELETE /api/departments/{id}", need("settings"), a.DeleteDepartment},

		{"GET /api/positions", open, a.ListPositions},
		{"POST /api/positions", need("settings"), a.CreatePosition},
		{"GET /api/positions/{id}", open, a.GetPosition},
		{"PATCH /api/positions/{id}", need("settings"), a.UpdatePosition},
		{"DELETE /api/positions/{id}", need("settings"), a.DeletePosition},

		{"GET /api/branches", open, a.ListBranches},
		{"POST /api/branches", need("settings"), a.CreateBranch},
		{"GET /api/branches/{id}", open, a.GetBranch},
		{"PATCH /api/branches/{id}", need("settings"), a.UpdateBranch},
		{"DELETE /api/branches/{id}", need("settings"), a.DeleteBranch},

		{"GET /api/roles", open, a.ListRoles},
		{"POST /api/roles", viaHandler, a.CreateRole},
		{"GET /api/roles/{id}", open, a.GetRole},
		{"PATCH /api/roles/{id}", viaHandler, a.UpdateRole},
		{"DELETE /api/roles/{id}", viaHandler, a.DeleteRole},

		// users: admin-only (requireAdmin).
		{"GET /api/users", viaHandler, a.ListUsers},
		{"POST /api/users", viaHandler, a.CreateUser}, // 201 array with one-time temporary_password
		{"GET /api/users/{id}", viaHandler, a.GetUser},
		{"PATCH /api/users/{id}", viaHandler, a.UpdateUser},  // last-active-admin guard (409)
		{"DELETE /api/users/{id}", viaHandler, a.DeleteUser}, // soft delete, idempotent 204

		// job_postings: recruitment.
		{"GET /api/job_postings", need("recruitment"), a.ListJobPostings},
		{"POST /api/job_postings", need("recruitment"), a.CreateJobPosting},
		{"GET /api/job_postings/{id}", need("recruitment"), a.GetJobPosting},
		{"PATCH /api/job_postings/{id}", need("recruitment"), a.UpdateJobPosting},
		{"DELETE /api/job_postings/{id}", need("recruitment"), a.DeleteJobPosting}, // 409 while applications exist

		// candidates: recruitment.
		{"GET /api/candidates", need("recruitment"), a.ListCandidates},
		{"POST /api/candidates", need("recruitment"), a.CreateCandidate},
		{"GET /api/candidates/{id}", need("recruitment"), a.GetCandidate},
		{"PATCH /api/candidates/{id}", need("recruitment"), a.UpdateCandidate},
		{"DELETE /api/candidates/{id}", need("recruitment"), a.DeleteCandidate}, // 409 while applications exist

		// applications: recruitment. The posting and the candidate must
		// belong to the caller's company (404 otherwise).
		{"GET /api/applications", need("recruitment"), a.ListApplications},
		{"POST /api/applications", need("recruitment"), a.CreateApplication},
		{"GET /api/applications/{id}", need("recruitment"), a.GetApplication},
		{"PATCH /api/applications/{id}", need("recruitment"), a.UpdateApplication},
		{"DELETE /api/applications/{id}", need("recruitment"), a.DeleteApplication}, // hard delete

		// companies: read-only, the caller's own company (no write routes; the
		// mux answers PATCH/POST/DELETE with 404 or 405).
		{"GET /api/companies", open, a.ListCompanies},
		{"GET /api/companies/{id}", open, a.GetCompany},

		// zkteco_devices: the caller's devices only (unclaimed NULL-company rows are
		// invisible). Any authenticated role; PATCH takes name and location ONLY.
		{"GET /api/zkteco_devices", open, a.ListZktecoDevices},
		{"PATCH /api/zkteco_devices/{id}", open, a.UpdateZktecoDevice},
		// Admin only. The literal "/unclaimed" ranks above "/{id}" under Go 1.22
		// ServeMux, like "/accrue" and "/bases" above.
		{"GET /api/zkteco_devices/unclaimed", viaHandler, a.ListUnclaimedZktecoDevices},
		{"POST /api/zkteco_devices/{id}/claim", viaHandler, a.ClaimZktecoDevice}, // atomic: only while company_id IS NULL

		// zkteco_unknown_pins: admin only (requireAdmin); the table has no company_id.
		{"GET /api/zkteco_unknown_pins", viaHandler, a.ListZktecoUnknownPins},

		{"GET /api/employees", open, a.ListEmployees},
		{"POST /api/employees", need("employees"), a.CreateEmployee},
		{"GET /api/employees/{id}", open, a.GetEmployee},
		{"PATCH /api/employees/{id}", need("employees"), a.UpdateEmployee},
		{"DELETE /api/employees/{id}", need("employees"), a.DeleteEmployee}, // soft delete (P6.2)

		{"GET /api/attendance_logs", need("attendance"), a.ListAttendanceLogs},
		{"POST /api/attendance_logs", need("attendance"), a.CreateAttendanceLog}, // upsert on (employee_id,date)
		{"GET /api/attendance_logs/{id}", need("attendance"), a.GetAttendanceLog},
		{"PATCH /api/attendance_logs/{id}", need("attendance"), a.UpdateAttendanceLog},
		{"DELETE /api/attendance_logs/{id}", need("attendance"), a.DeleteAttendanceLog}, // hard delete (Q3)

		// leave_balances: no generic POST, no DELETE (design Q3) — rows are owned
		// by the accrual scheduler. The one exception is the admin-gated manual
		// accrual trigger below; Go 1.22 ServeMux ranks the literal
		// "/accrue" path above the "/{id}" pattern, so there is no registration
		// conflict (design Q5c).
		{"GET /api/leave_balances", need("vacations"), a.ListLeaveBalances},
		{"GET /api/leave_balances/{id}", need("vacations"), a.GetLeaveBalance},
		{"PATCH /api/leave_balances/{id}", need("vacations"), a.UpdateLeaveBalance},   // {used_days} ONLY
		{"POST /api/leave_balances/accrue", need("vacations"), a.AccrueLeaveBalances}, // requirePermission("vacations")

		{"GET /api/leave_requests", need("vacations"), a.ListLeaveRequests},
		{"POST /api/leave_requests", need("vacations"), a.CreateLeaveRequest},
		{"GET /api/leave_requests/{id}", need("vacations"), a.GetLeaveRequest},
		{"PATCH /api/leave_requests/{id}", need("vacations"), a.UpdateLeaveRequest},  // approve/reject, requirePermission("vacations")
		{"DELETE /api/leave_requests/{id}", need("vacations"), a.DeleteLeaveRequest}, // hard delete while pending only, 409 once decided (design Q3 addendum)

		{"GET /api/overtime_logs", need("overtime"), a.ListOvertimeLogs},
		{"POST /api/overtime_logs", need("overtime"), a.CreateOvertimeLog}, // amount server-recomputed (Q6)
		{"GET /api/overtime_logs/{id}", need("overtime"), a.GetOvertimeLog},
		{"PATCH /api/overtime_logs/{id}", need("overtime"), a.UpdateOvertimeLog},
		{"DELETE /api/overtime_logs/{id}", need("overtime"), a.DeleteOvertimeLog}, // hard delete (Q3)

		{"GET /api/deductions", need("deductions"), a.ListDeductions},
		{"POST /api/deductions", need("deductions"), a.CreateDeduction}, // employee_id MAY be null (CSV import)
		{"GET /api/deductions/{id}", need("deductions"), a.GetDeduction},
		{"PATCH /api/deductions/{id}", need("deductions"), a.UpdateDeduction},
		{"DELETE /api/deductions/{id}", need("deductions"), a.DeleteDeduction}, // soft delete: status='cancelled' (Q3)

		{"GET /api/employee_pay_records", anyOf("payroll", "liquidation"), a.ListEmployeePayRecords},
		{"POST /api/employee_pay_records", need("liquidation"), a.CreateEmployeePayRecord},              // employee_id MAY be null (CSV import)
		{"GET /api/employee_pay_records/bases", anyOf("payroll", "liquidation"), a.GetEmployeePayBases}, // literal > /{id} (Q5c)
		{"GET /api/employee_pay_records/{id}", anyOf("payroll", "liquidation"), a.GetEmployeePayRecord},
		{"PATCH /api/employee_pay_records/{id}", need("liquidation"), a.UpdateEmployeePayRecord},
		{"DELETE /api/employee_pay_records/{id}", need("liquidation"), a.DeleteEmployeePayRecord}, // hard delete

		// payroll_history: the committing run (slice 3f). No PATCH -- immutable
		// historical record (design R7). No ledger cascade on delete (design
		// R5). Literal "/calculate" ranks above "/{id}" under Go 1.22 ServeMux
		// (Q5c).
		{"GET /api/payroll_history", need("payroll"), a.ListPayrollHistory},
		{"GET /api/payroll_history/calculate", need("payroll"), a.CalculatePayroll}, // preview, writes nothing
		{"POST /api/payroll_history", need("payroll"), a.CreatePayrollRun},          // tx: 1 aggregate + N ledger rows
		{"GET /api/payroll_history/{id}", need("payroll"), a.GetPayrollHistory},
		{"DELETE /api/payroll_history/{id}", need("payroll"), a.DeletePayrollHistory}, // hard; no ledger cascade

		// liquidation_history: no PATCH -- immutable historical record (design
		// R7). No cascade on delete -- no FK to employee_pay_records exists at
		// all. Literal "/calculate" ranks above "/{id}" under Go 1.22 ServeMux
		// (Q5c).
		{"GET /api/liquidation_history", need("liquidation"), a.ListLiquidationHistory},
		{"GET /api/liquidation_history/calculate", need("liquidation"), a.CalculateLiquidation}, // preview, writes nothing
		{"POST /api/liquidation_history", need("liquidation"), a.CreateLiquidation},             // recomputes server-side
		{"GET /api/liquidation_history/{id}", need("liquidation"), a.GetLiquidationHistory},
		{"DELETE /api/liquidation_history/{id}", need("liquidation"), a.DeleteLiquidationHistory}, // hard

		// document_templates: opaque CRUD, full-replace PATCH. DELETE answers 409
		// while generated_documents still reference the template (slice 4b).
		{"GET /api/document_templates", need("documents"), a.ListDocumentTemplates},
		{"POST /api/document_templates", need("documents"), a.CreateDocumentTemplate},
		{"GET /api/document_templates/{id}", need("documents"), a.GetDocumentTemplate},
		{"PATCH /api/document_templates/{id}", need("documents"), a.UpdateDocumentTemplate},
		{"DELETE /api/document_templates/{id}", need("documents"), a.DeleteDocumentTemplate}, // hard; 409 when referenced

		// generated_documents: issued records. Create/list/get only; no PATCH or
		// DELETE (design H1). Both nullable FKs are tenant-checked inside the INSERT.
		{"GET /api/generated_documents", need("documents"), a.ListGeneratedDocuments},
		{"POST /api/generated_documents", need("documents"), a.CreateGeneratedDocument},
		{"GET /api/generated_documents/{id}", need("documents"), a.GetGeneratedDocument},

		// evaluations: avg and category are recomputed server-side from scores
		// (D1). No PATCH route: PATCH answers 405 (slice 4d).
		{"GET /api/evaluations", need("employees"), a.ListEvaluations},
		{"POST /api/evaluations", need("employees"), a.CreateEvaluation},
		{"GET /api/evaluations/{id}", need("employees"), a.GetEvaluation},
		{"DELETE /api/evaluations/{id}", need("employees"), a.DeleteEvaluation}, // hard delete

		{"GET /api/medical_records", viaHandler, a.ListMedicalRecords},          // requirePermission("medical")
		{"POST /api/medical_records", viaHandler, a.CreateMedicalRecord},        // requirePermission("medical"); days split + cost computed server-side
		{"GET /api/medical_records/{id}", viaHandler, a.GetMedicalRecord},       // requirePermission("medical")
		{"PATCH /api/medical_records/{id}", viaHandler, a.UpdateMedicalRecord},  // requirePermission("medical"); partial, tx, always recomputes
		{"DELETE /api/medical_records/{id}", viaHandler, a.DeleteMedicalRecord}, // requirePermission("medical"); hard delete

		{"GET /api/uniforms", need("employees"), a.ListUniforms},
		{"POST /api/uniforms", need("employees"), a.CreateUniform},
		{"GET /api/uniforms/{id}", need("employees"), a.GetUniform},
		{"PATCH /api/uniforms/{id}", need("employees"), a.UpdateUniform},  // partial (returnUniform)
		{"DELETE /api/uniforms/{id}", need("employees"), a.DeleteUniform}, // hard delete
	}
}

// Routes builds the full API mux. Every tenant-scoped route lives on one
// `protected` sub-mux mounted once behind signer.Middleware: omitting a
// route from `protected` yields a 404 (fail-closed), instead of the
// fail-open risk of forgetting to wrap an individual route in middleware.
// Module permissions are applied per protectedRoutes classification.
func (a *API) Routes(signer *auth.Signer) http.Handler {
	protected := http.NewServeMux()
	for _, rt := range a.protectedRoutes() {
		h := rt.handler
		if rt.gate.kind == gatePermission {
			h = a.perm(rt.gate.keys, h)
		}
		protected.HandleFunc(rt.pattern, h)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health) // public — more specific than /api/
	// Login is public but throttled per client IP; more specific than /api/.
	mux.Handle("POST /api/auth/login", a.rateLimitedLogin(a.AuthLimiter, a.AuthAttemptLimiter, http.HandlerFunc(a.Login)))
	// register shares AuthLimiter (one bucket per client with login, AUTH-5) but
	// uses the generic wrapper: every attempt is charged and never refunded. Unlike
	// login (decision #911) there is no legitimate stream of successful registers
	// behind a shared NAT, and each success creates a tenant, so success must cost.
	mux.Handle("POST /api/auth/register", a.rateLimited(limiterAuth, a.AuthLimiter, http.HandlerFunc(a.Register)))
	// ZKTeco ADMS device endpoints: public (devices carry no JWT), outside /api/
	// and behind ADMSLimiter. PROVISIONAL wire format, see adms.go.
	a.admsRoutes(mux)
	mux.Handle("/api/", signer.Middleware(protected, a.SessionLookup))
	return mux
}
