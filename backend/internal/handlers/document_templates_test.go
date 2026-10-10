package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type templateRow struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

func createTemplate(t *testing.T, h http.Handler, token string, body map[string]any) templateRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/document_templates", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed template: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[templateRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

// seedGeneratedDocument inserts a generated_documents row directly: the
// generated_documents handler does not exist in this slice (4c).
func seedGeneratedDocument(t *testing.T, pool *pgxpool.Pool, companyID, templateID string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO generated_documents (company_id, template_id, content) VALUES ($1, $2, 'x')`,
		companyID, templateID)
	if err != nil {
		t.Fatalf("seed generated_document: %v", err)
	}
}

func TestDocumentTemplates_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "TplLeakA")
	b := testutil.Company(t, pool, "TplLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)

	tplA := createTemplate(t, h, ta, map[string]any{"name": "Carta", "content": "Hola"})

	t.Run("get other tenant row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/document_templates/"+tplA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if code := testutil.ErrCode(t, rec); code != "not_found" {
			t.Fatalf("expected not_found, got %q", code)
		}
	})

	t.Run("patch other tenant row is 404 and row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/document_templates/"+tplA.ID, tb,
			map[string]any{"name": "Hacked", "type": "x", "content": "Hacked"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		got := testutil.DecodeRow[templateRow](t, testutil.Do(t, h, http.MethodGet, "/api/document_templates/"+tplA.ID, ta, nil))
		if got.Name != "Carta" || got.Content != "Hola" {
			t.Fatalf("row mutated cross-tenant: %+v", got)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/document_templates/"+tplA.ID, tb, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodGet, "/api/document_templates/"+tplA.ID, ta, nil); rec.Code != http.StatusOK {
			t.Fatalf("expected row to survive, got %d", rec.Code)
		}
	})

	t.Run("list never includes other tenant rows", func(t *testing.T) {
		createTemplate(t, h, tb, map[string]any{"name": "DeB", "content": "b"})
		for _, r := range testutil.DecodeRows[templateRow](t, testutil.Do(t, h, http.MethodGet, "/api/document_templates", ta, nil)) {
			if r.CompanyID != a.CompanyID || r.Name == "DeB" {
				t.Fatalf("A's list leaked a foreign row: %+v", r)
			}
		}
	})

	t.Run("company_id in body is 400", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/document_templates", ta,
			map[string]any{"name": "x", "content": "y", "company_id": b.CompanyID})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestDocumentTemplates_CreateRoundTripAndDefaults(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplRoundTrip")
	tok := c.Token(t, signer)

	got := createTemplate(t, h, tok, map[string]any{"name": "Carta", "type": "letter", "content": "Hola {{NOMBRE}}"})
	if got.Content != "Hola {{NOMBRE}}" || got.Type != "letter" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	def := createTemplate(t, h, tok, map[string]any{"name": "Sin tipo", "content": "c"})
	if def.Type != "contract" {
		t.Fatalf("expected default type contract, got %q", def.Type)
	}

	free := createTemplate(t, h, tok, map[string]any{"name": "Libre", "type": "anything", "content": "c"})
	if free.Type != "anything" {
		t.Fatalf("type must be free text, got %q", free.Type)
	}
}

func TestDocumentTemplates_ValidationAndQueryParams(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplValidation")
	tok := c.Token(t, signer)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"empty name", map[string]any{"name": "", "content": "c"}, "name"},
		{"whitespace name", map[string]any{"name": "   ", "content": "c"}, "name"},
		{"missing content", map[string]any{"name": "n"}, "content"},
		{"empty content", map[string]any{"name": "n", "content": ""}, "content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testutil.Do(t, h, http.MethodPost, "/api/document_templates", tok, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
			}
			if code := testutil.ErrCode(t, rec); code != "validation_failed" {
				t.Fatalf("expected validation_failed, got %q", code)
			}
			if !strings.Contains(rec.Body.String(), `"`+tc.field+`"`) {
				t.Fatalf("expected fields to name %q, body=%s", tc.field, rec.Body.String())
			}
		})
	}

	for _, q := range []string{"?bogus=1", "?_order=name.asc", "?id=not-a-uuid"} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/document_templates"+q, tok, nil)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: expected 400 validation_failed, got %d", q, rec.Code)
		}
	}
}

func TestDocumentTemplates_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplFilterID")
	tok := c.Token(t, signer)

	one := createTemplate(t, h, tok, map[string]any{"name": "Uno", "type": "letter", "content": "1"})
	createTemplate(t, h, tok, map[string]any{"name": "Dos", "type": "contract", "content": "2"})

	rows := testutil.DecodeRows[templateRow](t, testutil.Do(t, h, http.MethodGet, "/api/document_templates?id="+one.ID, tok, nil))
	if len(rows) != 1 || rows[0].ID != one.ID {
		t.Fatalf("expected exactly [one], got %+v", rows)
	}

	byType := testutil.DecodeRows[templateRow](t, testutil.Do(t, h, http.MethodGet, "/api/document_templates?type=letter", tok, nil))
	if len(byType) != 1 || byType[0].ID != one.ID {
		t.Fatalf("type filter mismatch: %+v", byType)
	}
}

func TestDocumentTemplates_UpdateReturnsUpdatedRow(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplUpdate")
	tok := c.Token(t, signer)

	tpl := createTemplate(t, h, tok, map[string]any{"name": "Carta", "type": "letter", "content": "v1"})

	rec := testutil.Do(t, h, http.MethodPatch, "/api/document_templates/"+tpl.ID, tok,
		map[string]any{"name": "Carta 2", "type": "contract", "content": "v2 {{X}}"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[templateRow](t, rec)
	if len(rows) != 1 || rows[0].Name != "Carta 2" || rows[0].Type != "contract" || rows[0].Content != "v2 {{X}}" {
		t.Fatalf("expected the updated row in a single-element array, got %+v", rows)
	}

	bad := testutil.Do(t, h, http.MethodPatch, "/api/document_templates/"+tpl.ID, tok,
		map[string]any{"name": "", "type": "x", "content": "c"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on empty name, got %d", bad.Code)
	}

	withCo := testutil.Do(t, h, http.MethodPatch, "/api/document_templates/"+tpl.ID, tok,
		map[string]any{"name": "n", "type": "x", "content": "c", "company_id": c.CompanyID})
	if withCo.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on company_id in body, got %d", withCo.Code)
	}
}

// TestDocumentTemplates_UpdatedAtBumpedByTrigger pins trigger trg_tpl_upd
// (H-H): the UPDATE query does not set updated_at itself.
func TestDocumentTemplates_UpdatedAtBumpedByTrigger(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplTrigger")
	tok := c.Token(t, signer)

	tpl := createTemplate(t, h, tok, map[string]any{"name": "Carta", "content": "v1"})
	time.Sleep(20 * time.Millisecond)

	rec := testutil.Do(t, h, http.MethodPatch, "/api/document_templates/"+tpl.ID, tok,
		map[string]any{"name": "Carta", "type": "contract", "content": "v2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	after := testutil.DecodeRows[templateRow](t, rec)[0]
	if !after.UpdatedAt.After(tpl.UpdatedAt) {
		t.Fatalf("updated_at did not advance: before=%v after=%v", tpl.UpdatedAt, after.UpdatedAt)
	}
}

func TestDocumentTemplates_DeleteBlockedByGeneratedDocument(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TplDelete")
	tok := c.Token(t, signer)

	used := createTemplate(t, h, tok, map[string]any{"name": "Usada", "content": "c"})
	seedGeneratedDocument(t, pool, c.CompanyID, used.ID)

	rec := testutil.Do(t, h, http.MethodDelete, "/api/document_templates/"+used.ID, tok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if code := testutil.ErrCode(t, rec); code != "conflict" {
		t.Fatalf("expected conflict, got %q", code)
	}
	if !strings.Contains(rec.Body.String(), "La plantilla tiene documentos generados y no se puede eliminar") {
		t.Fatalf("expected the specific message, body=%s", rec.Body.String())
	}
	if g := testutil.Do(t, h, http.MethodGet, "/api/document_templates/"+used.ID, tok, nil); g.Code != http.StatusOK {
		t.Fatalf("template must survive the blocked delete, got %d", g.Code)
	}

	free := createTemplate(t, h, tok, map[string]any{"name": "Libre", "content": "c"})
	if rec := testutil.Do(t, h, http.MethodDelete, "/api/document_templates/"+free.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if g := testutil.Do(t, h, http.MethodGet, "/api/document_templates/"+free.ID, tok, nil); g.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", g.Code)
	}

	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/document_templates/"+id, tok, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("delete %s: expected 404, got %d", id, rec.Code)
		}
	}
}
