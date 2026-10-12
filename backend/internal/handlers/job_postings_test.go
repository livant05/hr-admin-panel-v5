package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type jobPostingRow struct {
	ID           string   `json:"id"`
	CompanyID    string   `json:"company_id"`
	Title        string   `json:"title"`
	Department   *string  `json:"department"`
	Location     *string  `json:"location"`
	Description  *string  `json:"description"`
	Requirements *string  `json:"requirements"`
	SalaryMin    *float64 `json:"salary_min"`
	SalaryMax    *float64 `json:"salary_max"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"created_at"`
}

const jobPostingsPath = "/api/job_postings"

func createJobPosting(t *testing.T, h http.Handler, token string, body map[string]any) jobPostingRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, jobPostingsPath, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create job posting %v: got %d (%s)", body, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[jobPostingRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create returned %d rows, want a one-element array", len(rows))
	}
	return rows[0]
}

// doRawBody sends a request with a literal body so malformed JSON and exact
// numeric literals reach the handler untouched.
func doRawBody(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func countJobPostings(t *testing.T, pool *pgxpool.Pool, companyID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM job_postings WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		t.Fatalf("count job postings: %v", err)
	}
	return n
}

func jobIDs(rows []jobPostingRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// TestJobPostings_CrossTenantLeak covers XC-1 for the read/create half of the
// resource (PATCH/DELETE assertions arrive with S2b).
func TestJobPostings_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "JobLeakA")
	b := testutil.Company(t, pool, "JobLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	jobA := createJobPosting(t, h, ta, map[string]any{"title": "Contador A"})
	jobB := createJobPosting(t, h, tb, map[string]any{"title": "Contador B"})

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+jobA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		if strings.Contains(rec.Body.String(), "Contador A") {
			t.Fatalf("404 body leaks the other tenant's data: %s", rec.Body.String())
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		aRows := testutil.DecodeRows[jobPostingRow](t, testutil.Do(t, h, http.MethodGet, jobPostingsPath, ta, nil))
		if got := jobIDs(aRows); len(got) != 1 || got[0] != jobA.ID {
			t.Fatalf("A's list = %v, want only %s", got, jobA.ID)
		}
		bRows := testutil.DecodeRows[jobPostingRow](t, testutil.Do(t, h, http.MethodGet, jobPostingsPath, tb, nil))
		if got := jobIDs(bRows); len(got) != 1 || got[0] != jobB.ID {
			t.Fatalf("B's list = %v, want only %s", got, jobB.ID)
		}
	})

	t.Run("id filter cannot reach another tenant", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"?id="+jobA.ID, tb, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rows := testutil.DecodeRows[jobPostingRow](t, rec); len(rows) != 0 {
			t.Fatalf("B filtered by A's id got %v, want []", jobIDs(rows))
		}
		own := testutil.DecodeRows[jobPostingRow](t, testutil.Do(t, h, http.MethodGet, jobPostingsPath+"?id="+jobA.ID, ta, nil))
		if len(own) != 1 || own[0].ID != jobA.ID {
			t.Fatalf("A filtered by its own id got %v, want [%s]", jobIDs(own), jobA.ID)
		}
	})

	t.Run("company_id in body is rejected and nothing is created", func(t *testing.T) {
		before := countJobPostings(t, pool, a.CompanyID) + countJobPostings(t, pool, b.CompanyID)
		rec := testutil.Do(t, h, http.MethodPost, jobPostingsPath, ta, map[string]any{
			"title": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		after := countJobPostings(t, pool, a.CompanyID) + countJobPostings(t, pool, b.CompanyID)
		if after != before {
			t.Fatalf("rows went from %d to %d on a rejected create", before, after)
		}
	})

	t.Run("created row belongs to the token company", func(t *testing.T) {
		if jobA.CompanyID != a.CompanyID || jobB.CompanyID != b.CompanyID {
			t.Fatalf("company ids = %s/%s, want %s/%s", jobA.CompanyID, jobB.CompanyID, a.CompanyID, b.CompanyID)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			rec := testutil.Do(t, h, m, jobPostingsPath, "", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: got %d, want 401", m, rec.Code)
			}
		}
	})
}

func TestJobPostings_CreateReturnsArrayWithDefaults(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobCreateCo")
	tok := c.Token(t, signer)

	t.Run("minimal body defaults status to open and leaves optionals null", func(t *testing.T) {
		row := createJobPosting(t, h, tok, map[string]any{"title": "  Analista  "})
		if row.Title != "Analista" {
			t.Fatalf("title = %q, want trimmed %q", row.Title, "Analista")
		}
		if row.Status != "open" {
			t.Fatalf("status = %q, want default open", row.Status)
		}
		if row.SalaryMin != nil || row.SalaryMax != nil || row.Department != nil {
			t.Fatalf("optional fields not null: %+v", row)
		}
		if _, err := uuid.Parse(row.ID); err != nil {
			t.Fatalf("id %q is not a server-assigned uuid: %v", row.ID, err)
		}
		if row.CreatedAt == "" {
			t.Fatalf("created_at missing")
		}
	})

	t.Run("full body round trips through GET", func(t *testing.T) {
		row := createJobPosting(t, h, tok, map[string]any{
			"title": "Gerente", "department": "Ventas", "location": "Panama Centro",
			"description": "Lidera ventas", "requirements": "5 años",
			"salary_min": 1500.5, "salary_max": 2500, "status": "paused",
		})
		got := testutil.DecodeRow[jobPostingRow](t, testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+row.ID, tok, nil))
		if got.Title != "Gerente" || got.Status != "paused" {
			t.Fatalf("title/status = %q/%q", got.Title, got.Status)
		}
		if got.Department == nil || *got.Department != "Ventas" || got.Location == nil || *got.Location != "Panama Centro" {
			t.Fatalf("department/location = %v/%v", got.Department, got.Location)
		}
		if got.Description == nil || *got.Description != "Lidera ventas" || got.Requirements == nil || *got.Requirements != "5 años" {
			t.Fatalf("description/requirements = %v/%v", got.Description, got.Requirements)
		}
		if got.SalaryMin == nil || *got.SalaryMin != 1500.5 || got.SalaryMax == nil || *got.SalaryMax != 2500 {
			t.Fatalf("salaries = %v/%v, want 1500.5/2500", got.SalaryMin, got.SalaryMax)
		}
	})

	t.Run("frontend shape with blank strings and null salaries is accepted", func(t *testing.T) {
		row := createJobPosting(t, h, tok, map[string]any{
			"title": "Vacante", "department": "", "location": "", "description": "",
			"requirements": "", "salary_min": nil, "salary_max": nil, "status": "open",
		})
		if row.SalaryMin != nil || row.SalaryMax != nil {
			t.Fatalf("null salaries stored as %v/%v", row.SalaryMin, row.SalaryMax)
		}
	})

	t.Run("every allowed status is accepted", func(t *testing.T) {
		for _, s := range []string{"open", "paused", "closed"} {
			if row := createJobPosting(t, h, tok, map[string]any{"title": "S-" + s, "status": s}); row.Status != s {
				t.Fatalf("status = %q, want %q", row.Status, s)
			}
		}
	})
}

func TestJobPostings_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobValidationCo")
	tok := c.Token(t, signer)

	// validation_failed carries fields.<name>; bad_request is a body that is
	// not decodable into the request struct at all.
	cases := []struct {
		name  string
		body  string
		code  string
		field string
	}{
		{"missing title", `{}`, "validation_failed", "title"},
		{"blank title", `{"title":"   "}`, "validation_failed", "title"},
		{"title over 200 runes", `{"title":"` + strings.Repeat("a", 201) + `"}`, "validation_failed", "title"},
		{"title over 200 runes multibyte", `{"title":"` + strings.Repeat("ñ", 201) + `"}`, "validation_failed", "title"},
		{"negative salary_min", `{"title":"x","salary_min":-1}`, "validation_failed", "salary_min"},
		{"negative salary_max", `{"title":"x","salary_max":-0.01}`, "validation_failed", "salary_max"},
		{"salary_min as string", `{"title":"x","salary_min":"abc"}`, "validation_failed", "salary_min"},
		{"salary_max overflows numeric(10,2)", `{"title":"x","salary_max":100000000}`, "validation_failed", "salary_max"},
		{"max below min", `{"title":"x","salary_min":2000,"salary_max":1000}`, "validation_failed", "salary_max"},
		{"unknown status", `{"title":"x","status":"archived"}`, "validation_failed", "status"},
		{"status wrong case", `{"title":"x","status":"Open"}`, "validation_failed", "status"},
		{"unknown field", `{"title":"x","bogus":1}`, "bad_request", ""},
		{"id in body", `{"title":"x","id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"created_at in body", `{"title":"x","created_at":"2020-01-01T00:00:00Z"}`, "bad_request", ""},
		{"company_id in body", `{"title":"x","company_id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"title wrong type", `{"title":5}`, "bad_request", ""},
		{"malformed json", `{"title":`, "bad_request", ""},
		{"trailing value", `{"title":"x"}{"title":"y"}`, "bad_request", ""},
		{"empty body", ``, "bad_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := countJobPostings(t, pool, c.CompanyID)
			rec := doRawBody(t, h, http.MethodPost, jobPostingsPath, tok, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d (%s), want 400", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != tc.code {
				t.Fatalf("error.code = %q, want %q (%s)", code, tc.code, rec.Body.String())
			}
			if tc.field != "" {
				var env struct {
					Error struct {
						Fields map[string]string `json:"fields"`
					} `json:"error"`
				}
				if err := decodeJSON(rec, &env); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if _, ok := env.Error.Fields[tc.field]; !ok {
					t.Fatalf("fields = %v, want a %q entry", env.Error.Fields, tc.field)
				}
			}
			if after := countJobPostings(t, pool, c.CompanyID); after != before {
				t.Fatalf("a rejected create left %d new row(s)", after-before)
			}
		})
	}

	accepted := []struct {
		name string
		body string
		min  *float64
		max  *float64
	}{
		{"title of exactly 200 runes", `{"title":"` + strings.Repeat("ñ", 200) + `"}`, nil, nil},
		{"zero salaries", `{"title":"x","salary_min":0,"salary_max":0}`, f64(0), f64(0)},
		{"equal min and max", `{"title":"x","salary_min":1000,"salary_max":1000}`, f64(1000), f64(1000)},
		{"only min", `{"title":"x","salary_min":1200.25}`, f64(1200.25), nil},
		{"only max", `{"title":"x","salary_max":3000}`, nil, f64(3000)},
		{"largest numeric(10,2) value", `{"title":"x","salary_max":99999999.99}`, nil, f64(99999999.99)},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			rec := doRawBody(t, h, http.MethodPost, jobPostingsPath, tok, tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("got %d (%s), want 201", rec.Code, rec.Body.String())
			}
			row := testutil.DecodeRows[jobPostingRow](t, rec)[0]
			if !sameF64(row.SalaryMin, tc.min) || !sameF64(row.SalaryMax, tc.max) {
				t.Fatalf("salaries = %v/%v, want %v/%v", deref(row.SalaryMin), deref(row.SalaryMax), deref(tc.min), deref(tc.max))
			}
		})
	}
}

func f64(v float64) *float64 { return &v }

func sameF64(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestJobPostings_ListGetFilters(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobFilterCo")
	tok := c.Token(t, signer)

	open1 := createJobPosting(t, h, tok, map[string]any{"title": "Open 1"})
	open2 := createJobPosting(t, h, tok, map[string]any{"title": "Open 2", "status": "open"})
	paused := createJobPosting(t, h, tok, map[string]any{"title": "Paused", "status": "paused"})
	closed := createJobPosting(t, h, tok, map[string]any{"title": "Closed", "status": "closed"})

	list := func(t *testing.T, query string) []jobPostingRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+query, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[jobPostingRow](t, rec)
	}
	has := func(rows []jobPostingRow, id string) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("no filter lists everything", func(t *testing.T) {
		rows := list(t, "")
		if len(rows) != 4 {
			t.Fatalf("got %d rows, want 4", len(rows))
		}
	})

	t.Run("status filter", func(t *testing.T) {
		rows := list(t, "?status=open")
		if len(rows) != 2 || !has(rows, open1.ID) || !has(rows, open2.ID) {
			t.Fatalf("status=open -> %v, want %s and %s", jobIDs(rows), open1.ID, open2.ID)
		}
		rows = list(t, "?status=paused")
		if len(rows) != 1 || rows[0].ID != paused.ID {
			t.Fatalf("status=paused -> %v, want [%s]", jobIDs(rows), paused.ID)
		}
		rows = list(t, "?status=closed")
		if len(rows) != 1 || rows[0].ID != closed.ID {
			t.Fatalf("status=closed -> %v, want [%s]", jobIDs(rows), closed.ID)
		}
	})

	t.Run("id and status combine", func(t *testing.T) {
		if rows := list(t, "?id="+paused.ID+"&status=paused"); len(rows) != 1 || rows[0].ID != paused.ID {
			t.Fatalf("matching id+status -> %v", jobIDs(rows))
		}
		if rows := list(t, "?id="+paused.ID+"&status=open"); len(rows) != 0 {
			t.Fatalf("conflicting id+status -> %v, want []", jobIDs(rows))
		}
	})

	t.Run("limit and offset page deterministically", func(t *testing.T) {
		first := list(t, "?_limit=2")
		rest := list(t, "?_limit=2&_offset=2")
		if len(first) != 2 || len(rest) != 2 {
			t.Fatalf("pages = %d/%d rows, want 2/2", len(first), len(rest))
		}
		seen := map[string]bool{}
		for _, r := range append(first, rest...) {
			if seen[r.ID] {
				t.Fatalf("row %s appears on two pages", r.ID)
			}
			seen[r.ID] = true
		}
	})

	t.Run("get one returns a bare object", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+closed.ID, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		got := testutil.DecodeRow[jobPostingRow](t, rec)
		if got.ID != closed.ID || got.Title != "Closed" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("unknown or malformed ids are 404", func(t *testing.T) {
		for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+id, tok, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s: got %d, want 404", id, rec.Code)
			}
		}
	})

	t.Run("unsupported params are validation_failed", func(t *testing.T) {
		for _, q := range []string{"?title=x", "?company_id=" + c.CompanyID, "?_order=created_at.desc", "?bogus=1", "?id=not-a-uuid", "?_limit=0", "?_offset=-1"} {
			rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+q, tok, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s: got %d, want 400", q, rec.Code)
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("GET %s: error.code = %q, want validation_failed", q, code)
			}
		}
	})

	t.Run("a company with no postings lists [] not null", func(t *testing.T) {
		empty := testutil.Company(t, pool, "JobEmptyCo")
		rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath, empty.Token(t, signer), nil)
		if strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("body = %q, want []", rec.Body.String())
		}
	})

	t.Run("any authenticated role may read", func(t *testing.T) {
		emp := c.TokenAs(t, signer, "empleado")
		if rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath, emp, nil); rec.Code != http.StatusOK {
			t.Fatalf("empleado list: got %d", rec.Code)
		}
	})
}
