package handlers

import (
	"encoding/json"
	"net/http"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// emptyPermissions is the default JSONB value roles.permissions carries in
// the schema (DEFAULT '{}'). CreateRole always sends an explicit value so
// the handler never depends on the DB default being applied.
var emptyPermissions = json.RawMessage(`{}`)

// GET /api/roles
func (a *API) ListRoles(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListRoles(r.Context(), db.ListRolesParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		Column2:   id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list roles")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/roles/{id}
func (a *API) GetRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetRole(r.Context(), db.GetRoleParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get role")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// createRoleRequest deliberately has NO company_id field (A1 rule 2): the
// tenant cannot be bound from the body even if a client sends it.
// Permissions is optional on create; an absent value defaults to `{}`,
// mirroring the frontend's own `saveRole` call (which always sends `{}`).
type createRoleRequest struct {
	Name        string           `json:"name"`
	Permissions *json.RawMessage `json:"permissions"`
}

// POST /api/roles
func (a *API) CreateRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createRoleRequest
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

	permissions := emptyPermissions
	if req.Permissions != nil {
		permissions = *req.Permissions
	}

	row, err := a.Queries.CreateRole(r.Context(), db.CreateRoleParams{
		CompanyID:   companyID, // $1 — from ctx, never from req
		Name:        name,
		Permissions: permissions,
	})
	if err != nil {
		a.writeDBErr(w, err, "create role")
		return
	}

	// A3: writes return the affected row(s) as a JSON ARRAY, so the
	// Array.isArray(res) checks in the frontend stay valid.
	writeRows(w, http.StatusCreated, []db.Role{row})
}

// updateRoleRequest supports true partial PATCH semantics: the frontend has
// two independent call sites — updateRole (name-only) and saveRolePerms
// (permissions-only) — and each must leave the other field untouched
// (spec "Roles CRUD with permissions" requirement). A field is only updated
// when its key is present in the request body.
type updateRoleRequest struct {
	Name        *string          `json:"name"`
	Permissions *json.RawMessage `json:"permissions"`
}

// PATCH /api/roles/{id} — updates `name` and/or `permissions` independently.
// Unlike departments/positions/branches, roles has no TEXT-match
// relationship to employees (per spec), so no rename-propagation or
// RESTRICT-on-delete logic applies here.
func (a *API) UpdateRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updateRoleRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	current, err := a.Queries.GetRole(r.Context(), db.GetRoleParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}

	name := current.Name
	if req.Name != nil {
		trimmed, ok := validateLookupName(w, *req.Name)
		if !ok {
			return
		}
		name = trimmed
	}

	permissions := current.Permissions
	if req.Permissions != nil {
		permissions = *req.Permissions
	}

	row, err := a.Queries.UpdateRole(r.Context(), db.UpdateRoleParams{
		CompanyID:   companyID,
		ID:          id,
		Name:        name,
		Permissions: permissions,
	})
	if err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}

	writeRows(w, http.StatusOK, []db.Role{row})
}

// DELETE /api/roles/{id} — plain delete, no RESTRICT: roles has no
// TEXT-match relationship to employees (only users.role, a separate
// existing capability out of scope for this phase per spec).
func (a *API) DeleteRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteRole(r.Context(), db.DeleteRoleParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
