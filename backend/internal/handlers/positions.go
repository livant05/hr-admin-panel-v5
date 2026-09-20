package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/positions
func (a *API) ListPositions(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListPositions(r.Context(), db.ListPositionsParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		Column2:   id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list positions")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/positions/{id}
func (a *API) GetPosition(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetPosition(r.Context(), db.GetPositionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get position")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// createPositionRequest deliberately has NO company_id field (A1 rule 2):
// the tenant cannot be bound from the body even if a client sends it.
type createPositionRequest struct {
	Name string `json:"name"`
}

// POST /api/positions
func (a *API) CreatePosition(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createPositionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields() // a body carrying company_id is a 400, never ignored
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	name, ok := validateLookupName(w, req.Name)
	if !ok {
		return
	}

	row, err := a.Queries.CreatePosition(r.Context(), db.CreatePositionParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "create position")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in the frontend stay valid.
	writeRows(w, http.StatusCreated, []db.Position{row})
}

type updatePositionRequest struct {
	Name string `json:"name"`
}

// PATCH /api/positions/{id} — renames the position and propagates the new
// name to every employee row whose `position` TEXT column matched the old
// name, in one transaction (design P6.1). Employees has no FK to positions
// — the relationship is a denormalized name match — so this handler is the
// only place that keeps them in sync.
func (a *API) UpdatePosition(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updatePositionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	name, ok := validateLookupName(w, req.Name)
	if !ok {
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error("update position begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())

	q := a.Queries.WithTx(tx)

	before, err := q.GetPosition(r.Context(), db.GetPositionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "update position")
		return
	}

	row, err := q.UpdatePosition(r.Context(), db.UpdatePositionParams{
		CompanyID: companyID,
		ID:        id,
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "update position")
		return
	}

	if before.Name != name {
		if _, err := q.RenameEmployeePosition(r.Context(), db.RenameEmployeePositionParams{
			CompanyID:  companyID,
			Position:   pgtype.Text{String: before.Name, Valid: true},
			Position_2: pgtype.Text{String: name, Valid: true},
		}); err != nil {
			a.writeDBErr(w, err, "rename employee position")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("update position commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	writeRows(w, http.StatusOK, []db.Position{row})
}

// DELETE /api/positions/{id} — RESTRICT that the schema cannot express:
// employees.position is a plain TEXT column with no FK, so a delete that
// left dangling employee records would be invisible to Postgres. A delete
// is blocked with 409 while at least one employee still references the
// position's name (design P6.1).
func (a *API) DeletePosition(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	pos, err := a.Queries.GetPosition(r.Context(), db.GetPositionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete position")
		return
	}

	n, err := a.Queries.CountEmployeesInPosition(r.Context(), db.CountEmployeesInPositionParams{
		CompanyID: companyID,
		Position:  pgtype.Text{String: pos.Name, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "delete position")
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict,
			fmt.Sprintf("%d empleado(s) siguen asignados a este cargo", n))
		return
	}

	affected, err := a.Queries.DeletePosition(r.Context(), db.DeletePositionParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete position")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
