package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/evaluation"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// pinRawMessage only accepts exactly json.RawMessage. A plain
// `var _ json.RawMessage = x` is NOT enough: []byte is assignable to
// json.RawMessage, so it would also compile against the base64-marshalling
// []byte the nullable-JSONB trap generates.
func pinRawMessage[T json.RawMessage](T) {}

// H4: nullable JSONB columns must generate json.RawMessage. Compile-time pin
// of the sqlc nullable override.
func init() {
	pinRawMessage(db.Evaluation{}.Scores)
	pinRawMessage(db.Survey{}.Questions)
	pinRawMessage(db.SurveyResponse{}.Answers)
}

// ---- integration tests ----

type evalRow struct {
	ID           string          `json:"id"`
	CompanyID    string          `json:"company_id"`
	EmployeeID   string          `json:"employee_id"`
	EmployeeName *string         `json:"employee_name"`
	Period       *string         `json:"period"`
	Evaluator    *string         `json:"evaluator"`
	Scores       json.RawMessage `json:"scores"`
	Avg          json.Number     `json:"avg"`
	Category     string          `json:"category"`
	Comments     *string         `json:"comments"`
	Status       string          `json:"status"`
}

func evalScores(v int) map[string]any {
	m := map[string]any{}
	for _, k := range evaluation.Criteria {
		m[k] = v
	}
	return m
}

func evalBody(employeeID string, scores any) map[string]any {
	return map[string]any{"employee_id": employeeID, "period": "2026-Q1", "evaluator": "Jefe", "scores": scores}
}

func createEval(t *testing.T, h http.Handler, token string, body map[string]any) evalRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed evaluation: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[evalRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func countEvals(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM evaluations`).Scan(&n); err != nil {
		t.Fatalf("count evaluations: %v", err)
	}
	return n
}

func listEvals(t *testing.T, h http.Handler, token, query string) []evalRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, "/api/evaluations"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list evaluations%s: expected 200, got %d (body=%s)", query, rec.Code, rec.Body.String())
	}
	return testutil.DecodeRows[evalRow](t, rec)
}

func TestEvaluations_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EvalLeakA")
	b := testutil.Company(t, pool, "EvalLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a)
	empB := testutil.Employee(t, pool, b)

	evA := createEval(t, h, ta, evalBody(empA.ID, evalScores(5)))
	evB := createEval(t, h, tb, evalBody(empB.ID, evalScores(5)))

	t.Run("get other tenant row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/evaluations/"+evB.ID, ta, nil)
		if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
			t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/evaluations/"+evB.ID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, "/api/evaluations/"+evB.ID, tb, nil); got.Code != http.StatusOK {
			t.Fatalf("B's row should survive, got %d", got.Code)
		}
	})

	t.Run("list returns own row and never other tenant rows", func(t *testing.T) {
		rows := listEvals(t, h, ta, "")
		var found bool
		for _, r := range rows {
			if r.CompanyID != a.CompanyID || r.ID == evB.ID {
				t.Fatalf("A's list leaked a foreign row: %+v", r)
			}
			if r.ID == evA.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("A's own row missing from list: %+v", rows)
		}
	})

	t.Run("patch route does not exist (405)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/evaluations/"+evA.ID, ta, map[string]any{"comments": "x"})
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})

	t.Run("company_id in body is 400", func(t *testing.T) {
		body := evalBody(empA.ID, evalScores(5))
		body["company_id"] = b.CompanyID
		rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", ta, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestEvaluations_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "EvalEmpA")
	b := testutil.Company(t, pool, "EvalEmpB")
	ta := a.Token(t, signer)
	empB := testutil.Employee(t, pool, b)

	before := countEvals(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", ta, evalBody(empB.ID, evalScores(5)))
	if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
		t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
	}
	if after := countEvals(t, pool); after != before {
		t.Fatalf("row created despite foreign employee_id (%d -> %d)", before, after)
	}
}

func TestEvaluations_ServerRecomputesAvgAndCategory(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalRecompute")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	body := evalBody(emp.ID, evalScores(5))
	body["avg"] = 10
	body["category"] = "Sobresaliente"
	got := createEval(t, h, tok, body)
	if got.Avg.String() != "5.00" || got.Category != "Regular" {
		t.Fatalf("expected stored 5.00/Regular, got %s/%s", got.Avg, got.Category)
	}
	if got.Status != "completada" {
		t.Fatalf("status should default to completada, got %q", got.Status)
	}

	// 61 -> 7.625 half-up -> 7.63, Excelente (decided on the integer sum).
	tier := evalScores(8)
	tier["actitud"] = 5 // 7*8 + 5 = 61
	got = createEval(t, h, tok, evalBody(emp.ID, tier))
	if got.Avg.String() != "7.63" || got.Category != "Excelente" {
		t.Fatalf("expected 7.63/Excelente, got %s/%s", got.Avg, got.Category)
	}
}

func TestEvaluations_ScoresRoundTripAsObject(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalRoundTrip")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	// 7.0 is accepted and stored as the integer 7.
	scores := evalScores(7)
	scores["calidad"] = json.Number("9.0")
	created := createEval(t, h, tok, evalBody(emp.ID, scores))

	assertObject := func(label string, raw json.RawMessage) {
		var got map[string]int
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: scores is not a JSON object of ints: %s (%v)", label, raw, err)
		}
		if len(got) != 8 || got["calidad"] != 9 || got["actitud"] != 7 {
			t.Fatalf("%s: unexpected scores %v", label, got)
		}
	}
	assertObject("create", created.Scores)

	rows := listEvals(t, h, tok, "")
	if len(rows) == 0 {
		t.Fatal("list empty")
	}
	assertObject("list", rows[0].Scores)

	got := testutil.DecodeRow[evalRow](t, testutil.Do(t, h, http.MethodGet, "/api/evaluations/"+created.ID, tok, nil))
	assertObject("get", got.Scores)
}

func TestEvaluations_InvalidScoresRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalInvalid")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	without := func(k string) map[string]any { m := evalScores(5); delete(m, k); return m }
	with := func(k string, v any) map[string]any { m := evalScores(5); m[k] = v; return m }
	double, _ := json.Marshal(evalScores(5))

	cases := []struct {
		name   string
		scores any
		field  string
	}{
		{"missing actitud", without("actitud"), "scores.actitud"},
		{"calidad 11", with("calidad", 11), "scores.calidad"},
		{"iniciativa 0", with("iniciativa", 0), "scores.iniciativa"},
		{"fractional 7.5", with("liderazgo", json.Number("7.5")), "scores.liderazgo"},
		{"string value", with("objetivos", "7"), "scores.objetivos"},
		{"extra key", with("bonus", 5), "scores.bonus"},
		{"double-encoded JSON string", string(double), "scores"},
		{"array", []int{5, 5, 5, 5, 5, 5, 5, 5}, "scores"},
		{"null", nil, "scores"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := countEvals(t, pool)
			rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", tok, evalBody(emp.ID, tc.scores))
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
				t.Fatalf("expected 400 validation_failed, got %d %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body.Error.Fields[tc.field]; !ok {
				t.Fatalf("expected field %q in %v", tc.field, body.Error.Fields)
			}
			if after := countEvals(t, pool); after != before {
				t.Fatalf("row created for invalid scores")
			}
		})
	}

	t.Run("scores absent", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", tok, map[string]any{"employee_id": emp.ID})
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("expected 400 validation_failed, got %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestEvaluations_CreateValidationAndDerivedName(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalCreate")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Pérez"))

	t.Run("employee_name derived, client value ignored", func(t *testing.T) {
		body := evalBody(emp.ID, evalScores(6))
		body["employee_name"] = "Spoof"
		got := createEval(t, h, tok, body)
		if got.EmployeeName == nil || *got.EmployeeName != "Ana Pérez" {
			t.Fatalf("employee_name not derived: %v", got.EmployeeName)
		}
		if got.EmployeeID != emp.ID {
			t.Fatalf("employee_id wrong: %s", got.EmployeeID)
		}
	})

	t.Run("employee_id required", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", tok, map[string]any{"scores": evalScores(5)})
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("expected 400 validation_failed, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed employee_id", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", tok, evalBody("nope", evalScores(5)))
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("expected 400 validation_failed, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown status rejected, completada accepted", func(t *testing.T) {
		body := evalBody(emp.ID, evalScores(5))
		body["status"] = "borrador"
		rec := testutil.Do(t, h, http.MethodPost, "/api/evaluations", tok, body)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("expected 400 validation_failed, got %d %s", rec.Code, rec.Body.String())
		}
		body["status"] = "completada"
		if got := createEval(t, h, tok, body); got.Status != "completada" {
			t.Fatalf("status = %q", got.Status)
		}
	})
}

func TestEvaluations_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalFilterID")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)

	first := createEval(t, h, tok, evalBody(emp.ID, evalScores(5)))
	createEval(t, h, tok, evalBody(emp.ID, evalScores(6)))

	rows := listEvals(t, h, tok, "?id="+first.ID)
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("expected exactly the filtered row, got %+v", rows)
	}
}

func TestEvaluations_ListFiltersAndParams(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalFilters")
	tok := c.Token(t, signer)
	emp1 := testutil.Employee(t, pool, c)
	emp2 := testutil.Employee(t, pool, c)

	e1 := createEval(t, h, tok, evalBody(emp1.ID, evalScores(5)))
	createEval(t, h, tok, evalBody(emp2.ID, evalScores(6)))

	rows := listEvals(t, h, tok, "?employee_id="+emp1.ID)
	if len(rows) != 1 || rows[0].ID != e1.ID {
		t.Fatalf("employee_id filter wrong: %+v", rows)
	}

	for _, q := range []string{"?bogus=1", "?_order=created_at", "?employee_id=not-a-uuid", "?id=zzz"} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/evaluations"+q, tok, nil)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: expected 400 validation_failed, got %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestEvaluations_Delete(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EvalDelete")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c)
	ev := createEval(t, h, tok, evalBody(emp.ID, evalScores(5)))

	if rec := testutil.Do(t, h, http.MethodDelete, "/api/evaluations/"+ev.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/evaluations/"+ev.ID, tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec.Code)
	}
	for _, id := range []string{ev.ID, "00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/evaluations/"+id, tok, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("delete %s: expected 404, got %d", id, rec.Code)
		}
	}
}
