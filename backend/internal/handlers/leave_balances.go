package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/leave_balances?id=&employee_id=&year=
//
// leave_balances has no POST and no DELETE (design Q3): nothing in the UI
// creates or deletes a balance, and every row is owned by the accrual job
// (AccrueVacationDays, scheduler lands in slice 2c2), which recomputes
// earned_days from scratch on every run.
func (a *API) ListLeaveBalances(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "year")
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
	year, err := optionalIntFilter(p.Filters["year"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid year")
		return
	}

	rows, err := a.Queries.ListLeaveBalances(r.Context(), db.ListLeaveBalancesParams{
		CompanyID:  companyID, // $1 — from ctx, never from a query param
		Column2:    id,
		EmployeeID: employeeID,
		Year:       year,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list leave balances")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/leave_balances/{id}
func (a *API) GetLeaveBalance(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetLeaveBalance(r.Context(), db.GetLeaveBalanceParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get leave balance")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// leaveBalanceUpdateRequest is the single field PATCH may set (design Q3):
// earned_days is accrual-job-owned and would be silently reverted on the
// next scheduled run, and remaining_days is a GENERATED column. used_days is
// the one balance field the accrual job's own ON CONFLICT clause explicitly
// does not touch, which is exactly why it is the one field exposed here.
type leaveBalanceUpdateRequest struct {
	UsedDays pgtype.Numeric `json:"used_days"`
}

// PATCH /api/leave_balances/{id} — {used_days} ONLY.
func (a *API) UpdateLeaveBalance(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req leaveBalanceUpdateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}
	if !req.UsedDays.Valid {
		writeFieldErr(w, map[string]string{"used_days": "required"})
		return
	}

	row, err := a.Queries.UpdateLeaveBalance(r.Context(), db.UpdateLeaveBalanceParams{
		CompanyID: companyID,
		ID:        id,
		UsedDays:  req.UsedDays,
	})
	if err != nil {
		a.writeDBErr(w, err, "update leave balance")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY.
	writeRows(w, http.StatusOK, []db.LeaveBalance{row})
}

// POST /api/leave_balances/accrue — admin-gated manual trigger (design Q5c;
// spec "Both a ticker and a manual endpoint"). Synchronous, tenant-scoped
// via Scheduler.AccrueCompany -- never AccrueAll, whose non-nullable
// companyID parameter makes the cross-tenant path structurally unreachable
// from here. Gated the same way as leave_requests' approve/reject
// (requirePermission(.., "vacations")): an admin always passes; any other
// role needs a truthy roles.permissions.vacations entry, matching the
// design's own choice to reuse Q4's mechanism rather than a stricter
// admin-only check.
func (a *API) AccrueLeaveBalances(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	if !a.requirePermission(w, r, companyID, "vacations") {
		return
	}

	rows, err := a.Scheduler.AccrueCompany(r.Context(), companyID)
	if err != nil {
		a.writeDBErr(w, err, "accrue leave balances")
		return
	}

	// A3: writes return the affected row(s) as a JSON array.
	writeRows(w, http.StatusOK, rows)
}

// optionalIntFilter parses an optional `year` equality filter (A4). An empty
// string means "no filter" and is represented as an invalid pgtype.Int4,
// which the `sqlc.narg('year')::int IS NULL OR ...` predicate treats as
// NULL. This is the first int4 list filter in the codebase (every prior
// table's filters are uuid/text/date), introduced here for leave_balances'
// `year` column.
func optionalIntFilter(raw string) (pgtype.Int4, error) {
	if raw == "" {
		return pgtype.Int4{}, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return pgtype.Int4{}, err
	}
	return pgtype.Int4{Int32: int32(n), Valid: true}, nil
}
