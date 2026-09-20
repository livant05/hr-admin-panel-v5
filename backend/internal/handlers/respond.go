package handlers

import "net/http"

// A2 error codes — every error response nests one of these under "error.code".
const (
	codeBadRequest   = "bad_request"
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
	codeNotFound     = "not_found"
	codeConflict     = "conflict"
	codeValidation   = "validation_failed"
	codeInternal     = "internal"
)

type errBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// writeErrCode writes the shared A2 error envelope: {"error":{"code","message"}}.
func writeErrCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]errBody{"error": {Code: code, Message: msg}})
}

// writeFieldErr writes a 400 validation_failed error carrying per-field
// messages, e.g. {"error":{"code":"validation_failed","fields":{"name":"required"}}}.
func writeFieldErr(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusBadRequest, map[string]errBody{
		"error": {Code: codeValidation, Message: "validation failed", Fields: fields},
	})
}

// writeRows always emits a JSON array, never null, for empty results (A3).
// A nil Go slice marshals to `null`, and every frontend call site does
// Array.isArray(rows) — which is false for null — so an empty list must
// still render as `[]`.
func writeRows[T any](w http.ResponseWriter, status int, rows []T) {
	if rows == nil {
		rows = []T{}
	}
	writeJSON(w, status, rows)
}
