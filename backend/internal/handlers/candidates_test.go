package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestCandidates_CrossTenantLeak covers XC-1: GET, PATCH, DELETE and LIST
// never reach another company's candidates and a create is always bound to the
// token's company.
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

	t.Run("patch other tenant row is 404 and leaves it byte-identical", func(t *testing.T) {
		before := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+candA.ID, ta, nil).Body.String()
		for _, body := range []map[string]any{{"full_name": "Hacked"}, {"email": "evil@x.test", "nationality": nil}} {
			rec := testutil.Do(t, h, http.MethodPatch, candidatesPath+"/"+candA.ID, tb, body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("PATCH %v: got %d (%s), want 404", body, rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "not_found" {
				t.Fatalf("error.code = %q, want not_found", code)
			}
		}
		if after := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+candA.ID, ta, nil).Body.String(); after != before {
			t.Fatalf("row changed by a cross-tenant PATCH:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("delete other tenant row is 404 and the row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, candidatesPath+"/"+candA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+candA.ID, ta, nil); got.Code != http.StatusOK {
			t.Fatalf("row did not survive the cross-tenant delete: %d", got.Code)
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

	t.Run("a role with the recruitment permission may read and create", func(t *testing.T) {
		seedMedRole(t, pool, c, "empleado", `{"recruitment": true}`)
		emp := c.TokenAs(t, pool, signer, "empleado")
		if rec := testutil.Do(t, h, http.MethodGet, candidatesPath, emp, nil); rec.Code != http.StatusOK {
			t.Fatalf("empleado list: got %d", rec.Code)
		}
		row := createCandidate(t, h, emp, map[string]any{"full_name": "Hecho por empleado"})
		if row.CompanyID != c.CompanyID {
			t.Fatalf("empleado-created row company = %s, want %s", row.CompanyID, c.CompanyID)
		}
	})
}

func getCandidate(t *testing.T, h http.Handler, token, id string) (candidateRow, int) {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, candidatesPath+"/"+id, token, nil)
	if rec.Code != http.StatusOK {
		return candidateRow{}, rec.Code
	}
	return testutil.DecodeRow[candidateRow](t, rec), rec.Code
}

func TestCandidates_CrudRoundTrip(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandCrudCo")
	tok := c.Token(t, signer)

	created := createCandidate(t, h, tok, map[string]any{
		"full_name": "Original", "email": "o@x.test", "phone": "111", "id_number": "8-1-1",
		"nationality": "Colombiana", "resume_notes": "notas",
	})
	path := candidatesPath + "/" + created.ID

	t.Run("patch changes only the sent fields and returns a one-element array", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"full_name": "  Renombrado  ", "phone": "222"})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		rows := testutil.DecodeRows[candidateRow](t, rec)
		if len(rows) != 1 {
			t.Fatalf("PATCH returned %d rows, want a one-element array", len(rows))
		}
		got := rows[0]
		if got.FullName != "Renombrado" || textOrNull(got.Phone) != "222" {
			t.Fatalf("full_name/phone = %q/%s", got.FullName, textOrNull(got.Phone))
		}
		if textOrNull(got.Email) != "o@x.test" || textOrNull(got.IDNumber) != "8-1-1" ||
			textOrNull(got.Nationality) != "Colombiana" || textOrNull(got.ResumeNotes) != "notas" {
			t.Fatalf("unsent fields changed: %+v", got)
		}
		if got.ID != created.ID || got.CompanyID != c.CompanyID || got.CreatedAt != created.CreatedAt {
			t.Fatalf("identity fields changed: %+v", got)
		}
		stored, _ := getCandidate(t, h, tok, created.ID)
		if stored.FullName != "Renombrado" || textOrNull(stored.Phone) != "222" {
			t.Fatalf("GET after PATCH = %q/%s", stored.FullName, textOrNull(stored.Phone))
		}
	})

	t.Run("null clears an optional field including nationality", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"email": nil, "nationality": nil})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRows[candidateRow](t, rec)[0]
		if got.Email != nil || got.Nationality != nil {
			t.Fatalf("email/nationality = %s/%s, want null", textOrNull(got.Email), textOrNull(got.Nationality))
		}
		if textOrNull(got.Phone) != "222" || got.FullName != "Renombrado" {
			t.Fatalf("a null on one field disturbed others: %+v", got)
		}
	})

	t.Run("a blank string is stored as blank, distinct from null", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"phone": ""})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := testutil.DecodeRows[candidateRow](t, rec)[0]; got.Phone == nil || *got.Phone != "" {
			t.Fatalf("phone = %s, want the stored blank string", textOrNull(got.Phone))
		}
	})

	t.Run("empty patch is a no-op that returns the row", func(t *testing.T) {
		before, _ := getCandidate(t, h, tok, created.ID)
		rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d (%s)", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRows[candidateRow](t, rec)[0]
		wantJSON, _ := json.Marshal(before)
		gotJSON, _ := json.Marshal(got)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("empty patch changed the row:\nbefore %s\nafter  %s", wantJSON, gotJSON)
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
		if _, code := getCandidate(t, h, tok, created.ID); code != http.StatusNotFound {
			t.Fatalf("GET after delete = %d, want 404", code)
		}
		if n := countCandidates(t, pool, c.CompanyID); n != 0 {
			t.Fatalf("%d rows left after delete", n)
		}
	})

	t.Run("a deleted or malformed id is 404 for patch and delete", func(t *testing.T) {
		for _, id := range []string{created.ID, uuid.NewString(), "not-a-uuid"} {
			for _, m := range []string{http.MethodPatch, http.MethodDelete} {
				rec := testutil.Do(t, h, m, candidatesPath+"/"+id, tok, map[string]any{"full_name": "x"})
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s %s: got %d, want 404", m, id, rec.Code)
				}
			}
		}
	})
}

func TestCandidates_PatchValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandPatchValidationCo")
	tok := c.Token(t, signer)

	cand := createCandidate(t, h, tok, map[string]any{"full_name": "Base", "email": "base@x.test"})
	path := candidatesPath + "/" + cand.ID
	baseline := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String()

	cases := []struct {
		name  string
		body  string
		code  string
		field string
	}{
		{"blank full_name", `{"full_name":"   "}`, "validation_failed", "full_name"},
		{"null full_name", `{"full_name":null}`, "validation_failed", "full_name"},
		{"full_name over 200 runes", `{"full_name":"` + strings.Repeat("ñ", 201) + `"}`, "validation_failed", "full_name"},
		{"unknown field", `{"bogus":1}`, "bad_request", ""},
		{"known key in another case", `{"FULL_NAME":"Renamed"}`, "bad_request", ""},
		{"company_id in body", `{"company_id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"id in body", `{"id":"` + uuid.NewString() + `"}`, "bad_request", ""},
		{"created_at in body", `{"created_at":"2020-01-01T00:00:00Z"}`, "bad_request", ""},
		{"full_name wrong type", `{"full_name":5}`, "bad_request", ""},
		{"email wrong type", `{"email":5}`, "bad_request", ""},
		{"malformed json", `{"full_name":`, "bad_request", ""},
		{"not an object", `["full_name"]`, "bad_request", ""},
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

	t.Run("a body over 1 MiB is 400 bad_request and leaves the row unchanged", func(t *testing.T) {
		const prefix, suffix = `{"resume_notes":"`, `"}`
		body := prefix + strings.Repeat("a", 1<<20+1-len(prefix)-len(suffix)) + suffix
		rec := doRawBody(t, h, http.MethodPatch, path, tok, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
		if after := testutil.Do(t, h, http.MethodGet, path, tok, nil).Body.String(); after != baseline {
			t.Fatalf("an oversized PATCH changed the row")
		}
	})
}

func seedCandidateApplication(t *testing.T, pool *pgxpool.Pool, companyID, candidateID string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO applications (id, company_id, candidate_id, job_title, candidate_name)
		 VALUES ($1, $2, $3, 'Seed', 'Candidate')`, id, companyID, candidateID); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	return id
}

func countCandidateApplications(t *testing.T, pool *pgxpool.Pool, candidateID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM applications WHERE candidate_id = $1`, candidateID).Scan(&n); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	return n
}

// TestCandidates_DeleteBlockedWhileApplicationsExist pins REC-4: the schema
// CASCADE never fires through the API, a delete with applications is 409 with
// the count, and nothing is removed.
func TestCandidates_DeleteBlockedWhileApplicationsExist(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandDeleteRestrictCo")
	tok := c.Token(t, signer)

	withApps := createCandidate(t, h, tok, map[string]any{"full_name": "With applications"})
	other := createCandidate(t, h, tok, map[string]any{"full_name": "Other"})
	seedCandidateApplication(t, pool, c.CompanyID, withApps.ID)
	seedCandidateApplication(t, pool, c.CompanyID, withApps.ID)
	seedCandidateApplication(t, pool, c.CompanyID, other.ID)

	rec := testutil.Do(t, h, http.MethodDelete, candidatesPath+"/"+withApps.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("error.code = %q, want conflict", code)
	}
	if msg := errMessage(t, rec); !strings.HasPrefix(msg, "2 postulación") {
		t.Fatalf("409 message = %q, want it to start with the application count %q", msg, "2 postulación")
	}
	if _, code := getCandidate(t, h, tok, withApps.ID); code != http.StatusOK {
		t.Fatalf("candidate was removed by a blocked delete: %d", code)
	}
	if n := countCandidateApplications(t, pool, withApps.ID); n != 2 {
		t.Fatalf("applications = %d after a blocked delete, want 2 (cascade must not fire)", n)
	}

	t.Run("another candidate's applications do not block this delete", func(t *testing.T) {
		third := createCandidate(t, h, tok, map[string]any{"full_name": "No applications"})
		if rec := testutil.Do(t, h, http.MethodDelete, candidatesPath+"/"+third.ID, tok, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", rec.Code, rec.Body.String())
		}
		if n := countCandidateApplications(t, pool, other.ID); n != 1 {
			t.Fatalf("unrelated applications = %d, want 1", n)
		}
	})

	t.Run("delete succeeds once the applications are gone", func(t *testing.T) {
		if _, err := pool.Exec(context.Background(), `DELETE FROM applications WHERE candidate_id = $1`, withApps.ID); err != nil {
			t.Fatalf("remove applications: %v", err)
		}
		if rec := testutil.Do(t, h, http.MethodDelete, candidatesPath+"/"+withApps.ID, tok, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", rec.Code, rec.Body.String())
		}
	})
}

// TestCandidates_DeleteWaitsForAnInFlightApplication holds an uncommitted
// application insert open while DELETE runs. The delete must not count past it
// and cascade the application away: it waits on the candidate row and then
// answers 409 once the insert commits.
func TestCandidates_DeleteWaitsForAnInFlightApplication(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandDeleteRaceCo")
	tok := c.Token(t, signer)
	cand := createCandidate(t, h, tok, map[string]any{"full_name": "Racy"})

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`INSERT INTO applications (company_id, candidate_id, job_title, candidate_name)
		 VALUES ($1, $2, 'Racy', 'Inflight')`, c.CompanyID, cand.ID); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	var holderPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, candidatesPath+"/"+cand.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(rec, req)
	}()
	waitUntilBlockedBy(t, pool, holderPID) // the handler is parked on the candidate row lock
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	wg.Wait()

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if n := countCandidateApplications(t, pool, cand.ID); n != 1 {
		t.Fatalf("applications = %d, want the in-flight one to survive", n)
	}
	if _, code := getCandidate(t, h, tok, cand.ID); code != http.StatusOK {
		t.Fatalf("candidate was deleted under an in-flight application: %d", code)
	}
}

// TestCandidates_PatchInFlightDoesNotBlockApplicationInsert is the candidate
// twin of the job-posting test: the PATCH merge read takes FOR NO KEY UPDATE, so
// an application insert for the same candidate completes while the PATCH is
// still in flight. DELETE keeps FOR UPDATE.
func TestCandidates_PatchInFlightDoesNotBlockApplicationInsert(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "CandPatchHoldCo")
	tok := c.Token(t, signer)
	cand := createCandidate(t, h, tok, map[string]any{"full_name": "Held"})

	const holdName = "hold-this-patch"
	holderPID, release := parkPatchesInTrigger(t, pool, "candidates", "full_name", holdName)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodPatch, candidatesPath+"/"+cand.ID, strings.NewReader(`{"full_name":"`+holdName+`"}`))
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
		`INSERT INTO applications (company_id, candidate_id, job_title, candidate_name)
		 VALUES ($1, $2, 'Held', 'Candidate')`, c.CompanyID, cand.ID); err != nil {
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
	if got := testutil.DecodeRows[candidateRow](t, rec)[0]; got.FullName != holdName {
		t.Fatalf("PATCH full_name = %q, want %q", got.FullName, holdName)
	}
	if n := countCandidateApplications(t, pool, cand.ID); n != 1 {
		t.Fatalf("applications = %d, want the concurrent insert to be kept", n)
	}
}
