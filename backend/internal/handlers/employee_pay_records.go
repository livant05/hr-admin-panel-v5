package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// employeePayRecordRequest is the exact contract savePayRecord/
// importPayRecordsCSV send (hr_admin_panel.html:3202-3338). employee_id is
// *string (not string) and nullable: CSV import legitimately sends
// employee_id:null for a name/cedula that matches no employee (same shape
// as deductionRequest). employee_name/cedula are declared so
// DisallowUnknownFields accepts the existing payload, but their values are
// only trusted when employee_id is absent (the CSV path) --
// CreateEmployeePayRecordWithEmployee derives BOTH from the employees row
// otherwise (spec "Manual entry with employee_id succeeds" -- unlike
// deductions.go, which trusts a client-supplied cedula even on its
// WithEmployee path). total_earned is declared only so the existing
// payload shape is accepted; its value is never read -- the server always
// recomputes it (design R4f/task 4.3).
type employeePayRecordRequest struct {
	EmployeeID      *string        `json:"employee_id"`
	EmployeeName    pgtype.Text    `json:"employee_name"`
	Cedula          pgtype.Text    `json:"cedula"`
	NumeroPlanilla  pgtype.Text    `json:"numero_planilla"`
	CentroCosto     pgtype.Text    `json:"centro_costo"`
	Periodo         pgtype.Text    `json:"periodo"`
	PeriodYear      pgtype.Int4    `json:"period_year"`
	PeriodMonth     pgtype.Int4    `json:"period_month"`
	GrossSalary     pgtype.Numeric `json:"gross_salary"`
	OvertimeAmount  pgtype.Numeric `json:"overtime_amount"`
	Commissions     pgtype.Numeric `json:"commissions"`
	Bonuses         pgtype.Numeric `json:"bonuses"`
	VacationsPaid   pgtype.Numeric `json:"vacations_paid"`
	OtherIncome     pgtype.Numeric `json:"other_income"`
	TotalEarned     pgtype.Numeric `json:"total_earned"` // accepted, never trusted -- always recomputed
	CssEmployee     pgtype.Numeric `json:"css_employee"`
	SeEmployee      pgtype.Numeric `json:"se_employee"`
	Isr             pgtype.Numeric `json:"isr"`
	OtherDeductions pgtype.Numeric `json:"other_deductions"`
	NetSalary       pgtype.Numeric `json:"net_salary"`
	Notes           pgtype.Text    `json:"notes"`
}

// decodeEmployeePayRecordRequest decodes and caps the body, rejecting
// unknown fields (A1 rule 2 extension -- a company_id or origin in the body
// is a 400, never a silent drop; both are server-owned per design R4a and
// are deliberately absent from this struct).
func decodeEmployeePayRecordRequest(w http.ResponseWriter, r *http.Request) (employeePayRecordRequest, bool) {
	var req employeePayRecordRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return employeePayRecordRequest{}, false
	}
	return req, true
}

// applyEmployeePayRecordDefaults mirrors the column defaults every
// INSERT/UPDATE below lists explicitly (same pattern as
// applyEmployeeDefaults/applyDeductionDefaults): every absent money field
// defaults to 0, matching the DDL's own `DEFAULT 0`.
func applyEmployeePayRecordDefaults(req *employeePayRecordRequest) {
	if !req.GrossSalary.Valid {
		req.GrossSalary = numericFromInt(0)
	}
	if !req.OvertimeAmount.Valid {
		req.OvertimeAmount = numericFromInt(0)
	}
	if !req.Commissions.Valid {
		req.Commissions = numericFromInt(0)
	}
	if !req.Bonuses.Valid {
		req.Bonuses = numericFromInt(0)
	}
	if !req.VacationsPaid.Valid {
		req.VacationsPaid = numericFromInt(0)
	}
	if !req.OtherIncome.Valid {
		req.OtherIncome = numericFromInt(0)
	}
	if !req.CssEmployee.Valid {
		req.CssEmployee = numericFromInt(0)
	}
	if !req.SeEmployee.Valid {
		req.SeEmployee = numericFromInt(0)
	}
	if !req.Isr.Valid {
		req.Isr = numericFromInt(0)
	}
	if !req.OtherDeductions.Valid {
		req.OtherDeductions = numericFromInt(0)
	}
	if !req.NetSalary.Valid {
		req.NetSalary = numericFromInt(0)
	}
}

// validateEmployeePayRecordPeriod validates period_year/period_month
// BEFORE the insert (task 4.5/R8): period_year is required (the column is
// NOT NULL with no default), and period_month must be 1..12 -- the gap
// importPayRecordsCSV can hit (an unparsed "Mes Año" string with an
// explicit period_year column yields period_month:0, which the bare DDL
// CHECK would otherwise surface as an unnamed 23514).
func validateEmployeePayRecordPeriod(w http.ResponseWriter, req employeePayRecordRequest) (int32, int32, bool) {
	fields := map[string]string{}
	if !req.PeriodYear.Valid {
		fields["period_year"] = "required"
	}
	if !req.PeriodMonth.Valid || req.PeriodMonth.Int32 < 1 || req.PeriodMonth.Int32 > 12 {
		fields["period_month"] = "must be between 1 and 12"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return 0, 0, false
	}
	return req.PeriodYear.Int32, req.PeriodMonth.Int32, true
}

// employeePayRecordResponse mirrors the shared RETURNING column list of
// every employee_pay_records query. sqlc generates one distinct Row type
// per query (GetEmployeePayRecordRow, ListEmployeePayRecordsRow,
// CreateEmployeePayRecordWithEmployeeRow,
// CreateEmployeePayRecordWithoutEmployeeRow, UpdateEmployeePayRecordRow),
// but all five share identical field name/type/order, so Go's direct
// struct-to-struct conversion (attendanceLogDBRow's precedent) serves every
// call site with one converter.
type employeePayRecordResponse struct {
	ID              pgtype.UUID        `json:"id"`
	CompanyID       pgtype.UUID        `json:"company_id"`
	EmployeeID      pgtype.UUID        `json:"employee_id"`
	EmployeeName    pgtype.Text        `json:"employee_name"`
	Cedula          pgtype.Text        `json:"cedula"`
	NumeroPlanilla  pgtype.Text        `json:"numero_planilla"`
	CentroCosto     pgtype.Text        `json:"centro_costo"`
	Periodo         pgtype.Text        `json:"periodo"`
	PeriodYear      int32              `json:"period_year"`
	PeriodMonth     int32              `json:"period_month"`
	GrossSalary     pgtype.Numeric     `json:"gross_salary"`
	OvertimeAmount  pgtype.Numeric     `json:"overtime_amount"`
	Commissions     pgtype.Numeric     `json:"commissions"`
	Bonuses         pgtype.Numeric     `json:"bonuses"`
	VacationsPaid   pgtype.Numeric     `json:"vacations_paid"`
	OtherIncome     pgtype.Numeric     `json:"other_income"`
	TotalEarned     pgtype.Numeric     `json:"total_earned"`
	CssEmployee     pgtype.Numeric     `json:"css_employee"`
	SeEmployee      pgtype.Numeric     `json:"se_employee"`
	Isr             pgtype.Numeric     `json:"isr"`
	OtherDeductions pgtype.Numeric     `json:"other_deductions"`
	NetSalary       pgtype.Numeric     `json:"net_salary"`
	Notes           pgtype.Text        `json:"notes"`
	Origin          string             `json:"origin"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
}

// GET /api/employee_pay_records?id=&employee_id=&period_year=&period_month=&_limit=&_offset=
// New server-side filtering (spec "List and delete with filters") -- the
// current JS fetches the full table and filters client-side.
func (a *API) ListEmployeePayRecords(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "period_year", "period_month")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}
	employeeID, err := optionalUUIDFilter(p.Filters["employee_id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid employee_id")
		return
	}
	periodYear, err := optionalIntFilter(p.Filters["period_year"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid period_year")
		return
	}
	periodMonth, err := optionalIntFilter(p.Filters["period_month"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid period_month")
		return
	}

	rows, err := a.Queries.ListEmployeePayRecords(r.Context(), db.ListEmployeePayRecordsParams{
		CompanyID:   companyID, // $1 -- from ctx, never from a query param
		Column2:     id,
		EmployeeID:  employeeID,
		PeriodYear:  periodYear,
		PeriodMonth: periodMonth,
		Limit:       p.Limit,
		Offset:      p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list employee pay records")
		return
	}

	resp := make([]employeePayRecordResponse, len(rows))
	for i, row := range rows {
		resp[i] = employeePayRecordResponse(row)
	}
	writeRows(w, http.StatusOK, resp)
}

// GET /api/employee_pay_records/{id}
func (a *API) GetEmployeePayRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetEmployeePayRecord(r.Context(), db.GetEmployeePayRecordParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get employee pay record")
		return
	}

	writeJSON(w, http.StatusOK, employeePayRecordResponse(row))
}

// POST /api/employee_pay_records -- employee_id MAY be null (CSV import,
// spec "CSV row imported successfully"). Two creation paths, same shape as
// CreateDeduction: when employee_id is present, A1 rule 6 applies and
// employee_name/cedula are derived; when absent, this is a plain
// company_id-scoped insert and employee_name/cedula are trusted as sent.
func (a *API) CreateEmployeePayRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	req, ok := decodeEmployeePayRecordRequest(w, r)
	if !ok {
		return
	}

	periodYear, periodMonth, ok := validateEmployeePayRecordPeriod(w, req)
	if !ok {
		return
	}
	applyEmployeePayRecordDefaults(&req)

	if req.EmployeeID != nil && strings.TrimSpace(*req.EmployeeID) != "" {
		employeeID, err := stringToUUID(*req.EmployeeID)
		if err != nil {
			writeFieldErr(w, map[string]string{"employee_id": "invalid"})
			return
		}

		// CreateEmployeePayRecordWithEmployeeParams.ID is the
		// client-supplied employee_id (sqlc named it from the query's
		// `e.id = $2` predicate, same naming quirk documented in
		// attendance_logs.go/deductions.go).
		row, err := a.Queries.CreateEmployeePayRecordWithEmployee(r.Context(), db.CreateEmployeePayRecordWithEmployeeParams{
			CompanyID:       companyID, // $1 -- from ctx, never from req
			ID:              employeeID,
			NumeroPlanilla:  req.NumeroPlanilla,
			CentroCosto:     req.CentroCosto,
			Periodo:         req.Periodo,
			PeriodYear:      periodYear,
			PeriodMonth:     periodMonth,
			GrossSalary:     req.GrossSalary,
			OvertimeAmount:  req.OvertimeAmount,
			Commissions:     req.Commissions,
			Bonuses:         req.Bonuses,
			VacationsPaid:   req.VacationsPaid,
			OtherIncome:     req.OtherIncome,
			CssEmployee:     req.CssEmployee,
			SeEmployee:      req.SeEmployee,
			Isr:             req.Isr,
			OtherDeductions: req.OtherDeductions,
			NetSalary:       req.NetSalary,
			Notes:           req.Notes,
		})
		if err != nil {
			a.writeDBErr(w, err, "create employee pay record")
			return
		}
		writeRows(w, http.StatusCreated, []employeePayRecordResponse{employeePayRecordResponse(row)})
		return
	}

	row, err := a.Queries.CreateEmployeePayRecordWithoutEmployee(r.Context(), db.CreateEmployeePayRecordWithoutEmployeeParams{
		CompanyID:       companyID, // $1 -- from ctx, never from req
		EmployeeName:    req.EmployeeName,
		Cedula:          req.Cedula,
		NumeroPlanilla:  req.NumeroPlanilla,
		CentroCosto:     req.CentroCosto,
		Periodo:         req.Periodo,
		PeriodYear:      periodYear,
		PeriodMonth:     periodMonth,
		GrossSalary:     req.GrossSalary,
		OvertimeAmount:  req.OvertimeAmount,
		Commissions:     req.Commissions,
		Bonuses:         req.Bonuses,
		VacationsPaid:   req.VacationsPaid,
		OtherIncome:     req.OtherIncome,
		CssEmployee:     req.CssEmployee,
		SeEmployee:      req.SeEmployee,
		Isr:             req.Isr,
		OtherDeductions: req.OtherDeductions,
		NetSalary:       req.NetSalary,
		Notes:           req.Notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "create employee pay record")
		return
	}
	writeRows(w, http.StatusCreated, []employeePayRecordResponse{employeePayRecordResponse(row)})
}

// PATCH /api/employee_pay_records/{id} -- full-column replace of every
// editable field (deductions.go's UpdateDeduction precedent).
// employee_id/company_id/origin/created_at stay immutable.
func (a *API) UpdateEmployeePayRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	req, ok := decodeEmployeePayRecordRequest(w, r)
	if !ok {
		return
	}

	periodYear, periodMonth, ok := validateEmployeePayRecordPeriod(w, req)
	if !ok {
		return
	}
	applyEmployeePayRecordDefaults(&req)

	row, err := a.Queries.UpdateEmployeePayRecord(r.Context(), db.UpdateEmployeePayRecordParams{
		CompanyID:       companyID,
		ID:              id,
		EmployeeName:    req.EmployeeName,
		Cedula:          req.Cedula,
		NumeroPlanilla:  req.NumeroPlanilla,
		CentroCosto:     req.CentroCosto,
		Periodo:         req.Periodo,
		PeriodYear:      periodYear,
		PeriodMonth:     periodMonth,
		GrossSalary:     req.GrossSalary,
		OvertimeAmount:  req.OvertimeAmount,
		Commissions:     req.Commissions,
		Bonuses:         req.Bonuses,
		VacationsPaid:   req.VacationsPaid,
		OtherIncome:     req.OtherIncome,
		CssEmployee:     req.CssEmployee,
		SeEmployee:      req.SeEmployee,
		Isr:             req.Isr,
		OtherDeductions: req.OtherDeductions,
		NetSalary:       req.NetSalary,
		Notes:           req.Notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "update employee pay record")
		return
	}

	writeRows(w, http.StatusOK, []employeePayRecordResponse{employeePayRecordResponse(row)})
}

// DELETE /api/employee_pay_records/{id} -- hard delete (spec "List and
// delete with filters" -- matches delPayRecord's existing behavior; no
// soft-delete semantics apply to this table, unlike deductions).
func (a *API) DeleteEmployeePayRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteEmployeePayRecord(r.Context(), db.DeleteEmployeePayRecordParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete employee pay record")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// employeePayBasesResponse mirrors GetEmployeePayBasesRow's column order
// exactly (acum_prima, acum_6m, sal30, acum_vac, acum_dec, months) for the
// same direct struct-to-struct conversion every other response type in this
// file uses.
type employeePayBasesResponse struct {
	AcumPrima pgtype.Numeric `json:"acum_prima"`
	Acum6m    pgtype.Numeric `json:"acum_6m"`
	Sal30     pgtype.Numeric `json:"sal30"`
	AcumVac   pgtype.Numeric `json:"acum_vac"`
	AcumDec   pgtype.Numeric `json:"acum_dec"`
	Months    int64          `json:"months"`
}

// GET /api/employee_pay_records/bases?employee_id= -- port of
// getEmpPayBases (hr_admin_panel.html:3343-3360), server-side (design R9,
// task 5.3). The underlying query is a plain aggregate with no GROUP BY, so
// it always returns exactly one row: an employee with no history gets
// all-zero sums and months:0 -- a bare object, never null and never 404
// (task 5.1's TestPayBases_EmptyHistoryReturnsZerosWithMonthsZero).
func (a *API) GetEmployeePayBases(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	employeeID, err := stringToUUID(r.URL.Query().Get("employee_id"))
	if err != nil {
		writeFieldErr(w, map[string]string{"employee_id": "required"})
		return
	}

	row, err := a.Queries.GetEmployeePayBases(r.Context(), db.GetEmployeePayBasesParams{
		CompanyID:  companyID,
		EmployeeID: employeeID,
	})
	if err != nil {
		a.writeDBErr(w, err, "get employee pay bases")
		return
	}

	writeJSON(w, http.StatusOK, employeePayBasesResponse(row))
}
