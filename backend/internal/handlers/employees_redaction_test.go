package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

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
				row := fetch(t, kind, c.TokenAs(t, signer, role))
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
				"hasemp":  c.TokenAs(t, signer, "hasemp"),
				"payroll": c.TokenAs(t, signer, "haspayrol"),
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
