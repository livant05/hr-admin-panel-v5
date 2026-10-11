package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// writeUnauthorized emits the same A2 nested error envelope the handlers
// package uses ({"error":{"code","message"}}), without importing handlers
// (which already imports auth) — this is the middleware's own 401 response,
// before any handler or tenant context exists.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": "unauthorized", "message": msg},
	})
}

type ctxKey string

const (
	ctxUserID    ctxKey = "user_id"
	ctxCompanyID ctxKey = "company_id"
	ctxRole      ctxKey = "role"
)

type Claims struct {
	UserID    string `json:"user_id"`
	CompanyID string `json:"company_id"`
	Role      string `json:"role"`
	jwt.RegisteredClaims
}

// Signer issues and validates JWTs for the API.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl}
}

// TTL is the lifetime of tokens issued by Sign.
func (s *Signer) TTL() time.Duration { return s.ttl }

func (s *Signer) Sign(userID, companyID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		CompanyID: companyID,
		Role:      role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *Signer) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Middleware validates the Authorization: Bearer <token> header and injects
// user_id/company_id/role into the request context. Every handler that
// touches tenant data must read company_id from context here — never from a
// client-supplied field — this is what replaces Supabase's RLS.
func (s *Signer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeUnauthorized(w, "missing bearer token")
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := s.Parse(tokenStr)
		if err != nil {
			writeUnauthorized(w, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxCompanyID, claims.CompanyID)
		ctx = context.WithValue(ctx, ctxRole, claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

func CompanyIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxCompanyID).(string)
	return v
}

func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxRole).(string)
	return v
}

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

var (
	dummyOnce sync.Once
	dummyHash []byte
)

// dummyBcryptHash returns a package-level bcrypt hash (DefaultCost) generated
// once. It is compared against when there is no real hash, so an unknown
// email costs the same bcrypt work as a wrong password (AUTH-4).
func dummyBcryptHash() []byte {
	dummyOnce.Do(func() {
		// Errors only on an invalid cost or an over-72-byte input; neither applies.
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing-parity"), bcrypt.DefaultCost)
	})
	return dummyHash
}

// CheckPassword reports whether plain matches hash. An empty hash (unknown
// user, or a user whose password_hash is the 0002 default ”) is compared
// against a dummy hash for equivalent work and always returns false.
func CheckPassword(hash, plain string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash(), []byte(plain))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
