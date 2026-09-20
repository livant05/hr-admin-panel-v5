package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/livant05/rrhh-go/internal/db/generated"

	"github.com/livant05/rrhh-go/internal/auth"
)

type API struct {
	Queries *db.Queries
	Pool    *pgxpool.Pool
	Signer  *auth.Signer
	Log     *slog.Logger
}

func New(pool *pgxpool.Pool, signer *auth.Signer, log *slog.Logger) *API {
	return &API{
		Queries: db.New(pool),
		Pool:    pool,
		Signer:  signer,
		Log:     log,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GET /api/health — no auth, just pings the DB.
func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Pool.Ping(ctx); err != nil {
		writeErrCode(w, http.StatusServiceUnavailable, codeInternal, "db unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// POST /api/auth/login — email+password -> bcrypt compare -> JWT.
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeFieldErr(w, map[string]string{"email": "required", "password": "required"})
		return
	}

	user, err := a.Queries.GetUserByEmail(r.Context(), pgtype.Text{String: req.Email, Valid: true})
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid credentials")
		return
	}

	userID := uuidToString(user.ID)
	companyID := uuidToString(user.CompanyID)

	token, err := a.Signer.Sign(userID, companyID, user.Role)
	if err != nil {
		a.Log.Error("sign token", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "could not issue token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"user": map[string]any{
			"id":         userID,
			"company_id": companyID,
			"email":      req.Email,
			"role":       user.Role,
			"first_name": textOrEmpty(user.FirstName),
			"last_name":  textOrEmpty(user.LastName),
		},
	})
}

// GET /api/me — auth-protected, proves the JWT + context wiring works end to end.
func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	companyID := auth.CompanyIDFromContext(r.Context())

	uid, err := stringToUUID(userID)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid token subject")
		return
	}
	user, err := a.Queries.GetUserByID(r.Context(), uid)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "user not found")
		return
	}

	cid, err := stringToUUID(companyID)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid token company")
		return
	}
	company, err := a.Queries.GetCompanyByID(r.Context(), cid)
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "company not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         userID,
		"email":      textOrEmpty(user.Email),
		"role":       user.Role,
		"first_name": textOrEmpty(user.FirstName),
		"last_name":  textOrEmpty(user.LastName),
		"company": map[string]any{
			"id":   companyID,
			"name": company.Name,
		},
	})
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
