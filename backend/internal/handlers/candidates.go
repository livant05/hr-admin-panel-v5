package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

const (
	maxCandidateName = 200
	// defaultNationality mirrors the candidates.nationality column default. The
	// column default cannot apply here because the insert always binds the
	// column, so the handler supplies it for an absent or null value on create.
	defaultNationality = "Panameña"
)

// candidateFields is the writable surface of a candidate. Like
// jobPostingFields it has NO id, company_id or created_at, so
// DisallowUnknownFields turns a body carrying any of them into a 400 and the
// tenant can never be bound from input (A1 rule 2).
type candidateFields struct {
	FullName    string      `json:"full_name"`
	Email       pgtype.Text `json:"email"`
	Phone       pgtype.Text `json:"phone"`
	Nationality pgtype.Text `json:"nationality"`
	IDNumber    pgtype.Text `json:"id_number"`
	ResumeNotes pgtype.Text `json:"resume_notes"`
}

// validateCandidate checks a full candidate and returns the sqlc params (minus
// company_id). Failures are reported per field.
func validateCandidate(f candidateFields) (db.CreateCandidateParams, map[string]string) {
	fields := map[string]string{}

	name := strings.TrimSpace(f.FullName)
	if name == "" || utf8.RuneCountInString(name) > maxCandidateName {
		fields["full_name"] = "required, max 200 characters"
	}

	return db.CreateCandidateParams{
		FullName:    name,
		Email:       f.Email,
		Phone:       f.Phone,
		Nationality: f.Nationality,
		IDNumber:    f.IDNumber,
		ResumeNotes: f.ResumeNotes,
	}, fields
}

// GET /api/candidates — filter id.
func (a *API) ListCandidates(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id")
	if !ok {
		return
	}
	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListCandidates(r.Context(), db.ListCandidatesParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		ID:        id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list candidates")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// GET /api/candidates/{id} — another company's id is 404.
func (a *API) GetCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetCandidate(r.Context(), db.GetCandidateParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get candidate")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// POST /api/candidates — 201 with a one-element array (A3).
func (a *API) CreateCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req candidateFields
	if !decodeStrictBody(w, r, &req) {
		return
	}
	if !req.Nationality.Valid {
		req.Nationality = pgtype.Text{String: defaultNationality, Valid: true}
	}
	params, fields := validateCandidate(req)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	params.CompanyID = companyID // $1 — from ctx, never from req

	row, err := a.Queries.CreateCandidate(r.Context(), params)
	if err != nil {
		a.writeDBErr(w, err, "create candidate")
		return
	}
	writeRows(w, http.StatusCreated, []db.Candidate{row})
}

// mergeCandidate overlays the sent fields on the stored row, so a PATCH is
// validated as the full candidate it will become. An explicit null clears an
// optional field, nationality included: the create default is not reapplied.
func mergeCandidate(cur db.Candidate, req candidateFields, sent map[string]json.RawMessage) candidateFields {
	merged := candidateFields{
		FullName:    cur.FullName,
		Email:       cur.Email,
		Phone:       cur.Phone,
		Nationality: cur.Nationality,
		IDNumber:    cur.IDNumber,
		ResumeNotes: cur.ResumeNotes,
	}

	has := func(k string) bool { _, ok := sent[k]; return ok }
	if has("full_name") {
		merged.FullName = req.FullName
	}
	if has("email") {
		merged.Email = req.Email
	}
	if has("phone") {
		merged.Phone = req.Phone
	}
	if has("nationality") {
		merged.Nationality = req.Nationality
	}
	if has("id_number") {
		merged.IDNumber = req.IDNumber
	}
	if has("resume_notes") {
		merged.ResumeNotes = req.ResumeNotes
	}
	return merged
}

// PATCH /api/candidates/{id} — partial update; returns a one-element array.
func (a *API) UpdateCandidate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req candidateFields
	sent, ok := decodeStrictPatch(w, r, &req)
	if !ok {
		return
	}

	tx, q, ok := a.beginWriteTx(w, r, "update candidate")
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())

	cur, err := q.GetCandidateForMerge(r.Context(), db.GetCandidateForMergeParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update candidate")
		return
	}

	v, fields := validateCandidate(mergeCandidate(cur, req, sent))
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := q.UpdateCandidate(r.Context(), db.UpdateCandidateParams{
		CompanyID:   companyID,
		ID:          id,
		FullName:    v.FullName,
		Email:       v.Email,
		Phone:       v.Phone,
		Nationality: v.Nationality,
		IDNumber:    v.IDNumber,
		ResumeNotes: v.ResumeNotes,
	})
	if err != nil {
		a.writeDBErr(w, err, "update candidate")
		return
	}
	if !a.commitWriteTx(w, r, tx, "update candidate") {
		return
	}
	writeRows(w, http.StatusOK, []db.Candidate{row})
}

// DELETE /api/candidates/{id} — 409 while any application still references the
// candidate (REC-4); see guardedDelete for the lock discipline.
func (a *API) DeleteCandidate(w http.ResponseWriter, r *http.Request) {
	a.deleteGuarded(w, r, guardedDelete{
		op: "delete candidate",
		lock: func(ctx context.Context, q *db.Queries, companyID, id pgtype.UUID) error {
			_, err := q.GetCandidateForUpdate(ctx, db.GetCandidateForUpdateParams{CompanyID: companyID, ID: id})
			return err
		},
		count: func(ctx context.Context, q *db.Queries, id pgtype.UUID) (int64, error) {
			return q.CountApplicationsForCandidate(ctx, id)
		},
		remove: func(ctx context.Context, q *db.Queries, companyID, id pgtype.UUID) (int64, error) {
			return q.DeleteCandidate(ctx, db.DeleteCandidateParams{CompanyID: companyID, ID: id})
		},
		conflict: func(n int64) string {
			return fmt.Sprintf("%d postulación(es) siguen asociadas a este candidato", n)
		},
	})
}
