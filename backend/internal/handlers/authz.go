package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"

	"github.com/livant05/rrhh-go/internal/auth"
)

// requireAdmin gates authority-managing routes (users, roles writes, device
// claims) on the JWT role "admin" (design D5). It writes the A2 403 envelope
// and returns false for every other role, including custom roles that hold
// permission keys: those keys are frontend-defined, so letting a permission
// open these routes would let a role editor grant itself anything.
//
// Order inside an admin handler: a.tenant, then requireAdmin, then any body
// parsing or data access, so a non-admin learns nothing from validation
// errors.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if auth.RoleFromContext(r.Context()) == "admin" {
		return true
	}
	writeErrCode(w, http.StatusForbidden, codeForbidden, "admin only")
	return false
}

// requirePermission enforces server-side the permission the frontend only
// hides from the UI (applyPermissions, hr_admin_panel.html:2415). FAIL-CLOSED:
// unlike the frontend's hasPerm (hr_admin_panel.html:2410), which returns
// true for an undefined key, a missing role row or a missing/falsy
// permission key here writes the A2 403 envelope and returns false.
//
// The admin bypass is load-bearing: a fresh tenant has no `roles` rows, and
// without it the first admin could never approve anything (design Q4).
// Writes nothing and returns true without touching a.Queries when the JWT
// role is "admin".
func (a *API) requirePermission(w http.ResponseWriter, r *http.Request, companyID pgtype.UUID, key string) bool {
	role := auth.RoleFromContext(r.Context())
	if role == "admin" {
		return true
	}

	row, err := a.Queries.GetRoleByName(r.Context(), db.GetRoleByNameParams{
		CompanyID: companyID,
		Name:      role,
	})
	if !permissionGranted(row, err, key) {
		writeErrCode(w, http.StatusForbidden, codeForbidden, "insufficient permissions")
		return false
	}
	return true
}

// permissionGranted is the pure decision at the heart of requirePermission,
// separated so it is testable without a database: a lookup error --
// including pgx.ErrNoRows for a role with no matching roles row -- fails
// closed, and a present row grants access only when permissions[key] is
// present and truthy. Truthiness mirrors the frontend's own loose check for
// a permission flag (any non-zero/non-empty/non-null/non-false JSON value
// grants), but unlike the frontend this fails closed on an undefined key.
func permissionGranted(row db.Role, err error, key string) bool {
	if err != nil {
		return false
	}

	var permissions map[string]json.RawMessage
	if jsonErr := json.Unmarshal(row.Permissions, &permissions); jsonErr != nil {
		return false
	}

	raw, ok := permissions[key]
	if !ok {
		return false
	}
	return isTruthy(raw)
}

// isTruthy reports whether a raw JSON permission value would be treated as
// "on" by the frontend (applyPermissions / isPageAllowed): a scalar must be
// neither null, false, a zero number nor an empty string; a sub-permission
// object (or array) is on only when at least one of its values is truthy,
// so a module whose sub-keys are all off is denied.
func isTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch val := v.(type) {
	case map[string]any:
		for _, sub := range val {
			if jsTruthy(sub) {
				return true
			}
		}
		return false
	case []any:
		for _, sub := range val {
			if jsTruthy(sub) {
				return true
			}
		}
		return false
	default:
		return jsTruthy(val)
	}
}

// jsTruthy mirrors JavaScript's !!value for a decoded JSON value: objects and
// arrays are always truthy, scalars follow the rules isTruthy documents.
func jsTruthy(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case string:
		return val != ""
	case nil:
		return false
	default:
		return true
	}
}
