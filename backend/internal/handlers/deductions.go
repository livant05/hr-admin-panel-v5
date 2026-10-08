package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// deductionDefaultType/Status/TipoPago mirror the column defaults the DDL
// already declares (migration 0001 + 0006), applied here because every
// deduction INSERT lists columns explicitly rather than relying on the
// table default (same pattern as leaveRequestDefaultType/
// applyEmployeeDefaults).
const (
	deductionDefaultType     = "prestamo"
	deductionDefaultStatus   = "active"
	deductionDefaultTipoPago = "Cheque"
)

// deductionDefaultPrioridad mirrors the column default (4) added by
// migration 0006.
const deductionDefaultPrioridad = 4

// deductionRequest is the exact contract saveDed/importDedCSV send
// (hr_admin_panel.html:3419-3523). employee_id is *string (not string) and
// nullable in the DDL as of migration 0006: importDedCSV legitimately sends
// employee_id:null for a name/cedula that matches no employee (design
// Q1/Q2). employee_name/cedula are declared like every other table so
// DisallowUnknownFields accepts the existing payload, but their values are
// only trusted when employee_id is absent -- CreateDeductionWithEmployee
// derives employee_name from the employees row otherwise, same defense as
// attendance_logs/leave_requests/overtime_logs.
type deductionRequest struct {
	EmployeeID     *string        `json:"employee_id"`
	EmployeeName   pgtype.Text    `json:"employee_name"`
	Type           pgtype.Text    `json:"type"`
	Description    pgtype.Text    `json:"description"`
	TotalAmount    pgtype.Numeric `json:"total_amount"`
	Quota          pgtype.Numeric `json:"quota"`
	Remaining      pgtype.Numeric `json:"remaining"`
	StartDate      pgtype.Date    `json:"start_date"`
	Status         pgtype.Text    `json:"status"`
	Cedula         pgtype.Text    `json:"cedula"`
	AcreedorNombre pgtype.Text    `json:"acreedor_nombre"`
	AcreedorCodigo pgtype.Text    `json:"acreedor_codigo"`
	TipoPago       pgtype.Text    `json:"tipo_pago"`
	CentroCosto    pgtype.Text    `json:"centro_costo"`
	Prioridad      pgtype.Int4    `json:"prioridad"`
	EndDate        pgtype.Date    `json:"end_date"`
	Periodo        pgtype.Text    `json:"periodo"`
	NumeroPlanilla pgtype.Text    `json:"numero_planilla"`
}

// decodeDeductionRequest decodes and caps the body, rejecting unknown
// fields (A1 rule 2 extension -- a company_id in the body is a 400, never a
// silent drop).
func decodeDeductionRequest(w http.ResponseWriter, r *http.Request) (deductionRequest, bool) {
	var req deductionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return deductionRequest{}, false
	}
	return req, true
}

// applyDeductionDefaults mirrors the column defaults every INSERT/UPDATE
// below lists explicitly (same pattern as applyEmployeeDefaults).
func applyDeductionDefaults(req *deductionRequest) {
	if !req.Type.Valid || strings.TrimSpace(req.Type.String) == "" {
		req.Type = pgtype.Text{String: deductionDefaultType, Valid: true}
	}
	if !req.Status.Valid || strings.TrimSpace(req.Status.String) == "" {
		req.Status = pgtype.Text{String: deductionDefaultStatus, Valid: true}
	}
	if !req.TipoPago.Valid || strings.TrimSpace(req.TipoPago.String) == "" {
		req.TipoPago = pgtype.Text{String: deductionDefaultTipoPago, Valid: true}
	}
	if !req.TotalAmount.Valid {
		req.TotalAmount = numericFromInt(0)
	}
	if !req.Remaining.Valid {
		req.Remaining = numericFromInt(0)
	}
	if !req.Prioridad.Valid {
		req.Prioridad = pgtype.Int4{Int32: deductionDefaultPrioridad, Valid: true}
	}
}

// validateDeductionQuota pins the spec's "Missing quota rejected" scenario:
// quota is required and must be > 0, matching saveDed's own client-side
// check (hr_admin_panel.html:3419).
func validateDeductionQuota(quota pgtype.Numeric) (ok bool, msg string) {
	if !quota.Valid {
		return false, "required"
	}
	f, err := quota.Float64Value()
	if err != nil {
		return false, "invalid"
	}
	if f.Float64 <= 0 {
		return false, "must be greater than 0"
	}
	return true, ""
}

// GET /api/deductions?id=&employee_id=&status=
func (a *API) ListDeductions(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "status")
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

	rows, err := a.Queries.ListDeductions(r.Context(), db.ListDeductionsParams{
		CompanyID:  companyID, // $1 -- from ctx, never from a query param
		Column2:    id,
		EmployeeID: employeeID,
		Status:     optionalTextFilter(p.Filters["status"]),
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list deductions")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/deductions/{id}
func (a *API) GetDeduction(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetDeduction(r.Context(), db.GetDeductionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get deduction")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/deductions -- employee_id MAY be null (design Q1/Q2, CSV
// import). Two creation paths: when employee_id is present, A1 rule 6
// applies (the tenant check on employee_id IS the insert, same as the other
// four Phase 2 tables); when it is absent, this is a plain company_id-scoped
// insert and the client-supplied employee_name/cedula are trusted as sent
// (design Q1's deliberate exception for an unmatched CSV row).
func (a *API) CreateDeduction(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	req, ok := decodeDeductionRequest(w, r)
	if !ok {
		return
	}

	if quotaOK, msg := validateDeductionQuota(req.Quota); !quotaOK {
		writeFieldErr(w, map[string]string{"quota": msg})
		return
	}
	applyDeductionDefaults(&req)

	if req.EmployeeID != nil && strings.TrimSpace(*req.EmployeeID) != "" {
		employeeID, err := stringToUUID(*req.EmployeeID)
		if err != nil {
			writeFieldErr(w, map[string]string{"employee_id": "invalid"})
			return
		}

		// CreateDeductionWithEmployeeParams.ID is the client-supplied
		// employee_id (sqlc named it from the query's `e.id = $2`
		// predicate, matching the same naming quirk documented in
		// attendance_logs.go/leave_requests.go/overtime_logs.go).
		row, err := a.Queries.CreateDeductionWithEmployee(r.Context(), db.CreateDeductionWithEmployeeParams{
			CompanyID:      companyID, // $1 -- from ctx, never from req
			ID:             employeeID,
			Type:           req.Type,
			Description:    req.Description,
			TotalAmount:    req.TotalAmount,
			Quota:          req.Quota,
			Remaining:      req.Remaining,
			StartDate:      req.StartDate,
			Status:         req.Status,
			Cedula:         req.Cedula,
			AcreedorNombre: req.AcreedorNombre,
			AcreedorCodigo: req.AcreedorCodigo,
			TipoPago:       req.TipoPago,
			CentroCosto:    req.CentroCosto,
			Prioridad:      req.Prioridad,
			EndDate:        req.EndDate,
			Periodo:        req.Periodo,
			NumeroPlanilla: req.NumeroPlanilla,
		})
		if err != nil {
			a.writeDBErr(w, err, "create deduction")
			return
		}
		writeRows(w, http.StatusCreated, []db.Deduction{row})
		return
	}

	row, err := a.Queries.CreateDeductionWithoutEmployee(r.Context(), db.CreateDeductionWithoutEmployeeParams{
		CompanyID:      companyID, // $1 -- from ctx, never from req
		EmployeeName:   req.EmployeeName,
		Type:           req.Type,
		Description:    req.Description,
		TotalAmount:    req.TotalAmount,
		Quota:          req.Quota,
		Remaining:      req.Remaining,
		StartDate:      req.StartDate,
		Status:         req.Status,
		Cedula:         req.Cedula,
		AcreedorNombre: req.AcreedorNombre,
		AcreedorCodigo: req.AcreedorCodigo,
		TipoPago:       req.TipoPago,
		CentroCosto:    req.CentroCosto,
		Prioridad:      req.Prioridad,
		EndDate:        req.EndDate,
		Periodo:        req.Periodo,
		NumeroPlanilla: req.NumeroPlanilla,
	})
	if err != nil {
		a.writeDBErr(w, err, "create deduction")
		return
	}
	writeRows(w, http.StatusCreated, []db.Deduction{row})
}

// PATCH /api/deductions/{id} -- full-column replace of every editable
// field; employee_id/company_id/created_at stay immutable (design's
// Interfaces/Contracts section states PATCH -> 200 [Deduction] with no
// field restriction, unlike leave_requests' Q4 state machine).
func (a *API) UpdateDeduction(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	req, ok := decodeDeductionRequest(w, r)
	if !ok {
		return
	}

	if quotaOK, msg := validateDeductionQuota(req.Quota); !quotaOK {
		writeFieldErr(w, map[string]string{"quota": msg})
		return
	}
	applyDeductionDefaults(&req)

	row, err := a.Queries.UpdateDeduction(r.Context(), db.UpdateDeductionParams{
		CompanyID:      companyID,
		ID:             id,
		EmployeeName:   req.EmployeeName,
		Type:           req.Type,
		Description:    req.Description,
		TotalAmount:    req.TotalAmount,
		Quota:          req.Quota,
		Remaining:      req.Remaining,
		StartDate:      req.StartDate,
		Status:         req.Status,
		Cedula:         req.Cedula,
		AcreedorNombre: req.AcreedorNombre,
		AcreedorCodigo: req.AcreedorCodigo,
		TipoPago:       req.TipoPago,
		CentroCosto:    req.CentroCosto,
		Prioridad:      req.Prioridad,
		EndDate:        req.EndDate,
		Periodo:        req.Periodo,
		NumeroPlanilla: req.NumeroPlanilla,
	})
	if err != nil {
		a.writeDBErr(w, err, "update deduction")
		return
	}

	writeRows(w, http.StatusOK, []db.Deduction{row})
}

// DELETE /api/deductions/{id} -- soft delete (design Q3): sets
// status='cancelled' rather than removing the row, matching exportDedCSV's
// existing read of status==='cancelled' and saldaDed's existing write of
// 'paid'. Mirrors DeactivateEmployee's exact shape (P6.2): a second DELETE
// on an already-cancelled row is 404 (0 rows affected by the
// `status <> 'cancelled'` guard), consistent with that precedent.
func (a *API) DeleteDeduction(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.CancelDeduction(r.Context(), db.CancelDeductionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "cancel deduction")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
