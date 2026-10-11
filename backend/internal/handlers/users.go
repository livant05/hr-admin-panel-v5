package handlers

import (
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
		Column2:   id,
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
