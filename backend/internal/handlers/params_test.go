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
