package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type applicationRow struct {
	ID            string  `json:"id"`
	CompanyID     string  `json:"company_id"`
	JobPostingID  *string `json:"job_posting_id"`
	JobTitle      *string `json:"job_title"`
	CandidateID   *string `json:"candidate_id"`
	CandidateName *string `json:"candidate_name"`
	Stage         *string `json:"stage"`
	Notes         *string `json:"notes"`
	AppliedAt     *string `json:"applied_at"`
	CreatedAt     string  `json:"created_at"`
}

const applicationsPath = "/api/applications"

// appBody is the minimal valid create body for a posting and a candidate.
func appBody(jobID, candID string, extra map[string]any) map[string]any {
	b := map[string]any{"job_posting_id": jobID, "candidate_id": candID}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func createApplication(t *testing.T, h http.Handler, token string, body map[string]any) applicationRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, applicationsPath, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create application %v: got %d (%s)", body, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[applicationRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create returned %d rows, want a one-element array", len(rows))
	}
	return rows[0]
}

func getApplication(t *testing.T, h http.Handler, token, id string) (applicationRow, int) {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, applicationsPath+"/"+id, token, nil)
	if rec.Code != http.StatusOK {
		return applicationRow{}, rec.Code
	}
	return testutil.DecodeRow[applicationRow](t, rec), rec.Code
}

func countCompanyApplications(t *testing.T, pool *pgxpool.Pool, companyID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM applications WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	return n
}

func applicationIDs(rows []applicationRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// TestApplications_CrossTenantLeak covers XC-1 and REC-2 for the read and
// create side: GET and LIST never reach another company's applications, and a
// create whose posting or candidate belongs to another company is a 404 that
// writes nothing and does not disclose that the id exists.
func TestApplications_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "AppLeakA")
	b := testutil.Company(t, pool, "AppLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	jobA := createJobPosting(t, h, ta, map[string]any{"title": "Contador A"})
	jobB := createJobPosting(t, h, tb, map[string]any{"title": "Contador B"})
	candA := createCandidate(t, h, ta, map[string]any{"full_name": "Ana A"})
	candB := createCandidate(t, h, tb, map[string]any{"full_name": "Beto B"})
	appA := createApplication(t, h, ta, appBody(jobA.ID, candA.ID, nil))
	appB := createApplication(t, h, tb, appBody(jobB.ID, candB.ID, nil))

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, applicationsPath+"/"+appA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		if body := rec.Body.String(); strings.Contains(body, "Contador A") || strings.Contains(body, "Ana A") {
			t.Fatalf("404 body leaks the other tenant's data: %s", body)
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		aRows := testutil.DecodeRows[applicationRow](t, testutil.Do(t, h, http.MethodGet, applicationsPath, ta, nil))
		if got := applicationIDs(aRows); len(got) != 1 || got[0] != appA.ID {
			t.Fatalf("A's list = %v, want only %s", got, appA.ID)
		}
		bRows := testutil.DecodeRows[applicationRow](t, testutil.Do(t, h, http.MethodGet, applicationsPath, tb, nil))
		if got := applicationIDs(bRows); len(got) != 1 || got[0] != appB.ID {
			t.Fatalf("B's list = %v, want only %s", got, appB.ID)
		}
	})

	t.Run("id and foreign key filters cannot reach another tenant", func(t *testing.T) {
		for _, q := range []string{"?id=" + appA.ID, "?job_posting_id=" + jobA.ID, "?candidate_id=" + candA.ID} {
			rec := testutil.Do(t, h, http.MethodGet, applicationsPath+q, tb, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: got %d", q, rec.Code)
			}
			if rows := testutil.DecodeRows[applicationRow](t, rec); len(rows) != 0 {
				t.Fatalf("B with %s got %v, want []", q, applicationIDs(rows))
			}
			own := testutil.DecodeRows[applicationRow](t, testutil.Do(t, h, http.MethodGet, applicationsPath+q, ta, nil))
			if len(own) != 1 || own[0].ID != appA.ID {
				t.Fatalf("A with %s got %v, want [%s]", q, applicationIDs(own), appA.ID)
			}
		}
	})

	t.Run("create with another company's posting or candidate is 404 and writes nothing", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
		}{
			{"foreign posting", appBody(jobB.ID, candA.ID, nil)},
			{"foreign candidate", appBody(jobA.ID, candB.ID, nil)},
			{"both foreign", appBody(jobB.ID, candB.ID, nil)},
			{"nonexistent posting", appBody(uuid.NewString(), candA.ID, nil)},
			{"nonexistent candidate", appBody(jobA.ID, uuid.NewString(), nil)},
		}
		before := countCompanyApplications(t, pool, a.CompanyID) + countCompanyApplications(t, pool, b.CompanyID)
		for _, tc := range cases {
			rec := testutil.Do(t, h, http.MethodPost, applicationsPath, ta, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: got %d (%s), want 404", tc.name, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "not_found" {
				t.Fatalf("%s: error.code = %q, want not_found", tc.name, code)
			}
			// The answer to a foreign id and to a nonexistent id must be the same.
			if msg := errMessage(t, rec); msg != "not found" {
				t.Fatalf("%s: message %q discloses more than %q", tc.name, msg, "not found")
			}
		}
		after := countCompanyApplications(t, pool, a.CompanyID) + countCompanyApplications(t, pool, b.CompanyID)
		if after != before {
			t.Fatalf("applications went from %d to %d across rejected creates", before, after)
		}
	})

	t.Run("company_id in body is rejected and nothing is created", func(t *testing.T) {
		before := countCompanyApplications(t, pool, b.CompanyID)
		rec := testutil.Do(t, h, http.MethodPost, applicationsPath, ta, appBody(jobA.ID, candA.ID, map[string]any{"company_id": b.CompanyID}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		if after := countCompanyApplications(t, pool, b.CompanyID); after != before {
			t.Fatalf("B gained %d application(s) from A's rejected create", after-before)
		}
	})

	t.Run("created row belongs to the token company", func(t *testing.T) {
		if appA.CompanyID != a.CompanyID || appB.CompanyID != b.CompanyID {
			t.Fatalf("company ids = %s/%s, want %s/%s", appA.CompanyID, appB.CompanyID, a.CompanyID, b.CompanyID)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			if rec := testutil.Do(t, h, m, applicationsPath, "", nil); rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: got %d, want 401", m, rec.Code)
			}
		}
	})
}

func TestApplications_CreateReturnsArrayWithDefaults(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "AppCreateCo")
	tok := c.Token(t, signer)
	job := createJobPosting(t, h, tok, map[string]any{"title": "Analista Contable"})
	cand := createCandidate(t, h, tok, map[string]any{"full_name": "Ana Torres Ramos"})

	t.Run("minimal body defaults the stage and the date and derives the names", func(t *testing.T) {
		row := createApplication(t, h, tok, appBody(job.ID, cand.ID, nil))
		if row.CompanyID != c.CompanyID {
			t.Fatalf("company_id = %s, want the token's %s", row.CompanyID, c.CompanyID)
		}
		if textOrNull(row.JobPostingID) != job.ID || textOrNull(row.CandidateID) != cand.ID {
			t.Fatalf("foreign keys = %s/%s, want %s/%s", textOrNull(row.JobPostingID), textOrNull(row.CandidateID), job.ID, cand.ID)
		}
		if textOrNull(row.JobTitle) != "Analista Contable" || textOrNull(row.CandidateName) != "Ana Torres Ramos" {
			t.Fatalf("derived names = %q/%q", textOrNull(row.JobTitle), textOrNull(row.CandidateName))
		}
		if textOrNull(row.Stage) != "applied" {
			t.Fatalf("stage = %q, want the default applied", textOrNull(row.Stage))
		}
		if row.Notes != nil {
			t.Fatalf("notes = %q, want null", *row.Notes)
		}
		var today string
		if err := pool.QueryRow(context.Background(), `SELECT to_char(CURRENT_DATE, 'YYYY-MM-DD')`).Scan(&today); err != nil {
			t.Fatalf("today: %v", err)
		}
		if textOrNull(row.AppliedAt) != today {
			t.Fatalf("applied_at = %q, want today %q", textOrNull(row.AppliedAt), today)
		}
		if row.CreatedAt == "" || row.ID == "" {
			t.Fatalf("server-assigned fields missing: %+v", row)
		}
	})

	t.Run("full body round trips through GET", func(t *testing.T) {
		row := createApplication(t, h, tok, appBody(job.ID, cand.ID, map[string]any{
			"stage": "interview", "notes": "  Entrevista técnica  ", "applied_at": "2025-05-03",
		}))
		got, code := getApplication(t, h, tok, row.ID)
		if code != http.StatusOK {
			t.Fatalf("GET after create: %d", code)
		}
		if textOrNull(got.Stage) != "interview" || textOrNull(got.AppliedAt) != "2025-05-03" {
			t.Fatalf("stage/applied_at = %q/%q", textOrNull(got.Stage), textOrNull(got.AppliedAt))
		}
		if textOrNull(got.Notes) != "  Entrevista técnica  " {
			t.Fatalf("notes = %q, want them stored verbatim", textOrNull(got.Notes))
		}
	})

	t.Run("job_title and candidate_name are accepted but ignored", func(t *testing.T) {
		row := createApplication(t, h, tok, appBody(job.ID, cand.ID, map[string]any{
			"job_title": "Forged title", "candidate_name": "Forged name",
		}))
		if textOrNull(row.JobTitle) != "Analista Contable" || textOrNull(row.CandidateName) != "Ana Torres Ramos" {
			t.Fatalf("names = %q/%q, want them derived from the posting and the candidate", textOrNull(row.JobTitle), textOrNull(row.CandidateName))
		}
		var title, name string
		if err := pool.QueryRow(context.Background(),
			`SELECT job_title, candidate_name FROM applications WHERE id = $1`, row.ID).Scan(&title, &name); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if title != "Analista Contable" || name != "Ana Torres Ramos" {
			t.Fatalf("stored names = %q/%q", title, name)
		}
	})

	t.Run("null stage, notes and applied_at fall back to the defaults", func(t *testing.T) {
		row := createApplication(t, h, tok, appBody(job.ID, cand.ID, map[string]any{"stage": nil, "notes": nil, "applied_at": nil}))
		if textOrNull(row.Stage) != "applied" || row.Notes != nil || row.AppliedAt == nil {
			t.Fatalf("got stage=%q notes=%v applied_at=%v", textOrNull(row.Stage), row.Notes, row.AppliedAt)
		}
	})

	t.Run("the derived title is a snapshot of the posting at create time", func(t *testing.T) {
		j := createJobPosting(t, h, tok, map[string]any{"title": "Before"})
		row := createApplication(t, h, tok, appBody(j.ID, cand.ID, nil))
		if rec := testutil.Do(t, h, http.MethodPatch, jobPostingsPath+"/"+j.ID, tok, map[string]any{"title": "After"}); rec.Code != http.StatusOK {
			t.Fatalf("rename posting: %d", rec.Code)
		}
		if got, _ := getApplication(t, h, tok, row.ID); textOrNull(got.JobTitle) != "Before" {
			t.Fatalf("job_title = %q, want the create-time %q", textOrNull(got.JobTitle), "Before")
		}
	})
}

func TestApplications_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "AppValidateCo")
	tok := c.Token(t, signer)
	other := testutil.Company(t, pool, "AppValidateOther")
	foreignJob := createJobPosting(t, h, other.Token(t, signer), map[string]any{"title": "Foreign"})
	job := createJobPosting(t, h, tok, map[string]any{"title": "Mine"})
	cand := createCandidate(t, h, tok, map[string]any{"full_name": "Mine"})

	t.Run("rejects are validation_failed with the field and create nothing", func(t *testing.T) {
		cases := []struct {
			name  string
			body  map[string]any
			field string
		}{
			{"missing posting", map[string]any{"candidate_id": cand.ID}, "job_posting_id"},
			{"missing candidate", map[string]any{"job_posting_id": job.ID}, "candidate_id"},
			{"null posting", map[string]any{"job_posting_id": nil, "candidate_id": cand.ID}, "job_posting_id"},
			{"empty posting", appBody("", cand.ID, nil), "job_posting_id"},
			{"posting not a uuid", appBody("not-a-uuid", cand.ID, nil), "job_posting_id"},
			{"candidate not a uuid", appBody(job.ID, "12345", nil), "candidate_id"},
			{"stage unknown", appBody(job.ID, cand.ID, map[string]any{"stage": "pending"}), "stage"},
			{"stage wrong case", appBody(job.ID, cand.ID, map[string]any{"stage": "Hired"}), "stage"},
			{"stage empty", appBody(job.ID, cand.ID, map[string]any{"stage": ""}), "stage"},
			{"stage padded", appBody(job.ID, cand.ID, map[string]any{"stage": " hired"}), "stage"},
			{"applied_at not a date", appBody(job.ID, cand.ID, map[string]any{"applied_at": "yesterday"}), "applied_at"},
			{"applied_at impossible date", appBody(job.ID, cand.ID, map[string]any{"applied_at": "2025-02-30"}), "applied_at"},
			{"applied_at with time", appBody(job.ID, cand.ID, map[string]any{"applied_at": "2025-05-03T10:00:00Z"}), "applied_at"},
			// Validation comes before the tenant check: a bad stage on a foreign
			// posting is a 400 about the stage, never a hint about the posting.
			{"stage checked before the foreign posting", appBody(foreignJob.ID, cand.ID, map[string]any{"stage": "nope"}), "stage"},
		}
		before := countCompanyApplications(t, pool, c.CompanyID)
		for _, tc := range cases {
			rec := testutil.Do(t, h, http.MethodPost, applicationsPath, tok, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", tc.name, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("%s: error.code = %q, want validation_failed", tc.name, code)
			}
			var env struct {
				Error struct {
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("%s: decode: %v", tc.name, err)
			}
			if _, ok := env.Error.Fields[tc.field]; !ok {
				t.Fatalf("%s: fields = %v, want an entry for %q", tc.name, env.Error.Fields, tc.field)
			}
		}
		if after := countCompanyApplications(t, pool, c.CompanyID); after != before {
			t.Fatalf("rejected creates left %d row(s)", after-before)
		}
	})

	t.Run("every stage in the enum is accepted", func(t *testing.T) {
		for _, stage := range []string{"applied", "screening", "interview", "offer", "hired", "rejected"} {
			row := createApplication(t, h, tok, appBody(job.ID, cand.ID, map[string]any{"stage": stage}))
			if textOrNull(row.Stage) != stage {
				t.Fatalf("stage = %q, want %q", textOrNull(row.Stage), stage)
			}
		}
	})

	t.Run("malformed bodies and unknown fields are 400 bad_request", func(t *testing.T) {
		before := countCompanyApplications(t, pool, c.CompanyID)
		for name, body := range map[string]string{
			"not json":       `nope`,
			"unknown field":  `{"job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `","salary":1}`,
			"id in body":     `{"id":"` + uuid.NewString() + `","job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `"}`,
			"created_at":     `{"created_at":"2025-01-01T00:00:00Z","job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `"}`,
			"notes a number": `{"job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `","notes":5}`,
			"trailing value": `{"job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `"} {}`,
			"empty body":     ``,
		} {
			rec := doRawBody(t, h, http.MethodPost, applicationsPath, tok, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d (%s), want 400", name, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "bad_request" {
				t.Fatalf("%s: error.code = %q, want bad_request", name, code)
			}
		}
		if after := countCompanyApplications(t, pool, c.CompanyID); after != before {
			t.Fatalf("rejected creates left %d row(s)", after-before)
		}
	})

	t.Run("a body over 1 MiB is 400 and creates nothing", func(t *testing.T) {
		before := countCompanyApplications(t, pool, c.CompanyID)
		big := `{"job_posting_id":"` + job.ID + `","candidate_id":"` + cand.ID + `","notes":"` + strings.Repeat("a", 1<<20) + `"}`
		rec := doRawBody(t, h, http.MethodPost, applicationsPath, tok, big)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
		if after := countCompanyApplications(t, pool, c.CompanyID); after != before {
			t.Fatalf("an oversized create left %d row(s)", after-before)
		}
	})
}

func TestApplications_ListGetFilters(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "AppFilterCo")
	tok := c.Token(t, signer)

	job1 := createJobPosting(t, h, tok, map[string]any{"title": "J1"})
	job2 := createJobPosting(t, h, tok, map[string]any{"title": "J2"})
	cand1 := createCandidate(t, h, tok, map[string]any{"full_name": "C1"})
	cand2 := createCandidate(t, h, tok, map[string]any{"full_name": "C2"})

	a11 := createApplication(t, h, tok, appBody(job1.ID, cand1.ID, map[string]any{"stage": "applied"}))
	a12 := createApplication(t, h, tok, appBody(job1.ID, cand2.ID, map[string]any{"stage": "interview"}))
	a21 := createApplication(t, h, tok, appBody(job2.ID, cand1.ID, map[string]any{"stage": "interview"}))
	a22 := createApplication(t, h, tok, appBody(job2.ID, cand2.ID, map[string]any{"stage": "hired"}))

	list := func(t *testing.T, q string) []applicationRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, applicationsPath+q, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d (%s)", q, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[applicationRow](t, rec)
	}
	same := func(t *testing.T, q string, want ...applicationRow) {
		t.Helper()
		rows := list(t, q)
		got := map[string]bool{}
		for _, r := range rows {
			got[r.ID] = true
		}
		if len(rows) != len(want) {
			t.Fatalf("GET %s -> %v, want %d rows", q, applicationIDs(rows), len(want))
		}
		for _, w := range want {
			if !got[w.ID] {
				t.Fatalf("GET %s -> %v, missing %s", q, applicationIDs(rows), w.ID)
			}
		}
	}

	t.Run("no filter lists everything", func(t *testing.T) { same(t, "", a11, a12, a21, a22) })
	t.Run("id", func(t *testing.T) { same(t, "?id="+a12.ID, a12) })
	t.Run("job_posting_id", func(t *testing.T) {
		same(t, "?job_posting_id="+job1.ID, a11, a12)
		same(t, "?job_posting_id="+job2.ID, a21, a22)
	})
	t.Run("candidate_id", func(t *testing.T) {
		same(t, "?candidate_id="+cand1.ID, a11, a21)
		same(t, "?candidate_id="+cand2.ID, a12, a22)
	})
	t.Run("stage", func(t *testing.T) {
		same(t, "?stage=interview", a12, a21)
		same(t, "?stage=hired", a22)
		same(t, "?stage=applied", a11)
		same(t, "?stage=offer")
	})
	t.Run("filters combine", func(t *testing.T) {
		same(t, "?job_posting_id="+job2.ID+"&stage=interview", a21)
		same(t, "?job_posting_id="+job1.ID+"&candidate_id="+cand2.ID+"&stage=interview", a12)
		same(t, "?job_posting_id="+job1.ID+"&stage=hired")
		same(t, "?id="+a11.ID+"&stage=hired")
	})

	t.Run("limit and offset page deterministically", func(t *testing.T) {
		first, rest := list(t, "?_limit=2"), list(t, "?_limit=2&_offset=2")
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
		got, code := getApplication(t, h, tok, a22.ID)
		if code != http.StatusOK || got.ID != a22.ID || textOrNull(got.Stage) != "hired" {
			t.Fatalf("got %d %+v", code, got)
		}
	})

	t.Run("unknown or malformed ids are 404", func(t *testing.T) {
		for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
			if rec := testutil.Do(t, h, http.MethodGet, applicationsPath+"/"+id, tok, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s: got %d, want 404", id, rec.Code)
			}
		}
	})

	t.Run("unsupported params and bad filter values are validation_failed", func(t *testing.T) {
		for _, q := range []string{
			"?notes=x", "?company_id=" + c.CompanyID, "?_order=created_at.desc", "?bogus=1",
			"?id=not-a-uuid", "?job_posting_id=nope", "?candidate_id=nope", "?stage=pending", "?stage=Hired",
			"?_limit=0", "?_offset=-1",
		} {
			rec := testutil.Do(t, h, http.MethodGet, applicationsPath+q, tok, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s: got %d, want 400", q, rec.Code)
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("GET %s: error.code = %q, want validation_failed", q, code)
			}
		}
	})

	t.Run("a company with no applications lists [] not null", func(t *testing.T) {
		empty := testutil.Company(t, pool, "AppEmptyCo")
		rec := testutil.Do(t, h, http.MethodGet, applicationsPath, empty.Token(t, signer), nil)
		if strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("body = %q, want []", rec.Body.String())
		}
	})

	t.Run("any authenticated role may read and create", func(t *testing.T) {
		emp := c.TokenAs(t, signer, "empleado")
		if rec := testutil.Do(t, h, http.MethodGet, applicationsPath, emp, nil); rec.Code != http.StatusOK {
			t.Fatalf("empleado list: got %d", rec.Code)
		}
		if row := createApplication(t, h, emp, appBody(job1.ID, cand1.ID, nil)); row.CompanyID != c.CompanyID {
			t.Fatalf("empleado create landed in company %s", row.CompanyID)
		}
	})
}

// referenced is a row an application points at, with the SQL that mimics an
// in-flight DELETE of it and the body fields naming it.
type referenced struct {
	name   string
	table  string
	create func(t *testing.T, h http.Handler, tok string) string
	body   func(id, otherID string) map[string]any
	delete string // API path prefix
}

var referencedRows = []referenced{
	{
		name: "job posting", table: "job_postings", delete: jobPostingsPath,
		create: func(t *testing.T, h http.Handler, tok string) string {
			return createJobPosting(t, h, tok, map[string]any{"title": "Raced"}).ID
		},
		body: func(id, other string) map[string]any { return appBody(id, other, nil) },
	},
	{
		name: "candidate", table: "candidates", delete: candidatesPath,
		create: func(t *testing.T, h http.Handler, tok string) string {
			return createCandidate(t, h, tok, map[string]any{"full_name": "Raced"}).ID
		},
		body: func(id, other string) map[string]any { return appBody(other, id, nil) },
	},
}

// TestApplications_CreateRacingADelete pins the lock discipline of the guarded
// insert in both directions, with pg_blocking_pids waits and no sleeps:
//
//   - a delete in flight on the posting/candidate makes the create wait and then
//     answer 404 (the row is gone), not a foreign key violation;
//   - a create in flight (parked after its row locks, before the foreign key
//     check) makes the delete wait and then answer 409 because it counts the new
//     application, so the cascade never eats it.
func TestApplications_CreateRacingADelete(t *testing.T) {
	for _, ref := range referencedRows {
		t.Run(ref.name+"/create waits for a delete in flight and answers 404", func(t *testing.T) {
			pool := testutil.Pool(t)
			h, signer := testutil.Server(t, pool)
			c := testutil.Company(t, pool, "AppRaceDel"+ref.table)
			tok := c.Token(t, signer)
			target := ref.create(t, h, tok)
			okJob := createJobPosting(t, h, tok, map[string]any{"title": "Other"}).ID
			okCand := createCandidate(t, h, tok, map[string]any{"full_name": "Other"}).ID
			other := okCand
			if ref.table == "candidates" {
				other = okJob
			}

			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `DELETE FROM `+ref.table+` WHERE id = $1`, target); err != nil {
				t.Fatalf("delete in tx: %v", err)
			}
			var holderPID int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				t.Fatalf("holder pid: %v", err)
			}

			var wg sync.WaitGroup
			defer func() {
				_ = tx.Rollback(ctx)
				wg.Wait()
			}()
			rec := serveAsync(h, &wg, authedRequest(http.MethodPost, applicationsPath, tok, mustJSON(t, ref.body(target, other))))
			waitUntilBlockedBy(t, pool, holderPID) // the create is parked on the row being deleted

			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit delete: %v", err)
			}
			wg.Wait()
			if rec.Code != http.StatusNotFound {
				t.Fatalf("create got %d (%s), want 404 once the row was deleted", rec.Code, rec.Body.String())
			}
			if n := countCompanyApplications(t, pool, c.CompanyID); n != 0 {
				t.Fatalf("%d application(s) exist, want none", n)
			}
		})

		t.Run(ref.name+"/delete waits for a create in flight and answers 409", func(t *testing.T) {
			pool, _ := testutil.IsolatedSchema(t)
			h, signer := testutil.Server(t, pool)
			c := testutil.Company(t, pool, "AppRaceIns"+ref.table)
			tok := c.Token(t, signer)
			target := ref.create(t, h, tok)
			other := createCandidate(t, h, tok, map[string]any{"full_name": "Other"}).ID
			if ref.table == "candidates" {
				other = createJobPosting(t, h, tok, map[string]any{"title": "Other"}).ID
			}

			const marker = "hold-this-create"
			holderPID, release := parkInsertsInTrigger(t, pool, "applications", "notes", marker)
			var wg sync.WaitGroup
			defer func() {
				release()
				wg.Wait()
			}()

			body := ref.body(target, other)
			body["notes"] = marker
			createRec := serveAsync(h, &wg, authedRequest(http.MethodPost, applicationsPath, tok, mustJSON(t, body)))
			createPID := waitUntilBlockedBy(t, pool, holderPID) // the create holds its row locks, parked in the trigger

			delRec := serveAsync(h, &wg, authedRequest(http.MethodDelete, ref.delete+"/"+target, tok, ""))
			waitUntilBlockedBy(t, pool, createPID) // the delete is queued behind the create

			release()
			wg.Wait()
			if createRec.Code != http.StatusCreated {
				t.Fatalf("create got %d (%s), want 201", createRec.Code, createRec.Body.String())
			}
			if delRec.Code != http.StatusConflict {
				t.Fatalf("delete got %d (%s), want 409 because it must count the new application", delRec.Code, delRec.Body.String())
			}
			if n := countCompanyApplications(t, pool, c.CompanyID); n != 1 {
				t.Fatalf("%d application(s) exist, want the new one to survive", n)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
