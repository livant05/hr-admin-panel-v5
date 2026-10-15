package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// Values of applications.stage (design D8); a new application starts applied.
const (
	stageApplied   = "applied"
	stageScreening = "screening"
	stageInterview = "interview"
	stageOffer     = "offer"
	stageHired     = "hired"
	stageRejected  = "rejected"

	dateLayout = "2006-01-02"
)

func applicationStageValid(s string) bool {
	switch s {
	case stageApplied, stageScreening, stageInterview, stageOffer, stageHired, stageRejected:
		return true
	}
	return false
}

// applicationFields is the writable surface of an application. Like the other
// recruitment resources it has NO id, company_id or created_at, so
// DisallowUnknownFields turns a body carrying any of them into a 400 and the
// tenant can never be bound from input (A1 rule 2). job_title and
// candidate_name are declared only because the frontend sends them: they are
// accepted and ignored, the server derives both from the referenced rows.
type applicationFields struct {
	JobPostingID  pgtype.Text     `json:"job_posting_id"`
	CandidateID   pgtype.Text     `json:"candidate_id"`
	JobTitle      json.RawMessage `json:"job_title"`
	CandidateName json.RawMessage `json:"candidate_name"`
	Stage         pgtype.Text     `json:"stage"`
	Notes         pgtype.Text     `json:"notes"`
	AppliedAt     pgtype.Text     `json:"applied_at"`
}

// requiredUUIDField parses a mandatory uuid body field into fields on failure.
func requiredUUIDField(fields map[string]string, name string, v pgtype.Text) pgtype.UUID {
	if !v.Valid || v.String == "" {
		fields[name] = "required"
		return pgtype.UUID{}
	}
	id, err := stringToUUID(v.String)
	if err != nil {
		fields[name] = "must be a uuid"
	}
	return id
}

// optionalDateField parses a nullable YYYY-MM-DD body field. Null or absent is
// an unset date (the column default applies on create).
func optionalDateField(fields map[string]string, name string, v pgtype.Text) pgtype.Date {
	if !v.Valid {
		return pgtype.Date{}
	}
	t, err := time.Parse(dateLayout, v.String)
	if err != nil {
		fields[name] = "must be a date as YYYY-MM-DD"
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// validateApplication checks a create body and returns the sqlc params (minus
// company_id). Failures are reported per field. Whether the posting and the
// candidate exist and belong to the caller is NOT decided here: that is the
// guarded insert, and it answers 404.
func validateApplication(f applicationFields) (db.CreateApplicationParams, map[string]string) {
	fields := map[string]string{}

	jobID := requiredUUIDField(fields, "job_posting_id", f.JobPostingID)
	candID := requiredUUIDField(fields, "candidate_id", f.CandidateID)

	stage := stageApplied
	if f.Stage.Valid {
		stage = f.Stage.String
	}
	if !applicationStageValid(stage) {
		fields["stage"] = "must be one of applied, screening, interview, offer, hired, rejected"
	}

	return db.CreateApplicationParams{
		JobPostingID: jobID,
		CandidateID:  candID,
		Stage:        stage,
		Notes:        f.Notes,
		AppliedAt:    optionalDateField(fields, "applied_at", f.AppliedAt),
	}, fields
}

// GET /api/applications — filters id, job_posting_id, candidate_id, stage.
func (a *API) ListApplications(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "job_posting_id", "candidate_id", "stage")
	if !ok {
		return
	}
	var uuids [3]pgtype.UUID
	for i, name := range []string{"id", "job_posting_id", "candidate_id"} {
		id, err := optionalUUIDFilter(p.Filters[name])
		if err != nil {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid "+name)
			return
		}
		uuids[i] = id
	}
	// A present-but-empty ?stage= is rejected like any other bad value; only
	// an absent parameter means "no stage filter".
	if s, present := p.Filters["stage"]; present && !applicationStageValid(s) {
		writeFieldErr(w, map[string]string{"stage": "must be one of applied, screening, interview, offer, hired, rejected"})
		return
	}

	rows, err := a.Queries.ListApplications(r.Context(), db.ListApplicationsParams{
		CompanyID:    companyID, // $1 — from ctx, never from a query param
		ID:           uuids[0],
		JobPostingID: uuids[1],
		CandidateID:  uuids[2],
		Stage:        optionalTextFilter(p.Filters["stage"]),
		Limit:        p.Limit,
		Offset:       p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list applications")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// GET /api/applications/{id} — another company's id is 404.
func (a *API) GetApplication(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetApplication(r.Context(), db.GetApplicationParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get application")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// POST /api/applications — 201 with a one-element array (A3). A posting or
// candidate of another company, or one that does not exist, is 404 and nothing
// is written (REC-2): the guarded insert (CreateApplication) is the check.
func (a *API) CreateApplication(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req applicationFields
	if !decodeRecruitmentBody(w, r, &req) {
		return
	}
	params, fields := validateApplication(req)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	params.CompanyID = companyID // from ctx, never from req

	row, err := a.Queries.CreateApplication(r.Context(), params)
	if err != nil {
		a.writeApplicationErr(w, err, "create application")
		return
	}
	writeRows(w, http.StatusCreated, []db.Application{row})
}

// validateApplicationPatch checks the fields a PATCH sent. Absent keeps the
// stored value; null is only meaningful for notes (it clears them): the foreign
// keys, the stage and the date cannot be cleared.
func validateApplicationPatch(req applicationFields, sent map[string]json.RawMessage) (jobID, candID pgtype.UUID, stage string, appliedAt pgtype.Date, fields map[string]string) {
	fields = map[string]string{}
	isNull := func(k string) bool { return bytes.Equal(bytes.TrimSpace(sent[k]), []byte("null")) }
	has := func(k string) bool { _, ok := sent[k]; return ok }

	for _, k := range []string{"job_posting_id", "candidate_id", "stage", "applied_at"} {
		if has(k) && isNull(k) {
			fields[k] = "cannot be null"
		}
	}
	if has("job_posting_id") && !isNull("job_posting_id") {
		jobID = requiredUUIDField(fields, "job_posting_id", req.JobPostingID)
	}
	if has("candidate_id") && !isNull("candidate_id") {
		candID = requiredUUIDField(fields, "candidate_id", req.CandidateID)
	}
	if has("stage") && !isNull("stage") {
		stage = req.Stage.String
		if !applicationStageValid(stage) {
			fields["stage"] = "must be one of applied, screening, interview, offer, hired, rejected"
		}
	}
	if has("applied_at") && !isNull("applied_at") {
		appliedAt = optionalDateField(fields, "applied_at", req.AppliedAt)
	}
	return jobID, candID, stage, appliedAt, fields
}

// PATCH /api/applications/{id} — partial update; returns a one-element array.
// Re-pointing job_posting_id or candidate_id to a row of another company (or to
// one that does not exist) is 404 and the application is left unchanged; the new
// target is locked FOR KEY SHARE for the rest of the transaction, so a guarded
// delete of it waits and then counts this application (REC-2).
func (a *API) UpdateApplication(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req applicationFields
	sent, ok := decodeRecruitmentPatch(w, r, &req)
	if !ok {
		return
	}

	tx, q, ok := a.beginRecruitmentTx(w, r, "update application")
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())

	cur, err := q.GetApplicationForMerge(r.Context(), db.GetApplicationForMergeParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update application")
		return
	}

	jobID, candID, stage, appliedAt, fields := validateApplicationPatch(req, sent)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	params := db.UpdateApplicationParams{
		CompanyID:     companyID,
		ID:            id,
		JobPostingID:  cur.JobPostingID,
		JobTitle:      cur.JobTitle,
		CandidateID:   cur.CandidateID,
		CandidateName: cur.CandidateName,
		Stage:         cur.Stage,
		Notes:         cur.Notes,
		AppliedAt:     cur.AppliedAt,
	}
	if _, has := sent["notes"]; has {
		params.Notes = req.Notes
	}
	if stage != "" {
		params.Stage = pgtype.Text{String: stage, Valid: true}
	}
	if appliedAt.Valid {
		params.AppliedAt = appliedAt
	}
	// A foreign key that is sent unchanged keeps the stored, create-time names;
	// one that moves is checked, locked and re-derived from the new row.
	if jobID.Valid && jobID != cur.JobPostingID {
		jp, err := q.LockJobPostingForShare(r.Context(), db.LockJobPostingForShareParams{CompanyID: companyID, ID: jobID})
		if err != nil {
			a.writeDBErr(w, err, "update application")
			return
		}
		params.JobPostingID = jp.ID
		params.JobTitle = pgtype.Text{String: jp.Title, Valid: true}
	}
	if candID.Valid && candID != cur.CandidateID {
		c, err := q.LockCandidateForShare(r.Context(), db.LockCandidateForShareParams{CompanyID: companyID, ID: candID})
		if err != nil {
			a.writeDBErr(w, err, "update application")
			return
		}
		params.CandidateID = c.ID
		params.CandidateName = pgtype.Text{String: c.FullName, Valid: true}
	}

	row, err := q.UpdateApplication(r.Context(), params)
	if err != nil {
		a.writeApplicationErr(w, err, "update application")
		return
	}
	if !a.commitRecruitmentTx(w, r, tx, "update application") {
		return
	}
	writeRows(w, http.StatusOK, []db.Application{row})
}

// DELETE /api/applications/{id} — hard delete (204). Nothing references an
// application, so no guard is needed; another company's id is 404.
func (a *API) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	n, err := a.Queries.DeleteApplication(r.Context(), db.DeleteApplicationParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete application")
		return
	}
	if n == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeApplicationErr is writeDBErr plus the one-application-per-pair rule
// (migration 0015): that unique violation gets a Spanish message the panel
// shows as-is, like the other recruitment conflicts.
func (a *API) writeApplicationErr(w http.ResponseWriter, err error, op string) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "uq_applications_posting_candidate" {
		writeErrCode(w, http.StatusConflict, codeConflict, "El candidato ya tiene una postulación para esta vacante")
		return
	}
	a.writeDBErr(w, err, op)
}
