package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/livant05/rrhh-go/internal/auth"
)

func TestCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	cases := []struct {
		name  string
		hash  string
		plain string
		want  bool
	}{
		{"matching password", hash, "correct-horse", true},
		{"wrong password", hash, "wrong", false},
		{"empty hash never matches an empty password", "", "", false},
		{"empty hash never matches a real password", "", "correct-horse", false},
		{"garbage hash", "not-a-bcrypt-hash", "correct-horse", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.CheckPassword(tc.hash, tc.plain); got != tc.want {
				t.Fatalf("CheckPassword(%q, %q) = %v, want %v", tc.hash, tc.plain, got, tc.want)
			}
		})
	}
}

// TestCheckPassword_EmptyHashDoesBcryptWork pins AUTH-4: the empty-hash path
// must spend comparable work to a real comparison (a dummy bcrypt compare),
// not return early. A bcrypt compare at DefaultCost takes milliseconds; an
// early return takes nanoseconds.
func TestCheckPassword_EmptyHashDoesBcryptWork(t *testing.T) {
	auth.CheckPassword("", "warm-up") // first call may generate the dummy hash

	start := time.Now()
	auth.CheckPassword("", "correct-horse")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("empty-hash compare returned in %s; expected real bcrypt work", elapsed)
	}
}

func TestSigner_TTL(t *testing.T) {
	for _, ttl := range []time.Duration{24 * time.Hour, time.Hour} {
		if got := auth.NewSigner("secret", ttl).TTL(); got != ttl {
			t.Fatalf("TTL() = %s, want %s", got, ttl)
		}
	}
}

const mwSecret = "middleware-test-secret-0123456789"

// stubLookup returns a fixed session (or error) and records the id asked for.
func stubLookup(s auth.Session, err error, asked *string) auth.SessionLookup {
	return func(_ context.Context, userID string) (auth.Session, error) {
		if asked != nil {
			*asked = userID
		}
		return s, err
	}
}

// callMiddleware sends one request with token through the middleware and
// returns the recorder plus the identity the downstream handler saw.
func callMiddleware(t *testing.T, lookup auth.SessionLookup, token string) (*httptest.ResponseRecorder, [3]string) {
	t.Helper()
	var seen [3]string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = [3]string{auth.UserIDFromContext(r.Context()), auth.CompanyIDFromContext(r.Context()), auth.RoleFromContext(r.Context())}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	auth.NewSigner(mwSecret, time.Hour).Middleware(next, lookup).ServeHTTP(rec, req)
	return rec, seen
}

func TestMiddleware_SessionChecks(t *testing.T) {
	signer := auth.NewSigner(mwSecret, time.Hour)
	sign := func(tv int32) string {
		tok, err := signer.Sign("u1", "c-claim", "admin", tv)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	active := auth.Session{Status: "active", Role: "empleado", CompanyID: "c-db", TokenVersion: 2}

	t.Run("matching version passes with role and company from the lookup", func(t *testing.T) {
		var asked string
		rec, seen := callMiddleware(t, stubLookup(active, nil, &asked), sign(2))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if asked != "u1" || seen != [3]string{"u1", "c-db", "empleado"} {
			t.Fatalf("asked=%q seen=%v; role and company must come from the lookup", asked, seen)
		}
	})
	t.Run("version mismatch is 401", func(t *testing.T) {
		if rec, _ := callMiddleware(t, stubLookup(active, nil, nil), sign(1)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("inactive is 401", func(t *testing.T) {
		s := active
		s.Status = "inactive"
		if rec, _ := callMiddleware(t, stubLookup(s, nil, nil), sign(2)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("unknown user is 401 with the standard envelope", func(t *testing.T) {
		rec, _ := callMiddleware(t, stubLookup(auth.Session{}, auth.ErrSessionNotFound, nil), sign(0))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "unauthorized" {
			t.Fatalf("envelope: %v %s", err, rec.Body.String())
		}
	})
	t.Run("lookup failure fails closed with 500, never a pass", func(t *testing.T) {
		rec, seen := callMiddleware(t, stubLookup(auth.Session{}, errors.New("db down"), nil), sign(0))
		if rec.Code != http.StatusInternalServerError || seen[0] != "" {
			t.Fatalf("got %d seen=%v", rec.Code, seen)
		}
	})
	t.Run("token without tv counts as version 0", func(t *testing.T) {
		legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"user_id": "u1", "company_id": "c-claim", "role": "admin",
			"exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte(mwSecret))
		if err != nil {
			t.Fatal(err)
		}
		zero := active
		zero.TokenVersion = 0
		if rec, _ := callMiddleware(t, stubLookup(zero, nil, nil), legacy); rec.Code != http.StatusNoContent {
			t.Fatalf("version 0 row: got %d", rec.Code)
		}
		if rec, _ := callMiddleware(t, stubLookup(active, nil, nil), legacy); rec.Code != http.StatusUnauthorized {
			t.Fatalf("version 2 row: got %d", rec.Code)
		}
	})
	t.Run("bad signature never reaches the lookup", func(t *testing.T) {
		var asked string
		other, _ := auth.NewSigner("another-secret-another-secret-00", time.Hour).Sign("u1", "c", "admin", 0)
		if rec, _ := callMiddleware(t, stubLookup(active, nil, &asked), other); rec.Code != http.StatusUnauthorized || asked != "" {
			t.Fatalf("got %d asked=%q", rec.Code, asked)
		}
	})
	t.Run("nil lookup panics at construction", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected a panic for a nil SessionLookup")
			}
		}()
		signer.Middleware(http.NotFoundHandler(), nil)
	})
}
