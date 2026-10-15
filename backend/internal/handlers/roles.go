package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

// POST /api/roles — admin only (UM-5): permission keys control authority.
func (a *API) CreateRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
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
	if !ok || !allowedRoleName(w, name) {
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

// PATCH /api/roles/{id} — admin only (UM-5); updates `name` and/or `permissions` independently.
// users.role holds the role NAME and permissions are looked up by it, so a
// rename also renames the company's users holding the old name, in the same
// transaction: without it they would silently lose every module.
func (a *API) UpdateRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
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

	var newName string
	if req.Name != nil {
		trimmed, ok := validateLookupName(w, *req.Name)
		if !ok || !allowedRoleName(w, trimmed) {
			return
		}
		newName = trimmed
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}
	defer tx.Rollback(r.Context())
	q := a.Queries.WithTx(tx)

	current, err := q.GetRoleForUpdate(r.Context(), db.GetRoleForUpdateParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}

	name := current.Name
	if req.Name != nil {
		name = newName
	}
	permissions := current.Permissions
	if req.Permissions != nil {
		permissions = *req.Permissions
	}

	row, err := q.UpdateRole(r.Context(), db.UpdateRoleParams{
		CompanyID:   companyID,
		ID:          id,
		Name:        name,
		Permissions: permissions,
	})
	if err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}
	if name != current.Name {
		if _, err := q.RenameUsersRole(r.Context(), db.RenameUsersRoleParams{CompanyID: companyID, OldName: current.Name, NewName: name}); err != nil {
			a.writeDBErr(w, err, "update role: rename users")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		a.writeDBErr(w, err, "update role")
		return
	}

	writeRows(w, http.StatusOK, []db.Role{row})
}

// DELETE /api/roles/{id} — admin only (UM-5). A role some user still holds is
// a 409 with the count: deleting it would leave those users with no module.
func (a *API) DeleteRole(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}
	defer tx.Rollback(r.Context())
	q := a.Queries.WithTx(tx)

	role, err := q.GetRoleForUpdate(r.Context(), db.GetRoleForUpdateParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}
	n, err := q.CountUsersWithRole(r.Context(), db.CountUsersWithRoleParams{CompanyID: companyID, Name: role.Name})
	if err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict, fmt.Sprintf("%d usuario(s) tienen este rol", n))
		return
	}
	if _, err := q.DeleteRole(r.Context(), db.DeleteRoleParams{CompanyID: companyID, ID: id}); err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		a.writeDBErr(w, err, "delete role")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// allowedRoleName rejects "admin" in any case: it is the built-in role every
// permission check bypasses on, so a roles row with that name would turn its
// users into administrators. Writes the 400 and returns false.
func allowedRoleName(w http.ResponseWriter, name string) bool {
	if strings.EqualFold(name, "admin") {
		writeFieldErr(w, map[string]string{"name": "reserved name"})
		return false
	}
	return true
}
