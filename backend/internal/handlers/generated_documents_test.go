package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type genDocRow struct {
	ID           string  `json:"id"`
	CompanyID    string  `json:"company_id"`
	EmployeeID   *string `json:"employee_id"`
	EmployeeName *string `json:"employee_name"`
	TemplateID   *string `json:"template_id"`
	TemplateName *string `json:"template_name"`
	DocumentName *string `json:"document_name"`
	Content      string  `json:"content"`
}

func createGenDoc(t *testing.T, h http.Handler, token string, body map[string]any) genDocRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/generated_documents", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed generated_document: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[genDocRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func countGenDocs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM generated_documents`).Scan(&n); err != nil {
		t.Fatalf("count generated_documents: %v", err)
	}
	return n
}

func strVal(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestGeneratedDocuments_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "GDocLeakA")
	b := testutil.Company(t, pool, "GDocLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	docA := createGenDoc(t, h, ta, map[string]any{"document_name": "Carta A", "content": "contenido A"})

	t.Run("get other tenant row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/generated_documents/"+docA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected not_found, got %q", code)
		}
	})

	t.Run("list never includes other tenant rows", func(t *testing.T) {
		createGenDoc(t, h, tb, map[string]any{"document_name": "Carta B", "content": "contenido B"})
		for _, r := range testutil.DecodeRows[genDocRow](t, testutil.Do(t, h, http.MethodGet, "/api/generated_documents", ta, nil)) {
			if r.CompanyID != a.CompanyID || r.Content == "contenido B" {
				t.Fatalf("A's list leaked a foreign row: %+v", r)
			}
		}
	})

	t.Run("patch and delete routes do not exist (405)", func(t *testing.T) {
		for _, m := range []string{http.MethodPatch, http.MethodDelete} {
			rec := testutil.Do(t, h, m, "/api/generated_documents/"+docA.ID, ta, map[string]any{"content": "x"})
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s: expected 405, got %d", m, rec.Code)
			}
		}
	})

	t.Run("company_id in body is 400", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/generated_documents", ta,
			map[string]any{"content": "x", "company_id": b.CompanyID})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestGeneratedDocuments_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "GDocEmpA")
	b := testutil.Company(t, pool, "GDocEmpB")
	ta := a.Token(t, signer)
	empB := testutil.Employee(t, pool, b)

	before := countGenDocs(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/generated_documents", ta,
		map[string]any{"employee_id": empB.ID, "content": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "not_found" {
		t.Fatalf("expected not_found, got %q", code)
	}
	if after := countGenDocs(t, pool); after != before {
		t.Fatalf("row created despite foreign employee_id (%d -> %d)", before, after)
	}
}

func TestGeneratedDocuments_CrossTenantTemplateIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "GDocTplA")
	b := testutil.Company(t, pool, "GDocTplB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	tplB := createTemplate(t, h, tb, map[string]any{"name": "DeB", "content": "b"})

	before := countGenDocs(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/generated_documents", ta,
		map[string]any{"template_id": tplB.ID, "content": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if after := countGenDocs(t, pool); after != before {
		t.Fatalf("row created despite foreign template_id (%d -> %d)", before, after)
	}
}

func TestGeneratedDocuments_NullFKCombinations(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "GDocNullFK")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Pérez"))
	tpl := createTemplate(t, h, tok, map[string]any{"name": "Plantilla Real", "content": "tpl"})

	t.Run("both ids: names derived, client names ignored", func(t *testing.T) {
		got := createGenDoc(t, h, tok, map[string]any{
			"document_name": "Doc", "employee_id": emp.ID, "employee_name": "Spoof E",
			"template_id": tpl.ID, "template_name": "Spoof T", "content": "c",
		})
		if strVal(got.EmployeeName) != "Ana Pérez" || strVal(got.TemplateName) != "Plantilla Real" {
			t.Fatalf("names not derived: %+v / %s / %s", got, strVal(got.EmployeeName), strVal(got.TemplateName))
		}
		if strVal(got.EmployeeID) != emp.ID || strVal(got.TemplateID) != tpl.ID {
			t.Fatalf("ids not stored: %+v", got)
		}
	})

	t.Run("employee only: template name as sent", func(t *testing.T) {
		got := createGenDoc(t, h, tok, map[string]any{
			"employee_id": emp.ID, "employee_name": "Spoof E", "template_name": "Plantilla Libre", "content": "c",
		})
		if strVal(got.EmployeeName) != "Ana Pérez" {
			t.Fatalf("employee name not derived: %s", strVal(got.EmployeeName))
		}
		if got.TemplateID != nil || strVal(got.TemplateName) != "Plantilla Libre" {
			t.Fatalf("template side wrong: id=%v name=%s", got.TemplateID, strVal(got.TemplateName))
		}
	})

	t.Run("template only: employee name as sent", func(t *testing.T) {
		got := createGenDoc(t, h, tok, map[string]any{
			"template_id": tpl.ID, "template_name": "Spoof T", "employee_name": "Nombre Libre", "content": "c",
		})
		if strVal(got.TemplateName) != "Plantilla Real" {
			t.Fatalf("template name not derived: %s", strVal(got.TemplateName))
		}
		if got.EmployeeID != nil || strVal(got.EmployeeName) != "Nombre Libre" {
			t.Fatalf("employee side wrong: id=%v name=%s", got.EmployeeID, strVal(got.EmployeeName))
		}
	})

	t.Run("neither id: both FKs NULL, names as sent", func(t *testing.T) {
		got := createGenDoc(t, h, tok, map[string]any{"content": "c"})
		if got.EmployeeID != nil || got.TemplateID != nil || got.EmployeeName != nil || got.TemplateName != nil {
			t.Fatalf("expected all NULL, got %+v", got)
		}
		sent := createGenDoc(t, h, tok, map[string]any{"employee_name": "X", "template_name": "Y", "content": "c"})
		if strVal(sent.EmployeeName) != "X" || strVal(sent.TemplateName) != "Y" {
			t.Fatalf("names should be stored as sent: %+v", sent)
		}
	})
}

func TestGeneratedDocuments_ContentStoredVerbatim(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "GDocVerbatim")
	tok := c.Token(t, signer)

	created := createGenDoc(t, h, tok, map[string]any{"document_name": "D", "content": "Hola {{NOMBRE}} <b>x</b>"})
	if created.Content != "Hola {{NOMBRE}} <b>x</b>" {
		t.Fatalf("content altered on create: %q", created.Content)
	}
	got := testutil.DecodeRow[genDocRow](t, testutil.Do(t, h, http.MethodGet, "/api/generated_documents/"+created.ID, tok, nil))
	if got.Content != "Hola {{NOMBRE}} <b>x</b>" {
		t.Fatalf("content altered on get: %q", got.Content)
	}
}

func TestGeneratedDocuments_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "GDocFilterID")
	tok := c.Token(t, signer)

	first := createGenDoc(t, h, tok, map[string]any{"content": "one"})
	createGenDoc(t, h, tok, map[string]any{"content": "two"})

	rows := testutil.DecodeRows[genDocRow](t, testutil.Do(t, h, http.MethodGet, "/api/generated_documents?id="+first.ID, tok, nil))
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("expected exactly the filtered row, got %+v", rows)
	}
}

func TestGeneratedDocuments_ListFilters(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "GDocFilters")
	tok := c.Token(t, signer)
	emp1 := testutil.Employee(t, pool, c)
	emp2 := testutil.Employee(t, pool, c)
	tpl := createTemplate(t, h, tok, map[string]any{"name": "T", "content": "t"})

	d1 := createGenDoc(t, h, tok, map[string]any{"employee_id": emp1.ID, "template_id": tpl.ID, "content": "1"})
	createGenDoc(t, h, tok, map[string]any{"employee_id": emp2.ID, "content": "2"})

	byEmp := testutil.DecodeRows[genDocRow](t, testutil.Do(t, h, http.MethodGet, "/api/generated_documents?employee_id="+emp1.ID, tok, nil))
	if len(byEmp) != 1 || byEmp[0].ID != d1.ID {
		t.Fatalf("employee_id filter wrong: %+v", byEmp)
	}
	byTpl := testutil.DecodeRows[genDocRow](t, testutil.Do(t, h, http.MethodGet, "/api/generated_documents?template_id="+tpl.ID, tok, nil))
	if len(byTpl) != 1 || byTpl[0].ID != d1.ID {
		t.Fatalf("template_id filter wrong: %+v", byTpl)
	}

	for _, q := range []string{"?bogus=1", "?_order=created_at", "?employee_id=not-a-uuid", "?template_id=nope", "?id=zzz"} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/generated_documents"+q, tok, nil)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: expected 400 validation_failed, got %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestGeneratedDocuments_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "GDocValidate")
	tok := c.Token(t, signer)

	cases := map[string]map[string]any{
		"missing content":    {"document_name": "x"},
		"blank content":      {"content": "   "},
		"malformed employee": {"content": "x", "employee_id": "not-a-uuid"},
		"malformed template": {"content": "x", "template_id": "not-a-uuid"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := testutil.Do(t, h, http.MethodPost, "/api/generated_documents", tok, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("expected validation_failed, got %q", code)
			}
		})
	}
}

func TestGeneratedDocuments_CompanyCleanupCascades(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	// testutil.Company registers a cleanup that deletes the company; the
	// NO ACTION FKs from generated_documents must not make it fail.
	c := testutil.Company(t, pool, "GDocCleanup")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)
	tpl := createTemplate(t, h, tok, map[string]any{"name": "T", "content": "t"})
	createGenDoc(t, h, tok, map[string]any{"employee_id": emp.ID, "template_id": tpl.ID, "content": "x"})
}
