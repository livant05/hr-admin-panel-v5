package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type uniRow struct {
	ID           string      `json:"id"`
	CompanyID    string      `json:"company_id"`
	EmployeeID   string      `json:"employee_id"`
	EmployeeName *string     `json:"employee_name"`
	Item         string      `json:"item"`
	Category     *string     `json:"category"`
	Size         *string     `json:"size"`
	Quantity     int         `json:"quantity"`
	Date         *string     `json:"date"`
	Value        json.Number `json:"value"`
	Notes        *string     `json:"notes"`
	Status       string      `json:"status"`
}

func uniBody(employeeID string) map[string]any {
	return map[string]any{
		"employee_id": employeeID,
		"item":        "Camisa polo",
		"category":    "camisa",
		"size":        "M",
		"quantity":    2,
		"date":        "2026-03-10",
		"value":       25.5,
		"notes":       "entrega inicial",
	}
}

func createUni(t *testing.T, h http.Handler, token string, body map[string]any) uniRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/uniforms", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed uniform: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[uniRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func patchUni(t *testing.T, h http.Handler, token, id string, body map[string]any) uniRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+id, token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch uniform: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[uniRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func listUni(t *testing.T, h http.Handler, token, query string) []uniRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, "/api/uniforms"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list uniforms%s: expected 200, got %d (body=%s)", query, rec.Code, rec.Body.String())
	}
	return testutil.DecodeRows[uniRow](t, rec)
}

func countUni(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM uniforms`).Scan(&n); err != nil {
		t.Fatalf("count uniforms: %v", err)
	}
	return n
}

func TestUniforms_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "UniLeakA")
	b := testutil.Company(t, pool, "UniLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a)
	empB := testutil.Employee(t, pool, b)

	uA := createUni(t, h, ta, uniBody(empA.ID))
	uB := createUni(t, h, tb, uniBody(empB.ID))

	t.Run("get other tenant row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+uB.ID, ta, nil)
		if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
			t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("patch other tenant row is 404 and row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+uB.ID, ta, map[string]any{"status": "devuelto"})
		if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
			t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRow[uniRow](t, testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+uB.ID, tb, nil))
		if got.Status != "activo" {
			t.Fatalf("B's row was modified: status=%q", got.Status)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/uniforms/"+uB.ID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+uB.ID, tb, nil); got.Code != http.StatusOK {
			t.Fatalf("B's row should survive, got %d", got.Code)
		}
	})

	t.Run("list returns own row and never other tenant rows", func(t *testing.T) {
		rows := listUni(t, h, ta, "")
		var found bool
		for _, r := range rows {
			if r.CompanyID != a.CompanyID || r.ID == uB.ID {
				t.Fatalf("A's list leaked a foreign row: %+v", r)
			}
			if r.ID == uA.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("A's own row missing from list: %+v", rows)
		}
	})

	t.Run("company_id in body is 400", func(t *testing.T) {
		body := uniBody(empA.ID)
		body["company_id"] = b.CompanyID
		rec := testutil.Do(t, h, http.MethodPost, "/api/uniforms", ta, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestUniforms_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "UniEmpA")
	b := testutil.Company(t, pool, "UniEmpB")
	ta := a.Token(t, signer)
	empB := testutil.Employee(t, pool, b)

	before := countUni(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/uniforms", ta, uniBody(empB.ID))
	if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
		t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
	}
	if after := countUni(t, pool); after != before {
		t.Fatalf("row created despite foreign employee_id (%d -> %d)", before, after)
	}
}

func TestUniforms_ValidationRejects(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UniVal")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		name  string
		patch func(m map[string]any)
		field string
	}{
		{"quantity zero", func(m map[string]any) { m["quantity"] = 0 }, "quantity"},
		{"quantity negative", func(m map[string]any) { m["quantity"] = -3 }, "quantity"},
		{"quantity fractional", func(m map[string]any) { m["quantity"] = raw("1.5") }, "quantity"},
		{"quantity string", func(m map[string]any) { m["quantity"] = "2" }, "quantity"},
		{"quantity above cap", func(m map[string]any) { m["quantity"] = 100001 }, "quantity"},
		{"quantity beyond int32", func(m map[string]any) { m["quantity"] = raw("99999999999999999999") }, "quantity"},
		{"quantity huge exponent", func(m map[string]any) { m["quantity"] = raw("1e1000000") }, "quantity"},
		{"value negative", func(m map[string]any) { m["value"] = -1 }, "value"},
		{"value string", func(m map[string]any) { m["value"] = "5" }, "value"},
		{"value over numeric(10,2)", func(m map[string]any) { m["value"] = raw("100000000") }, "value"},
		{"value rounds over numeric(10,2)", func(m map[string]any) { m["value"] = raw("99999999.996") }, "value"},
		{"value huge exponent", func(m map[string]any) { m["value"] = raw("1e1000000") }, "value"},
		{"value overlong literal", func(m map[string]any) { m["value"] = raw("1." + strings.Repeat("0", 400)) }, "value"},
		{"empty item", func(m map[string]any) { m["item"] = "" }, "item"},
		{"blank item", func(m map[string]any) { m["item"] = "   " }, "item"},
		{"missing item", func(m map[string]any) { delete(m, "item") }, "item"},
		{"missing employee_id", func(m map[string]any) { delete(m, "employee_id") }, "employee_id"},
		{"malformed employee_id", func(m map[string]any) { m["employee_id"] = "nope" }, "employee_id"},
		{"unknown status", func(m map[string]any) { m["status"] = "perdido" }, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := countUni(t, pool)
			body := uniBody(emp.ID)
			tc.patch(body)
			rec := testutil.Do(t, h, http.MethodPost, "/api/uniforms", tok, body)
			assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, tc.field)
			if countUni(t, pool) != before {
				t.Fatal("row created despite invalid body")
			}
		})
	}

	t.Run("boundaries accepted", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["quantity"], body["value"] = 100000, json.RawMessage("99999999.99")
		got := createUni(t, h, tok, body)
		if got.Quantity != 100000 || got.Value.String() != "99999999.99" {
			t.Fatalf("got quantity=%d value=%s", got.Quantity, got.Value)
		}
		body["quantity"], body["value"] = 1, 0
		got = createUni(t, h, tok, body)
		if got.Quantity != 1 || !isZeroNumber(got.Value) {
			t.Fatalf("got quantity=%d value=%s", got.Quantity, got.Value)
		}
	})

	// A vanishing exponent saturates to 0 in ParseFloat: bounded, harmless,
	// stored as zero rather than driving any big-number allocation.
	t.Run("tiny exponent value stores zero", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["value"] = json.RawMessage("1e-1000000")
		if got := createUni(t, h, tok, body); !isZeroNumber(got.Value) {
			t.Fatalf("expected zero value, got %s", got.Value)
		}
	})

	t.Run("value rounded to two decimals", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["value"] = json.RawMessage("12.345")
		got := createUni(t, h, tok, body)
		if v := got.Value.String(); v != "12.35" && v != "12.34" {
			t.Fatalf("expected 2-decimal value, got %s", v)
		}
	})
}

func TestUniforms_CreateDefaultsAndDerivedFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UniDefaults")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Rosa", "Vega"))

	t.Run("quantity value status defaults", func(t *testing.T) {
		got := createUni(t, h, tok, map[string]any{"employee_id": emp.ID, "item": "Gorra"})
		if got.Quantity != 1 || !isZeroNumber(got.Value) || got.Status != "activo" {
			t.Fatalf("defaults wrong: quantity=%d value=%s status=%q", got.Quantity, got.Value, got.Status)
		}
	})

	t.Run("free text fields are not enum validated", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["category"], body["size"] = "cualquiera", "XXL-especial"
		got := createUni(t, h, tok, body)
		if got.Category == nil || *got.Category != "cualquiera" || got.Size == nil || *got.Size != "XXL-especial" {
			t.Fatalf("free-text fields not stored: %+v", got)
		}
	})

	t.Run("employee_name derived and client value ignored", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["employee_name"] = "Falso Nombre"
		got := createUni(t, h, tok, body)
		if got.EmployeeName == nil || *got.EmployeeName != "Rosa Vega" {
			t.Fatalf("employee_name = %v, want Rosa Vega", got.EmployeeName)
		}
	})

	t.Run("date defaults to today when absent", func(t *testing.T) {
		body := uniBody(emp.ID)
		delete(body, "date")
		got := createUni(t, h, tok, body)
		var dbToday string
		if err := pool.QueryRow(context.Background(), `SELECT CURRENT_DATE::text`).Scan(&dbToday); err != nil {
			t.Fatal(err)
		}
		if got.Date == nil || *got.Date != dbToday {
			t.Fatalf("date = %v, want %s (now %s)", got.Date, dbToday, time.Now().Format("2006-01-02"))
		}
	})

	t.Run("explicit date honored", func(t *testing.T) {
		got := createUni(t, h, tok, uniBody(emp.ID))
		if got.Date == nil || *got.Date != "2026-03-10" {
			t.Fatalf("date = %v, want 2026-03-10", got.Date)
		}
	})

	t.Run("explicit status devuelto accepted", func(t *testing.T) {
		body := uniBody(emp.ID)
		body["status"] = "devuelto"
		if got := createUni(t, h, tok, body); got.Status != "devuelto" {
			t.Fatalf("status = %q", got.Status)
		}
	})
}

func TestUniforms_PatchStatusOnlyKeepsOtherFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UniPatchStatus")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	created := createUni(t, h, tok, uniBody(emp.ID))
	got := patchUni(t, h, tok, created.ID, map[string]any{"status": "devuelto"})

	if got.Status != "devuelto" {
		t.Fatalf("status = %q, want devuelto", got.Status)
	}
	want := created
	want.Status = "devuelto"
	if got.ID != want.ID || got.Item != want.Item || got.Quantity != want.Quantity ||
		got.Value.String() != want.Value.String() ||
		*got.Date != *want.Date || *got.Category != *want.Category ||
		*got.Size != *want.Size || *got.Notes != *want.Notes ||
		got.EmployeeID != want.EmployeeID || *got.EmployeeName != *want.EmployeeName {
		t.Fatalf("PATCH changed more than status:\n got %+v\nwant %+v", got, want)
	}
}

func TestUniforms_PatchRules(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "UniPatchRules")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)
	created := createUni(t, h, tok, uniBody(emp.ID))

	t.Run("update returns updated row", func(t *testing.T) {
		got := patchUni(t, h, tok, created.ID, map[string]any{"item": "Camisa azul", "quantity": 4, "value": 30, "size": "L"})
		if got.Item != "Camisa azul" || got.Quantity != 4 || got.Value.String() != "30" && got.Value.String() != "30.00" || *got.Size != "L" {
			t.Fatalf("row not updated: %+v", got)
		}
		again := testutil.DecodeRow[uniRow](t, testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+created.ID, tok, nil))
		if again.Item != "Camisa azul" || again.Quantity != 4 {
			t.Fatalf("update not persisted: %+v", again)
		}
	})

	t.Run("filter by id returns single-element array", func(t *testing.T) {
		rows := listUni(t, h, tok, "?id="+created.ID)
		if len(rows) != 1 || rows[0].ID != created.ID {
			t.Fatalf("expected exactly the filtered row, got %+v", rows)
		}
	})

	t.Run("present empty item is 400", func(t *testing.T) {
		for _, v := range []string{"", "  "} {
			rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+created.ID, tok, map[string]any{"item": v})
			assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "item")
		}
	})

	t.Run("validators apply to present fields", func(t *testing.T) {
		for field, v := range map[string]any{
			"quantity": 0,
			"value":    -1,
			"status":   "perdido",
		} {
			rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+created.ID, tok, map[string]any{field: v})
			assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, field)
		}
		rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+created.ID, tok, map[string]any{"value": json.RawMessage("1e1000000")})
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "value")
	})

	t.Run("employee_id in body is 400", func(t *testing.T) {
		other := testutil.Employee(t, pool, c)
		rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/"+created.ID, tok, map[string]any{"employee_id": other.ID})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("null keeps stored value", func(t *testing.T) {
		before := testutil.DecodeRow[uniRow](t, testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+created.ID, tok, nil))
		got := patchUni(t, h, tok, created.ID, map[string]any{"item": nil, "quantity": nil, "value": nil, "notes": nil, "status": nil, "date": nil})
		if got.Item != before.Item || got.Quantity != before.Quantity || got.Value.String() != before.Value.String() ||
			*got.Notes != *before.Notes || got.Status != before.Status || *got.Date != *before.Date {
			t.Fatalf("null changed the row:\n got %+v\nwant %+v", got, before)
		}
	})

	t.Run("patch unknown id is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/uniforms/00000000-0000-0000-0000-000000000000", tok, map[string]any{"status": "devuelto"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})
}

func TestUniforms_ListFiltersAndDelete(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "UniFilters")
	b := testutil.Company(t, pool, "UniFiltersOther")
	tok, tokB := a.Token(t, signer), b.Token(t, signer)
	emp1 := testutil.Employee(t, pool, a)
	emp2 := testutil.Employee(t, pool, a)
	empB := testutil.Employee(t, pool, b)

	u1 := createUni(t, h, tok, uniBody(emp1.ID))
	u2 := createUni(t, h, tok, uniBody(emp2.ID))
	patchUni(t, h, tok, u2.ID, map[string]any{"status": "devuelto"})

	t.Run("filter by employee_id", func(t *testing.T) {
		rows := listUni(t, h, tok, "?employee_id="+emp1.ID)
		if len(rows) != 1 || rows[0].ID != u1.ID {
			t.Fatalf("expected only u1, got %+v", rows)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		rows := listUni(t, h, tok, "?status=devuelto")
		if len(rows) != 1 || rows[0].ID != u2.ID {
			t.Fatalf("expected only u2, got %+v", rows)
		}
	})

	t.Run("unknown param and _order rejected", func(t *testing.T) {
		for _, q := range []string{"?category=camisa", "?_order=item"} {
			rec := testutil.Do(t, h, http.MethodGet, "/api/uniforms"+q, tok, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", q, rec.Code)
			}
		}
	})

	t.Run("delete is hard and 204", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/uniforms/"+u1.ID, tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, "/api/uniforms/"+u1.ID, tok, nil); got.Code != http.StatusNotFound {
			t.Fatalf("expected 404 after delete, got %d", got.Code)
		}
	})

	t.Run("delete foreign or nonexistent is 404", func(t *testing.T) {
		foreign := createUni(t, h, tokB, uniBody(empB.ID))
		for _, id := range []string{foreign.ID, "00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodDelete, "/api/uniforms/"+id, tok, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("delete %s: expected 404, got %d", id, rec.Code)
			}
		}
	})
}
