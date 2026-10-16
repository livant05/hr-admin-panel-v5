package importer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/importer"
	"github.com/livant05/rrhh-go/internal/overtime"
	"github.com/livant05/rrhh-go/internal/testutil"
)

// legacyScores is the double-encoded payload the old Supabase client stored:
// JSON.stringify(scores) saved into a JSONB column, i.e. a JSON string.
func legacyScores(vals ...int) string {
	keys := []string{"puntualidad", "calidad", "trabajo_equipo", "iniciativa",
		"comunicacion", "liderazgo", "objetivos", "actitud"}
	m := map[string]int{}
	for i, k := range keys {
		m[k] = vals[i]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func withEmployees(extra map[string]rows) map[string]rows {
	exp := baseExport()
	exp["employees"] = rows{
		{"id": emp1, "company_id": coID, "first_name": "Ana", "last_name": "Prueba", "salary": 3000},
		{"id": emp2, "company_id": coID, "first_name": "Beto", "last_name": "Muestra", "salary": 900},
	}
	for k, v := range extra {
		exp[k] = v
	}
	return exp
}

func invalidByReason(rep *importer.Report, table string) []importer.InvalidRow {
	var out []importer.InvalidRow
	for _, iv := range rep.Invalid {
		if iv.Table == table {
			out = append(out, iv)
		}
	}
	return out
}

func TestEvaluations_LegacyStringScoresBecomeCanonicalWithServerAvg(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	const ev1 = "88888888-8888-4888-8888-888888888881"
	exp := withEmployees(map[string]rows{"evaluations": {{
		"id": ev1, "company_id": coID, "employee_id": emp1, "period": "2024-Q1",
		"scores": legacyScores(10, 9, 8, 7, 6, 5, 8, 7), // sum 60 -> 7.50 Excelente
		"avg":    9.99, "category": "Sobresaliente", "status": nil,
	}}})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tr := rep.Table("evaluations"); tr.Inserted != 1 {
		t.Fatalf("evaluations = %+v", tr)
	}
	var avg, cat, status, typ string
	var puntualidad int
	if err := pool.QueryRow(context.Background(),
		`SELECT avg::text, category, status, jsonb_typeof(scores), (scores->>'puntualidad')::int FROM evaluations WHERE id = $1`, ev1,
	).Scan(&avg, &cat, &status, &typ, &puntualidad); err != nil {
		t.Fatal(err)
	}
	if avg != "7.50" || cat != "Excelente" || status != "completada" || typ != "object" || puntualidad != 10 {
		t.Fatalf("evaluation = avg %s cat %s status %s scores %s puntualidad %d", avg, cat, status, typ, puntualidad)
	}
}

func TestEvaluations_NativeObjectScoresAlsoRecomputed(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	const ev1 = "88888888-8888-4888-8888-888888888882"
	exp := withEmployees(map[string]rows{"evaluations": {{
		"id": ev1, "company_id": coID, "employee_id": emp1,
		"scores": map[string]any{"puntualidad": 10, "calidad": 10, "trabajo_equipo": 10, "iniciativa": 10,
			"comunicacion": 10, "liderazgo": 10, "objetivos": 10, "actitud": 10},
		"avg": 1, "category": "Deficiente",
	}}})
	if _, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var avg, cat string
	if err := pool.QueryRow(context.Background(), `SELECT avg::text, category FROM evaluations WHERE id = $1`, ev1).Scan(&avg, &cat); err != nil {
		t.Fatal(err)
	}
	if avg != "10.00" || cat != "Sobresaliente" {
		t.Fatalf("avg=%s category=%s", avg, cat)
	}
}

func TestEvaluations_InvalidScoresAreRejectedNotRounded(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	good := legacyScores(5, 5, 5, 5, 5, 5, 5, 5)
	bad := strings.Replace(good, `"calidad":5`, `"calidad":7.5`, 1)
	out := strings.Replace(good, `"calidad":5`, `"calidad":11`, 1)
	missing := strings.Replace(good, `"calidad":5,`, ``, 1)
	mk := func(n int, scores any) map[string]any {
		return map[string]any{"id": "88888888-8888-4888-8888-88888888880" + string(rune('0'+n)),
			"company_id": coID, "employee_id": emp1, "scores": scores}
	}
	exp := withEmployees(map[string]rows{"evaluations": {mk(1, good), mk(2, bad), mk(3, out), mk(4, missing), mk(5, nil), mk(6, "not json")}})

	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("evaluations")
	if tr.Inserted != 1 || tr.Invalid != 5 {
		t.Fatalf("evaluations = %+v, want 1 inserted 5 invalid", tr)
	}
	for _, iv := range invalidByReason(rep, "evaluations") {
		if !strings.Contains(iv.Reason, "scores") {
			t.Errorf("reason for %s should name scores: %q", iv.ID, iv.Reason)
		}
	}
}

func TestMedicalRecords_RecomputedFromEmployeeSalary(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	const m1 = "99999999-9999-4999-8999-999999999991"
	exp := withEmployees(map[string]rows{"medical_records": {{
		"id": m1, "company_id": coID, "employee_id": emp1, "type": "enfermedad",
		"start_date": "2024-03-01", "end_date": "2024-03-20",
		"days": 1, "employer_days": 1, "css_days": 1, "cost": 1, "salary_basis": 1, "status": nil,
	}}})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tr := rep.Table("medical_records"); tr.Inserted != 1 {
		t.Fatalf("medical_records = %+v", tr)
	}
	// salary 3000 -> daily 100; 20 days: 3 employer + 17 CSS at 70%.
	var days, emp, css int
	var basis, cost, status string
	if err := pool.QueryRow(context.Background(),
		`SELECT days, employer_days, css_days, salary_basis::text, cost::text, status FROM medical_records WHERE id = $1`, m1,
	).Scan(&days, &emp, &css, &basis, &cost, &status); err != nil {
		t.Fatal(err)
	}
	if days != 20 || emp != 3 || css != 17 || basis != "3000.00" || cost != "1490.00" || status != "activa" {
		t.Fatalf("medical = %d/%d/%d basis %s cost %s status %s", days, emp, css, basis, cost, status)
	}
}

func TestMedicalRecords_InvalidRowsRejected(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	mk := func(n int, over map[string]any) map[string]any {
		r := map[string]any{"id": "99999999-9999-4999-8999-99999999999" + string(rune('0'+n)),
			"company_id": coID, "employee_id": emp1, "type": "enfermedad",
			"start_date": "2024-03-01", "end_date": "2024-03-05"}
		for k, v := range over {
			r[k] = v
		}
		return r
	}
	exp := withEmployees(map[string]rows{"medical_records": {
		mk(1, nil),
		mk(2, map[string]any{"end_date": "2024-02-01"}),                              // end before start
		mk(3, map[string]any{"type": "vacaciones"}),                                  // unknown type
		mk(4, map[string]any{"end_date": "2040-01-01"}),                              // span over 3650 days
		mk(5, map[string]any{"status": "pendiente"}),                                 // unknown status
		mk(6, map[string]any{"employee_id": "33333333-3333-4333-8333-3333333333ff"}), // no such employee
		mk(7, map[string]any{"start_date": nil}),
	}})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("medical_records")
	if tr.Inserted != 1 || tr.Invalid != 6 {
		t.Fatalf("medical_records = %+v, want 1 inserted 6 invalid; invalid=%+v", tr, rep.Invalid)
	}
}

func TestOvertimeLogs_AmountRecomputedWithStatutoryRates(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	mk := func(n int, over map[string]any) map[string]any {
		r := map[string]any{"id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa" + string(rune('0'+n)),
			"company_id": coID, "employee_id": emp1, "date": "2024-03-0" + string(rune('0'+n)),
			"hours": 2.5, "type": "night", "hourly_rate": 5.4321, "amount": 999}
		for k, v := range over {
			r[k] = v
		}
		return r
	}
	exp := withEmployees(map[string]rows{"overtime_logs": {
		mk(1, nil), // 5.4321*1.75*2.5 = 23.77
		mk(2, map[string]any{"type": nil, "hours": 1, "hourly_rate": 10, "amount": nil}), // defaults to regular: 12.50
		mk(3, map[string]any{"type": "weekend"}),                                         // unknown type
		mk(4, map[string]any{"hours": 4}),                                                // over the 3h cap
		mk(5, map[string]any{"hours": 0}),
		mk(6, map[string]any{"hourly_rate": nil}),
	}})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("overtime_logs")
	if tr.Inserted != 2 || tr.Invalid != 4 {
		t.Fatalf("overtime_logs = %+v; invalid=%+v", tr, rep.Invalid)
	}
	amounts := map[string]string{}
	rowsQ, err := pool.Query(context.Background(), `SELECT type, amount::text FROM overtime_logs ORDER BY date`)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsQ.Close()
	var order []string
	for rowsQ.Next() {
		var typ, amt string
		if err := rowsQ.Scan(&typ, &amt); err != nil {
			t.Fatal(err)
		}
		amounts[typ] = amt
		order = append(order, typ)
	}
	if amounts["night"] != "23.77" || amounts["regular"] != "12.50" {
		t.Fatalf("amounts = %v (order %v)", amounts, order)
	}
}

func TestRoles_ImportedRolesGetMedicalPermission(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["roles"] = rows{
		{"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1", "company_id": coID, "name": "att_on", "permissions": map[string]any{"attendance": true}},
		{"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2", "company_id": coID, "name": "att_off", "permissions": map[string]any{"attendance": false}},
		{"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb3", "company_id": coID, "name": "keeps", "permissions": map[string]any{"attendance": true, "medical": false}},
		{"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb4", "company_id": coID, "name": "nested", "permissions": map[string]any{"attendance": map[string]any{"view": false, "edit": true}}},
	}
	if _, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[string]string{"att_on": "true", "att_off": "false", "keeps": "false", "nested": "true"}
	for name, w := range want {
		var got string
		if err := pool.QueryRow(context.Background(), `SELECT (permissions->'medical')::text FROM roles WHERE name = $1`, name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("role %s medical = %s, want %s", name, got, w)
		}
	}
}

func TestUniqueKeyDuplicatesInExportAreInvalid(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := withEmployees(map[string]rows{
		"attendance_logs": {
			{"id": att1, "company_id": coID, "employee_id": emp1, "date": "2024-03-01"},
			{"id": "44444444-4444-4444-8444-444444444442", "company_id": coID, "employee_id": emp1, "date": "2024-03-01"},
		},
		"leave_balances": {
			{"id": "55555555-5555-4555-8555-555555555551", "company_id": coID, "employee_id": emp1, "year": 2024, "earned_days": 1},
			{"id": "55555555-5555-4555-8555-555555555552", "company_id": coID, "employee_id": emp1, "year": 2024, "earned_days": 2},
		},
	})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, table := range []string{"attendance_logs", "leave_balances"} {
		tr := rep.Table(table)
		if tr.Inserted != 1 || tr.Invalid != 1 {
			t.Fatalf("%s = %+v", table, tr)
		}
		iv := invalidByReason(rep, table)[0]
		if !strings.Contains(iv.Reason, "duplicate") || !strings.Contains(iv.Reason, "in export") {
			t.Errorf("%s reason = %q, want a duplicate-in-export message", table, iv.Reason)
		}
	}
}

// One application per candidate and posting (migration 0015): a repeated pair
// in the export is rejected with a reason, while rows whose candidate is gone
// (NULL) never collide.
func TestApplications_DuplicatePairInExportIsInvalid(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	const (
		job  = "66666666-6666-4666-8666-666666666661"
		cand = "77777777-7777-4777-8777-777777777771"
	)
	exp := baseExport()
	exp["job_postings"] = rows{{"id": job, "company_id": coID, "title": "Cajero"}}
	exp["candidates"] = rows{{"id": cand, "company_id": coID, "full_name": "Ana Torres"}}
	exp["applications"] = rows{
		{"id": "88888888-8888-4888-8888-888888888881", "company_id": coID, "job_posting_id": job, "candidate_id": cand},
		{"id": "88888888-8888-4888-8888-888888888882", "company_id": coID, "job_posting_id": job, "candidate_id": cand},
		{"id": "88888888-8888-4888-8888-888888888883", "company_id": coID, "job_posting_id": job, "candidate_id": nil},
		{"id": "88888888-8888-4888-8888-888888888884", "company_id": coID, "job_posting_id": job, "candidate_id": nil},
	}
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("applications")
	if tr.Inserted != 3 || tr.Invalid != 1 {
		t.Fatalf("applications = %+v, want 3 inserted and 1 invalid", tr)
	}
	iv := invalidByReason(rep, "applications")[0]
	if iv.ID != "88888888-8888-4888-8888-888888888882" || !strings.Contains(iv.Reason, "duplicate (job_posting_id, candidate_id) in export") {
		t.Errorf("invalid = %+v, want the second pair rejected as a duplicate in export", iv)
	}
}

// One enrollment per employee and course (migration 0016): a repeated pair in
// the export is rejected with a reason.
func TestEnrollments_DuplicatePairInExportIsInvalid(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	const tr = "99999999-9999-4999-8999-999999999991"
	exp := withEmployees(map[string]rows{
		"trainings": {{"id": tr, "company_id": coID, "name": "Excel"}},
		"enrollments": {
			{"id": "aaaaaaaa-0000-4000-8000-000000000001", "company_id": coID, "training_id": tr, "employee_id": emp1},
			{"id": "aaaaaaaa-0000-4000-8000-000000000002", "company_id": coID, "training_id": tr, "employee_id": emp1, "score": 90},
		},
	})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tr := rep.Table("enrollments"); tr.Inserted != 1 || tr.Invalid != 1 {
		t.Fatalf("enrollments = %+v, want 1 inserted and 1 invalid", tr)
	}
	iv := invalidByReason(rep, "enrollments")[0]
	if iv.ID != "aaaaaaaa-0000-4000-8000-000000000002" || !strings.Contains(iv.Reason, "duplicate (training_id, employee_id) in export") {
		t.Errorf("invalid = %+v, want the second pair rejected as a duplicate in export", iv)
	}
}

// TestParityWithAPI proves the import computes what POST /api/... would for
// the same inputs: same rates, same incapacity formula, same rounding.
func TestParityWithAPI(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	ctx := context.Background()
	tenant := testutil.Company(t, pool, "ParityCo")
	h, signer := testutil.Server(t, pool)
	token := tenant.Token(t, signer)
	emp := testutil.Employee(t, pool, tenant, testutil.EmployeeSalary(2750.55))

	rec := testutil.Do(t, h, http.MethodPost, "/api/overtime_logs", token, map[string]any{
		"employee_id": emp.ID, "date": "2024-03-04", "hours": 2.5, "type": "holiday", "hourly_rate": 7.3333})
	if rec.Code != http.StatusCreated {
		t.Fatalf("api overtime: %d %s", rec.Code, rec.Body)
	}
	apiOT := testutil.DecodeRows[map[string]any](t, rec)[0]
	rec = testutil.Do(t, h, http.MethodPost, "/api/medical_records", token, map[string]any{
		"employee_id": emp.ID, "type": "maternidad", "start_date": "2024-03-01", "end_date": "2024-04-10"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("api medical: %d %s", rec.Code, rec.Body)
	}
	apiMed := testutil.DecodeRows[map[string]any](t, rec)[0]

	const (
		company = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		empID   = "cccccccc-cccc-4ccc-8ccc-ccccccccccc1"
	)
	exp := map[string]rows{
		"companies": {{"id": company, "name": "ImportCo"}},
		"employees": {{"id": empID, "company_id": company, "first_name": "Ana", "last_name": "Paridad", "salary": 2750.55}},
		"overtime_logs": {{"id": "cccccccc-cccc-4ccc-8ccc-ccccccccccc2", "company_id": company, "employee_id": empID,
			"date": "2024-03-04", "hours": 2.5, "type": "holiday", "hourly_rate": 7.3333, "amount": 1}},
		"medical_records": {{"id": "cccccccc-cccc-4ccc-8ccc-ccccccccccc3", "company_id": company, "employee_id": empID,
			"type": "maternidad", "start_date": "2024-03-01", "end_date": "2024-04-10"}},
	}
	if _, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, company) })

	same := func(q string, apiID any, importID string) {
		t.Helper()
		var a, b string
		if err := pool.QueryRow(ctx, q, apiID).Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, q, importID).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Errorf("api %q != import %q for %s", a, b, q)
		}
	}
	same(`SELECT amount::text FROM overtime_logs WHERE id = $1::uuid`, apiOT["id"], "cccccccc-cccc-4ccc-8ccc-ccccccccccc2")
	same(`SELECT days||'/'||employer_days||'/'||css_days||'/'||salary_basis||'/'||cost FROM medical_records WHERE id = $1::uuid`,
		apiMed["id"], "cccccccc-cccc-4ccc-8ccc-ccccccccccc3")
}

func TestOvertime_UnknownTypeMessageListsTheOvertimePackageTypes(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := withEmployees(map[string]rows{"overtime_logs": {{
		"id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1", "company_id": coID, "employee_id": emp1,
		"date": "2024-03-01", "hours": 2, "type": "weekend", "hourly_rate": 5,
	}}})
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "type: must be one of " + strings.Join(overtime.Types(), ", ")
	if len(rep.Invalid) != 1 || rep.Invalid[0].Reason != want {
		t.Fatalf("invalid = %+v, want reason %q", rep.Invalid, want)
	}
}
