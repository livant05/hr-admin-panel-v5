package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"

	"github.com/livant05/rrhh-go/internal/auth"
)

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

// isTruthy reports whether a raw JSON value would be treated as "on" by the
// frontend's loose permission check: present, and neither null, false, a
// zero number, nor an empty string.
func isTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
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
