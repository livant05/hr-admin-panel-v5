package handlers

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// writeDBErr maps a pgx/Postgres error to the shared A2 envelope. pgx.ErrNoRows
// is deliberately mapped to 404 for both "does not exist" and "exists but
// belongs to another tenant" — returning 403 for the latter would disclose
// that the row exists (A1).
func (a *API) writeDBErr(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505": // unique_violation
			writeErrCode(w, http.StatusConflict, codeConflict, "already exists")
			return
		case "23503": // foreign_key_violation
			writeErrCode(w, http.StatusConflict, codeConflict, "referenced record missing")
			return
		case "23514": // check_violation
			writeFieldErr(w, map[string]string{"": "constraint violated"})
			return
		}
	}

	a.Log.Error(op, "err", err)
	writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
}
