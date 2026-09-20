package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/branches
func (a *API) ListBranches(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListBranches(r.Context(), db.ListBranchesParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		Column2:   id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list branches")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/branches/{id}
func (a *API) GetBranch(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetBranch(r.Context(), db.GetBranchParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get branch")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// createBranchRequest deliberately has NO company_id field (A1 rule 2): the
// tenant cannot be bound from the body even if a client sends it.
type createBranchRequest struct {
	Name string `json:"name"`
}

// POST /api/branches
func (a *API) CreateBranch(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createBranchRequest
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

	row, err := a.Queries.CreateBranch(r.Context(), db.CreateBranchParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "create branch")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in the frontend stay valid.
	writeRows(w, http.StatusCreated, []db.Branch{row})
}

type updateBranchRequest struct {
	Name string `json:"name"`
}

// PATCH /api/branches/{id} — renames the branch and propagates the new name
// to every employee row whose `branch` TEXT column matched the old name, in
// one transaction (design P6.1). Employees has no FK to branches — the
// relationship is a denormalized name match — so this handler is the only
// place that keeps them in sync.
func (a *API) UpdateBranch(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updateBranchRequest
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
		a.Log.Error("update branch begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())

	q := a.Queries.WithTx(tx)

	before, err := q.GetBranch(r.Context(), db.GetBranchParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "update branch")
		return
	}

	row, err := q.UpdateBranch(r.Context(), db.UpdateBranchParams{
		CompanyID: companyID,
		ID:        id,
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "update branch")
		return
	}

	if before.Name != name {
		if _, err := q.RenameEmployeeBranch(r.Context(), db.RenameEmployeeBranchParams{
			CompanyID: companyID,
			Branch:    pgtype.Text{String: before.Name, Valid: true},
			Branch_2:  pgtype.Text{String: name, Valid: true},
		}); err != nil {
			a.writeDBErr(w, err, "rename employee branch")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("update branch commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	writeRows(w, http.StatusOK, []db.Branch{row})
}

// DELETE /api/branches/{id} — RESTRICT that the schema cannot express:
// employees.branch is a plain TEXT column with no FK, so a delete that left
// dangling employee records would be invisible to Postgres. A delete is
// blocked with 409 while at least one employee still references the
// branch's name (design P6.1).
func (a *API) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	branch, err := a.Queries.GetBranch(r.Context(), db.GetBranchParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete branch")
		return
	}

	n, err := a.Queries.CountEmployeesInBranch(r.Context(), db.CountEmployeesInBranchParams{
		CompanyID: companyID,
		Branch:    pgtype.Text{String: branch.Name, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "delete branch")
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict,
			fmt.Sprintf("%d empleado(s) siguen asignados a esta sucursal", n))
		return
	}

	affected, err := a.Queries.DeleteBranch(r.Context(), db.DeleteBranchParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete branch")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
