package handlers

import (
	"encoding/json"
	"math/big"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/employees — whitelists exactly `id` and `status` (spec "Employees
// list filters" requirement; every call site in hr_admin_panel.html sends
// only these two, e.g. Supa.sel('employees',{status:'active'})).
func (a *API) ListEmployees(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "status")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListEmployees(r.Context(), db.ListEmployeesParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		Column2:   id,
		Status:    optionalTextFilter(p.Filters["status"]),
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list employees")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/employees/{id}
func (a *API) GetEmployee(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetEmployee(r.Context(), db.GetEmployeeParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get employee")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// employeeRequest is the exact 33-field contract `saveEmp` sends
// (hr_admin_panel.html:2629, spec "Employees create/update field contract").
// It deliberately has NO id/company_id/created_at/updated_at fields (A1 rule
// 2): DisallowUnknownFields turns an attempt to set them into a 400 instead
// of a silent drop.
type employeeRequest struct {
	FirstName         string         `json:"first_name"`
	LastName          string         `json:"last_name"`
	Cedula            pgtype.Text    `json:"cedula"`
	SsNumber          pgtype.Text    `json:"ss_number"`
	Dv                pgtype.Text    `json:"dv"`
	BirthDate         pgtype.Date    `json:"birth_date"`
	Age               pgtype.Int4    `json:"age"`
	Sex               pgtype.Text    `json:"sex"`
	MaritalStatus     pgtype.Text    `json:"marital_status"`
	BloodType         pgtype.Text    `json:"blood_type"`
	Nationality       pgtype.Text    `json:"nationality"`
	Address           pgtype.Text    `json:"address"`
	Phone             pgtype.Text    `json:"phone"`
	Email             pgtype.Text    `json:"email"`
	LicenseNumber     pgtype.Text    `json:"license_number"`
	LicenseType       pgtype.Text    `json:"license_type"`
	LicenseExpiry     pgtype.Date    `json:"license_expiry"`
	Position          pgtype.Text    `json:"position"`
	Department        pgtype.Text    `json:"department"`
	Branch            pgtype.Text    `json:"branch"`
	Salary            pgtype.Numeric `json:"salary"`
	WeeklyHours       pgtype.Numeric `json:"weekly_hours"`
	MonthlyHours      pgtype.Numeric `json:"monthly_hours"`
	HourlyRate        pgtype.Numeric `json:"hourly_rate"`
	ContractType      pgtype.Text    `json:"contract_type"`
	StartDate         pgtype.Date    `json:"start_date"`
	Status            pgtype.Text    `json:"status"`
	ContractExpiry    pgtype.Date    `json:"contract_expiry"`
	WorkPermitExpiry  pgtype.Date    `json:"work_permit_expiry"`
	EmergencyContact  pgtype.Text    `json:"emergency_contact"`
	EmergencyPhone    pgtype.Text    `json:"emergency_phone"`
	EmergencyRelation pgtype.Text    `json:"emergency_relation"`
	PhotoUrl          pgtype.Text    `json:"photo_url"`
}

// decodeEmployeeRequest decodes and caps the body, rejecting unknown fields.
func decodeEmployeeRequest(w http.ResponseWriter, r *http.Request) (employeeRequest, bool) {
	var req employeeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return employeeRequest{}, false
	}
	return req, true
}

// validateEmployeeNames enforces the spec's "first_name/last_name MUST be
// required non-empty server-side" rule, mirroring the client's own
// validation at hr_admin_panel.html:2625 but authoritative on the server.
func validateEmployeeNames(w http.ResponseWriter, req employeeRequest) (string, string, bool) {
	fn := strings.TrimSpace(req.FirstName)
	ln := strings.TrimSpace(req.LastName)
	fields := map[string]string{}
	if fn == "" {
		fields["first_name"] = "required"
	}
	if ln == "" {
		fields["last_name"] = "required"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return "", "", false
	}
	return fn, ln, true
}

// numericFromInt builds a valid pgtype.Numeric from a whole number, used for
// the spec's "other absent numeric fields default to 0" and
// "absent weekly_hours defaults to 48" rules.
func numericFromInt(v int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(v), Valid: true}
}

// applyEmployeeDefaults mirrors the client's own `||` fallbacks
// (hr_admin_panel.html:2629: weekly_hours||48, status||'active',
// salary/monthly_hours/hourly_rate||0) so Go and JS never disagree on a
// default (spec "Employees create/update field contract" requirement).
func applyEmployeeDefaults(req *employeeRequest) {
	if !req.WeeklyHours.Valid {
		req.WeeklyHours = numericFromInt(48)
	}
	if !req.Status.Valid || strings.TrimSpace(req.Status.String) == "" {
		req.Status = pgtype.Text{String: "active", Valid: true}
	}
	if !req.Salary.Valid {
		req.Salary = numericFromInt(0)
	}
	if !req.MonthlyHours.Valid {
		req.MonthlyHours = numericFromInt(0)
	}
	if !req.HourlyRate.Valid {
		req.HourlyRate = numericFromInt(0)
	}
}

// optionalTextFilter parses an optional equality filter (A4). An empty
// string means "no filter" and is represented as an invalid pgtype.Text,
// which the `sqlc.narg('status')::text IS NULL OR status = ...` predicate
// treats as NULL.
func optionalTextFilter(raw string) pgtype.Text {
	if raw == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: raw, Valid: true}
}

// POST /api/employees
func (a *API) CreateEmployee(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	req, ok := decodeEmployeeRequest(w, r)
	if !ok {
		return
	}

	fn, ln, ok := validateEmployeeNames(w, req)
	if !ok {
		return
	}
	req.FirstName, req.LastName = fn, ln
	applyEmployeeDefaults(&req)

	row, err := a.Queries.CreateEmployee(r.Context(), db.CreateEmployeeParams{
		CompanyID:         companyID, // $1 — from ctx, never from req
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		Cedula:            req.Cedula,
		SsNumber:          req.SsNumber,
		Dv:                req.Dv,
		BirthDate:         req.BirthDate,
		Age:               req.Age,
		Sex:               req.Sex,
		MaritalStatus:     req.MaritalStatus,
		BloodType:         req.BloodType,
		Nationality:       req.Nationality,
		Address:           req.Address,
		Phone:             req.Phone,
		Email:             req.Email,
		LicenseNumber:     req.LicenseNumber,
		LicenseType:       req.LicenseType,
		LicenseExpiry:     req.LicenseExpiry,
		Position:          req.Position,
		Department:        req.Department,
		Branch:            req.Branch,
		Salary:            req.Salary,
		WeeklyHours:       req.WeeklyHours,
		MonthlyHours:      req.MonthlyHours,
		HourlyRate:        req.HourlyRate,
		ContractType:      req.ContractType,
		StartDate:         req.StartDate,
		Status:            req.Status,
		ContractExpiry:    req.ContractExpiry,
		WorkPermitExpiry:  req.WorkPermitExpiry,
		EmergencyContact:  req.EmergencyContact,
		EmergencyPhone:    req.EmergencyPhone,
		EmergencyRelation: req.EmergencyRelation,
		PhotoUrl:          req.PhotoUrl,
	})
	if err != nil {
		a.writeDBErr(w, err, "create employee")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in saveEmp stay valid.
	writeRows(w, http.StatusCreated, []db.Employee{row})
}

// PATCH /api/employees/{id} — full-column replace, not a partial merge
// (design P2.3): `saveEmp` always sends the complete 33-field payload for
// both create and update, so COALESCE/sqlc.narg partial-update semantics are
// not needed here, unlike roles' independent name/permissions PATCH.
func (a *API) UpdateEmployee(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	req, ok := decodeEmployeeRequest(w, r)
	if !ok {
		return
	}

	fn, ln, ok := validateEmployeeNames(w, req)
	if !ok {
		return
	}
	req.FirstName, req.LastName = fn, ln
	applyEmployeeDefaults(&req)

	row, err := a.Queries.UpdateEmployee(r.Context(), db.UpdateEmployeeParams{
		CompanyID:         companyID,
		ID:                id,
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		Cedula:            req.Cedula,
		SsNumber:          req.SsNumber,
		Dv:                req.Dv,
		BirthDate:         req.BirthDate,
		Age:               req.Age,
		Sex:               req.Sex,
		MaritalStatus:     req.MaritalStatus,
		BloodType:         req.BloodType,
		Nationality:       req.Nationality,
		Address:           req.Address,
		Phone:             req.Phone,
		Email:             req.Email,
		LicenseNumber:     req.LicenseNumber,
		LicenseType:       req.LicenseType,
		LicenseExpiry:     req.LicenseExpiry,
		Position:          req.Position,
		Department:        req.Department,
		Branch:            req.Branch,
		Salary:            req.Salary,
		WeeklyHours:       req.WeeklyHours,
		MonthlyHours:      req.MonthlyHours,
		HourlyRate:        req.HourlyRate,
		ContractType:      req.ContractType,
		StartDate:         req.StartDate,
		Status:            req.Status,
		ContractExpiry:    req.ContractExpiry,
		WorkPermitExpiry:  req.WorkPermitExpiry,
		EmergencyContact:  req.EmergencyContact,
		EmergencyPhone:    req.EmergencyPhone,
		EmergencyRelation: req.EmergencyRelation,
		PhotoUrl:          req.PhotoUrl,
	})
	if err != nil {
		a.writeDBErr(w, err, "update employee")
		return
	}

	writeRows(w, http.StatusOK, []db.Employee{row})
}

// DELETE /api/employees/{id} — soft delete only (design P6.2). A real
// DELETE would cascade into 9 legal payroll/audit tables (attendance_logs,
// leave_balances, leave_requests, overtime_logs, deductions,
// medical_records, evaluations, uniforms, enrollments) and would be blocked
// by 2 more with no ON DELETE clause (liquidation_history,
// generated_documents). Instead this flips `status` to 'inactive', which
// the UI already filters out of every operational list
// (Supa.sel('employees',{status:'active'})).
func (a *API) DeleteEmployee(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeactivateEmployee(r.Context(), db.DeactivateEmployeeParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete employee")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
