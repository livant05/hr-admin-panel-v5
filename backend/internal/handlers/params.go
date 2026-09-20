package handlers

import (
	"net/http"
	"strconv"
)

const (
	defaultLimit = 200
	maxLimit     = 1000
)

// listParams is the result of parsing a GET list request's query string (A4).
type listParams struct {
	Filters map[string]string
	Limit   int32
	Offset  int32
}

// parseList parses `_limit`, `_offset`, and rejects `_order` (unsupported in
// Phase 1 — sqlc cannot parameterize an identifier, see design P2.1 rule 6).
// Every other query parameter must be listed in allow; an unknown parameter
// is a 400, never silently ignored, because a silently-dropped filter is how
// a tenant scope leak or an unnoticed no-op filter happens (A4).
func parseList(w http.ResponseWriter, r *http.Request, allow ...string) (listParams, bool) {
	allowed := make(map[string]bool, len(allow))
	for _, a := range allow {
		allowed[a] = true
	}

	q := r.URL.Query()

	if q.Has("_order") {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "_order is not supported")
		return listParams{}, false
	}

	limit := int32(defaultLimit)
	if v := q.Get("_limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "_limit must be a positive integer")
			return listParams{}, false
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = int32(n)
	}

	offset := int32(0)
	if v := q.Get("_offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "_offset must be a non-negative integer")
			return listParams{}, false
		}
		offset = int32(n)
	}

	filters := make(map[string]string)
	for key, vals := range q {
		if key == "_limit" || key == "_offset" || key == "_order" {
			continue
		}
		if !allowed[key] {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "unsupported filter: "+key)
			return listParams{}, false
		}
		if len(vals) > 0 {
			filters[key] = vals[0]
		}
	}

	return listParams{Filters: filters, Limit: limit, Offset: offset}, true
}
