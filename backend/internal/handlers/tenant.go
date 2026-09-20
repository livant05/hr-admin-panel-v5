package handlers

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/livant05/rrhh-go/internal/auth"
)

// tenant resolves the JWT company_id as a UUID (A1). It writes the 401
// envelope and returns ok=false on failure. No handler may re-derive the
// tenant by any other means (body, query, path) — this is the single
// source of truth that replaces Supabase's RLS.
func (a *API) tenant(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	cid, err := stringToUUID(auth.CompanyIDFromContext(r.Context()))
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, codeUnauthorized, "invalid token company")
		return pgtype.UUID{}, false
	}
	return cid, true
}
