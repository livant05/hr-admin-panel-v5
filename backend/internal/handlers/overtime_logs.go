package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// overtimeLogRequest is the exact contract saveOT sends
// (hr_admin_panel.html:3371). employee_name is declared so
// DisallowUnknownFields accepts the existing payload, but its value is
// never trusted: the server derives it from the employees row the
// tenant-scoped employee_id join resolves (A1 rule 6, design Q2), same
// defense as attendance_logs/leave_requests. amount is declared for the same
// DisallowUnknownFields reason, but is always server-recomputed (design Q6)
// -- the client-submitted value is never persisted.
type overtimeLogRequest struct {
	EmployeeID   string         `json:"employee_id"`
	EmployeeName pgtype.Text    `json:"employee_name"`
	Date         pgtype.Date    `json:"date"`
	Hours        pgtype.Numeric `json:"hours"`
	Type         pgtype.Text    `json:"type"`
	HourlyRate   pgtype.Numeric `json:"hourly_rate"`
	Amount       pgtype.Numeric `json:"amount"`
	Notes        pgtype.Text    `json:"notes"`
}

// decodeOvertimeLogRequest decodes and caps the body, rejecting unknown
// fields (A1 rule 2 extension -- a company_id in the body is a 400, never a
// silent drop).
func decodeOvertimeLogRequest(w http.ResponseWriter, r *http.Request) (overtimeLogRequest, bool) {
	var req overtimeLogRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return overtimeLogRequest{}, false
	}
	return req, true
}

// normalizeOvertimeType defaults an absent/empty type to "regular" (the
// column default, matching saveOT's own `type: gv('ot-type')||'regular'`
// fallback) and rejects any explicitly-sent value outside otRates (design
// Q6: an unknown type is rejected, never silently defaulted to the lowest
// rate the way the current JS does).
func normalizeOvertimeType(raw pgtype.Text) (string, bool) {
	t := defaultOvertimeType
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		t = raw.String
	}
	if _, known := otRates[t]; !known {
		return "", false
	}
	return t, true
}

// validateOvertimeHours enforces both "hours is required" and the 3-hour
// per-record cap (Art. 36 num. 4, design Q6/spec "Hours over the daily cap
// rejected").
func validateOvertimeHours(hours pgtype.Numeric) (ok bool, fieldMsg string) {
	if !hours.Valid {
		return false, "required"
	}
	f, err := hours.Float64Value()
	if err != nil {
		return false, "invalid"
	}
	if f.Float64 <= 0 {
		return false, "must be greater than 0"
	}
	if f.Float64 > maxOvertimeHoursPerRecord {
		return false, fmt.Sprintf("must not exceed %d hours per record (Art. 36 num. 4)", maxOvertimeHoursPerRecord)
	}
	return true, ""
}

// GET /api/overtime_logs?id=&employee_id=&date=&_limit=&_offset=
func (a *API) ListOvertimeLogs(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "date")
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
	date, err := optionalDateFilter(p.Filters["date"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid date")
		return
	}

	rows, err := a.Queries.ListOvertimeLogs(r.Context(), db.ListOvertimeLogsParams{
		CompanyID:  companyID, // $1 -- from ctx, never from a query param
		Column2:    id,
		EmployeeID: employeeID,
		Date:       date,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list overtime logs")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/overtime_logs/{id}
func (a *API) GetOvertimeLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetOvertimeLog(r.Context(), db.GetOvertimeLogParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get overtime log")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/overtime_logs -- amount is always server-recomputed from
// hourly_rate * rate(type) * hours using exact Postgres NUMERIC arithmetic
// (design Q6); the client-submitted amount is ignored.
func (a *API) CreateOvertimeLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	req, ok := decodeOvertimeLogRequest(w, r)
	if !ok {
		return
	}

	fields := map[string]string{}

	employeeID, err := stringToUUID(req.EmployeeID)
	if req.EmployeeID == "" || err != nil {
		fields["employee_id"] = "required"
	}
	if !req.Date.Valid {
		fields["date"] = "required"
	}
	if !req.HourlyRate.Valid {
		fields["hourly_rate"] = "required"
	}

	otType, typeOK := normalizeOvertimeType(req.Type)
	if !typeOK {
		fields["type"] = "must be one of regular, mixed, night, sunday, holiday"
	}

	if hoursOK, msg := validateOvertimeHours(req.Hours); !hoursOK {
		fields["hours"] = msg
	}

	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	// typeOK already confirmed otType is a known key of otRates, so this
	// lookup cannot fail -- overtimeRateNumeric's ok=false branch is
	// unreachable here, but checked anyway rather than assumed.
	rate, ok := overtimeRateNumeric(otType)
	if !ok {
		writeFieldErr(w, map[string]string{"type": "must be one of regular, mixed, night, sunday, holiday"})
		return
	}

	// CreateOvertimeLogParams.ID is the client-supplied employee_id (sqlc
	// named it from the query's `e.id = $2` predicate, matching the same
	// naming quirk documented in attendance_logs.go/leave_requests.go).
	row, err := a.Queries.CreateOvertimeLog(r.Context(), db.CreateOvertimeLogParams{
		CompanyID:  companyID, // $1 -- from ctx, never from req
		ID:         employeeID,
		Date:       req.Date,
		Hours:      req.Hours,
		Type:       pgtype.Text{String: otType, Valid: true},
		HourlyRate: req.HourlyRate,
		Notes:      req.Notes,
		Rate:       rate,
	})
	if err != nil {
		a.writeDBErr(w, err, "create overtime log")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY.
	writeRows(w, http.StatusCreated, []db.OvertimeLog{row})
}

// PATCH /api/overtime_logs/{id} -- restricted to the operationally-editable
// fields (hours, type, hourly_rate, notes). employee_id and date are
// immutable here, mirroring attendance_logs'/leave_requests' precedent:
// changing which employee or day an overtime record belongs to is a new
// record, not an edit. amount is always recomputed, never trusted from the
// client, same as create.
func (a *API) UpdateOvertimeLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	req, ok := decodeOvertimeLogRequest(w, r)
	if !ok {
		return
	}

	fields := map[string]string{}
	if !req.HourlyRate.Valid {
		fields["hourly_rate"] = "required"
	}
	otType, typeOK := normalizeOvertimeType(req.Type)
	if !typeOK {
		fields["type"] = "must be one of regular, mixed, night, sunday, holiday"
	}
	if hoursOK, msg := validateOvertimeHours(req.Hours); !hoursOK {
		fields["hours"] = msg
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	rate, ok := overtimeRateNumeric(otType)
	if !ok {
		writeFieldErr(w, map[string]string{"type": "must be one of regular, mixed, night, sunday, holiday"})
		return
	}

	row, err := a.Queries.UpdateOvertimeLog(r.Context(), db.UpdateOvertimeLogParams{
		CompanyID:  companyID,
		ID:         id,
		Hours:      req.Hours,
		Type:       pgtype.Text{String: otType, Valid: true},
		HourlyRate: req.HourlyRate,
		Notes:      req.Notes,
		Rate:       rate,
	})
	if err != nil {
		a.writeDBErr(w, err, "update overtime log")
		return
	}

	writeRows(w, http.StatusOK, []db.OvertimeLog{row})
}

// DELETE /api/overtime_logs/{id} -- hard delete (design Q3): the payroll
// amount is snapshotted into employee_pay_records at run time, so deleting
// the log cannot retro-alter a paid slip.
func (a *API) DeleteOvertimeLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteOvertimeLog(r.Context(), db.DeleteOvertimeLogParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete overtime log")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
