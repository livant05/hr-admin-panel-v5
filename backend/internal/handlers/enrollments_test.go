package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type enrollmentRow struct {
	ID           string   `json:"id"`
	CompanyID    string   `json:"company_id"`
	TrainingID   *string  `json:"training_id"`
	TrainingName *string  `json:"training_name"`
	EmployeeID   string   `json:"employee_id"`
	EmployeeName *string  `json:"employee_name"`
	Status       *string  `json:"status"`
	Score        *float64 `json:"score"`
	Notes        *string  `json:"notes"`
	CreatedAt    string   `json:"created_at"`
}

const enrollmentsPath = "/api/enrollments"

func createEnrollment(t *testing.T, h http.Handler, token string, body map[string]any) enrollmentRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, enrollmentsPath, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create enrollment %v: got %d (%s)", body, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[enrollmentRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create returned %d rows, want a one-element array", len(rows))
	}
	return rows[0]
}

func TestEnrollments_CreateDerivesNamesAndChecksTheTenant(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EnrollCreateCo")
	other := testutil.Company(t, pool, "EnrollCreateOtherCo")
	tok := c.Token(t, signer)
	tr := createTraining(t, h, tok, map[string]any{"name": "Primeros Auxilios"})
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	foreignEmp := testutil.Employee(t, pool, other, testutil.EmployeeName("Luis", "Pinto"))
	foreignTr := createTraining(t, h, other.Token(t, signer), map[string]any{"name": "Ajeno"})

	row := createEnrollment(t, h, tok, map[string]any{
		"training_id": tr.ID, "employee_id": emp.ID,
		"training_name": "Forged", "employee_name": "Forged",
	})
	if row.CompanyID != c.CompanyID || textOrNull(row.TrainingID) != tr.ID || row.EmployeeID != emp.ID {
		t.Fatalf("ids = %+v", row)
	}
	if textOrNull(row.TrainingName) != "Primeros Auxilios" || textOrNull(row.EmployeeName) != "Ana Diaz" {
		t.Fatalf("names = %q/%q, want them derived by the server", textOrNull(row.TrainingName), textOrNull(row.EmployeeName))
	}
	if textOrNull(row.Status) != "programado" || row.Score != nil {
		t.Fatalf("defaults: status %q score %v", textOrNull(row.Status), row.Score)
	}

	t.Run("another company's course or employee is 404 and writes nothing", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"training_id": foreignTr.ID, "employee_id": emp.ID},
			{"training_id": tr.ID, "employee_id": foreignEmp.ID},
			{"training_id": uuid.NewString(), "employee_id": emp.ID},
		} {
			if rec := testutil.Do(t, h, http.MethodPost, enrollmentsPath, tok, body); rec.Code != http.StatusNotFound {
				t.Fatalf("%v: got %d %s, want 404", body, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("the same employee twice in one course is 409 in Spanish", func(t *testing.T) {
		rec := testutil.Do(t, h, http.MethodPost, enrollmentsPath, tok, map[string]any{"training_id": tr.ID, "employee_id": emp.ID})
		if rec.Code != http.StatusConflict || errMessage(t, rec) != "El empleado ya está inscrito en esta capacitación" {
			t.Fatalf("got %d %s", rec.Code, rec.Body.String())
		}
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM enrollments WHERE training_id = $1 AND employee_id = $2`, tr.ID, emp.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rows for the pair = %d (err %v), want 1", n, err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		for name, tc := range map[string]struct{ body, code, field string }{
			"missing training_id": {`{"employee_id":"` + emp.ID + `"}`, "validation_failed", "training_id"},
			"missing employee_id": {`{"training_id":"` + tr.ID + `"}`, "validation_failed", "employee_id"},
			"malformed uuid":      {`{"training_id":"x","employee_id":"` + emp.ID + `"}`, "validation_failed", "training_id"},
			"unknown status":      {`{"training_id":"` + tr.ID + `","employee_id":"` + emp.ID + `","status":"aprobado"}`, "validation_failed", "status"},
			"score over 100":      {`{"training_id":"` + tr.ID + `","employee_id":"` + emp.ID + `","score":100.5}`, "validation_failed", "score"},
			"unknown field":       {`{"training_id":"` + tr.ID + `","employee_id":"` + emp.ID + `","bogus":1}`, "bad_request", ""},
		} {
			rec := doRawBody(t, h, http.MethodPost, enrollmentsPath, tok, tc.body)
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != tc.code || (tc.field != "" && errField(t, rec, tc.field) == "") {
				t.Fatalf("%s: got %d %s, want 400 %s %s", name, rec.Code, rec.Body.String(), tc.code, tc.field)
			}
		}
	})
}

// Grading: the panel's "Calificar" sets status, score and notes; certificates
// come from completed enrollments with a score.
func TestEnrollments_PatchGradesAnEnrollment(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EnrollPatchCo")
	tok := c.Token(t, signer)
	tr := createTraining(t, h, tok, map[string]any{"name": "Excel"})
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	en := createEnrollment(t, h, tok, map[string]any{"training_id": tr.ID, "employee_id": emp.ID, "notes": "turno A"})
	path := enrollmentsPath + "/" + en.ID

	rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"status": "completado", "score": 95.5})
	if rec.Code != http.StatusOK {
		t.Fatalf("grade: %d %s", rec.Code, rec.Body.String())
	}
	got := testutil.DecodeRows[enrollmentRow](t, rec)[0]
	if textOrNull(got.Status) != "completado" || got.Score == nil || *got.Score != 95.5 || textOrNull(got.Notes) != "turno A" {
		t.Fatalf("after grading: %+v", got)
	}

	rec = testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"score": nil})
	if got := testutil.DecodeRows[enrollmentRow](t, rec)[0]; rec.Code != http.StatusOK || got.Score != nil || textOrNull(got.Status) != "completado" {
		t.Fatalf("null clears the score only: %d %+v", rec.Code, got)
	}

	for name, body := range map[string]string{
		"negative score": `{"score":-1}`,
		"score over 100": `{"score":101}`,
		"null status":    `{"status":null}`,
		"unknown status": `{"status":"aprobado"}`,
		"score as text":  `{"score":"80"}`,
	} {
		rec := doRawBody(t, h, http.MethodPatch, path, tok, body)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: got %d %s, want 400 validation_failed", name, rec.Code, rec.Body.String())
		}
	}
	for name, body := range map[string]string{
		"re-point the course":   `{"training_id":"` + tr.ID + `"}`,
		"re-point the employee": `{"employee_id":"` + emp.ID + `"}`,
		"key in another case":   `{"Score":80}`,
	} {
		if rec := doRawBody(t, h, http.MethodPatch, path, tok, body); rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "bad_request" {
			t.Fatalf("%s: got %d %s, want 400 bad_request", name, rec.Code, rec.Body.String())
		}
	}
	if rec := testutil.Do(t, h, http.MethodPatch, enrollmentsPath+"/"+uuid.NewString(), tok, map[string]any{"score": 80}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", rec.Code)
	}
}

func TestEnrollments_ListFiltersDeleteAndIsolation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "EnrollListA")
	b := testutil.Company(t, pool, "EnrollListB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	tr1 := createTraining(t, h, ta, map[string]any{"name": "Excel"})
	tr2 := createTraining(t, h, ta, map[string]any{"name": "Word"})
	ana := testutil.Employee(t, pool, a, testutil.EmployeeName("Ana", "Diaz"))
	luis := testutil.Employee(t, pool, a, testutil.EmployeeName("Luis", "Pinto"))
	e1 := createEnrollment(t, h, ta, map[string]any{"training_id": tr1.ID, "employee_id": ana.ID})
	createEnrollment(t, h, ta, map[string]any{"training_id": tr1.ID, "employee_id": luis.ID})
	createEnrollment(t, h, ta, map[string]any{"training_id": tr2.ID, "employee_id": ana.ID})

	count := func(q string) int {
		return len(testutil.DecodeRows[enrollmentRow](t, testutil.Do(t, h, http.MethodGet, enrollmentsPath+q, ta, nil)))
	}
	if n := count(""); n != 3 {
		t.Fatalf("list = %d, want 3", n)
	}
	if n := count("?training_id=" + tr1.ID); n != 2 {
		t.Fatalf("?training_id = %d, want 2", n)
	}
	if n := count("?employee_id=" + ana.ID); n != 2 {
		t.Fatalf("?employee_id = %d, want 2", n)
	}
	if n := count("?id=" + e1.ID); n != 1 {
		t.Fatalf("?id = %d, want 1", n)
	}
	if rec := testutil.Do(t, h, http.MethodGet, enrollmentsPath+"?training_id=x", ta, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed filter: got %d, want 400", rec.Code)
	}

	if list := testutil.DecodeRows[enrollmentRow](t, testutil.Do(t, h, http.MethodGet, enrollmentsPath, tb, nil)); len(list) != 0 {
		t.Fatalf("foreign list sees %d rows", len(list))
	}
	for _, m := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		if rec := testutil.Do(t, h, m, enrollmentsPath+"/"+e1.ID, tb, map[string]any{"score": 1}); rec.Code != http.StatusNotFound {
			t.Fatalf("foreign %s: got %d, want 404", m, rec.Code)
		}
	}

	if rec := testutil.Do(t, h, http.MethodDelete, enrollmentsPath+"/"+e1.ID, ta, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", rec.Code)
	}
	if n := count(""); n != 2 {
		t.Fatalf("after delete list = %d, want 2", n)
	}

	seedMedRole(t, pool, a, "cajero", `{"payroll": true}`)
	if rec := testutil.Do(t, h, http.MethodGet, enrollmentsPath, a.TokenAs(t, pool, signer, "cajero"), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("role without employees: got %d, want 403", rec.Code)
	}
}
