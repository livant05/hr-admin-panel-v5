package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// Values of job_postings.status (design D8); the column default is open.
const (
	jobStatusOpen   = "open"
	jobStatusPaused = "paused"
	jobStatusClosed = "closed"

	maxJobTitle = 200
	// maxRecruitmentBody bounds a recruitment write body (1 MiB, the same cap
	// the other tenant resources use).
	maxRecruitmentBody = 1 << 20
)

// jobPostingFields is the writable surface of a job posting. It deliberately
// has NO id, company_id or created_at: DisallowUnknownFields turns a body that
// carries any of them into a 400, so the tenant can never be bound from input
// (A1 rule 2). The salaries stay json.RawMessage so they are bounded and parsed
// by hand (parseUniformValue), never handed to a big-number parser.
type jobPostingFields struct {
	Title        string          `json:"title"`
	Department   pgtype.Text     `json:"department"`
	Location     pgtype.Text     `json:"location"`
	Description  pgtype.Text     `json:"description"`
	Requirements pgtype.Text     `json:"requirements"`
	SalaryMin    json.RawMessage `json:"salary_min"`
	SalaryMax    json.RawMessage `json:"salary_max"`
	Status       pgtype.Text     `json:"status"`
}

// decodeRecruitmentBody reads exactly one JSON value, rejects unknown fields
// and writes the 400 itself. Shared by the recruitment resources.
func decodeRecruitmentBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRecruitmentBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	return true
}

func jobStatusValid(s string) bool {
	return s == jobStatusOpen || s == jobStatusPaused || s == jobStatusClosed
}

// optionalSalary parses a nullable salary: absent or JSON null is NULL,
// anything else must be a number in [0, 99999999.99] (NUMERIC(10,2)).
func optionalSalary(raw json.RawMessage) (pgtype.Numeric, bool) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || string(t) == "null" {
		return pgtype.Numeric{}, true
	}
	return parseUniformValue(raw)
}

// validateJobPosting checks a full create body and returns the sqlc params
// (minus company_id). Failures are reported per field.
func validateJobPosting(f jobPostingFields) (db.CreateJobPostingParams, map[string]string) {
	fields := map[string]string{}

	title := strings.TrimSpace(f.Title)
	if title == "" || utf8.RuneCountInString(title) > maxJobTitle {
		fields["title"] = "required, max 200 characters"
	}

	status := jobStatusOpen
	if f.Status.Valid {
		status = f.Status.String
	}
	if !jobStatusValid(status) {
		fields["status"] = "must be one of open, paused, closed"
	}

	min, minOK := optionalSalary(f.SalaryMin)
	if !minOK {
		fields["salary_min"] = "must be a number between 0 and 99999999.99"
	}
	max, maxOK := optionalSalary(f.SalaryMax)
	if !maxOK {
		fields["salary_max"] = "must be a number between 0 and 99999999.99"
	}
	if minOK && maxOK && min.Valid && max.Valid && salaryLess(max, min) {
		fields["salary_max"] = "must be greater than or equal to salary_min"
	}

	return db.CreateJobPostingParams{
		Title:        title,
		Department:   f.Department,
		Location:     f.Location,
		Description:  f.Description,
		Requirements: f.Requirements,
		SalaryMin:    min,
		SalaryMax:    max,
		Status:       pgtype.Text{String: status, Valid: true},
	}, fields
}

// salaryLess reports a < b for two valid, already range-checked numerics.
func salaryLess(a, b pgtype.Numeric) bool {
	af, aerr := a.Float64Value()
	bf, berr := b.Float64Value()
	return aerr == nil && berr == nil && af.Float64 < bf.Float64
}

// GET /api/job_postings — filters id, status.
func (a *API) ListJobPostings(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "status")
	if !ok {
		return
	}
	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	rows, err := a.Queries.ListJobPostings(r.Context(), db.ListJobPostingsParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		ID:        id,
		Status:    optionalTextFilter(p.Filters["status"]),
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list job postings")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// GET /api/job_postings/{id} — another company's id is 404.
func (a *API) GetJobPosting(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetJobPosting(r.Context(), db.GetJobPostingParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get job posting")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// POST /api/job_postings — 201 with a one-element array (A3).
func (a *API) CreateJobPosting(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req jobPostingFields
	if !decodeRecruitmentBody(w, r, &req) {
		return
	}
	params, fields := validateJobPosting(req)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	params.CompanyID = companyID // $1 — from ctx, never from req

	row, err := a.Queries.CreateJobPosting(r.Context(), params)
	if err != nil {
		a.writeDBErr(w, err, "create job posting")
		return
	}
	writeRows(w, http.StatusCreated, []db.JobPosting{row})
}

// decodeRecruitmentPatch is decodeRecruitmentBody plus the set of keys the
// client actually sent, so an absent field (keep) can be told from an explicit
// null (clear). Unknown fields are still a 400.
func decodeRecruitmentPatch(w http.ResponseWriter, r *http.Request, dst any) (map[string]json.RawMessage, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecruitmentBody))
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return nil, false
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sent); err != nil || sent == nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return nil, false
	}
	return sent, true
}

// mergeJobPosting overlays the sent fields on the stored row, so a PATCH is
// validated as the full posting it will become (the salary ordering rule
// holds against a stored salary the body did not repeat).
func mergeJobPosting(cur db.JobPosting, req jobPostingFields, sent map[string]json.RawMessage) jobPostingFields {
	merged := jobPostingFields{
		Title:        cur.Title,
		Department:   cur.Department,
		Location:     cur.Location,
		Description:  cur.Description,
		Requirements: cur.Requirements,
		Status:       cur.Status,
	}
	merged.SalaryMin, _ = json.Marshal(cur.SalaryMin)
	merged.SalaryMax, _ = json.Marshal(cur.SalaryMax)

	has := func(k string) bool { _, ok := sent[k]; return ok }
	if has("title") {
		merged.Title = req.Title
	}
	if has("department") {
		merged.Department = req.Department
	}
	if has("location") {
		merged.Location = req.Location
	}
	if has("description") {
		merged.Description = req.Description
	}
	if has("requirements") {
		merged.Requirements = req.Requirements
	}
	if has("salary_min") {
		merged.SalaryMin = req.SalaryMin
	}
	if has("salary_max") {
		merged.SalaryMax = req.SalaryMax
	}
	if has("status") {
		merged.Status = req.Status
		if !req.Status.Valid {
			// An explicit null must not silently fall back to the create default.
			merged.Status = pgtype.Text{Valid: true}
		}
	}
	return merged
}

// PATCH /api/job_postings/{id} — partial update; returns a one-element array.
func (a *API) UpdateJobPosting(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req jobPostingFields
	sent, ok := decodeRecruitmentPatch(w, r, &req)
	if !ok {
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error("update job posting begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())
	q := a.Queries.WithTx(tx)

	cur, err := q.GetJobPostingForMerge(r.Context(), db.GetJobPostingForMergeParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update job posting")
		return
	}

	v, fields := validateJobPosting(mergeJobPosting(cur, req, sent))
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := q.UpdateJobPosting(r.Context(), db.UpdateJobPostingParams{
		CompanyID:    companyID,
		ID:           id,
		Title:        v.Title,
		Department:   v.Department,
		Location:     v.Location,
		Description:  v.Description,
		Requirements: v.Requirements,
		SalaryMin:    v.SalaryMin,
		SalaryMax:    v.SalaryMax,
		Status:       v.Status,
	})
	if err != nil {
		a.writeDBErr(w, err, "update job posting")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("update job posting commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	writeRows(w, http.StatusOK, []db.JobPosting{row})
}

// DELETE /api/job_postings/{id} — RESTRICT that the schema cannot express:
// applications reference the posting with ON DELETE CASCADE, which would
// silently destroy pipeline history, so the delete is 409 while any application
// still points at it (REC-4). The posting row is locked first: an application
// insert in flight holds a key-share lock on it, so the delete waits for that
// insert and then sees (and counts) it instead of cascading it away.
func (a *API) DeleteJobPosting(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error("delete job posting begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())
	q := a.Queries.WithTx(tx)

	if _, err := q.GetJobPostingForUpdate(r.Context(), db.GetJobPostingForUpdateParams{CompanyID: companyID, ID: id}); err != nil {
		a.writeDBErr(w, err, "delete job posting")
		return
	}
	n, err := q.CountApplicationsForJobPosting(r.Context(), id)
	if err != nil {
		a.writeDBErr(w, err, "delete job posting")
		return
	}
	if n > 0 {
		writeErrCode(w, http.StatusConflict, codeConflict,
			fmt.Sprintf("%d postulación(es) siguen asociadas a esta vacante", n))
		return
	}

	affected, err := q.DeleteJobPosting(r.Context(), db.DeleteJobPostingParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete job posting")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("delete job posting commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
