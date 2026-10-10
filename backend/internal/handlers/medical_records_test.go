package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type medRow struct {
	ID           string      `json:"id"`
	CompanyID    string      `json:"company_id"`
	EmployeeID   string      `json:"employee_id"`
	EmployeeName *string     `json:"employee_name"`
	Type         string      `json:"type"`
	Diagnosis    *string     `json:"diagnosis"`
	StartDate    string      `json:"start_date"`
	EndDate      string      `json:"end_date"`
	Days         int         `json:"days"`
	EmployerDays int         `json:"employer_days"`
	CSSDays      int         `json:"css_days"`
	CertNumber   *string     `json:"cert_number"`
	Notes        *string     `json:"notes"`
	Status       string      `json:"status"`
	SalaryBasis  json.Number `json:"salary_basis"`
	Cost         json.Number `json:"cost"`
}

// medBody is a valid create body: enfermedad, 2026-03-10..2026-03-14 = 5 days.
func medBody(employeeID string) map[string]any {
	return map[string]any{
		"employee_id": employeeID,
		"type":        "enfermedad",
		"diagnosis":   "Gripe",
		"start_date":  "2026-03-10",
		"end_date":    "2026-03-14",
		"cert_number": "CSS-1",
		"notes":       "reposo",
	}
}

func createMed(t *testing.T, h http.Handler, token string, body map[string]any) medRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed medical record: expected 201, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[medRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func patchMed(t *testing.T, h http.Handler, token, id string, body map[string]any) medRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+id, token, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch medical record: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[medRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("expected single-element array, got %d", len(rows))
	}
	return rows[0]
}

func listMed(t *testing.T, h http.Handler, token, query string) []medRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodGet, "/api/medical_records"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list medical_records%s: expected 200, got %d (body=%s)", query, rec.Code, rec.Body.String())
	}
	return testutil.DecodeRows[medRow](t, rec)
}

func countMed(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM medical_records`).Scan(&n); err != nil {
		t.Fatalf("count medical_records: %v", err)
	}
	return n
}

func assertValidationField(t *testing.T, rec *bytes.Buffer, code int, wantStatus int, field string) {
	t.Helper()
	if code != wantStatus {
		t.Fatalf("expected %d, got %d", wantStatus, code)
	}
	var body struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "validation_failed" {
		t.Fatalf("expected validation_failed, got %q (%s)", body.Error.Code, rec.Bytes())
	}
	if _, ok := body.Error.Fields[field]; !ok {
		t.Fatalf("expected field %q in %v", field, body.Error.Fields)
	}
}

func isZeroNumber(n json.Number) bool { return n.String() == "0" || n.String() == "0.00" }

func TestMedicalRecords_CrossTenantLeak(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "MedLeakA")
	b := testutil.Company(t, pool, "MedLeakB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	empA := testutil.Employee(t, pool, a, testutil.EmployeeSalary(1000))
	empB := testutil.Employee(t, pool, b, testutil.EmployeeSalary(1000))

	mA := createMed(t, h, ta, medBody(empA.ID))
	mB := createMed(t, h, tb, medBody(empB.ID))

	t.Run("get other tenant row is 404", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+mB.ID, ta, nil)
		if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
			t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("patch other tenant row is 404 and row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+mB.ID, ta, map[string]any{"status": "cerrada"})
		if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
			t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
		}
		got := testutil.DecodeRow[medRow](t, testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+mB.ID, tb, nil))
		if got.Status != "activa" {
			t.Fatalf("B's row was modified: status=%q", got.Status)
		}
	})

	t.Run("delete other tenant row is 404 and row survives", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodDelete, "/api/medical_records/"+mB.ID, ta, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if got := testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+mB.ID, tb, nil); got.Code != http.StatusOK {
			t.Fatalf("B's row should survive, got %d", got.Code)
		}
	})

	t.Run("list returns own row and never other tenant rows", func(t *testing.T) {
		rows := listMed(t, h, ta, "")
		var found bool
		for _, r := range rows {
			if r.CompanyID != a.CompanyID || r.ID == mB.ID {
				t.Fatalf("A's list leaked a foreign row: %+v", r)
			}
			if r.ID == mA.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("A's own row missing from list: %+v", rows)
		}
	})

	t.Run("company_id in body is 400", func(t *testing.T) {
		body := medBody(empA.ID)
		body["company_id"] = b.CompanyID
		rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", ta, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestMedicalRecords_CrossTenantEmployeeIDRejected(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "MedEmpA")
	b := testutil.Company(t, pool, "MedEmpB")
	ta := a.Token(t, signer)
	empB := testutil.Employee(t, pool, b, testutil.EmployeeSalary(1000))

	before := countMed(t, pool)
	rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", ta, medBody(empB.ID))
	if rec.Code != http.StatusNotFound || testutil.ErrCode(t, rec) != "not_found" {
		t.Fatalf("expected 404 not_found, got %d %s", rec.Code, rec.Body.String())
	}
	if after := countMed(t, pool); after != before {
		t.Fatalf("row created despite foreign employee_id (%d -> %d)", before, after)
	}
}

func TestMedicalRecords_ServerComputesDaysAndCost(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedCompute")
	tok := c.Token(t, signer)

	cases := []struct {
		name                      string
		salary                    float64
		typ, start, end           string
		days, employer, css       int
		wantCost, wantSalaryBasis string
	}{
		// 3*(1000/30) + 2*(1000/30)*0.70 = 100 + 46.666.. = 146.67
		{"enfermedad 5d salary 1000", 1000, "enfermedad", "2026-03-10", "2026-03-14", 5, 3, 2, "146.67", "1000.00"},
		// paternidad charges 3 employer days: 3*(900/30) = 90.00
		{"paternidad 1d salary 900", 900, "paternidad", "2026-03-10", "2026-03-10", 1, 3, 0, "90.00", "900.00"},
		// 3*30 + 7*30*0.70 = 90 + 147 = 237.00
		{"enfermedad 10d salary 900", 900, "enfermedad", "2026-03-01", "2026-03-10", 10, 3, 7, "237.00", "900.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(tc.salary))
			body := medBody(emp.ID)
			body["type"], body["start_date"], body["end_date"] = tc.typ, tc.start, tc.end
			body["days"], body["employer_days"], body["css_days"] = 99, 99, 99
			got := createMed(t, h, tok, body)
			if got.Days != tc.days || got.EmployerDays != tc.employer || got.CSSDays != tc.css {
				t.Fatalf("days split = %d/%d/%d, want %d/%d/%d", got.Days, got.EmployerDays, got.CSSDays, tc.days, tc.employer, tc.css)
			}
			if got.Cost.String() != tc.wantCost || got.SalaryBasis.String() != tc.wantSalaryBasis {
				t.Fatalf("cost/salary_basis = %s/%s, want %s/%s", got.Cost, got.SalaryBasis, tc.wantCost, tc.wantSalaryBasis)
			}
		})
	}

	t.Run("NULL employee salary costs 0.00", func(t *testing.T) {
		emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
		if _, err := pool.Exec(context.Background(), `UPDATE employees SET salary = NULL WHERE id = $1`, emp.ID); err != nil {
			t.Fatal(err)
		}
		got := createMed(t, h, tok, medBody(emp.ID))
		// pgtype.Numeric marshals a zero as a bare 0, whatever its scale.
		if !isZeroNumber(got.Cost) || !isZeroNumber(got.SalaryBasis) || got.Days != 5 {
			t.Fatalf("expected cost 0.00 / basis 0.00 / 5 days, got %s / %s / %d", got.Cost, got.SalaryBasis, got.Days)
		}
	})

	t.Run("persisted values survive a list and get", func(t *testing.T) {
		emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
		created := createMed(t, h, tok, medBody(emp.ID))
		rows := listMed(t, h, tok, "?id="+created.ID)
		if len(rows) != 1 || rows[0].Cost.String() != "146.67" {
			t.Fatalf("list row mismatch: %+v", rows)
		}
	})
}

func TestMedicalRecords_CostNotAcceptedFromClient(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedCostReject")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	existing := createMed(t, h, tok, medBody(emp.ID))

	for _, key := range []string{"cost", "salary_basis"} {
		t.Run("create with "+key, func(t *testing.T) {
			before := countMed(t, pool)
			body := medBody(emp.ID)
			body[key] = 1
			rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
			}
			if after := countMed(t, pool); after != before {
				t.Fatal("row created")
			}
		})
		t.Run("patch with "+key, func(t *testing.T) {
			rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+existing.ID, tok, map[string]any{key: 1})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMedicalRecords_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedCreateVal")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Luis", "Mora"), testutil.EmployeeSalary(1000))

	t.Run("unknown type rejected", func(t *testing.T) {
		for _, typ := range []string{"Paternidad", "paternida", "otro", ""} {
			body := medBody(emp.ID)
			body["type"] = typ
			rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, body)
			assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "type")
		}
	})

	t.Run("end before start rejected on end_date", func(t *testing.T) {
		before := countMed(t, pool)
		body := medBody(emp.ID)
		body["start_date"], body["end_date"] = "2026-03-10", "2026-03-09"
		rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, body)
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "end_date")
		if countMed(t, pool) != before {
			t.Fatal("row created")
		}
	})

	for _, key := range []string{"employee_id", "type", "start_date", "end_date"} {
		t.Run("required "+key, func(t *testing.T) {
			body := medBody(emp.ID)
			delete(body, key)
			rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, body)
			assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, key)
		})
	}

	t.Run("malformed employee_id", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, medBody("nope"))
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "employee_id")
	})

	t.Run("status defaults to activa, unknown rejected", func(t *testing.T) {
		if got := createMed(t, h, tok, medBody(emp.ID)); got.Status != "activa" {
			t.Fatalf("status = %q", got.Status)
		}
		body := medBody(emp.ID)
		body["status"] = "pendiente"
		rec := testutil.Do(t, h, http.MethodPost, "/api/medical_records", tok, body)
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "status")
		body["status"] = "cerrada"
		if got := createMed(t, h, tok, body); got.Status != "cerrada" {
			t.Fatalf("status = %q", got.Status)
		}
	})

	t.Run("employee_name derived, client value ignored", func(t *testing.T) {
		body := medBody(emp.ID)
		body["employee_name"] = "Spoof"
		got := createMed(t, h, tok, body)
		if got.EmployeeName == nil || *got.EmployeeName != "Luis Mora" {
			t.Fatalf("employee_name not derived: %v", got.EmployeeName)
		}
	})
}

func TestMedicalRecords_PatchStatusOnlyKeepsComputedFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedPatchStatus")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	created := createMed(t, h, tok, medBody(emp.ID))

	// A raise after creation must not reprice a status-only PATCH.
	if _, err := pool.Exec(context.Background(), `UPDATE employees SET salary = 5000 WHERE id = $1`, emp.ID); err != nil {
		t.Fatal(err)
	}

	got := patchMed(t, h, tok, created.ID, map[string]any{"status": "cerrada"})
	if got.Status != "cerrada" {
		t.Fatalf("status = %q", got.Status)
	}
	want := created
	want.Status = "cerrada"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status-only PATCH changed other columns:\n got=%+v\nwant=%+v", got, want)
	}
	if got.Cost.String() != "146.67" || got.SalaryBasis.String() != "1000.00" {
		t.Fatalf("cost/basis changed: %s/%s", got.Cost, got.SalaryBasis)
	}
}

func TestMedicalRecords_PatchDatesRecomputes(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedPatchDates")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(900))
	created := createMed(t, h, tok, medBody(emp.ID)) // 5 days

	// Extend to 2026-03-19 = 10 days at 900: 3*30 + 7*30*0.70 = 237.00.
	// A client-sent days is declared-and-ignored.
	got := patchMed(t, h, tok, created.ID, map[string]any{"end_date": "2026-03-19", "days": 1})
	if got.Days != 10 || got.EmployerDays != 3 || got.CSSDays != 7 {
		t.Fatalf("days split = %d/%d/%d, want 10/3/7", got.Days, got.EmployerDays, got.CSSDays)
	}
	if got.Cost.String() != "237.00" || got.EndDate != "2026-03-19" {
		t.Fatalf("cost/end_date = %s/%s", got.Cost, got.EndDate)
	}

	t.Run("type change recomputes", func(t *testing.T) {
		// paternidad over 10 days: still 3 employer days, 7 CSS days.
		got := patchMed(t, h, tok, created.ID, map[string]any{"type": "paternidad", "end_date": "2026-03-10"})
		// 1 day paternidad: 3 employer days, 0 CSS: 3*30 = 90.00
		if got.Days != 1 || got.EmployerDays != 3 || got.CSSDays != 0 || got.Cost.String() != "90.00" || got.Type != "paternidad" {
			t.Fatalf("unexpected recompute: %+v", got)
		}
	})
}

func TestMedicalRecords_PatchUsesSalarySnapshot(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedPatchSnap")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(900))
	created := createMed(t, h, tok, medBody(emp.ID))

	if _, err := pool.Exec(context.Background(), `UPDATE employees SET salary = 9000 WHERE id = $1`, emp.ID); err != nil {
		t.Fatal(err)
	}
	// 2026-03-10..2026-03-19 = 10 days at the SNAPSHOT 900: 90 + 147 = 237.00.
	got := patchMed(t, h, tok, created.ID, map[string]any{"end_date": "2026-03-19"})
	if got.Days != 10 || got.EmployerDays != 3 || got.CSSDays != 7 {
		t.Fatalf("days split = %d/%d/%d", got.Days, got.EmployerDays, got.CSSDays)
	}
	if got.Cost.String() != "237.00" || got.SalaryBasis.String() != "900.00" {
		t.Fatalf("cost/basis = %s/%s, want 237.00/900.00 (snapshot, not current salary)", got.Cost, got.SalaryBasis)
	}
}

func TestMedicalRecords_PatchLegacyRowSelfHeals(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedPatchLegacy")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1500))

	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO medical_records (company_id, employee_id, employee_name, type, start_date, end_date, days, employer_days, css_days, status)
		VALUES ($1, $2, 'Legacy', 'enfermedad', '2026-03-10', '2026-03-14', 5, 3, 2, 'activa')
		RETURNING id`, c.CompanyID, emp.ID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	// 3*50 + 2*50*0.70 = 150 + 70 = 220.00 at the CURRENT salary (no snapshot).
	got := patchMed(t, h, tok, id, map[string]any{"status": "cerrada"})
	if got.SalaryBasis.String() != "1500.00" || got.Cost.String() != "220.00" {
		t.Fatalf("legacy row did not self-heal: basis=%s cost=%s", got.SalaryBasis, got.Cost)
	}

	var basis string
	if err := pool.QueryRow(context.Background(), `SELECT salary_basis::text FROM medical_records WHERE id = $1`, id).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	if basis != "1500.00" {
		t.Fatalf("salary_basis not persisted: %q", basis)
	}
}

func TestMedicalRecords_PatchSemantics(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedPatchSem")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	other := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	created := createMed(t, h, tok, medBody(emp.ID))

	t.Run("update returns updated row", func(t *testing.T) {
		got := patchMed(t, h, tok, created.ID, map[string]any{"diagnosis": "Dengue", "notes": "x"})
		if got.ID != created.ID || got.Diagnosis == nil || *got.Diagnosis != "Dengue" || got.Notes == nil || *got.Notes != "x" {
			t.Fatalf("unexpected row: %+v", got)
		}
		if got.CertNumber == nil || *got.CertNumber != "CSS-1" {
			t.Fatalf("untouched cert_number changed: %v", got.CertNumber)
		}
		reread := testutil.DecodeRow[medRow](t, testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+created.ID, tok, nil))
		if !reflect.DeepEqual(reread, got) {
			t.Fatalf("persisted row differs from response:\n%+v\n%+v", reread, got)
		}
	})

	t.Run("null keeps stored value", func(t *testing.T) {
		got := patchMed(t, h, tok, created.ID, map[string]any{"diagnosis": nil, "type": nil, "end_date": nil})
		if got.Diagnosis == nil || *got.Diagnosis != "Dengue" || got.Type != "enfermedad" || got.EndDate != "2026-03-14" {
			t.Fatalf("null did not keep stored values: %+v", got)
		}
	})

	t.Run("employee_id is immutable (400)", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+created.ID, tok, map[string]any{"employee_id": other.ID})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("merged end before start rejected, row unchanged", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+created.ID, tok, map[string]any{"end_date": "2026-03-01"})
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "end_date")
		rec = testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+created.ID, tok, map[string]any{"start_date": "2026-04-01"})
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "end_date")
		got := testutil.DecodeRow[medRow](t, testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+created.ID, tok, nil))
		if got.StartDate != "2026-03-10" || got.EndDate != "2026-03-14" {
			t.Fatalf("row changed: %+v", got)
		}
	})

	t.Run("unknown type and status rejected", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+created.ID, tok, map[string]any{"type": "otro"})
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "type")
		rec = testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+created.ID, tok, map[string]any{"status": "borrada"})
		assertValidationField(t, rec.Body, rec.Code, http.StatusBadRequest, "status")
	})

	t.Run("patch unknown or missing id is 404", func(t *testing.T) {
		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
			rec := testutil.Do(t, h, http.MethodPatch, "/api/medical_records/"+id, tok, map[string]any{"status": "cerrada"})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("patch %s: expected 404, got %d", id, rec.Code)
			}
		}
	})
}

func TestMedicalRecords_FilterByIDReturnsSingleElementArray(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedFilterID")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))

	first := createMed(t, h, tok, medBody(emp.ID))
	createMed(t, h, tok, medBody(emp.ID))

	rows := listMed(t, h, tok, "?id="+first.ID)
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("expected exactly the filtered row, got %+v", rows)
	}
}

func TestMedicalRecords_ListFiltersAndParams(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedFilters")
	tok := c.Token(t, signer)
	emp1 := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	emp2 := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))

	m1 := createMed(t, h, tok, medBody(emp1.ID))
	b2 := medBody(emp2.ID)
	b2["status"] = "cerrada"
	m2 := createMed(t, h, tok, b2)

	rows := listMed(t, h, tok, "?employee_id="+emp1.ID)
	if len(rows) != 1 || rows[0].ID != m1.ID {
		t.Fatalf("employee_id filter wrong: %+v", rows)
	}
	rows = listMed(t, h, tok, "?status=cerrada")
	if len(rows) != 1 || rows[0].ID != m2.ID {
		t.Fatalf("status filter wrong: %+v", rows)
	}

	for _, q := range []string{"?bogus=1", "?_order=start_date", "?employee_id=not-a-uuid", "?id=zzz", "?type=enfermedad"} {
		rec := testutil.Do(t, h, http.MethodGet, "/api/medical_records"+q, tok, nil)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: expected 400 validation_failed, got %d %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestMedicalRecords_Delete(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "MedDelete")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1000))
	m := createMed(t, h, tok, medBody(emp.ID))

	if rec := testutil.Do(t, h, http.MethodDelete, "/api/medical_records/"+m.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodGet, "/api/medical_records/"+m.ID, tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec.Code)
	}
	for _, id := range []string{m.ID, "00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if rec := testutil.Do(t, h, http.MethodDelete, "/api/medical_records/"+id, tok, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("delete %s: expected 404, got %d", id, rec.Code)
		}
	}
}
