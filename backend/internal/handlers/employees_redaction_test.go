package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/livant05/rrhh-go/internal/auth"
	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

func decodeEmployeeMaps(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode employees: %v (%s)", err, body)
	}
	return rows
}

// TestEmployees_ReadRedactsSensitiveFields pins the shared-read decision:
// every authenticated role may list and get employees (directory, birthday
// calendar), but roles holding neither `employees` nor `payroll` receive JSON
// null for pay and personal-identity fields. Admin and permitted roles see
// everything; phone and birth_date stay visible to all.
func TestEmployees_ReadRedactsSensitiveFields(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "EmpRedact")
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1500))
	if _, err := pool.Exec(context.Background(),
		`UPDATE employees SET cedula='8-123-456', ss_number='SS-99', address='Calle 1',
		   hourly_rate=7.5, phone='555-0101', birth_date='1990-05-04',
		   emergency_contact='Ana', emergency_phone='555-0202', emergency_relation='Madre'
		 WHERE id=$1`, emp.ID); err != nil {
		t.Fatalf("seed sensitive fields: %v", err)
	}
	for name, perms := range map[string]string{
		"nokey":     `{"attendance": true}`,
		"empfalse":  `{"employees": false, "payroll": false}`,
		"hasemp":    `{"employees": true}`,
		"haspayrol": `{"payroll": true}`,
	} {
		seedMedRole(t, pool, c, name, perms)
	}

	paths := map[string]string{
		"list": "/api/employees",
		"get":  "/api/employees/" + emp.ID,
	}
	fetch := func(t *testing.T, kind, token string) map[string]json.RawMessage {
		t.Helper()
		rec := testutil.Do(t, h, http.MethodGet, paths[kind], token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d (body=%s)", kind, rec.Code, rec.Body.String())
		}
		var rows []map[string]json.RawMessage
		if kind == "get" {
			var one map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
				// the get endpoint may also answer a one-element array
				rows = decodeEmployeeMaps(t, rec.Body.Bytes())
			} else {
				rows = []map[string]json.RawMessage{one}
			}
		} else {
			rows = decodeEmployeeMaps(t, rec.Body.Bytes())
		}
		for _, r := range rows {
			var id string
			_ = json.Unmarshal(r["id"], &id)
			if id == emp.ID {
				return r
			}
		}
		t.Fatalf("%s: seeded employee not returned (%s)", kind, rec.Body.String())
		return nil
	}

	for _, kind := range []string{"list", "get"} {
		t.Run(kind+" redacts for roles without employees or payroll", func(t *testing.T) {
			for _, role := range []string{"nokey", "empfalse", "ghost"} {
				row := fetch(t, kind, c.TokenAs(t, pool, signer, role))
				for _, f := range []string{"salary", "hourly_rate", "cedula", "ss_number", "address", "emergency_contact", "emergency_phone", "emergency_relation"} {
					if raw, ok := row[f]; !ok || string(raw) != "null" {
						t.Errorf("%s as %s: %s = %s, want null", kind, role, f, raw)
					}
				}
				if string(row["phone"]) != `"555-0101"` || string(row["birth_date"]) != `"1990-05-04"` {
					t.Errorf("%s as %s: phone/birth_date must stay visible, got %s / %s", kind, role, row["phone"], row["birth_date"])
				}
				if len(row["first_name"]) == 0 || string(row["first_name"]) == "null" {
					t.Errorf("%s as %s: first_name must stay visible", kind, role)
				}
			}
		})

		t.Run(kind+" shows everything to admin, employees and payroll roles", func(t *testing.T) {
			tokens := map[string]string{
				"admin":   c.Token(t, signer),
				"hasemp":  c.TokenAs(t, pool, signer, "hasemp"),
				"payroll": c.TokenAs(t, pool, signer, "haspayrol"),
			}
			for who, tok := range tokens {
				row := fetch(t, kind, tok)
				want := map[string]string{
					"cedula": `"8-123-456"`, "ss_number": `"SS-99"`, "address": `"Calle 1"`,
					"emergency_contact": `"Ana"`, "emergency_phone": `"555-0202"`, "emergency_relation": `"Madre"`,
				}
				for f, v := range want {
					if string(row[f]) != v {
						t.Errorf("%s as %s: %s = %s, want %s", kind, who, f, row[f], v)
					}
				}
				for _, f := range []string{"salary", "hourly_rate"} {
					if s := string(row[f]); s == "null" || s == "" {
						t.Errorf("%s as %s: %s must be visible, got %q", kind, who, f, s)
					}
				}
			}
		})
	}
}

// rolesLookupFails delegates to the real pool but fails the GetRoleByName
// query with err, so only the role permission lookup errors while the
// employee rows still load.
type rolesLookupFails struct {
	db.DBTX
	err error
}

type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }

func (f rolesLookupFails) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "name: GetRoleByName") {
		return failedRow{f.err}
	}
	return f.DBTX.QueryRow(ctx, sql, args...)
}

// TestEmployees_ReadFailsLoudlyWhenThePermissionLookupFails pins that a
// failed role lookup is a 500, never a silently redacted 200: the PATCH is a
// full-column replace, so a permitted user saving a record that came back
// with null salary/cedula would overwrite the real values.
func TestEmployees_ReadFailsLoudlyWhenThePermissionLookupFails(t *testing.T) {
	pool := testutil.Pool(t)
	c := testutil.Company(t, pool, "EmpRedactFail")
	emp := testutil.Employee(t, pool, c, testutil.EmployeeSalary(1500))
	seedMedRole(t, pool, c, "hasempfail", `{"employees": true}`)

	signer := auth.NewSigner(testutil.TestSecret, time.Hour)
	var logs bytes.Buffer
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(&logs, nil)))
	token := c.TokenAs(t, pool, signer, "hasempfail")
	api.Queries = db.New(rolesLookupFails{pool, errors.New("roles lookup unavailable")})
	h := api.Routes(signer)

	for _, path := range []string{"/api/employees", "/api/employees/" + emp.ID} {
		rec := testutil.Do(t, h, http.MethodGet, path, token, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: expected 500 when the role lookup fails, got %d (body=%s)", path, rec.Code, rec.Body.String())
		}
		// The 500 comes from the employee read itself: the GET routes are
		// `open`, so no route gate looks the role up first.
		if !strings.Contains(rec.Body.String(), "could not check permissions") {
			t.Fatalf("%s: expected the permission-check error, got %s", path, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), emp.ID) || strings.Contains(rec.Body.String(), `"salary"`) {
			t.Fatalf("%s: the failed response must not carry employee data: %s", path, rec.Body.String())
		}
	}
	if !strings.Contains(logs.String(), "roles lookup unavailable") {
		t.Fatalf("the lookup failure must be logged, got log: %q", logs.String())
	}
}

// TestEmployees_CanceledPermissionLookupIsNotLoggedAsAnError pins that a
// client that goes away mid-request (context canceled) does not produce an
// error-level log entry, so real roles-lookup outages stay visible.
func TestEmployees_CanceledPermissionLookupIsNotLoggedAsAnError(t *testing.T) {
	pool := testutil.Pool(t)
	c := testutil.Company(t, pool, "EmpRedactCancel")
	testutil.Employee(t, pool, c, testutil.EmployeeSalary(1500))
	seedMedRole(t, pool, c, "hasempcancel", `{"employees": true}`)

	signer := auth.NewSigner(testutil.TestSecret, time.Hour)
	var logs bytes.Buffer
	api := handlers.New(pool, signer, slog.New(slog.NewTextHandler(&logs, nil)))
	token := c.TokenAs(t, pool, signer, "hasempcancel")
	api.Queries = db.New(rolesLookupFails{pool, context.Canceled})
	h := api.Routes(signer)

	rec := testutil.Do(t, h, http.MethodGet, "/api/employees", token, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("a canceled request must not log at error level, got: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "employee field permission lookup") {
		t.Fatalf("the canceled lookup should still be logged at a lower level, got: %q", logs.String())
	}
}
