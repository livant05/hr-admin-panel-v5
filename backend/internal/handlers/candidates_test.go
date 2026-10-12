package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type candidateRow struct {
	ID          string  `json:"id"`
	CompanyID   string  `json:"company_id"`
	FullName    string  `json:"full_name"`
	Email       *string `json:"email"`
	Phone       *string `json:"phone"`
	Nationality *string `json:"nationality"`
	IDNumber    *string `json:"id_number"`
	ResumeNotes *string `json:"resume_notes"`
	CreatedAt   string  `json:"created_at"`
}

const candidatesPath = "/api/candidates"

func createCandidate(t *testing.T, h http.Handler, token string, body map[string]any) candidateRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, candidatesPath, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create candidate %v: got %d (%s)", body, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[candidateRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create returned %d rows, want a one-element array", len(rows))
	}
	return rows[0]
}

func countCandidates(t *testing.T, pool *pgxpool.Pool, companyID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM candidates WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		t.Fatalf("count candidates: %v", err)
	}
	return n
}

func candidateIDs(rows []candidateRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func textOrNull(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

// TestCandidates_CrossTenantLeak covers XC-1 for the read and create side:
// GET and LIST never reach another company's candidates and a create is always
// bound to the token's company. PATCH and DELETE are added with S2d.
func TestCandidates_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "CandLeakA")
	b := testutil.Company(t, pool, "CandLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	candA := createCandidate(t, h, ta, map[string]any{"full_name": "Ana de A", "email": "ana@a.test"})
	candB := createCandidate(t, h, tb, map[string]any{"full_name": "Beto de B"})

	t.Run("get other tenant row is 404 not 403", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+candA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("error.code = %q, want not_found", code)
		}
		if strings.Contains(rec.Body.String(), "Ana de A") || strings.Contains(rec.Body.String(), "ana@a.test") {
			t.Fatalf("404 body leaks the other tenant's data: %s", rec.Body.String())
		}
	})

	t.Run("list returns only own rows", func(t *testing.T) {
		aRows := testutil.DecodeRows[candidateRow](t, testutil.Do(t, h, http.MethodGet, candidatesPath, ta, nil))
		if got := candidateIDs(aRows); len(got) != 1 || got[0] != candA.ID {
			t.Fatalf("A's list = %v, want only %s", got, candA.ID)
		}
		bRows := testutil.DecodeRows[candidateRow](t, testutil.Do(t, h, http.MethodGet, candidatesPath, tb, nil))
		if got := candidateIDs(bRows); len(got) != 1 || got[0] != candB.ID {
			t.Fatalf("B's list = %v, want only %s", got, candB.ID)
		}
	})

	t.Run("id filter cannot reach another tenant", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"?id="+candA.ID, tb, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rows := testutil.DecodeRows[candidateRow](t, rec); len(rows) != 0 {
			t.Fatalf("B filtered by A's id got %v, want []", candidateIDs(rows))
		}
		own := testutil.DecodeRows[candidateRow](t, testutil.Do(t, h, http.MethodGet, candidatesPath+"?id="+candA.ID, ta, nil))
		if len(own) != 1 || own[0].ID != candA.ID {
			t.Fatalf("A filtered by its own id got %v, want [%s]", candidateIDs(own), candA.ID)
		}
	})

	t.Run("company_id in body is rejected and nothing is created", func(t *testing.T) {
		before := countCandidates(t, pool, a.CompanyID) + countCandidates(t, pool, b.CompanyID)
		rec := testutil.Do(t, h, http.MethodPost, candidatesPath, ta, map[string]any{
			"full_name": "Sneaky", "company_id": b.CompanyID,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		after := countCandidates(t, pool, a.CompanyID) + countCandidates(t, pool, b.CompanyID)
		if after != before {
			t.Fatalf("rows went from %d to %d on a rejected create", before, after)
		}
	})

	t.Run("created row belongs to the token company", func(t *testing.T) {
		if candA.CompanyID != a.CompanyID || candB.CompanyID != b.CompanyID {
			t.Fatalf("company ids = %s/%s, want %s/%s", candA.CompanyID, candB.CompanyID, a.CompanyID, b.CompanyID)
		}
	})

	t.Run("no token is 401", func(t *testing.T) {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			rec := testutil.Do(t, h, m, candidatesPath, "", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s without token: got %d, want 401", m, rec.Code)
			}
		}
	})
}

func TestCandidates_CreateReturnsArrayWithDefaults(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandCreateCo")
	tok := c.Token(t, signer)

	t.Run("minimal body trims the name, defaults nationality and leaves optionals null", func(t *testing.T) {
		row := createCandidate(t, h, tok, map[string]any{"full_name": "  Ana Torres  "})
		if row.FullName != "Ana Torres" {
			t.Fatalf("full_name = %q, want trimmed %q", row.FullName, "Ana Torres")
		}
		if row.Nationality == nil || *row.Nationality != "Panameña" {
			t.Fatalf("nationality = %s, want the default Panameña", textOrNull(row.Nationality))
		}
		if row.Email != nil || row.Phone != nil || row.IDNumber != nil || row.ResumeNotes != nil {
			t.Fatalf("optional fields not null: %+v", row)
		}
		if _, err := uuid.Parse(row.ID); err != nil {
			t.Fatalf("id %q is not a server-assigned uuid: %v", row.ID, err)
		}
		if row.CreatedAt == "" {
			t.Fatalf("created_at missing")
		}
	})

	t.Run("an explicit null nationality also gets the default", func(t *testing.T) {
		row := createCandidate(t, h, tok, map[string]any{"full_name": "Null Nat", "nationality": nil})
		if row.Nationality == nil || *row.Nationality != "Panameña" {
			t.Fatalf("nationality = %s, want the default Panameña", textOrNull(row.Nationality))
		}
	})

	t.Run("an explicit nationality wins over the default", func(t *testing.T) {
		row := createCandidate(t, h, tok, map[string]any{"full_name": "Luis", "nationality": "Colombiana"})
		if row.Nationality == nil || *row.Nationality != "Colombiana" {
			t.Fatalf("nationality = %s, want Colombiana", textOrNull(row.Nationality))
		}
	})

	t.Run("full body round trips through GET", func(t *testing.T) {
		row := createCandidate(t, h, tok, map[string]any{
			"full_name": "Carmen Ríos", "email": "carmen@x.test", "phone": "+507 6000-0000",
			"nationality": "Panameña", "id_number": "8-123-456", "resume_notes": "5 años en RRHH",
		})
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+row.ID, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: got %d", rec.Code)
		}
		got := testutil.DecodeRow[candidateRow](t, rec)
		want := map[string]string{
			"full_name": "Carmen Ríos", "email": "carmen@x.test", "phone": "+507 6000-0000",
			"nationality": "Panameña", "id_number": "8-123-456", "resume_notes": "5 años en RRHH",
		}
		have := map[string]string{
			"full_name": got.FullName, "email": textOrNull(got.Email), "phone": textOrNull(got.Phone),
			"nationality": textOrNull(got.Nationality), "id_number": textOrNull(got.IDNumber), "resume_notes": textOrNull(got.ResumeNotes),
		}
		for k, w := range want {
			if have[k] != w {
				t.Fatalf("%s = %q, want %q", k, have[k], w)
			}
		}
	})

	t.Run("frontend shape with blank strings stores them as blank, not null", func(t *testing.T) {
		row := createCandidate(t, h, tok, map[string]any{
			"full_name": "Solo Nombre", "id_number": "", "email": "", "phone": "",
			"nationality": "", "resume_notes": "",
		})
		for name, got := range map[string]*string{
			"id_number": row.IDNumber, "email": row.Email, "phone": row.Phone,
			"nationality": row.Nationality, "resume_notes": row.ResumeNotes,
		} {
			if got == nil || *got != "" {
				t.Fatalf("%s = %s, want the stored blank string", name, textOrNull(got))
			}
		}
	})
}

func TestCandidates_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandValidationCo")
	tok := c.Token(t, signer)

	cases := []struct {
		name  string
		body  string
		code  string
		field string
	}{
		{"missing full_name", `{}`, "validation_failed", "full_name"},
		{"blank full_name", `{"full_name":"   "}`, "validation_failed", "full_name"},
		{"null full_name", `{"full_name":null}`, "validation_failed", "full_name"},
		{"full_name over 200 runes", `{"full_name":"` + strings.Repeat("a", 201) + `"}`, "validation_failed", "full_name"},
		{"full_name over 200 runes multibyte", `{"full_name":"` + strings.Repeat("ñ", 201) + `"}`, "validation_failed", "full_name"},
		{"unknown field", `{"full_name":"x","bogus":1}`, "bad_request", ""},
		{"id in body", `{"full_name":"x","id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"created_at in body", `{"full_name":"x","created_at":"2020-01-01T00:00:00Z"}`, "bad_request", ""},
		{"company_id in body", `{"full_name":"x","company_id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"full_name wrong type", `{"full_name":5}`, "bad_request", ""},
		{"email wrong type", `{"full_name":"x","email":5}`, "bad_request", ""},
		{"malformed json", `{"full_name":`, "bad_request", ""},
		{"trailing value", `{"full_name":"x"}{"full_name":"y"}`, "bad_request", ""},
		{"empty body", ``, "bad_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := countCandidates(t, pool, c.CompanyID)
			rec := doRawBody(t, h, http.MethodPost, candidatesPath, tok, tc.body)
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
			if after := countCandidates(t, pool, c.CompanyID); after != before {
				t.Fatalf("a rejected create left %d new row(s)", after-before)
			}
		})
	}

	t.Run("accepts a full_name of exactly 200 runes", func(t *testing.T) {
		name := strings.Repeat("ñ", 200)
		rec := doRawBody(t, h, http.MethodPost, candidatesPath, tok, `{"full_name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("got %d (%s), want 201", rec.Code, rec.Body.String())
		}
		if got := testutil.DecodeRows[candidateRow](t, rec)[0].FullName; got != name {
			t.Fatalf("stored name has %d runes, want 200", len([]rune(got)))
		}
	})

	t.Run("a body over 1 MiB is 400 bad_request and creates nothing", func(t *testing.T) {
		const prefix, suffix = `{"full_name":"big","resume_notes":"`, `"}`
		body := prefix + strings.Repeat("a", 1<<20+1-len(prefix)-len(suffix)) + suffix
		before := countCandidates(t, pool, c.CompanyID)
		rec := doRawBody(t, h, http.MethodPost, candidatesPath, tok, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "bad_request" {
			t.Fatalf("error.code = %q, want bad_request", code)
		}
		if after := countCandidates(t, pool, c.CompanyID); after != before {
			t.Fatalf("an oversized create left %d new row(s)", after-before)
		}
	})
}

func TestCandidates_ListGetFilters(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandFilterCo")
	tok := c.Token(t, signer)

	one := createCandidate(t, h, tok, map[string]any{"full_name": "Uno"})
	two := createCandidate(t, h, tok, map[string]any{"full_name": "Dos"})
	three := createCandidate(t, h, tok, map[string]any{"full_name": "Tres"})
	createCandidate(t, h, tok, map[string]any{"full_name": "Cuatro"})

	list := func(t *testing.T, query string) []candidateRow {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath+query, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d (%s)", query, rec.Code, rec.Body.String())
		}
		return testutil.DecodeRows[candidateRow](t, rec)
	}

	t.Run("no filter lists everything newest first", func(t *testing.T) {
		rows := list(t, "")
		if len(rows) != 4 {
			t.Fatalf("got %d rows, want 4", len(rows))
		}
		if rows[3].ID != one.ID {
			t.Fatalf("oldest row is %s, want the first created %s last", rows[3].ID, one.ID)
		}
	})

	t.Run("id filter returns exactly that candidate", func(t *testing.T) {
		rows := list(t, "?id="+two.ID)
		if len(rows) != 1 || rows[0].ID != two.ID || rows[0].FullName != "Dos" {
			t.Fatalf("id=%s -> %+v, want only Dos", two.ID, rows)
		}
		if rows := list(t, "?id="+uuid.NewString()); len(rows) != 0 {
			t.Fatalf("unknown id -> %v, want []", candidateIDs(rows))
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
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+three.ID, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
		got := testutil.DecodeRow[candidateRow](t, rec)
		if got.ID != three.ID || got.FullName != "Tres" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("unknown or malformed ids are 404", func(t *testing.T) {
		for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+id, tok, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s: got %d, want 404", id, rec.Code)
			}
		}
	})

	t.Run("unsupported params are validation_failed", func(t *testing.T) {
		for _, q := range []string{"?full_name=Uno", "?nationality=x", "?company_id=" + c.CompanyID, "?_order=created_at.desc", "?bogus=1", "?id=not-a-uuid", "?_limit=0", "?_offset=-1"} {
			rec := testutil.Do(t, h, http.MethodGet, candidatesPath+q, tok, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s: got %d, want 400", q, rec.Code)
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("GET %s: error.code = %q, want validation_failed", q, code)
			}
		}
	})

	t.Run("a company with no candidates lists [] not null", func(t *testing.T) {
		empty := testutil.Company(t, pool, "CandEmptyCo")
		rec := testutil.Do(t, h, http.MethodGet, candidatesPath, empty.Token(t, signer), nil)
		if strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("body = %q, want []", rec.Body.String())
		}
	})

	t.Run("any authenticated role may read and create", func(t *testing.T) {
		emp := c.TokenAs(t, signer, "empleado")
		if rec := testutil.Do(t, h, http.MethodGet, candidatesPath, emp, nil); rec.Code != http.StatusOK {
			t.Fatalf("empleado list: got %d", rec.Code)
		}
		row := createCandidate(t, h, emp, map[string]any{"full_name": "Hecho por empleado"})
		if row.CompanyID != c.CompanyID {
			t.Fatalf("empleado-created row company = %s, want %s", row.CompanyID, c.CompanyID)
		}
	})
}
