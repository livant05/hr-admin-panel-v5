package handlers_test

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/testutil"
)

const zeroUUID = "00000000-0000-0000-0000-000000000001"

// wantRouteClasses is the reviewed authorization map: every protected route
// and how it is gated. Adding a route to the router without adding it here
// (or changing a gate without updating it) fails
// TestRoutes_EveryProtectedRouteIsClassified.
//
//	open      any authenticated user
//	handler   authorization enforced inside the handler (admin / medical)
//	perm:a|b  route wrapper requires at least one of the module permissions
var wantRouteClasses = map[string]string{
	"GET /api/me": "open",

	"GET /api/departments":         "open",
	"POST /api/departments":        "perm:settings",
	"GET /api/departments/{id}":    "open",
	"PATCH /api/departments/{id}":  "perm:settings",
	"DELETE /api/departments/{id}": "perm:settings",
	"GET /api/positions":           "open",
	"POST /api/positions":          "perm:settings",
	"GET /api/positions/{id}":      "open",
	"PATCH /api/positions/{id}":    "perm:settings",
	"DELETE /api/positions/{id}":   "perm:settings",
	"GET /api/branches":            "open",
	"POST /api/branches":           "perm:settings",
	"GET /api/branches/{id}":       "open",
	"PATCH /api/branches/{id}":     "perm:settings",
	"DELETE /api/branches/{id}":    "perm:settings",

	"GET /api/roles":         "open",
	"POST /api/roles":        "handler",
	"GET /api/roles/{id}":    "open",
	"PATCH /api/roles/{id}":  "handler",
	"DELETE /api/roles/{id}": "handler",

	"GET /api/users":         "handler",
	"POST /api/users":        "handler",
	"GET /api/users/{id}":    "handler",
	"PATCH /api/users/{id}":  "handler",
	"DELETE /api/users/{id}": "handler",

	"GET /api/companies":      "open",
	"GET /api/companies/{id}": "open",

	"GET /api/zkteco_devices":             "open",
	"PATCH /api/zkteco_devices/{id}":      "open",
	"GET /api/zkteco_devices/unclaimed":   "handler",
	"POST /api/zkteco_devices/{id}/claim": "handler",
	"GET /api/zkteco_unknown_pins":        "handler",

	"GET /api/employees":         "open",
	"POST /api/employees":        "perm:employees",
	"GET /api/employees/{id}":    "open",
	"PATCH /api/employees/{id}":  "perm:employees",
	"DELETE /api/employees/{id}": "perm:employees",

	"GET /api/evaluations":         "perm:employees",
	"POST /api/evaluations":        "perm:employees",
	"GET /api/evaluations/{id}":    "perm:employees",
	"DELETE /api/evaluations/{id}": "perm:employees",
	"GET /api/uniforms":            "perm:employees",
	"POST /api/uniforms":           "perm:employees",
	"GET /api/uniforms/{id}":       "perm:employees",
	"PATCH /api/uniforms/{id}":     "perm:employees",
	"DELETE /api/uniforms/{id}":    "perm:employees",

	"GET /api/attendance_logs":         "perm:attendance",
	"POST /api/attendance_logs":        "perm:attendance",
	"GET /api/attendance_logs/{id}":    "perm:attendance",
	"PATCH /api/attendance_logs/{id}":  "perm:attendance",
	"DELETE /api/attendance_logs/{id}": "perm:attendance",

	"GET /api/leave_balances":         "perm:vacations",
	"GET /api/leave_balances/{id}":    "perm:vacations",
	"PATCH /api/leave_balances/{id}":  "perm:vacations",
	"POST /api/leave_balances/accrue": "perm:vacations",
	"GET /api/leave_requests":         "perm:vacations",
	"POST /api/leave_requests":        "perm:vacations",
	"GET /api/leave_requests/{id}":    "perm:vacations",
	"PATCH /api/leave_requests/{id}":  "perm:vacations",
	"DELETE /api/leave_requests/{id}": "perm:vacations",

	"GET /api/medical_records":         "handler",
	"POST /api/medical_records":        "handler",
	"GET /api/medical_records/{id}":    "handler",
	"PATCH /api/medical_records/{id}":  "handler",
	"DELETE /api/medical_records/{id}": "handler",

	"GET /api/overtime_logs":         "perm:overtime",
	"POST /api/overtime_logs":        "perm:overtime",
	"GET /api/overtime_logs/{id}":    "perm:overtime",
	"PATCH /api/overtime_logs/{id}":  "perm:overtime",
	"DELETE /api/overtime_logs/{id}": "perm:overtime",

	"GET /api/deductions":         "perm:deductions",
	"POST /api/deductions":        "perm:deductions",
	"GET /api/deductions/{id}":    "perm:deductions",
	"PATCH /api/deductions/{id}":  "perm:deductions",
	"DELETE /api/deductions/{id}": "perm:deductions",

	"GET /api/payroll_history":           "perm:payroll",
	"GET /api/payroll_history/calculate": "perm:payroll",
	"POST /api/payroll_history":          "perm:payroll",
	"GET /api/payroll_history/{id}":      "perm:payroll",
	"DELETE /api/payroll_history/{id}":   "perm:payroll",

	"GET /api/liquidation_history":           "perm:liquidation",
	"GET /api/liquidation_history/calculate": "perm:liquidation",
	"POST /api/liquidation_history":          "perm:liquidation",
	"GET /api/liquidation_history/{id}":      "perm:liquidation",
	"DELETE /api/liquidation_history/{id}":   "perm:liquidation",

	"GET /api/employee_pay_records":         "perm:payroll|liquidation",
	"GET /api/employee_pay_records/bases":   "perm:payroll|liquidation",
	"GET /api/employee_pay_records/{id}":    "perm:payroll|liquidation",
	"POST /api/employee_pay_records":        "perm:liquidation",
	"PATCH /api/employee_pay_records/{id}":  "perm:liquidation",
	"DELETE /api/employee_pay_records/{id}": "perm:liquidation",

	"GET /api/document_templates":         "perm:documents",
	"POST /api/document_templates":        "perm:documents",
	"GET /api/document_templates/{id}":    "perm:documents",
	"PATCH /api/document_templates/{id}":  "perm:documents",
	"DELETE /api/document_templates/{id}": "perm:documents",
	"GET /api/generated_documents":        "perm:documents",
	"POST /api/generated_documents":       "perm:documents",
	"GET /api/generated_documents/{id}":   "perm:documents",

	"GET /api/job_postings":         "perm:recruitment",
	"POST /api/job_postings":        "perm:recruitment",
	"GET /api/job_postings/{id}":    "perm:recruitment",
	"PATCH /api/job_postings/{id}":  "perm:recruitment",
	"DELETE /api/job_postings/{id}": "perm:recruitment",
	"GET /api/candidates":           "perm:recruitment",
	"POST /api/candidates":          "perm:recruitment",
	"GET /api/candidates/{id}":      "perm:recruitment",
	"PATCH /api/candidates/{id}":    "perm:recruitment",
	"DELETE /api/candidates/{id}":   "perm:recruitment",
	"GET /api/applications":         "perm:recruitment",
	"POST /api/applications":        "perm:recruitment",
	"GET /api/applications/{id}":    "perm:recruitment",
	"PATCH /api/applications/{id}":  "perm:recruitment",
	"DELETE /api/applications/{id}": "perm:recruitment",
}

// TestRoutes_EveryProtectedRouteIsClassified fails when a protected route is
// registered without a reviewed entry in wantRouteClasses, when an entry no
// longer matches a registered route, or when a gate drifts from the map.
func TestRoutes_EveryProtectedRouteIsClassified(t *testing.T) {
	got := map[string]string{}
	for _, rg := range handlers.ProtectedRouteGates() {
		if _, dup := got[rg.Pattern]; dup {
			t.Errorf("route %q registered twice", rg.Pattern)
		}
		got[rg.Pattern] = rg.Class
	}

	var problems []string
	for pattern, class := range got {
		want, ok := wantRouteClasses[pattern]
		switch {
		case class == "unclassified":
			problems = append(problems, "unclassified route: "+pattern)
		case !ok:
			problems = append(problems, "route missing from the reviewed map: "+pattern+" ("+class+")")
		case want != class:
			problems = append(problems, "gate drift on "+pattern+": registered "+class+", map says "+want)
		}
	}
	for pattern := range wantRouteClasses {
		if _, ok := got[pattern]; !ok {
			problems = append(problems, "map entry without a registered route: "+pattern)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("route classification mismatch:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestRoutes_ModulePermissionGate drives every permission-gated route of the
// reviewed map (kept equal to the registry by the test above) through
// the real router: a non-admin role without the module key gets 403 with the
// forbidden envelope, a role holding any one of the accepted keys is let
// through to the handler (anything but 403), and admin always passes.
func TestRoutes_ModulePermissionGate(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)
	c := testutil.Company(t, pool, "ModuleGate")

	seed := func(name, permissions string) {
		t.Helper()
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO roles (company_id, name, permissions) VALUES ($1, $2, $3::jsonb)`,
			c.CompanyID, name, permissions); err != nil {
			t.Fatalf("seed role %s: %v", name, err)
		}
	}
	// A role holding other modules and the target key set to false.
	seed("elsewhere", `{"dashboard": true, "medical": true, "settings": false, "employees": false}`)
	keys := map[string]bool{}
	for _, class := range wantRouteClasses {
		if k, ok := strings.CutPrefix(class, "perm:"); ok {
			for _, key := range strings.Split(k, "|") {
				keys[key] = true
			}
		}
	}
	for key := range keys {
		seed("has_"+key, `{"`+key+`": true}`)
	}
	admin := c.Token(t, signer)
	elsewhere := c.TokenAs(t, pool, signer, "elsewhere")
	norole := c.TokenAs(t, pool, signer, "ghost")

	for route, class := range wantRouteClasses {
		list, ok := strings.CutPrefix(class, "perm:")
		if !ok {
			continue
		}
		method, pattern, _ := strings.Cut(route, " ")
		path := strings.ReplaceAll(pattern, "{id}", zeroUUID)
		accepted := strings.Split(list, "|")

		t.Run(route, func(t *testing.T) {
			for label, tok := range map[string]string{"role without the key": elsewhere, "role with no row": norole} {
				rec := testutil.Do(t, h, method, path, tok, nil)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s: expected 403, got %d (body=%s)", label, rec.Code, rec.Body.String())
				}
				if code := testutil.ErrCode(t, rec); code != "forbidden" {
					t.Fatalf("%s: expected error.code=forbidden, got %q", label, code)
				}
			}
			// The manual accrual trigger needs a wired scheduler (the plain
			// test server has none); its allowed path is covered by the
			// leave_balances accrue tests.
			if strings.HasSuffix(path, "/accrue") {
				return
			}
			if rec := testutil.Do(t, h, method, path, admin, nil); rec.Code == http.StatusForbidden {
				t.Fatalf("admin must pass the gate, got 403 (body=%s)", rec.Body.String())
			}
			for _, key := range accepted {
				tok := c.TokenAs(t, pool, signer, "has_"+key)
				if rec := testutil.Do(t, h, method, path, tok, nil); rec.Code == http.StatusForbidden {
					t.Fatalf("role with %q must pass the gate, got 403 (body=%s)", key, rec.Body.String())
				}
			}
		})
	}
}
