package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type companyRow struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Ruc       *string `json:"ruc"`
	Address   *string `json:"address"`
	Phone     *string `json:"phone"`
	CreatedAt string  `json:"created_at"`
}

// companyKeys are exactly the columns of the companies table (design D7).
var companyKeys = []string{"address", "created_at", "id", "name", "phone", "ruc"}

// UM-6 / XC-1: every authenticated caller reads exactly its own company.
func TestCompanies_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	nameA := "CompaniesLeakA-" + uuid.NewString()[:6]
	nameB := "CompaniesLeakB-" + uuid.NewString()[:6]
	a := testutil.Company(t, pool, nameA)
	b := testutil.Company(t, pool, nameB)
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	t.Run("each token sees exactly its own company", func(t *testing.T) {
		for _, tc := range []struct {
			token string
			want  testutil.Tenant
			name  string
			other string
		}{{ta, a, nameA, nameB}, {tb, b, nameB, nameA}} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/companies", tc.token, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("list: got %d (%s)", rec.Code, rec.Body.String())
			}
			rows := testutil.DecodeRows[companyRow](t, rec)
			if len(rows) != 1 || rows[0].ID != tc.want.CompanyID || rows[0].Name != tc.name {
				t.Fatalf("list = %+v, want exactly company %s (%s)", rows, tc.want.CompanyID, tc.name)
			}
			if strings.Contains(rec.Body.String(), tc.other) {
				t.Fatalf("list body leaks the other company's name: %s", rec.Body.String())
			}
			var raw []json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v", err)
			}
			assertKeys(t, raw[0], companyKeys)
		}
	})

	t.Run("get own company by id", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/companies/"+a.CompanyID, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRow[companyRow](t, rec)
		if got.ID != a.CompanyID || got.Name != nameA {
			t.Fatalf("got %+v", got)
		}
		assertKeys(t, rec.Body.Bytes(), companyKeys)
	})

	t.Run("get another company by id is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/companies/"+b.CompanyID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 (never 403), got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		if strings.Contains(rec.Body.String(), nameB) {
			t.Fatal("404 body leaks the other company")
		}
	})

	t.Run("unknown and malformed ids are 404", func(t *testing.T) {
		for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
			if rec := testutil.Do(t, h, http.MethodGet, "/api/companies/"+id, ta, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("id %q: got %d, want 404", id, rec.Code)
			}
		}
	})

	t.Run("id filter: own company, another company or garbage", func(t *testing.T) {
		own := testutil.DecodeRows[companyRow](t, testutil.Do(t, h, http.MethodGet, "/api/companies?id="+a.CompanyID, ta, nil))
		if len(own) != 1 || own[0].ID != a.CompanyID {
			t.Fatalf("own id filter = %+v", own)
		}
		rec := testutil.Do(t, h, http.MethodGet, "/api/companies?id="+b.CompanyID, ta, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
			t.Fatalf("another company's id filter must return [], got %s", body)
		}
		if rec := testutil.Do(t, h, http.MethodGet, "/api/companies?id=not-a-uuid", ta, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("garbage id filter: got %d, want 400", rec.Code)
		}
	})

	t.Run("unknown params are rejected", func(t *testing.T) {
		for _, bad := range []string{"?company_id=" + b.CompanyID, "?name=x", "?_order=name"} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/companies"+bad, ta, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d, want 400", bad, rec.Code)
			}
		}
	})

	t.Run("any authenticated role may read its own company", func(t *testing.T) {
		for _, role := range []string{"empleado", "manager"} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/companies", a.TokenAs(t, pool, signer, role), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: got %d", role, rec.Code)
			}
			if rows := testutil.DecodeRows[companyRow](t, rec); len(rows) != 1 || rows[0].ID != a.CompanyID {
				t.Fatalf("%s: rows = %+v", role, rows)
			}
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		for _, path := range []string{"/api/companies", "/api/companies/" + a.CompanyID} {
			if rec := testutil.Do(t, h, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: got %d, want 401", path, rec.Code)
			}
		}
	})

	t.Run("write routes are not offered", func(t *testing.T) {
		for _, call := range []struct{ method, path string }{
			{http.MethodPost, "/api/companies"},
			{http.MethodPatch, "/api/companies"},
			{http.MethodPatch, "/api/companies/" + a.CompanyID},
			{http.MethodPut, "/api/companies/" + a.CompanyID},
			{http.MethodDelete, "/api/companies/" + a.CompanyID},
		} {
			rec := testutil.Do(t, h, call.method, call.path, ta, map[string]any{"name": "Hijacked"})
			// Status only: ServeMux's own 404/405 bodies are not the A2 envelope.
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: got %d, want 404 or 405", call.method, call.path, rec.Code)
			}
		}
		var name string
		if err := pool.QueryRow(t.Context(), `SELECT name FROM companies WHERE id = $1`, a.CompanyID).Scan(&name); err != nil || name != nameA {
			t.Fatalf("company changed through a write route: name=%q err=%v", name, err)
		}
	})
}
