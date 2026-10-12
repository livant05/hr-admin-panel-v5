package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestJobPostings_CrossTenantLeak covers XC-1: GET, PATCH, DELETE and LIST
// never reach another company's postings.
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

	t.Run("patch other tenant row is 404 and leaves it byte-identical", func(t *testing.T) {
		before := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+jobA.ID, ta, nil).Body.String()
		for _, body := range []map[string]any{{"title": "Hacked"}, {"status": "closed", "salary_min": 1}} {
			rec := testutil.Do(t, h, http.MethodPatch, jobPostingsPath+"/"+jobA.ID, tb, body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("PATCH %v: got %d (%s), want 404", body, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "not_found" {
				t.Fatalf("error.code = %q, want not_found", code)
			}
		}
		if after := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+jobA.ID, ta, nil).Body.String(); after != before {
			t.Fatalf("row changed by a cross-tenant PATCH:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("delete other tenant row is 404 and the row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, jobPostingsPath+"/"+jobA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+jobA.ID, ta, nil); got.Code != http.StatusOK {
			t.Fatalf("row did not survive the cross-tenant delete: %d", got.Code)
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
		// Pinned (CO-S2a-1): a blank optional string is stored as "", not NULL,
		// and reads back as "". The design does not ask for NULL normalization.
		for name, got := range map[string]*string{
			"department": row.Department, "location": row.Location,
			"description": row.Description, "requirements": row.Requirements,
		} {
			if got == nil || *got != "" {
				t.Fatalf("%s = %v, want the stored blank string %q", name, got, "")
			}
		}
		stored, _ := getJobPosting(t, h, tok, row.ID)
		if stored.Department == nil || *stored.Department != "" || stored.Requirements == nil || *stored.Requirements != "" {
			t.Fatalf("blank strings did not survive a GET: %v/%v", stored.Department, stored.Requirements)
		}
	})

	t.Run("an omitted optional string is NULL, distinct from a blank one", func(t *testing.T) {
		row := createJobPosting(t, h, tok, map[string]any{"title": "Omitted"})
		if row.Department != nil || row.Location != nil || row.Description != nil || row.Requirements != nil {
			t.Fatalf("omitted optional strings = %v/%v/%v/%v, want all null",
				row.Department, row.Location, row.Description, row.Requirements)
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

func getJobPosting(t *testing.T, h http.Handler, token, id string) (jobPostingRow, int) {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, jobPostingsPath+"/"+id, token, nil)
	if rec.Code != http.StatusOK {
		return jobPostingRow{}, rec.Code
	}
	return testutil.DecodeRow[jobPostingRow](t, rec), rec.Code
}

func TestJobPostings_CrudRoundTrip(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobCrudCo")
	tok := c.Token(t, signer)

	created := createJobPosting(t, h, tok, map[string]any{
		"title": "Original", "department": "Ventas", "description": "desc",
		"salary_min": 1000, "salary_max": 2000, "status": "open",
	})
	path := jobPostingsPath + "/" + created.ID

	t.Run("patch changes only the sent fields and returns a one-element array", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"title": "  Renamed  ", "status": "paused"})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		rows := testutil.DecodeRows[jobPostingRow](t, rec)
		if len(rows) != 1 {
			t.Fatalf("PATCH returned %d rows, want a one-element array", len(rows))
		}
		got := rows[0]
		if got.Title != "Renamed" || got.Status != "paused" {
			t.Fatalf("title/status = %q/%q", got.Title, got.Status)
		}
		if got.Department == nil || *got.Department != "Ventas" || got.Description == nil || *got.Description != "desc" {
			t.Fatalf("unsent text fields changed: %v/%v", got.Department, got.Description)
		}
		if got.SalaryMin == nil || *got.SalaryMin != 1000 || got.SalaryMax == nil || *got.SalaryMax != 2000 {
			t.Fatalf("unsent salaries changed: %v/%v", deref(got.SalaryMin), deref(got.SalaryMax))
		}
		if got.ID != created.ID || got.CompanyID != c.CompanyID || got.CreatedAt != created.CreatedAt {
			t.Fatalf("identity fields changed: %+v", got)
		}
		stored, _ := getJobPosting(t, h, tok, created.ID)
		if stored.Title != "Renamed" || stored.Status != "paused" {
			t.Fatalf("GET after PATCH = %q/%q", stored.Title, stored.Status)
		}
	})

	t.Run("null clears an optional field", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"salary_min": nil, "department": nil})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRows[jobPostingRow](t, rec)[0]
		if got.SalaryMin != nil || got.Department != nil {
			t.Fatalf("salary_min/department = %v/%v, want null", deref(got.SalaryMin), got.Department)
		}
		if got.SalaryMax == nil || *got.SalaryMax != 2000 || got.Title != "Renamed" {
			t.Fatalf("a null on one field disturbed others: %+v", got)
		}
	})

	t.Run("empty patch is a no-op that returns the row", func(t *testing.T) {
		before, _ := getJobPosting(t, h, tok, created.ID)
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRows[jobPostingRow](t, rec)[0]
		if got.ID != before.ID || got.Title != before.Title || got.Status != before.Status ||
			!sameF64(got.SalaryMax, before.SalaryMax) || !sameF64(got.SalaryMin, before.SalaryMin) {
			t.Fatalf("empty patch changed the row: %+v -> %+v", before, got)
		}
	})

	t.Run("delete returns 204 and the id is gone", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, path, tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", rec.Code, rec.Body.String())
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("204 carries a body: %q", rec.Body.String())
		}
		if _, code := getJobPosting(t, h, tok, created.ID); code != http.StatusNotFound {
			t.Fatalf("GET after delete = %d, want 404", code)
		}
		if n := countJobPostings(t, pool, c.CompanyID); n != 0 {
			t.Fatalf("%d rows left after delete", n)
		}
	})

	t.Run("a deleted or malformed id is 404 for patch and delete", func(t *testing.T) {
		for _, id := range []string{created.ID, uuid.NewString(), "not-a-uuid"} {
			for _, m := range []string{http.MethodPatch, http.MethodDelete} {
				rec := testutil.Do(t, h, m, jobPostingsPath+"/"+id, tok, map[string]any{"title": "x"})
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s %s: got %d, want 404", m, id, rec.Code)
				}
			}
		}
	})
}

func TestJobPostings_PatchValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobPatchValidationCo")
	tok := c.Token(t, signer)

	job := createJobPosting(t, h, tok, map[string]any{
		"title": "Base", "salary_min": 1000, "salary_max": 2000, "status": "open",
	})
	path := jobPostingsPath + "/" + job.ID
	baseline := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String()

	cases := []struct {
		name  string
		body  string
		code  string
		field string
	}{
		{"blank title", `{"title":"   "}`, "validation_failed", "title"},
		{"null title", `{"title":null}`, "validation_failed", "title"},
		{"title over 200 runes", `{"title":"` + strings.Repeat("a", 201) + `"}`, "validation_failed", "title"},
		{"negative salary_min", `{"salary_min":-1}`, "validation_failed", "salary_min"},
		{"salary_max as string", `{"salary_max":"abc"}`, "validation_failed", "salary_max"},
		{"salary overflows numeric(10,2)", `{"salary_max":100000000}`, "validation_failed", "salary_max"},
		{"max below the stored min", `{"salary_max":999}`, "validation_failed", "salary_max"},
		{"min above the stored max", `{"salary_min":2001}`, "validation_failed", "salary_max"},
		{"max below min in the same body", `{"salary_min":5000,"salary_max":4000}`, "validation_failed", "salary_max"},
		{"unknown status", `{"status":"archived"}`, "validation_failed", "status"},
		{"null status", `{"status":null}`, "validation_failed", "status"},
		{"empty status", `{"status":""}`, "validation_failed", "status"},
		{"unknown field", `{"bogus":1}`, "bad_request", ""},
		{"company_id in body", `{"company_id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"id in body", `{"id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"created_at in body", `{"created_at":"2020-01-01T00:00:00Z"}`, "bad_request", ""},
		{"title wrong type", `{"title":5}`, "bad_request", ""},
		{"malformed json", `{"title":`, "bad_request", ""},
		{"not an object", `["title"]`, "bad_request", ""},
		{"trailing value", `{"title":"x"}{"title":"y"}`, "bad_request", ""},
		{"empty body", ``, "bad_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRawBody(t, h, http.MethodPatch, path, tok, tc.body)
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
			if after := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String(); after != baseline {
				t.Fatalf("a rejected PATCH changed the row:\nbefore %s\nafter  %s", baseline, after)
			}
		})
	}

	accepted := []struct {
		name string
		body string
		min  *float64
		max  *float64
	}{
		{"widening both salaries together", `{"salary_min":100,"salary_max":9000}`, f64(100), f64(9000)},
		{"max equal to the stored min", `{"salary_min":1000,"salary_max":1000}`, f64(1000), f64(1000)},
		{"clearing max lifts the ordering rule", `{"salary_max":null,"salary_min":5000}`, f64(5000), nil},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			rec := doRawBody(t, h, http.MethodPatch, path, tok, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d (%s), want 200", rec.Code, rec.Body.String())
			}
			row := testutil.DecodeRows[jobPostingRow](t, rec)[0]
			if !sameF64(row.SalaryMin, tc.min) || !sameF64(row.SalaryMax, tc.max) {
				t.Fatalf("salaries = %v/%v, want %v/%v", deref(row.SalaryMin), deref(row.SalaryMax), deref(tc.min), deref(tc.max))
			}
		})
	}
}

// errMessage extracts error.message (A2) for assertions.
func errMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := decodeJSON(rec, &env); err != nil {
		t.Fatalf("decode error body (%s): %v", rec.Body.String(), err)
	}
	return env.Error.Message
}

// waitUntilBlockedBy polls until some backend is waiting on a lock held by the
// backend holderPID, so a test knows the request under test has reached the
// lock instead of guessing with a sleep. It returns the pid of a backend that
// is blocked (the request's own connection) and fails the test after 10
// seconds.
func waitUntilBlockedBy(t *testing.T, pool *pgxpool.Pool, holderPID int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := pool.QueryRow(context.Background(),
			`SELECT pid FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`,
			holderPID).Scan(&waiting)
		if err == nil {
			return waiting
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("find backends blocked by %d: %v", holderPID, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no backend became blocked by backend %d within 10s", holderPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func seedApplication(t *testing.T, pool *pgxpool.Pool, companyID, jobID string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO applications (id, company_id, job_posting_id, job_title, candidate_name)
		 VALUES ($1, $2, $3, 'Seed', 'Candidate')`, id, companyID, jobID); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	return id
}

func countApplications(t *testing.T, pool *pgxpool.Pool, jobID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM applications WHERE job_posting_id = $1`, jobID).Scan(&n); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	return n
}

// TestJobPostings_DeleteBlockedWhileApplicationsExist pins REC-4: the schema
// CASCADE never fires through the API, a delete with applications is 409 with
// the count, and nothing is removed.
func TestJobPostings_DeleteBlockedWhileApplicationsExist(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobDeleteRestrictCo")
	tok := c.Token(t, signer)

	withApps := createJobPosting(t, h, tok, map[string]any{"title": "With applications"})
	other := createJobPosting(t, h, tok, map[string]any{"title": "Other"})
	seedApplication(t, pool, c.CompanyID, withApps.ID)
	seedApplication(t, pool, c.CompanyID, withApps.ID)
	seedApplication(t, pool, c.CompanyID, other.ID)

	rec := testutil.Do(t, h, http.MethodDelete, jobPostingsPath+"/"+withApps.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("error.code = %q, want conflict", code)
	}
	if msg := errMessage(t, rec); !strings.HasPrefix(msg, "2 postulación") {
		t.Fatalf("409 message = %q, want it to start with the application count %q", msg, "2 postulación")
	}
	if _, code := getJobPosting(t, h, tok, withApps.ID); code != http.StatusOK {
		t.Fatalf("posting was removed by a blocked delete: %d", code)
	}
	if n := countApplications(t, pool, withApps.ID); n != 2 {
		t.Fatalf("applications = %d after a blocked delete, want 2 (cascade must not fire)", n)
	}

	t.Run("another posting's applications do not block this delete", func(t *testing.T) {
		third := createJobPosting(t, h, tok, map[string]any{"title": "No applications"})
		if rec := testutil.Do(t, h, http.MethodDelete, jobPostingsPath+"/"+third.ID, tok, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", rec.Code, rec.Body.String())
		}
		if n := countApplications(t, pool, other.ID); n != 1 {
			t.Fatalf("unrelated applications = %d, want 1", n)
		}
	})

	t.Run("delete succeeds once the applications are gone", func(t *testing.T) {
		if _, err := pool.Exec(context.Background(), `DELETE FROM applications WHERE job_posting_id = $1`, withApps.ID); err != nil {
			t.Fatalf("remove applications: %v", err)
		}
		if rec := testutil.Do(t, h, http.MethodDelete, jobPostingsPath+"/"+withApps.ID, tok, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", rec.Code, rec.Body.String())
		}
	})
}

// TestJobPostings_DeleteWaitsForAnInFlightApplication holds an uncommitted
// application insert open while DELETE runs. The delete must not count past
// it and cascade the application away: it waits on the posting row and then
// answers 409 once the insert commits.
func TestJobPostings_DeleteWaitsForAnInFlightApplication(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobDeleteRaceCo")
	tok := c.Token(t, signer)
	job := createJobPosting(t, h, tok, map[string]any{"title": "Racy"})

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO applications (company_id, job_posting_id, job_title, candidate_name)
		 VALUES ($1, $2, 'Racy', 'Inflight')`, c.CompanyID, job.ID); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	var holderPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, jobPostingsPath+"/"+job.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec, req)
	}()
	waitUntilBlockedBy(t, pool, holderPID) // the handler is parked on the posting row lock
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	wg.Wait()

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if n := countApplications(t, pool, job.ID); n != 1 {
		t.Fatalf("applications = %d, want the in-flight one to survive", n)
	}
	if _, code := getJobPosting(t, h, tok, job.ID); code != http.StatusOK {
		t.Fatalf("posting was deleted under an in-flight application: %d", code)
	}
}

// TestJobPostings_BodyIsCappedAtOneMiB pins the 1 MiB cap of
// decodeRecruitmentBody and decodeRecruitmentPatch (CO-S2a-2): a body over the
// cap is a 400 bad_request and writes nothing, while one just under it passes.
func TestJobPostings_BodyIsCappedAtOneMiB(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobBodyCapCo")
	tok := c.Token(t, signer)

	const oneMiB = 1 << 20
	bodyOfSize := func(n int) string {
		const prefix, suffix = `{"title":"big","description":"`, `"}`
		return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
	}

	t.Run("create over the cap is 400 and creates nothing", func(t *testing.T) {
		before := countJobPostings(t, pool, c.CompanyID)
		rec := doRawBody(t, h, http.MethodPost, jobPostingsPath, tok, bodyOfSize(oneMiB+1))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d (%s), want 400", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		if after := countJobPostings(t, pool, c.CompanyID); after != before {
			t.Fatalf("an oversized create left %d new row(s)", after-before)
		}
	})

	t.Run("create at exactly the cap is accepted", func(t *testing.T) {
		rec := doRawBody(t, h, http.MethodPost, jobPostingsPath, tok, bodyOfSize(oneMiB))
		if rec.Code != http.StatusCreated {
			t.Fatalf("got %d, want 201 for a body of exactly 1 MiB", rec.Code)
		}
		row := testutil.DecodeRows[jobPostingRow](t, rec)[0]
		if row.Description == nil || len(*row.Description) < oneMiB-100 {
			t.Fatalf("stored description is %d bytes, want the whole padded value", len(deref0(row.Description)))
		}
	})

	t.Run("patch over the cap is 400 and leaves the row unchanged", func(t *testing.T) {
		job := createJobPosting(t, h, tok, map[string]any{"title": "Patch cap"})
		path := jobPostingsPath + "/" + job.ID
		before := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String()
		rec := doRawBody(t, h, http.MethodPatch, path, tok, bodyOfSize(oneMiB+1))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d (%s), want 400", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		if after := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String(); after != before {
			t.Fatalf("an oversized PATCH changed the row:\nbefore %s\nafter  %s", before, after)
		}
	})
}

func deref0(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestJobPostings_PatchInFlightDoesNotBlockApplicationInsert holds a PATCH open
// after its merge read (a BEFORE UPDATE trigger parks it on an advisory lock)
// and inserts an application for the same posting. The merge read takes FOR NO
// KEY UPDATE, which does not conflict with the FOR KEY SHARE an application
// insert takes on the posting, so the insert must complete while the PATCH is
// still in flight (CO-S2b-3). DELETE keeps FOR UPDATE and is covered by
// TestJobPostings_DeleteWaitsForAnInFlightApplication.
func TestJobPostings_PatchInFlightDoesNotBlockApplicationInsert(t *testing.T) {
	// The trigger lives in an isolated schema so it can never fire for another
	// test; the advisory lock key is random because advisory locks are
	// database-wide.
	pool, _ := testutil.IsolatedSchema(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "JobPatchHoldCo")
	tok := c.Token(t, signer)
	job := createJobPosting(t, h, tok, map[string]any{"title": "Held"})

	const holdTitle = "hold-this-patch"
	holderPID, release := parkPatchesInTrigger(t, pool, "job_postings", "title", holdTitle)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodPatch, jobPostingsPath+"/"+job.ID, strings.NewReader(`{"title":"`+holdTitle+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec, req)
	}()
	// A failed assertion must not leave the PATCH goroutine parked in the trigger
	// and running past the test: release the hold, then wait for it.
	defer func() {
		release()
		wg.Wait()
	}()
	waitUntilBlockedBy(t, pool, holderPID) // the PATCH holds its merge-read lock and is parked in the trigger

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO applications (company_id, job_posting_id, job_title, candidate_name)
		 VALUES ($1, $2, 'Held', 'Candidate')`, c.CompanyID, job.ID); err != nil {
		t.Fatalf("application insert was blocked by a PATCH in flight: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit application: %v", err)
	}

	release()
	wg.Wait()
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if got := testutil.DecodeRows[jobPostingRow](t, rec)[0]; got.Title != holdTitle {
		t.Fatalf("PATCH title = %q, want %q", got.Title, holdTitle)
	}
	if n := countApplications(t, pool, job.ID); n != 1 {
		t.Fatalf("applications = %d, want the concurrent insert to be kept", n)
	}
}

// parkPatchesInTrigger makes any UPDATE of table that sets column to marker
// block inside a BEFORE UPDATE trigger on a database-wide advisory lock, which
// this helper holds on a dedicated connection. A PATCH to the marker value
// therefore stays in flight after its merge read, holding whatever row lock
// that read took. The pool MUST be schema-isolated (testutil.IsolatedSchema) so
// the trigger can never fire for another test. It returns the holder backend
// pid (for waitUntilBlockedBy) and an idempotent release; cleanup releases too.
func parkPatchesInTrigger(t *testing.T, pool *pgxpool.Pool, table, column, marker string) (holderPID int, release func()) {
	t.Helper()
	return parkInTrigger(t, pool, "UPDATE", table, column, marker)
}

// parkInsertsInTrigger is parkPatchesInTrigger for INSERT: a row whose column is
// marker stays in flight inside the BEFORE INSERT trigger, after the statement
// has taken every lock its SELECT part acquired and before the foreign key
// checks run.
func parkInsertsInTrigger(t *testing.T, pool *pgxpool.Pool, table, column, marker string) (holderPID int, release func()) {
	t.Helper()
	return parkInTrigger(t, pool, "INSERT", table, column, marker)
}

func parkInTrigger(t *testing.T, pool *pgxpool.Pool, event, table, column, marker string) (holderPID int, release func()) {
	t.Helper()
	ctx := context.Background()
	key := rand.Int63() // advisory locks are database-wide, so the key is random
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION hold_%[1]s_%[5]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(%[4]d); RETURN NEW; END $$;
		CREATE TRIGGER trg_hold_%[1]s_%[5]s BEFORE %[5]s ON %[1]s
		FOR EACH ROW WHEN (NEW.%[2]s = '%[3]s') EXECUTE FUNCTION hold_%[1]s_%[5]s();`,
		table, column, marker, key, event)); err != nil {
		t.Fatalf("install hold trigger on %s: %v", table, err)
	}

	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatalf("hold advisory lock: %v", err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			_, _ = holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
			holder.Release()
		}
	}
	t.Cleanup(release)
	return holderPID, release
}
