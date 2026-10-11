package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/livant05/rrhh-go/internal/auth"
	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// System roles every company has without a roles row (the users.role column
// default and the bootstrap admin). Any other role must exist in the caller's
// own roles table (design D6).
const (
	roleAdmin    = "admin"
	roleEmpleado = "empleado"
)

// Values of users.status.
const (
	statusActive   = "active"
	statusInactive = "inactive"
)

const (
	maxUserEmail = 254
	maxUserName  = 100
)

// userResponse is the only shape a user leaves the API in (design D6). It has
// no password field of any kind; the sqlc queries behind it do not select
// password_hash either, so the hash cannot reach this type.
type userResponse struct {
	ID        pgtype.UUID        `json:"id"`
	CompanyID pgtype.UUID        `json:"company_id"`
	Email     pgtype.Text        `json:"email"`
	Role      string             `json:"role"`
	FirstName pgtype.Text        `json:"first_name"`
	LastName  pgtype.Text        `json:"last_name"`
	Status    string             `json:"status"`
	CreatedAt pgtype.Timestamptz `json:"created_at"`
}

// createdUserResponse is userResponse plus the one-time temporary password.
type createdUserResponse struct {
	userResponse
	TemporaryPassword string `json:"temporary_password"`
}

// GET /api/users — admin only; filters id, role, status.
func (a *API) ListUsers(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	p, ok := parseList(w, r, "id", "role", "status")
	if !ok {
		return
	}
	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListUsers(r.Context(), db.ListUsersParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		ID:        id,
		Role:      optionalTextFilter(p.Filters["role"]),
		Status:    optionalTextFilter(p.Filters["status"]),
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list users")
		return
	}

	out := make([]userResponse, len(rows))
	for i, row := range rows {
		out[i] = userResponse(row)
	}
	writeRows(w, http.StatusOK, out)
}

// GET /api/users/{id} — admin only; another company's id is 404.
func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
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

	row, err := a.Queries.GetUser(r.Context(), db.GetUserParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get user")
		return
	}
	writeJSON(w, http.StatusOK, userResponse(row))
}

// createUserRequest deliberately has no company_id, id, password or
// password_hash field: the tenant comes from the token, the credential is
// generated here, and DisallowUnknownFields turns any such key into a 400.
type createUserRequest struct {
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Role      string `json:"role"`
	Status    string `json:"status"`
}

// POST /api/users — admin only. The server generates a temporary password,
// stores only its bcrypt hash and returns the plaintext once in the 201 body.
func (a *API) CreateUser(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	var req createUserRequest
	if !decodeAuthBody(w, r, &req) {
		return
	}

	email := normalizeEmail(req.Email)
	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = roleEmpleado
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "active"
	}

	fields := map[string]string{}
	if !plausibleEmail(email) {
		fields["email"] = "required, a valid address of at most 254 characters"
	}
	if utf8.RuneCountInString(firstName) > maxUserName {
		fields["first_name"] = "at most 100 characters"
	}
	if utf8.RuneCountInString(lastName) > maxUserName {
		fields["last_name"] = "at most 100 characters"
	}
	if status != "active" && status != "inactive" {
		fields["status"] = "must be active or inactive"
	}
	if len(fields) == 0 {
		known, err := a.roleAssignable(r, companyID, role)
		if err != nil {
			a.writeDBErr(w, err, "validate user role")
			return
		}
		if !known {
			fields["role"] = "must be admin, empleado or an existing role"
		}
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	password, err := auth.GenerateTempPassword()
	if err != nil {
		a.Log.Error("generate temporary password", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		a.Log.Error("hash temporary password", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	row, err := a.Queries.CreateUser(r.Context(), db.CreateUserParams{
		CompanyID:    companyID, // $1 — from ctx, never from req
		Email:        pgtype.Text{String: email, Valid: true},
		Role:         role,
		FirstName:    optionalText(firstName),
		LastName:     optionalText(lastName),
		Status:       status,
		PasswordHash: hash,
	})
	if err != nil {
		a.writeDBErr(w, err, "create user") // 23505 on lower(email) -> 409
		return
	}

	// The body carries a credential: it must never sit in a shared cache.
	w.Header().Set("Cache-Control", "no-store")
	writeRows(w, http.StatusCreated, []createdUserResponse{{
		userResponse:      userResponse(row),
		TemporaryPassword: password,
	}})
}

// updateUserRequest is all-optional: a nil field is left as it is. It has no
// company_id, id, password or password_hash field, so DisallowUnknownFields
// turns any attempt to set them into a 400. Passwords are never changed here.
type updateUserRequest struct {
	Email     *string `json:"email"`
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	Role      *string `json:"role"`
	Status    *string `json:"status"`
}

// errLastActiveAdmin is returned when a change would leave a company with no
// active admin (UM-3, UM-4).
var errLastActiveAdmin = errors.New("company must keep at least one active admin")

// userChange is a planned mutation of a locked user row: the role and status
// the row will have afterwards (for the last-admin guard) and the write that
// produces them. A nil write means "nothing to store".
type userChange struct {
	role, status string
	write        func(q *db.Queries) (userResponse, error)
}

// changeUser runs one user mutation under the last-active-admin guard.
//
// Locking order is the whole point (design D6). The company's active admins
// are locked first, in id order, and the target row second. Counting admins
// without locks lets two requests that each demote a different admin both see
// "two admins" and both commit; locking the target first would let the same two
// requests wait on each other's row. With one global order, concurrent
// requests serialize on the first admin row and the loser re-reads the
// committed state when its lock is granted.
func (a *API) changeUser(ctx context.Context, companyID, id pgtype.UUID, plan func(cur db.GetUserForUpdateRow) userChange) (userResponse, error) {
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return userResponse{}, err
	}
	defer tx.Rollback(ctx)
	q := a.Queries.WithTx(tx)

	admins, err := q.LockActiveAdmins(ctx, companyID)
	if err != nil {
		return userResponse{}, err
	}
	cur, err := q.GetUserForUpdate(ctx, db.GetUserForUpdateParams{CompanyID: companyID, ID: id})
	if err != nil {
		return userResponse{}, err // pgx.ErrNoRows: absent or another tenant's -> 404
	}

	change := plan(cur)
	wasActiveAdmin := cur.Role == roleAdmin && cur.Status == statusActive
	staysActiveAdmin := change.role == roleAdmin && change.status == statusActive
	if wasActiveAdmin && !staysActiveAdmin && len(admins) <= 1 {
		return userResponse{}, errLastActiveAdmin
	}

	out := userResponse(cur)
	if change.write != nil {
		if out, err = change.write(q); err != nil {
			return userResponse{}, err
		}
	}
	return out, tx.Commit(ctx)
}

// writeUserChangeErr maps changeUser failures: the guard is a 409 conflict,
// everything else follows the shared database mapping.
func (a *API) writeUserChangeErr(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, errLastActiveAdmin) {
		writeErrCode(w, http.StatusConflict, codeConflict, errLastActiveAdmin.Error())
		return
	}
	a.writeDBErr(w, err, op)
}

// PATCH /api/users/{id} — admin only. Changes name, email, role or status
// within the caller's company; refuses any change that would leave it without
// an active admin.
func (a *API) UpdateUser(w http.ResponseWriter, r *http.Request) {
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

	var req updateUserRequest
	if !decodeAuthBody(w, r, &req) {
		return
	}

	fields := map[string]string{}
	var email, firstName, lastName, role, status string
	if req.Email != nil {
		if email = normalizeEmail(*req.Email); !plausibleEmail(email) {
			fields["email"] = "a valid address of at most 254 characters"
		}
	}
	if req.FirstName != nil {
		if firstName = strings.TrimSpace(*req.FirstName); utf8.RuneCountInString(firstName) > maxUserName {
			fields["first_name"] = "at most 100 characters"
		}
	}
	if req.LastName != nil {
		if lastName = strings.TrimSpace(*req.LastName); utf8.RuneCountInString(lastName) > maxUserName {
			fields["last_name"] = "at most 100 characters"
		}
	}
	if req.Status != nil {
		if status = strings.TrimSpace(*req.Status); status != statusActive && status != statusInactive {
			fields["status"] = "must be active or inactive"
		}
	}
	if req.Role != nil {
		role = strings.TrimSpace(*req.Role)
		if role == "" {
			fields["role"] = "must be admin, empleado or an existing role"
		}
	}
	if len(fields) == 0 && req.Role != nil {
		known, err := a.roleAssignable(r, companyID, role)
		if err != nil {
			a.writeDBErr(w, err, "validate user role")
			return
		}
		if !known {
			fields["role"] = "must be admin, empleado or an existing role"
		}
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := a.changeUser(r.Context(), companyID, id, func(cur db.GetUserForUpdateRow) userChange {
		p := db.UpdateUserParams{
			CompanyID: companyID, // $1 — from ctx, never from req
			ID:        id,
			Email:     cur.Email,
			Role:      cur.Role,
			FirstName: cur.FirstName,
			LastName:  cur.LastName,
			Status:    cur.Status,
		}
		if req.Email != nil {
			p.Email = pgtype.Text{String: email, Valid: true}
		}
		if req.FirstName != nil {
			p.FirstName = optionalText(firstName)
		}
		if req.LastName != nil {
			p.LastName = optionalText(lastName)
		}
		if req.Role != nil {
			p.Role = role
		}
		if req.Status != nil {
			p.Status = status
		}
		return userChange{role: p.Role, status: p.Status, write: func(q *db.Queries) (userResponse, error) {
			updated, err := q.UpdateUser(r.Context(), p) // 23505 on lower(email) -> 409
			return userResponse(updated), err
		}}
	})
	if err != nil {
		a.writeUserChangeErr(w, err, "update user")
		return
	}
	writeRows(w, http.StatusOK, []userResponse{row})
}

// DELETE /api/users/{id} — admin only. Soft delete: the row stays with
// status 'inactive' and can no longer log in. Deleting an already-inactive
// user succeeds without writing; deleting the last active admin is a 409.
func (a *API) DeleteUser(w http.ResponseWriter, r *http.Request) {
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

	_, err = a.changeUser(r.Context(), companyID, id, func(cur db.GetUserForUpdateRow) userChange {
		if cur.Status == statusInactive {
			return userChange{role: cur.Role, status: cur.Status} // idempotent: nothing to write
		}
		return userChange{role: cur.Role, status: statusInactive, write: func(q *db.Queries) (userResponse, error) {
			row, err := q.DeactivateUser(r.Context(), db.DeactivateUserParams{CompanyID: companyID, ID: id})
			return userResponse(row), err
		}}
	})
	if err != nil {
		a.writeUserChangeErr(w, err, "delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// roleAssignable reports whether role may be given to a user of companyID:
// one of the system roles, or the exact name of one of the company's roles.
func (a *API) roleAssignable(r *http.Request, companyID pgtype.UUID, role string) (bool, error) {
	if role == roleAdmin || role == roleEmpleado {
		return true, nil
	}
	_, err := a.Queries.GetRoleByName(r.Context(), db.GetRoleByNameParams{CompanyID: companyID, Name: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// plausibleEmail is a cheap structural check (the unique index and the
// login flow are the real gates): one @ with text on both sides, within the
// 254-character address limit.
func plausibleEmail(email string) bool {
	if email == "" || len(email) > maxUserEmail || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && strings.Count(email, "@") == 1
}

// optionalText stores an empty string as NULL.
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
