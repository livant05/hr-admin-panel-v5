package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// GET /api/departments
func (a *API) ListDepartments(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListDepartments(r.Context(), db.ListDepartmentsParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		Column2:   id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list departments")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/departments/{id}
func (a *API) GetDepartment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetDepartment(r.Context(), db.GetDepartmentParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get department")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// createDepartmentRequest deliberately has NO company_id field (A1 rule 2):
// the tenant cannot be bound from the body even if a client sends it.
type createDepartmentRequest struct {
	Name string `json:"name"`
}

// POST /api/departments
func (a *API) CreateDepartment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createDepartmentRequest
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

	row, err := a.Queries.CreateDepartment(r.Context(), db.CreateDepartmentParams{
		CompanyID: companyID, // $1 — from ctx, never from req
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "create department")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in the frontend stay valid.
	writeRows(w, http.StatusCreated, []db.Department{row})
}

type updateDepartmentRequest struct {
	Name string `json:"name"`
}

// PATCH /api/departments/{id} — renames the department and propagates the
// new name to every employee row whose `department` TEXT column matched the
// old name, in one transaction (design P6.1). Employees has no FK to
// departments — the relationship is a denormalized name match — so this
// handler is the only place that keeps them in sync.
func (a *API) UpdateDepartment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updateDepartmentRequest
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
		a.Log.Error("update department begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())

	q := a.Queries.WithTx(tx)

	before, err := q.GetDepartment(r.Context(), db.GetDepartmentParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "update department")
		return
	}

	row, err := q.UpdateDepartment(r.Context(), db.UpdateDepartmentParams{
		CompanyID: companyID,
		ID:        id,
		Name:      name,
	})
	if err != nil {
		a.writeDBErr(w, err, "update department")
		return
	}

	if before.Name != name {
		if _, err := q.RenameEmployeeDepartment(r.Context(), db.RenameEmployeeDepartmentParams{
			CompanyID:    companyID,
			Department:   pgtype.Text{String: before.Name, Valid: true},
			Department_2: pgtype.Text{String: name, Valid: true},
		}); err != nil {
			a.writeDBErr(w, err, "rename employee department")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("update department commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	writeRows(w, http.StatusOK, []db.Department{row})
}

// DELETE /api/departments/{id} — RESTRICT that the schema cannot express:
// employees.department is a plain TEXT column with no FK, so a delete that
// left dangling employee records would be invisible to Postgres. A delete
// is blocked with 409 while at least one employee still references the
// department's name (design P6.1).
func (a *API) DeleteDepartment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	dep, err := a.Queries.GetDepartment(r.Context(), db.GetDepartmentParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete department")
		return
	}

	n, err := a.Queries.CountEmployeesInDepartment(r.Context(), db.CountEmployeesInDepartmentParams{
		CompanyID:  companyID,
		Department: pgtype.Text{String: dep.Name, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "delete department")
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict,
			fmt.Sprintf("%d empleado(s) siguen asignados a este departamento", n))
		return
	}

	affected, err := a.Queries.DeleteDepartment(r.Context(), db.DeleteDepartmentParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete department")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// validateLookupName trims and validates a lookup table's required `name`
// field, shared by departments/positions/branches (design P2 "Lookup
// tables" requirement).
func validateLookupName(w http.ResponseWriter, raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		writeFieldErr(w, map[string]string{"name": "required, max 120 characters"})
		return "", false
	}
	return name, true
}

// optionalUUIDFilter parses an optional `id` equality filter (A4). An empty
// string means "no filter" and is represented as an invalid pgtype.UUID,
// which the `$2::uuid IS NULL OR id = $2` SQL predicate treats as NULL.
func optionalUUIDFilter(raw string) (pgtype.UUID, error) {
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	return stringToUUID(raw)
}
