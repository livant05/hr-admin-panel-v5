package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestParseList pins the design's own Testing Strategy row for A4's
// parseList: unknown query parameter -> 400, `_limit` clamp at maxLimit, and
// `_order` -> 400. Table-driven, no DB (design phase1-design "Testing
// Strategy" | "Unit | A4 parseList ... | Table-driven").
func TestParseList(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		allow      []string
		wantOK     bool
		wantCode   string
		wantLimit  int32
		checkLimit bool
	}{
		{
			name:     "unknown query parameter is rejected",
			query:    "?bogus=1",
			allow:    []string{"id"},
			wantOK:   false,
			wantCode: codeValidation,
		},
		{
			name:     "_order is rejected as unsupported",
			query:    "?_order=name.asc",
			allow:    []string{"id"},
			wantOK:   false,
			wantCode: codeValidation,
		},
		{
			name:       "_limit clamps at maxLimit",
			query:      "?_limit=999999",
			allow:      []string{"id"},
			wantOK:     true,
			checkLimit: true,
			wantLimit:  maxLimit,
		},
		{
			name:       "_limit under maxLimit is used as-is",
			query:      "?_limit=5",
			allow:      []string{"id"},
			wantOK:     true,
			checkLimit: true,
			wantLimit:  5,
		},
		{
			name:     "_limit that is not a positive integer is rejected",
			query:    "?_limit=0",
			allow:    []string{"id"},
			wantOK:   false,
			wantCode: codeValidation,
		},
		{
			name:   "an allowed filter passes through",
			query:  "?id=some-id",
			allow:  []string{"id"},
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/departments"+tc.query, nil)
			rec := httptest.NewRecorder()

			p, ok := parseList(rec, req, tc.allow...)
			if ok != tc.wantOK {
				t.Fatalf("expected ok=%v, got %v (body=%s)", tc.wantOK, ok, rec.Body.String())
			}

			if !tc.wantOK {
				if rec.Code != 400 {
					t.Fatalf("expected 400, got %d", rec.Code)
				}
				if code := errCodeFromBody(t, rec); code != tc.wantCode {
					t.Fatalf("expected error.code=%s, got %q", tc.wantCode, code)
				}
				return
			}

			if tc.checkLimit && p.Limit != tc.wantLimit {
				t.Fatalf("expected Limit=%d, got %d", tc.wantLimit, p.Limit)
			}
		})
	}
}

// errCodeFromBody extracts error.code (A2) from a recorded response body.
func errCodeFromBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error errBody `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body (body=%s): %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// TestParseListOrdered pins the per-endpoint order whitelist (design D9):
// parseList keeps rejecting `_order` outright, while an endpoint that passes an
// explicit whitelist accepts exactly those values and nothing else.
func TestParseListOrdered(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		orders    []string
		allow     []string
		wantOK    bool
		wantOrder string
		wantField string // a filter that must come through
	}{
		{name: "no _order returns the empty order", query: "?id=x", orders: []string{"created_at.desc"}, allow: []string{"id"}, wantOK: true, wantOrder: "", wantField: "id"},
		{name: "a whitelisted _order is returned", query: "?_order=created_at.desc", orders: []string{"created_at.desc"}, allow: []string{"id"}, wantOK: true, wantOrder: "created_at.desc"},
		{name: "the whitelist may hold several values", query: "?_order=date.asc", orders: []string{"created_at.desc", "date.asc"}, allow: nil, wantOK: true, wantOrder: "date.asc"},
		{name: "another direction is rejected", query: "?_order=created_at.asc", orders: []string{"created_at.desc"}, allow: nil, wantOK: false},
		{name: "another column is rejected", query: "?_order=date.desc", orders: []string{"created_at.desc"}, allow: nil, wantOK: false},
		{name: "an empty _order is rejected", query: "?_order=", orders: []string{"created_at.desc"}, allow: nil, wantOK: false},
		{name: "nil whitelist rejects every _order", query: "?_order=created_at.desc", orders: nil, allow: nil, wantOK: false},
		{name: "unknown parameter is still rejected", query: "?bogus=1&_order=created_at.desc", orders: []string{"created_at.desc"}, allow: nil, wantOK: false},
		{name: "_limit rules still apply", query: "?_limit=0&_order=created_at.desc", orders: []string{"created_at.desc"}, allow: nil, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/attendance_logs"+tc.query, nil)
			rec := httptest.NewRecorder()

			p, order, ok := parseListOrdered(rec, req, tc.orders, tc.allow...)
			if ok != tc.wantOK {
				t.Fatalf("expected ok=%v, got %v (body=%s)", tc.wantOK, ok, rec.Body.String())
			}
			if !tc.wantOK {
				if rec.Code != 400 {
					t.Fatalf("expected 400, got %d", rec.Code)
				}
				if code := errCodeFromBody(t, rec); code != codeValidation {
					t.Fatalf("expected error.code=%s, got %q", codeValidation, code)
				}
				return
			}
			if order != tc.wantOrder {
				t.Fatalf("order = %q, want %q", order, tc.wantOrder)
			}
			if tc.wantField != "" && p.Filters[tc.wantField] != "x" {
				t.Fatalf("filter %q lost: %v", tc.wantField, p.Filters)
			}
		})
	}
}
