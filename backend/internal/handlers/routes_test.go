package handlers_test

import (
	"net/http"
	"testing"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// TestRoutes_ProtectedRequiresToken pins design P4's fail-closed decision:
// a route registered on the `protected` sub-mux, with no Authorization
// header, must 401 — never bypass auth.
func TestRoutes_ProtectedRequiresToken(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := testutil.Server(t, pool)

	rec := testutil.Do(t, h, http.MethodGet, "/api/departments", "", nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "unauthorized" {
		t.Fatalf("expected error.code=unauthorized, got %q", code)
	}
}

// TestRoutes_PublicStayPublic pins that mounting all protected routes on one
// sub-mux does not regress the two intentionally public routes.
func TestRoutes_PublicStayPublic(t *testing.T) {
	pool := testutil.Pool(t)
	h, _ := testutil.Server(t, pool)

	t.Run("health is public", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/health", "", nil)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("expected /api/health to stay public, got 401")
		}
	})

	t.Run("login is public", func(t *testing.T) {
		// No Authorization header is sent — if /api/auth/login were behind
		// signer.Middleware, this would be rejected before the handler ever
		// ran. It is registered directly on the outer mux, so it reaches the
		// login handler and is rejected on invalid credentials instead.
		rec := testutil.Do(t, h, http.MethodPost, "/api/auth/login", "", map[string]string{
			"email": "nobody@example.test", "password": "wrong",
		})
		if rec.Code == http.StatusNotFound {
			t.Fatalf("expected /api/auth/login to be registered, got 404")
		}
	})
}
