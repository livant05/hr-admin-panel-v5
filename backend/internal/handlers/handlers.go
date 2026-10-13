package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/livant05/rrhh-go/internal/db/generated"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/ratelimit"
	"github.com/livant05/rrhh-go/internal/scheduler"
)

type API struct {
	Queries *db.Queries
	Pool    *pgxpool.Pool
	Signer  *auth.Signer
	Log     *slog.Logger

	// Scheduler is optional (nil in most tests): AccrueLeaveBalances (POST
	// /api/leave_balances/accrue, design Q5c) needs it, but handlers.New
	// deliberately does not require one at construction time, since every
	// pre-Phase-2c2 call site (and most Phase 2 handler tests) has no
	// scheduler to wire. Set it explicitly after New, as cmd/api/main.go and
	// testutil.ServerWithScheduler both do.
	Scheduler *scheduler.Scheduler

	// PasswordCheck is a test hook for the login password comparison (nil
	// means auth.CheckPassword). Tests set it, like Scheduler, to assert how
	// many comparisons a request performs and with which hash.
	PasswordCheck func(hash, plain string) bool

	// AuthLimiter throttles the public auth endpoints per client IP; login
	// (and register, once it exists) share this one instance so they share one
	// bucket. ADMSLimiter throttles the device ingestion endpoints. New
	// installs both with the production defaults; main overrides them from the
	// environment and tests replace them with fake-clock instances.
	AuthLimiter, ADMSLimiter *ratelimit.Limiter

	// AuthAttemptLimiter is the looser per-client budget that counts EVERY
	// login attempt, successful or not. AuthLimiter only charges failures
	// (decision #911), so without this cap a client could force unbounded
	// bcrypt work with valid credentials.
	AuthAttemptLimiter *ratelimit.Limiter

	// TrustedProxies lists the reverse-proxy prefixes whose X-Real-IP header
	// is believed when keying the limiters. Empty trusts nobody.
	TrustedProxies []netip.Prefix
}

// Production defaults for the public-endpoint limiters (requests per minute,
// also used as the burst). The ADMS figure is provisional until a real-device
// check (design D1, OPS-1). DefaultAuthAttemptRatePerMin sizes the looser
// login budget that counts successes as well (CO-S1b-2).
const (
	DefaultAuthRatePerMin        = 10
	DefaultAuthAttemptRatePerMin = 60
	DefaultADMSRatePerMin        = 600
)

func New(pool *pgxpool.Pool, signer *auth.Signer, log *slog.Logger) *API {
	return &API{
		Queries:     db.New(pool),
		Pool:        pool,
		Signer:      signer,
		Log:         log,
		AuthLimiter: ratelimit.New(DefaultAuthRatePerMin, DefaultAuthRatePerMin),
		ADMSLimiter: ratelimit.New(DefaultADMSRatePerMin, DefaultADMSRatePerMin),

		AuthAttemptLimiter: ratelimit.New(DefaultAuthAttemptRatePerMin, DefaultAuthAttemptRatePerMin),
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

// maxAuthBody caps public auth request bodies (AUTH-8).
const maxAuthBody = 4 << 10

// normalizeEmail trims and lowercases an email for lookup and storage (AUTH-8).
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// decodeAuthBody reads exactly one JSON value capped at maxAuthBody, rejecting
// unknown fields. It writes the 400 response itself and reports success.
func decodeAuthBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	return true
}

// sessionUser is everything writeSession needs to issue a session payload.
type sessionUser struct {
	ID          string
	CompanyID   string
	CompanyName string
	Email       string
	Role        string
	FirstName   string
	LastName    string
}

// writeSession signs a token and writes the shared login/register payload
// (A6, AUTH-1). user.user_metadata is the path the frontend's doLogin reads;
// company_name saves it a companies lookup. It never carries a password hash.
func (a *API) writeSession(w http.ResponseWriter, status int, u sessionUser) {
	token, err := a.Signer.Sign(u.ID, u.CompanyID, u.Role)
	if err != nil {
		a.Log.Error("sign token", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "could not issue token")
		return
	}
	writeJSON(w, status, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"expires_in":   int(a.Signer.TTL().Seconds()),
		"company_name": u.CompanyName,
		"user": map[string]any{
			"id":         u.ID,
			"company_id": u.CompanyID,
			"email":      u.Email,
			"role":       u.Role,
			"first_name": u.FirstName,
			"last_name":  u.LastName,
			"user_metadata": map[string]any{
				"role":         u.Role,
				"company_id":   u.CompanyID,
				"first_name":   u.FirstName,
				"last_name":    u.LastName,
				"company_name": u.CompanyName,
			},
		},
	})
}

// POST /api/auth/login — email+password -> bcrypt compare -> JWT.
//
// Exactly one bcrypt comparison runs per request whatever the outcome (an
// unknown email or empty stored hash compares against a dummy hash), and
// every failure returns the identical 401 (AUTH-2, AUTH-4).
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeAuthBody(w, r, &req) {
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" || req.Password == "" {
		writeFieldErr(w, map[string]string{"email": "required", "password": "required"})
		return
	}

	user, err := a.Queries.GetUserForLogin(r.Context(), email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.Log.Error("login lookup", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "could not log in")
		return
	}

	hash := ""
	if found {
		hash = user.PasswordHash
	}
	check := a.PasswordCheck
	if check == nil {
		check = auth.CheckPassword
	}
	ok := check(hash, req.Password)

	if !found || !ok || user.Status != "active" {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid credentials")
		return
	}

	a.writeSession(w, http.StatusOK, sessionUser{
		ID:          uuidToString(user.ID),
		CompanyID:   uuidToString(user.CompanyID),
		CompanyName: user.CompanyName,
		Email:       textOrEmpty(user.Email),
		Role:        user.Role,
		FirstName:   textOrEmpty(user.FirstName),
		LastName:    textOrEmpty(user.LastName),
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
