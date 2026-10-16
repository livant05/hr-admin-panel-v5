package handlers

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	db "github.com/livant05/rrhh-go/internal/db/generated"

	"github.com/livant05/rrhh-go/internal/auth"
)

// maxCompanyName caps company_name in runes (AUTH-6, design D14).
const maxCompanyName = 200

// registerRequest has no role, status or company_id field: the first user of a
// new company is always an active admin, and DisallowUnknownFields (via
// decodeAuthBody) turns any such key into a 400.
type registerRequest struct {
	CompanyName string `json:"company_name"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

// POST /api/auth/register — public, behind the shared AuthLimiter (routes.go).
//
// Creates a company and its first admin in one transaction and answers with
// the same payload as login (201, auto-login). The password is hashed BEFORE
// the transaction so no row lock is held during bcrypt. A duplicate email
// (any case, any company) hits the unique lower(email) index, rolls the whole
// transaction back so no company remains, and answers 409.
func (a *API) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeAuthBody(w, r, &req) {
		return
	}

	company := strings.TrimSpace(req.CompanyName)
	firstName := strings.TrimSpace(req.FirstName)
	lastName := strings.TrimSpace(req.LastName)
	email := normalizeEmail(req.Email)

	fields := map[string]string{}
	if n := utf8.RuneCountInString(company); n < 1 || n > maxCompanyName {
		fields["company_name"] = "required, at most 200 characters"
	}
	if n := utf8.RuneCountInString(firstName); n < 1 || n > maxUserName {
		fields["first_name"] = "required, at most 100 characters"
	}
	if utf8.RuneCountInString(lastName) > maxUserName {
		fields["last_name"] = "at most 100 characters"
	}
	// A NUL would fail the insert with a 500; any other control character
	// (newline, bell...) would be stored in a name shown across the panel.
	for name, v := range map[string]string{"company_name": company, "first_name": firstName, "last_name": lastName} {
		if _, bad := fields[name]; !bad && strings.IndexFunc(v, unicode.IsControl) >= 0 {
			fields[name] = "must not contain control characters"
		}
	}
	if !plausibleEmail(email) {
		fields["email"] = "required, a valid address of at most 254 characters"
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		fields["password"] = err.Error()
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.Log.Error("hash registration password", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	ctx := r.Context()
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		a.writeDBErr(w, err, "begin register")
		return
	}
	defer tx.Rollback(ctx) // no-op after Commit
	q := a.Queries.WithTx(tx)

	co, err := q.CreateCompany(ctx, company)
	if err != nil {
		a.writeDBErr(w, err, "register: create company")
		return
	}
	user, err := q.CreateAdminUser(ctx, db.CreateAdminUserParams{
		CompanyID:    co.ID,
		Email:        optionalText(email),
		FirstName:    optionalText(firstName),
		LastName:     optionalText(lastName),
		PasswordHash: hash,
	})
	if err != nil {
		a.writeDBErr(w, err, "register: create admin") // 23505 -> 409; deferred Rollback drops the company
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.writeDBErr(w, err, "commit register")
		return
	}

	a.writeSession(w, http.StatusCreated, sessionUser{
		ID:          uuidToString(user.ID),
		CompanyID:   uuidToString(user.CompanyID),
		CompanyName: co.Name,
		Email:       textOrEmpty(user.Email),
		Role:        user.Role,
		FirstName:   textOrEmpty(user.FirstName),
		LastName:    textOrEmpty(user.LastName),
	})
}
