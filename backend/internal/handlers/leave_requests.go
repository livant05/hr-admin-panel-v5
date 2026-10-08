package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/leave_requests?id=&employee_id=&status=
func (a *API) ListLeaveRequests(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListLeaveRequests(r.Context(), db.ListLeaveRequestsParams{
		CompanyID:  companyID, // $1 — from ctx, never from a query param
		Column2:    id,
		EmployeeID: employeeID,
		Status:     optionalTextFilter(p.Filters["status"]),
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list leave requests")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/leave_requests/{id}
func (a *API) GetLeaveRequest(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetLeaveRequest(r.Context(), db.GetLeaveRequestParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get leave request")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// leaveRequestCreateRequest is the create contract saveLeaveReq sends
// (hr_admin_panel.html:2910). employee_name is declared so
// DisallowUnknownFields accepts a payload that includes it, but its value is
// never trusted: the server derives it from the employees row the
// tenant-scoped employee_id join resolves (A1 rule 6, design Q2), same
// defense as attendance_logs. days is accepted verbatim -- the spec is
// explicit that it is "computed client-side and passed through, not
// recomputed server-side."
type leaveRequestCreateRequest struct {
	EmployeeID   string      `json:"employee_id"`
	EmployeeName pgtype.Text `json:"employee_name"`
	Type         pgtype.Text `json:"type"`
	StartDate    pgtype.Date `json:"start_date"`
	EndDate      pgtype.Date `json:"end_date"`
	Days         pgtype.Int4 `json:"days"`
	Notes        pgtype.Text `json:"notes"`
}

// leaveRequestDefaultType mirrors the column default ('vacaciones') the DDL
// already declares, applied here because this INSERT lists columns
// explicitly rather than relying on the table default.
const leaveRequestDefaultType = "vacaciones"

// POST /api/leave_requests — ungated beyond tenant scoping (spec: "Creating
// a request stays ungated beyond tenant scoping — anyone with a token may
// file one, as today").
func (a *API) CreateLeaveRequest(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req leaveRequestCreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	fields := map[string]string{}
	employeeID, idErr := stringToUUID(req.EmployeeID)
	if req.EmployeeID == "" || idErr != nil {
		fields["employee_id"] = "required"
	}
	if !req.StartDate.Valid {
		fields["start_date"] = "required"
	}
	if !req.EndDate.Valid {
		fields["end_date"] = "required"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	leaveType := req.Type
	if !leaveType.Valid || strings.TrimSpace(leaveType.String) == "" {
		leaveType = pgtype.Text{String: leaveRequestDefaultType, Valid: true}
	}

	// CreateLeaveRequestParams.ID is the client-supplied employee_id (sqlc
	// named it from the query's `e.id = $2` predicate, matching the same
	// naming quirk documented in attendance_logs.go).
	row, err := a.Queries.CreateLeaveRequest(r.Context(), db.CreateLeaveRequestParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		ID:        employeeID,
		Type:      leaveType,
		StartDate: req.StartDate,
		EndDate:   req.EndDate,
		Days:      req.Days,
		Notes:     req.Notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "create leave request")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY.
	writeRows(w, http.StatusCreated, []db.LeaveRequest{row})
}

// leaveRequestStatusRequest is the PATCH contract approveLeave sends
// (hr_admin_panel.html:2918): {status}. Restricting the accepted fields to
// status/notes means start_date/end_date/type/employee_id are immutable
// after creation (design Q4).
type leaveRequestStatusRequest struct {
	Status pgtype.Text `json:"status"`
	Notes  pgtype.Text `json:"notes"`
}

var validLeaveRequestTargetStatuses = map[string]bool{"approved": true, "rejected": true}

// PATCH /api/leave_requests/{id} — approve/reject. Gated server-side by
// requirePermission(.., "vacations") (design Q4): an admin bypasses the
// lookup, any other role needs a truthy roles.permissions.vacations entry
// for its own JWT role, and a missing/falsy entry fails closed with 403.
//
// Guard order, matching the design's SQL-closed race: tenant -> decode and
// validate the target status -> requirePermission -> Get (tenant-scoped,
// 404 if absent/foreign) -> reject a non-pending current status with 409 ->
// UpdateLeaveRequestStatus, whose own `WHERE status='pending'` predicate
// closes the same race in SQL, mapped to 409 (not 404 — existence was
// already confirmed) if a concurrent request won it first.
func (a *API) UpdateLeaveRequest(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req leaveRequestStatusRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}
	if !req.Status.Valid || !validLeaveRequestTargetStatuses[req.Status.String] {
		writeFieldErr(w, map[string]string{"status": "must be one of approved, rejected"})
		return
	}

	if !a.requirePermission(w, r, companyID, "vacations") {
		return
	}

	current, err := a.Queries.GetLeaveRequest(r.Context(), db.GetLeaveRequestParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get leave request")
		return
	}
	if !current.Status.Valid || current.Status.String != "pending" {
		writeErrCode(w, http.StatusConflict, codeConflict, "leave request already decided")
		return
	}

	// approveLeave (hr_admin_panel.html:2918) sends only {status} — if an
	// absent notes field were written as-is, the unconditional SQL SET would
	// silently NULL out whatever saveLeaveReq stored at creation. Preserve
	// the current value when the request omits notes, matching
	// updateRoleRequest's "leave the other field untouched" precedent
	// (roles.go) for an independently-settable field.
	notes := current.Notes
	if req.Notes.Valid {
		notes = req.Notes
	}

	row, err := a.Queries.UpdateLeaveRequestStatus(r.Context(), db.UpdateLeaveRequestStatusParams{
		CompanyID: companyID,
		ID:        id,
		Status:    req.Status,
		Notes:     notes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Existence was already confirmed above; zero rows affected here
			// means a concurrent request won the race and moved status away
			// from 'pending' between the Get and this UPDATE.
			writeErrCode(w, http.StatusConflict, codeConflict, "leave request already decided")
			return
		}
		a.writeDBErr(w, err, "update leave request")
		return
	}

	writeRows(w, http.StatusOK, []db.LeaveRequest{row})
}

// DELETE /api/leave_requests/{id} — design Q3 addendum: hard delete, but
// only while status='pending'; 409 once a request has been decided. No UI
// delete exists for a decided request (hr_admin_panel.html:2901 offers only
// approve/reject on a pending row), so this is a cancel-my-own-request
// affordance -- an approved leave is the record behind a paid absence, same
// reasoning as the non-pending PATCH 409.
//
// Guard order mirrors UpdateLeaveRequest: tenant -> Get (tenant-scoped, 404
// if absent/foreign) -> reject a non-pending current status with 409 ->
// DeleteLeaveRequest, whose own `WHERE status='pending'` predicate closes
// the same race in SQL, mapped to 409 (not 404 -- existence was already
// confirmed) if a concurrent PATCH/DELETE won first.
func (a *API) DeleteLeaveRequest(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	current, err := a.Queries.GetLeaveRequest(r.Context(), db.GetLeaveRequestParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get leave request")
		return
	}
	if !current.Status.Valid || current.Status.String != "pending" {
		writeErrCode(w, http.StatusConflict, codeConflict, "leave request already decided")
		return
	}

	affected, err := a.Queries.DeleteLeaveRequest(r.Context(), db.DeleteLeaveRequestParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete leave request")
		return
	}
	if affected == 0 {
		// Existence was already confirmed above; zero rows affected here
		// means a concurrent PATCH/DELETE won the race and moved status
		// away from 'pending' between the Get and this DELETE.
		writeErrCode(w, http.StatusConflict, codeConflict, "leave request already decided")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
