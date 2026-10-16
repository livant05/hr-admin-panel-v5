package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/livant05/rrhh-go/internal/testutil"
)

type trainingRow struct {
	ID          string  `json:"id"`
	CompanyID   string  `json:"company_id"`
	Name        string  `json:"name"`
	Category    *string `json:"category"`
	Hours       *int    `json:"hours"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	Mode        *string `json:"mode"`
	Instructor  *string `json:"instructor"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	CreatedAt   string  `json:"created_at"`
}

const trainingsPath = "/api/trainings"

func createTraining(t *testing.T, h http.Handler, token string, body map[string]any) trainingRow {
	t.Helper()
	rec := testutil.Do(t, h, http.MethodPost, trainingsPath, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create training %v: got %d (%s)", body, rec.Code, rec.Body.String())
	}
	rows := testutil.DecodeRows[trainingRow](t, rec)
	if len(rows) != 1 {
		t.Fatalf("create returned %d rows, want a one-element array", len(rows))
	}
	return rows[0]
}

func TestTrainings_CreateGetListAndDefaults(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainCreateCo")
	tok := c.Token(t, signer)

	full := createTraining(t, h, tok, map[string]any{
		"name": "  Primeros Auxilios  ", "category": "seguridad", "hours": 8,
		"start_date": "2026-11-02", "end_date": "2026-11-03", "mode": "presencial",
		"instructor": "Cruz Roja", "description": "Curso básico",
	})
	if full.CompanyID != c.CompanyID || full.Name != "Primeros Auxilios" {
		t.Fatalf("company/name = %s/%q, want the token's company and the trimmed name", full.CompanyID, full.Name)
	}
	if textOrNull(full.Status) != "programado" {
		t.Fatalf("status = %q, want the default programado", textOrNull(full.Status))
	}
	if full.Hours == nil || *full.Hours != 8 || textOrNull(full.StartDate) != "2026-11-02" || textOrNull(full.EndDate) != "2026-11-03" {
		t.Fatalf("hours/dates = %v/%s/%s", full.Hours, textOrNull(full.StartDate), textOrNull(full.EndDate))
	}

	minimal := createTraining(t, h, tok, map[string]any{"name": "Excel"})
	if minimal.Hours != nil || minimal.StartDate != nil || minimal.Category != nil {
		t.Fatalf("absent optional fields must be null, got %+v", minimal)
	}

	get := testutil.Do(t, h, http.MethodGet, trainingsPath+"/"+full.ID, tok, nil)
	if got := testutil.DecodeRow[trainingRow](t, get); get.Code != http.StatusOK || got.Name != "Primeros Auxilios" {
		t.Fatalf("get: %d %s", get.Code, get.Body.String())
	}
	list := testutil.DecodeRows[trainingRow](t, testutil.Do(t, h, http.MethodGet, trainingsPath, tok, nil))
	if len(list) != 2 {
		t.Fatalf("list = %d rows, want 2", len(list))
	}
	byID := testutil.DecodeRows[trainingRow](t, testutil.Do(t, h, http.MethodGet, trainingsPath+"?id="+minimal.ID, tok, nil))
	if len(byID) != 1 || byID[0].ID != minimal.ID {
		t.Fatalf("?id= returned %+v", byID)
	}
}

func TestTrainings_CreateValidation(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainValidCo")
	tok := c.Token(t, signer)

	cases := []struct {
		name, body, code, field string
	}{
		{"missing name", `{"category":"legal"}`, "validation_failed", "name"},
		{"blank name", `{"name":"   "}`, "validation_failed", "name"},
		{"name over 200 runes", `{"name":"` + strings.Repeat("ñ", 201) + `"}`, "validation_failed", "name"},
		{"negative hours", `{"name":"x","hours":-1}`, "validation_failed", "hours"},
		{"hours over 10000", `{"name":"x","hours":10001}`, "validation_failed", "hours"},
		{"unknown status", `{"name":"x","status":"cancelado"}`, "validation_failed", "status"},
		{"bad start_date", `{"name":"x","start_date":"2026-02-30"}`, "validation_failed", "start_date"},
		{"empty end_date", `{"name":"x","end_date":""}`, "validation_failed", "end_date"},
		{"end before start", `{"name":"x","start_date":"2026-11-03","end_date":"2026-11-02"}`, "validation_failed", "end_date"},
		{"hours as text", `{"name":"x","hours":"8"}`, "bad_request", ""},
		{"fractional hours", `{"name":"x","hours":1.5}`, "bad_request", ""},
		{"unknown field", `{"name":"x","bogus":1}`, "bad_request", ""},
		{"company_id in body", `{"name":"x","company_id":"` + uuid.NewString() + `"}`, "bad_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRawBody(t, h, http.MethodPost, trainingsPath, tok, tc.body)
			if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != tc.code {
				t.Fatalf("got %d %s, want 400 %s", rec.Code, rec.Body.String(), tc.code)
			}
			if tc.field != "" && errField(t, rec, tc.field) == "" {
				t.Fatalf("expected fields.%s in %s", tc.field, rec.Body.String())
			}
		})
	}
	if list := testutil.DecodeRows[trainingRow](t, testutil.Do(t, h, http.MethodGet, trainingsPath, tok, nil)); len(list) != 0 {
		t.Fatalf("rejected bodies created %d trainings", len(list))
	}
}

func TestTrainings_PatchIsPartialAndChangesTheStatus(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainPatchCo")
	tok := c.Token(t, signer)
	tr := createTraining(t, h, tok, map[string]any{"name": "Liderazgo", "hours": 12, "instructor": "Ana", "start_date": "2026-11-02"})
	path := trainingsPath + "/" + tr.ID

	rec := testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"status": "en_progreso"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status patch: %d %s", rec.Code, rec.Body.String())
	}
	got := testutil.DecodeRows[trainingRow](t, rec)[0]
	if textOrNull(got.Status) != "en_progreso" || got.Name != "Liderazgo" || got.Hours == nil || *got.Hours != 12 || textOrNull(got.Instructor) != "Ana" {
		t.Fatalf("after a status-only patch: %+v", got)
	}

	rec = testutil.Do(t, h, http.MethodPatch, path, tok, map[string]any{"instructor": nil, "status": "completado"})
	got = testutil.DecodeRows[trainingRow](t, rec)[0]
	if rec.Code != http.StatusOK || got.Instructor != nil || textOrNull(got.Status) != "completado" {
		t.Fatalf("null clears an optional field: %d %+v", rec.Code, got)
	}

	for name, body := range map[string]string{
		"null name":                   `{"name":null}`,
		"null status":                 `{"status":null}`,
		"unknown status":              `{"status":"cancelado"}`,
		"end before the stored start": `{"end_date":"2026-11-01"}`,
	} {
		rec := doRawBody(t, h, http.MethodPatch, path, tok, body)
		if rec.Code != http.StatusBadRequest || testutil.ErrCode(t, rec) != "validation_failed" {
			t.Fatalf("%s: got %d %s, want 400 validation_failed", name, rec.Code, rec.Body.String())
		}
	}
	if rec := doRawBody(t, h, http.MethodPatch, path, tok, `{"Status":"programado"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a key in another case must be 400, got %d", rec.Code)
	}
	if rec := testutil.Do(t, h, http.MethodPatch, trainingsPath+"/"+uuid.NewString(), tok, map[string]any{"status": "programado"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", rec.Code)
	}
}

func TestTrainings_DeleteRemovesItsEnrollments(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainDeleteCo")
	tok := c.Token(t, signer)
	emp := testutil.Employee(t, pool, c, testutil.EmployeeName("Ana", "Diaz"))
	tr := createTraining(t, h, tok, map[string]any{"name": "Excel"})
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO enrollments (company_id, training_id, training_name, employee_id, employee_name) VALUES ($1, $2, 'Excel', $3, 'Ana Diaz')`,
		c.CompanyID, tr.ID, emp.ID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}

	if rec := testutil.Do(t, h, http.MethodDelete, trainingsPath+"/"+tr.ID, tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d %s, want 204", rec.Code, rec.Body.String())
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM enrollments WHERE training_id = $1`, tr.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("enrollments left = %d (err %v), want 0", n, err)
	}
	if rec := testutil.Do(t, h, http.MethodDelete, trainingsPath+"/"+tr.ID, tok, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: got %d, want 404", rec.Code)
	}
}

func TestTrainings_TenantIsolationAndPermission(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	a := testutil.Company(t, pool, "TrainTenantA")
	b := testutil.Company(t, pool, "TrainTenantB")
	ta, tb := a.Token(t, signer), b.Token(t, signer)
	tr := createTraining(t, h, ta, map[string]any{"name": "Seguridad"})

	if rec := testutil.Do(t, h, http.MethodGet, trainingsPath+"/"+tr.ID, tb, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign get: got %d, want 404", rec.Code)
	}
	if list := testutil.DecodeRows[trainingRow](t, testutil.Do(t, h, http.MethodGet, trainingsPath, tb, nil)); len(list) != 0 {
		t.Fatalf("foreign list sees %d rows", len(list))
	}
	for _, m := range []string{http.MethodPatch, http.MethodDelete} {
		if rec := testutil.Do(t, h, m, trainingsPath+"/"+tr.ID, tb, map[string]any{"name": "hijack"}); rec.Code != http.StatusNotFound {
			t.Fatalf("foreign %s: got %d, want 404", m, rec.Code)
		}
	}

	seedMedRole(t, pool, a, "cajero", `{"payroll": true}`)
	noPerm := a.TokenAs(t, pool, signer, "cajero")
	if rec := testutil.Do(t, h, http.MethodGet, trainingsPath, noPerm, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("role without employees: got %d, want 403", rec.Code)
	}
	seedMedRole(t, pool, a, "rrhh", `{"employees": true}`)
	withPerm := a.TokenAs(t, pool, signer, "rrhh")
	if rec := testutil.Do(t, h, http.MethodPost, trainingsPath, withPerm, map[string]any{"name": "Excel"}); rec.Code != http.StatusCreated {
		t.Fatalf("role with employees: got %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// Imported rows may already break the create rules (an unknown status, a long
// name). A PATCH validates only what the client sends, so such a row can still
// be edited; the stored values it does not touch are kept as they are.
func TestTrainings_PatchOfALegacyRowValidatesOnlyTheSentFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainLegacyCo")
	tok := c.Token(t, signer)
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO trainings (company_id, name, status, hours) VALUES ($1, $2, 'cancelado', -3) RETURNING id`,
		c.CompanyID, strings.Repeat("x", 250)).Scan(&id); err != nil {
		t.Fatalf("seed legacy training: %v", err)
	}
	rec := testutil.Do(t, h, http.MethodPatch, trainingsPath+"/"+id, tok, map[string]any{"instructor": "Ana"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", rec.Code, rec.Body.String())
	}
	got := testutil.DecodeRows[trainingRow](t, rec)[0]
	if textOrNull(got.Instructor) != "Ana" || textOrNull(got.Status) != "cancelado" || len(got.Name) != 250 || got.Hours == nil || *got.Hours != -3 {
		t.Fatalf("stored values must be kept: %+v", got)
	}
	if rec := testutil.Do(t, h, http.MethodPatch, trainingsPath+"/"+id, tok, map[string]any{"status": "cancelado"}); rec.Code != http.StatusBadRequest || errField(t, rec, "status") == "" {
		t.Fatalf("a sent invalid status is still rejected: got %d %s", rec.Code, rec.Body.String())
	}
}

// An imported course may have no status at all. A PATCH that does not send
// status keeps it NULL instead of writing the create default (programado).
func TestTrainings_PatchKeepsANullStatusWhenStatusIsNotSent(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "TrainNullStatusCo")
	tok := c.Token(t, signer)
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO trainings (company_id, name, status) VALUES ($1, 'Excel', NULL) RETURNING id`,
		c.CompanyID).Scan(&id); err != nil {
		t.Fatalf("seed training without status: %v", err)
	}
	rec := testutil.Do(t, h, http.MethodPatch, trainingsPath+"/"+id, tok, map[string]any{"instructor": "Ana"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := testutil.DecodeRows[trainingRow](t, rec)[0]; got.Status != nil || textOrNull(got.Instructor) != "Ana" {
		t.Fatalf("status must stay null and instructor change: %+v", got)
	}
	var stored *string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM trainings WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if stored != nil {
		t.Fatalf("stored status = %q, want NULL", *stored)
	}
	// Sending a status still sets it.
	rec = testutil.Do(t, h, http.MethodPatch, trainingsPath+"/"+id, tok, map[string]any{"status": "completado"})
	if rec.Code != http.StatusOK || textOrNull(testutil.DecodeRows[trainingRow](t, rec)[0].Status) != "completado" {
		t.Fatalf("sent status: got %d %s, want 200 completado", rec.Code, rec.Body.String())
	}
}
