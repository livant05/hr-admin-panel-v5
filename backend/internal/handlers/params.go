package handlers

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
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
	p, _, ok := parseListOrdered(w, r, nil, allow...)
	return p, ok
}

// parseListOrdered is parseList for an endpoint that offers a fixed set of
// `_order` values (design D9). orders is the whitelist, spelled exactly as the
// client sends it (e.g. "created_at.desc"); the accepted value is returned
// verbatim so the handler can pick the matching sqlc query. An absent `_order`
// returns "". Any other value, including an empty one, is a 400, and a nil or
// empty whitelist rejects every `_order`, which is what keeps all other
// endpoints on parseList's behavior.
func parseListOrdered(w http.ResponseWriter, r *http.Request, orders []string, allow ...string) (listParams, string, bool) {
	allowed := make(map[string]bool, len(allow))
	for _, a := range allow {
		allowed[a] = true
	}

	q := r.URL.Query()

	order := ""
	if q.Has("_order") {
		order = q.Get("_order")
		if !slices.Contains(orders, order) {
			msg := "_order is not supported"
			if len(orders) > 0 {
				msg = "_order must be one of: " + strings.Join(orders, ", ")
			}
			writeErrCode(w, http.StatusBadRequest, codeValidation, msg)
			return listParams{}, "", false
		}
	}

	limit := int32(defaultLimit)
	if v := q.Get("_limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "_limit must be a positive integer")
			return listParams{}, "", false
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
			return listParams{}, "", false
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
			return listParams{}, "", false
		}
		if len(vals) > 0 {
			filters[key] = vals[0]
		}
	}

	return listParams{Filters: filters, Limit: limit, Offset: offset}, order, true
}
