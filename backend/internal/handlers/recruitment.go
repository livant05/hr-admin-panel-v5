package handlers

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// Shared plumbing of the recruitment resources. The body decoders
// (decodeRecruitmentBody, decodeRecruitmentPatch) live in job_postings.go.

// beginRecruitmentTx opens the transaction of a recruitment write and writes
// the 500 itself when it cannot. The caller defers the returned rollback.
func (a *API) beginRecruitmentTx(w http.ResponseWriter, r *http.Request, op string) (pgx.Tx, *db.Queries, bool) {
	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error(op+" begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return nil, nil, false
	}
	return tx, a.Queries.WithTx(tx), true
}

// commitRecruitmentTx commits and writes the 500 itself when it cannot.
func (a *API) commitRecruitmentTx(w http.ResponseWriter, r *http.Request, tx pgx.Tx, op string) bool {
	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error(op+" commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return false
	}
	return true
}

// guardedDelete describes a recruitment row that applications reference with
// ON DELETE CASCADE. The schema cannot express RESTRICT, so the delete itself is
// guarded (REC-4): while any application still points at the row the answer is
// 409 and nothing is removed, so the cascade never fires through the API.
//
// The lock discipline is part of the contract and shared on purpose: the row is
// locked FOR UPDATE first. An application insert in flight holds a key-share lock
// on it, so the delete waits for that insert and then counts it instead of
// cascading it away. PATCH uses the weaker FOR NO KEY UPDATE and never blocks an
// insert, but still serializes with this delete.
type guardedDelete struct {
	op       string // log context, e.g. "delete candidate"
	lock     func(ctx context.Context, q *db.Queries, companyID, id pgtype.UUID) error
	count    func(ctx context.Context, q *db.Queries, id pgtype.UUID) (int64, error)
	remove   func(ctx context.Context, q *db.Queries, companyID, id pgtype.UUID) (int64, error)
	conflict func(n int64) string
}

// deleteGuarded runs the whole DELETE /{id} flow for g: tenant, id, lock, count,
// 409 or delete, 204.
func (a *API) deleteGuarded(w http.ResponseWriter, r *http.Request, g guardedDelete) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	tx, q, ok := a.beginRecruitmentTx(w, r, g.op)
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())

	if err := g.lock(r.Context(), q, companyID, id); err != nil {
		a.writeDBErr(w, err, g.op)
		return
	}
	n, err := g.count(r.Context(), q, id)
	if err != nil {
		a.writeDBErr(w, err, g.op)
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict, g.conflict(n))
		return
	}

	affected, err := g.remove(r.Context(), q, companyID, id)
	if err != nil {
		a.writeDBErr(w, err, g.op)
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	if !a.commitRecruitmentTx(w, r, tx, g.op) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
