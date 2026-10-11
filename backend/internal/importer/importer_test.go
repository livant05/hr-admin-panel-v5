package importer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/importer"
	"github.com/livant05/rrhh-go/internal/testutil"
)

const (
	seedCompany = "904fad55-e273-4533-aa73-4bb4daa4171e"
	seedAdmin   = "7b03222e-c5b3-4912-9565-95dda72222f8"

	coID   = "11111111-1111-4111-8111-111111111111"
	deptID = "22222222-2222-4222-8222-222222222222"
	emp1   = "33333333-3333-4333-8333-333333333331"
	emp2   = "33333333-3333-4333-8333-333333333332"
	att1   = "44444444-4444-4444-8444-444444444441"
)

type rows = []map[string]any

// writeExport writes one <table>.json per entry into a fresh temp dir.
func writeExport(t *testing.T, tables map[string]rows) string {
	t.Helper()
	dir := t.TempDir()
	for name, r := range tables {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func baseExport() map[string]rows {
	return map[string]rows{
		"companies":   {{"id": coID, "name": "Acme Test"}},
		"departments": {{"id": deptID, "company_id": coID, "name": "Ops"}},
		"employees": {
			{"id": emp1, "company_id": coID, "first_name": "Ana", "last_name": "Prueba", "salary": 1200.5},
			{"id": emp2, "company_id": coID, "first_name": "Beto", "last_name": "Muestra", "salary": 900},
		},
		"attendance_logs": {
			{"id": att1, "company_id": coID, "employee_id": emp1, "date": "2024-03-01", "status": "present"},
		},
	}
}

func run(t *testing.T, pool *pgxpool.Pool, opts importer.Options) (*importer.Report, error) {
	t.Helper()
	return importer.Run(context.Background(), pool, opts)
}

func TestRun_InsertsInTierOrderPreservingIDs(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	dir := writeExport(t, baseExport())

	rep, err := run(t, pool, importer.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, name := range []string{"companies", "departments", "employees", "attendance_logs"} {
		tr := rep.Table(name)
		if tr == nil || tr.Inserted == 0 || tr.Invalid != 0 {
			t.Fatalf("table %s report = %+v", name, tr)
		}
	}
	if got := count(t, pool, `SELECT count(*) FROM employees WHERE id IN ($1,$2) AND company_id = $3`, emp1, emp2, coID); got != 2 {
		t.Fatalf("employees with preserved ids = %d, want 2", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM attendance_logs WHERE id = $1 AND employee_id = $2`, att1, emp1); got != 1 {
		t.Fatalf("attendance relation not preserved")
	}
	if tr := rep.Table("leave_requests"); tr == nil || !tr.Missing {
		t.Fatalf("leave_requests should be reported as missing file, got %+v", tr)
	}
}

func TestRun_IsIdempotent(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	dir := writeExport(t, baseExport())

	if _, err := run(t, pool, importer.Options{Dir: dir}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, err := run(t, pool, importer.Options{Dir: dir})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	tr := rep.Table("employees")
	if tr.Inserted != 0 || tr.Present != 2 || tr.Read != 2 {
		t.Fatalf("employees second run = %+v, want inserted 0 present 2", tr)
	}
	if got := count(t, pool, `SELECT count(*) FROM employees WHERE company_id = $1`, coID); got != 2 {
		t.Fatalf("employees = %d, want 2", got)
	}
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	dir := writeExport(t, baseExport())

	rep, err := run(t, pool, importer.Options{Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if tr := rep.Table("employees"); tr.Inserted != 2 {
		t.Fatalf("dry run should report would-insert 2, got %+v", tr)
	}
	if got := count(t, pool, `SELECT count(*) FROM employees`); got != 0 {
		t.Fatalf("dry run persisted %d employees", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM companies WHERE id = $1`, coID); got != 0 {
		t.Fatalf("dry run persisted the company")
	}
}

func TestRun_SeedRowsAreUntouched(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	dir := writeExport(t, map[string]rows{
		"companies": {{"id": seedCompany, "name": "Renamed In Supabase"}},
	})

	rep, err := run(t, pool, importer.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tr := rep.Table("companies"); tr.Present != 1 || tr.Inserted != 0 {
		t.Fatalf("companies = %+v, want present 1", tr)
	}
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM companies WHERE id = $1`, seedCompany).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Ventatec" {
		t.Fatalf("seed company overwritten: %q", name)
	}
	if got := count(t, pool, `SELECT count(*) FROM users WHERE id = $1`, seedAdmin); got != 1 {
		t.Fatalf("seed admin missing")
	}
}

func TestRun_DropsUnknownAndGeneratedColumns(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["leave_balances"] = rows{{
		"id": "55555555-5555-4555-8555-555555555551", "company_id": coID, "employee_id": emp1,
		"year": 2024, "earned_days": 10, "used_days": 4, "remaining_days": 99, "legacy_flag": true,
	}}
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tr := rep.Table("leave_balances")
	if tr.Inserted != 1 {
		t.Fatalf("leave_balances = %+v", tr)
	}
	if got := strings.Join(tr.Dropped, ","); got != "legacy_flag,remaining_days" {
		t.Fatalf("dropped keys = %q, want legacy_flag,remaining_days", got)
	}
	var remaining float64
	if err := pool.QueryRow(context.Background(), `SELECT remaining_days FROM leave_balances WHERE employee_id = $1`, emp1).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 6 {
		t.Fatalf("remaining_days = %v, want generated 6", remaining)
	}
}

func TestRun_ValueTypesRoundTrip(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["employees"] = rows{{
		"id": emp1, "company_id": coID, "first_name": "Ana", "last_name": "Prueba",
		"birth_date": "1990-05-17", "age": 34, "sex": "F", "salary": 1234.56,
		"hourly_rate": 5.4321, "created_at": "2023-01-02T03:04:05.123456+00:00",
		"updated_at": "2023-06-07T08:09:10+00:00",
	}}
	exp["attendance_logs"] = rows{{
		"id": att1, "company_id": coID, "employee_id": emp1, "date": "2024-03-01",
		"time_in": "08:15:00", "time_out": "17:00:00", "work_type": 2,
	}}
	exp["surveys"] = rows{
		{"id": "66666666-6666-4666-8666-666666666661", "company_id": coID, "title": "obj", "questions": []any{map[string]any{"q": "How?"}}},
		{"id": "66666666-6666-4666-8666-666666666662", "company_id": coID, "title": "str", "questions": "[{\"q\":\"How?\"}]"},
	}
	exp["zkteco_unknown_pins"] = rows{{
		"id": "77777777-7777-4777-8777-777777777771", "pin": "1234", "attempts": 3, "resolved": true,
		"last_seen": "2024-02-02T10:00:00+00:00",
	}}
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Invalid) != 0 {
		t.Fatalf("unexpected invalid rows: %+v", rep.Invalid)
	}

	ctx := context.Background()
	var birth, ts, updated, timeIn, sex, salary, hourly string
	var age int
	if err := pool.QueryRow(ctx, `SELECT birth_date::text, age, sex, salary::text, hourly_rate::text,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US'),
		to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS')
		FROM employees WHERE id = $1`, emp1).Scan(&birth, &age, &sex, &salary, &hourly, &ts, &updated); err != nil {
		t.Fatal(err)
	}
	if birth != "1990-05-17" || age != 34 || sex != "F" || salary != "1234.56" || hourly != "5.4321" ||
		ts != "2023-01-02T03:04:05.123456" || updated != "2023-06-07T08:09:10" {
		t.Fatalf("employee round trip: %s %d %s %s %s %s %s", birth, age, sex, salary, hourly, ts, updated)
	}
	var wt int
	if err := pool.QueryRow(ctx, `SELECT time_in::text, work_type FROM attendance_logs WHERE id = $1`, att1).Scan(&timeIn, &wt); err != nil {
		t.Fatal(err)
	}
	if timeIn != "08:15:00" || wt != 2 {
		t.Fatalf("attendance round trip: %s %d", timeIn, wt)
	}
	// jsonb: an array stays an array, a JSON string stays a JSON string.
	if got := count(t, pool, `SELECT count(*) FROM surveys WHERE title = 'obj' AND jsonb_typeof(questions) = 'array'`); got != 1 {
		t.Fatalf("object jsonb not preserved")
	}
	if got := count(t, pool, `SELECT count(*) FROM surveys WHERE title = 'str' AND jsonb_typeof(questions) = 'string'`); got != 1 {
		t.Fatalf("string jsonb not preserved")
	}
	if got := count(t, pool, `SELECT count(*) FROM zkteco_unknown_pins WHERE pin = '1234' AND resolved AND attempts = 3`); got != 1 {
		t.Fatalf("bool/int round trip failed")
	}
}

func TestRun_InvalidRowAbortsByDefault(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["employees"] = rows{
		{"id": emp1, "company_id": coID, "first_name": "Ana", "last_name": "Prueba"},
		{"id": emp2, "company_id": coID, "last_name": "SinNombre", "first_name": nil}, // NOT NULL violation
	}
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp)})
	if !errors.Is(err, importer.ErrInvalidRows) {
		t.Fatalf("err = %v, want ErrInvalidRows", err)
	}
	if rep == nil || len(rep.Invalid) == 0 {
		t.Fatalf("report should list invalid rows, got %+v", rep)
	}
	if got := count(t, pool, `SELECT count(*) FROM companies WHERE id = $1`, coID); got != 0 {
		t.Fatalf("abort must roll back everything, company persisted")
	}
}

func TestRun_SkipInvalidKeepsGoodRowsAndListsBadOnes(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["employees"] = rows{
		{"id": emp1, "company_id": coID, "first_name": "Ana", "last_name": "Prueba"},
		{"id": emp2, "company_id": coID, "last_name": "SinNombre", "first_name": nil},
		{"id": "not-a-uuid", "company_id": coID, "first_name": "X", "last_name": "Y"},
		{"id": emp1, "company_id": coID, "first_name": "Dup", "last_name": "Dup"},
	}
	exp["attendance_logs"] = rows{
		{"id": att1, "company_id": coID, "employee_id": emp1, "date": "2024-03-01"},
		{"id": "44444444-4444-4444-8444-444444444442", "company_id": coID, "employee_id": emp2, "date": "2024-03-01"}, // orphan after skip
	}
	rep, err := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := count(t, pool, `SELECT count(*) FROM employees WHERE company_id = $1`, coID); got != 1 {
		t.Fatalf("employees = %d, want only the valid one", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM attendance_logs WHERE company_id = $1`, coID); got != 1 {
		t.Fatalf("attendance = %d, want 1 (orphan skipped)", got)
	}
	if tr := rep.Table("employees"); tr.Invalid != 3 || tr.Inserted != 1 {
		t.Fatalf("employees = %+v, want invalid 3 inserted 1", tr)
	}
	byTable := map[string]int{}
	for _, iv := range rep.Invalid {
		byTable[iv.Table]++
		if iv.Reason == "" {
			t.Fatalf("invalid row without reason: %+v", iv)
		}
	}
	if byTable["employees"] != 3 || byTable["attendance_logs"] != 1 {
		t.Fatalf("invalid by table = %v", byTable)
	}
}

func TestReport_WriteListsTablesAndInvalidRows(t *testing.T) {
	pool, _ := testutil.IsolatedSchema(t)
	exp := baseExport()
	exp["employees"] = rows{{"id": emp1, "company_id": coID, "first_name": nil, "last_name": "X"}}
	rep, _ := run(t, pool, importer.Options{Dir: writeExport(t, exp), SkipInvalid: true})

	var buf bytes.Buffer
	rep.Write(&buf)
	out := buf.String()
	for _, want := range []string{"employees", "attendance_logs", "invalid", emp1} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}
