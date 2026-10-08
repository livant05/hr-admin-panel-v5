package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// validAttendanceStatuses are the four canonical statuses the server
// recognizes (design Q1, spec "attendance_logs field contract"): the client
// derives these from its own 30-entry PAYDAY_CODES table
// (hr_admin_panel.html:1818-1855), but the server only enforces the 4 values
// every status badge/counter actually switches on (att-stats-row).
var validAttendanceStatuses = map[string]bool{
	"present": true, "late": true, "absent": true, "permission": true,
}

// attendanceLogRequest is the exact contract saveAttendance sends
// (hr_admin_panel.html:2772). employee_name/department are declared so
// DisallowUnknownFields accepts the existing payload, but their values are
// never trusted: the server derives both from the employees row the
// tenant-scoped employee_id join resolves (design Q2) — a spoofing surface
// and a rename-drift bug otherwise (phase1-design P6.1's same argument).
type attendanceLogRequest struct {
	EmployeeID   string      `json:"employee_id"`
	EmployeeName pgtype.Text `json:"employee_name"`
	Department   pgtype.Text `json:"department"`
	Date         pgtype.Date `json:"date"`
	TimeIn       *string     `json:"time_in"`
	TimeOut      *string     `json:"time_out"`
	Status       pgtype.Text `json:"status"`
	WorkType     pgtype.Int4 `json:"work_type"`
	Notes        pgtype.Text `json:"notes"`
}

// decodeAttendanceLogRequest decodes and caps the body, rejecting unknown
// fields (A1 rule 2 extension — a company_id in the body is a 400, never a
// silent drop).
func decodeAttendanceLogRequest(w http.ResponseWriter, r *http.Request) (attendanceLogRequest, bool) {
	var req attendanceLogRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return attendanceLogRequest{}, false
	}
	return req, true
}

// parseClockTime parses an optional "HH:MM" or "HH:MM:SS" wall-clock string
// (the format <input type="time"> sends, hr_admin_panel.html att-in/att-out)
// into a pgtype.Time. pgtype.Time, unlike pgtype.Date/Text/Numeric, implements
// neither json.Marshaler nor json.Unmarshaler in pgx v5.11.0 — it would
// otherwise (de)serialize as its bare {"Microseconds":N,"Valid":bool} struct
// shape instead of a plain time string — so request/response conversion is
// done by hand here rather than relying on the generated struct's own JSON
// tag. Spec: "time_in/time_out are optional TIME values with format
// validation only — the server MUST NOT add an ordering constraint beyond
// current behavior."
func parseClockTime(w http.ResponseWriter, field string, raw *string) (pgtype.Time, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.Time{}, true
	}
	layout := "15:04"
	if strings.Count(*raw, ":") == 2 {
		layout = "15:04:05"
	}
	t, err := time.Parse(layout, *raw)
	if err != nil {
		writeFieldErr(w, map[string]string{field: "must be HH:MM or HH:MM:SS"})
		return pgtype.Time{}, false
	}
	usec := (int64(t.Hour())*3600 + int64(t.Minute())*60 + int64(t.Second())) * 1_000_000
	return pgtype.Time{Microseconds: usec, Valid: true}, true
}

// formatClockTime is parseClockTime's inverse for responses: nil when unset,
// otherwise "HH:MM" — matching what <input type="time"> originally sent and
// what every display/calc site (hwCalc, the attendance grid) expects to
// split on ':'.
func formatClockTime(t pgtype.Time) *string {
	if !t.Valid {
		return nil
	}
	usec := t.Microseconds
	h := usec / 3_600_000_000
	usec -= h * 3_600_000_000
	m := usec / 60_000_000
	s := fmt.Sprintf("%02d:%02d", h, m)
	return &s
}

// attendanceLogResponse mirrors the sqlc-generated row shape but carries
// time_in/time_out as plain "HH:MM" strings (or null) instead of
// pgtype.Time's bare struct, matching every frontend read site.
type attendanceLogResponse struct {
	ID           pgtype.UUID        `json:"id"`
	CompanyID    pgtype.UUID        `json:"company_id"`
	EmployeeID   pgtype.UUID        `json:"employee_id"`
	EmployeeName pgtype.Text        `json:"employee_name"`
	Department   pgtype.Text        `json:"department"`
	Date         pgtype.Date        `json:"date"`
	TimeIn       *string            `json:"time_in"`
	TimeOut      *string            `json:"time_out"`
	Status       pgtype.Text        `json:"status"`
	WorkType     pgtype.Int4        `json:"work_type"`
	Notes        pgtype.Text        `json:"notes"`
	CreatedAt    pgtype.Timestamptz `json:"created_at"`
}

// attendanceLogDBRow has the exact field name/type/order every
// sqlc-generated attendance_logs row type shares (GetAttendanceLogRow,
// ListAttendanceLogsRow, UpdateAttendanceLogRow, UpsertAttendanceLogRow).
// Go permits a direct type conversion between structs with identical field
// composition (struct tags are ignored for conversion), so each call site
// converts its own Row type to this one instead of four duplicated
// converters.
type attendanceLogDBRow struct {
	ID           pgtype.UUID
	CompanyID    pgtype.UUID
	EmployeeID   pgtype.UUID
	EmployeeName pgtype.Text
	Department   pgtype.Text
	Date         pgtype.Date
	TimeIn       pgtype.Time
	TimeOut      pgtype.Time
	Status       pgtype.Text
	WorkType     pgtype.Int4
	Notes        pgtype.Text
	CreatedAt    pgtype.Timestamptz
}

func toAttendanceLogResponse(row attendanceLogDBRow) attendanceLogResponse {
	return attendanceLogResponse{
		ID:           row.ID,
		CompanyID:    row.CompanyID,
		EmployeeID:   row.EmployeeID,
		EmployeeName: row.EmployeeName,
		Department:   row.Department,
		Date:         row.Date,
		TimeIn:       formatClockTime(row.TimeIn),
		TimeOut:      formatClockTime(row.TimeOut),
		Status:       row.Status,
		WorkType:     row.WorkType,
		Notes:        row.Notes,
		CreatedAt:    row.CreatedAt,
	}
}

// GET /api/attendance_logs?id=&employee_id=&date=&date_from=&date_to=&_limit=&_offset=
func (a *API) ListAttendanceLogs(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "date", "date_from", "date_to")
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
	dateFrom, err := optionalDateFilter(p.Filters["date_from"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid date_from")
		return
	}
	dateTo, err := optionalDateFilter(p.Filters["date_to"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid date_to")
		return
	}

	rows, err := a.Queries.ListAttendanceLogs(r.Context(), db.ListAttendanceLogsParams{
		CompanyID:  companyID, // $1 — from ctx, never from a query param
		Column2:    id,
		EmployeeID: employeeID,
		Date:       date,
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list attendance logs")
		return
	}

	resp := make([]attendanceLogResponse, len(rows))
	for i, row := range rows {
		resp[i] = toAttendanceLogResponse(attendanceLogDBRow(row))
	}
	writeRows(w, http.StatusOK, resp)
}

// GET /api/attendance_logs/{id}
func (a *API) GetAttendanceLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetAttendanceLog(r.Context(), db.GetAttendanceLogParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get attendance log")
		return
	}

	writeJSON(w, http.StatusOK, toAttendanceLogResponse(attendanceLogDBRow(row)))
}

// validateAttendanceLogCreate validates a POST body: employee_id and date are
// required (spec "Required fields rejected"), status defaults to "present"
// when absent (matching the column default and saveAttendance's own
// `status: gv('att-st')||'present'` fallback) and must be one of the 4
// canonical values otherwise, and time_in/time_out are format-validated.
func validateAttendanceLogCreate(w http.ResponseWriter, req attendanceLogRequest) (pgtype.UUID, pgtype.Text, pgtype.Time, pgtype.Time, bool) {
	timeIn, ok := parseClockTime(w, "time_in", req.TimeIn)
	if !ok {
		return pgtype.UUID{}, pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}
	timeOut, ok := parseClockTime(w, "time_out", req.TimeOut)
	if !ok {
		return pgtype.UUID{}, pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}

	fields := map[string]string{}

	employeeID, err := stringToUUID(req.EmployeeID)
	if req.EmployeeID == "" || err != nil {
		fields["employee_id"] = "required"
	}
	if !req.Date.Valid {
		fields["date"] = "required"
	}

	status, statusOK := normalizeAttendanceStatus(req.Status)
	if !statusOK {
		fields["status"] = "must be one of present, late, absent, permission"
	}

	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return pgtype.UUID{}, pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}

	return employeeID, status, timeIn, timeOut, true
}

// validateAttendanceLogUpdate validates a PATCH body. employee_id and date
// are deliberately not revalidated here: PATCH never changes them (see
// UpdateAttendanceLog's query comment).
func validateAttendanceLogUpdate(w http.ResponseWriter, req attendanceLogRequest) (pgtype.Text, pgtype.Time, pgtype.Time, bool) {
	timeIn, ok := parseClockTime(w, "time_in", req.TimeIn)
	if !ok {
		return pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}
	timeOut, ok := parseClockTime(w, "time_out", req.TimeOut)
	if !ok {
		return pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}

	status, ok := normalizeAttendanceStatus(req.Status)
	if !ok {
		writeFieldErr(w, map[string]string{"status": "must be one of present, late, absent, permission"})
		return pgtype.Text{}, pgtype.Time{}, pgtype.Time{}, false
	}

	return status, timeIn, timeOut, true
}

// normalizeAttendanceStatus defaults an absent/empty status to "present"
// (the column default) and rejects any explicitly-sent value outside the 4
// canonical statuses.
func normalizeAttendanceStatus(raw pgtype.Text) (pgtype.Text, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return pgtype.Text{String: "present", Valid: true}, true
	}
	if !validAttendanceStatuses[raw.String] {
		return pgtype.Text{}, false
	}
	return raw, true
}

// POST /api/attendance_logs — upsert on (employee_id,date) (design Q2):
// saveAttendance's own existing-row lookup, generateTestData's
// on_conflict=employee_id,date raw fetch, and Phase 6's ZKTeco ingestion all
// rely on this being idempotent per (employee_id,date), so a second create
// for the same pair updates the existing row instead of 409ing. Always 201
// on both the insert and the upsert-update branch — no call site reads the
// status code.
func (a *API) CreateAttendanceLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	req, ok := decodeAttendanceLogRequest(w, r)
	if !ok {
		return
	}

	employeeID, status, timeIn, timeOut, ok := validateAttendanceLogCreate(w, req)
	if !ok {
		return
	}

	// UpsertAttendanceLogParams.ID is the client-supplied employee_id (sqlc
	// named it from the query's `e.id = $2` predicate, not from the
	// attendance log's own row id — there is no attendance log id yet on
	// create).
	row, err := a.Queries.UpsertAttendanceLog(r.Context(), db.UpsertAttendanceLogParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		ID:        employeeID,
		Date:      req.Date,
		TimeIn:    timeIn,
		TimeOut:   timeOut,
		Status:    status,
		WorkType:  req.WorkType,
		Notes:     req.Notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "create attendance log")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in saveAttendance stay valid.
	writeRows(w, http.StatusCreated, []attendanceLogResponse{toAttendanceLogResponse(attendanceLogDBRow(row))})
}

// PATCH /api/attendance_logs/{id} — restricted to the operationally-editable
// fields (time_in, time_out, status, work_type, notes). employee_id and date
// are immutable here (design note on UpdateAttendanceLog): changing which
// employee or day a punch belongs to is a new record, not an edit.
func (a *API) UpdateAttendanceLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	req, ok := decodeAttendanceLogRequest(w, r)
	if !ok {
		return
	}

	status, timeIn, timeOut, ok := validateAttendanceLogUpdate(w, req)
	if !ok {
		return
	}

	row, err := a.Queries.UpdateAttendanceLog(r.Context(), db.UpdateAttendanceLogParams{
		CompanyID: companyID,
		ID:        id,
		TimeIn:    timeIn,
		TimeOut:   timeOut,
		Status:    status,
		WorkType:  req.WorkType,
		Notes:     req.Notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "update attendance log")
		return
	}

	writeRows(w, http.StatusOK, []attendanceLogResponse{toAttendanceLogResponse(attendanceLogDBRow(row))})
}

// DELETE /api/attendance_logs/{id} — hard delete (design Q3): a mistyped
// punch is an operational correction, not history worth preserving.
func (a *API) DeleteAttendanceLog(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteAttendanceLog(r.Context(), db.DeleteAttendanceLogParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete attendance log")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// optionalDateFilter parses an optional `date`/`date_from`/`date_to`
// equality filter (A4) by reusing pgtype.Date's own JSON unmarshaler, which
// already accepts a plain "2006-01-02" string. An empty string means "no
// filter" and is represented as an invalid pgtype.Date, which the
// `sqlc.narg('x')::date IS NULL OR ...` predicate treats as NULL.
func optionalDateFilter(raw string) (pgtype.Date, error) {
	if raw == "" {
		return pgtype.Date{}, nil
	}
	var d pgtype.Date
	quoted, err := json.Marshal(raw)
	if err != nil {
		return pgtype.Date{}, err
	}
	if err := d.UnmarshalJSON(quoted); err != nil {
		return pgtype.Date{}, err
	}
	return d, nil
}
