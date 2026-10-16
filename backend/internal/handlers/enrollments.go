package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

const maxEnrollmentScore = 100

// enrollmentCreate is the create body. training_name and employee_name are
// accepted (the panel's demo rows carry them) but ignored: the server derives
// both from the course and the employee.
type enrollmentCreate struct {
	TrainingID   string          `json:"training_id"`
	EmployeeID   string          `json:"employee_id"`
	Status       pgtype.Text     `json:"status"`
	Score        json.RawMessage `json:"score"`
	Notes        pgtype.Text     `json:"notes"`
	TrainingName pgtype.Text     `json:"training_name"`
	EmployeeName pgtype.Text     `json:"employee_name"`
}

// enrollmentGrade is the PATCH body: grading only. training_id and
// employee_id are not in it, so a body carrying them is a 400.
type enrollmentGrade struct {
	Status pgtype.Text     `json:"status"`
	Score  json.RawMessage `json:"score"`
	Notes  pgtype.Text     `json:"notes"`
}

// optionalScore parses a nullable score: absent or null is NULL, anything else
// must be a number in [0, 100], stored with two decimals (NUMERIC(5,2)).
func optionalScore(raw json.RawMessage) (pgtype.Numeric, bool) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || string(t) == "null" {
		return pgtype.Numeric{}, true
	}
	f, ok := boundedNumber(raw, 0, maxEnrollmentScore)
	if !ok {
		return pgtype.Numeric{}, false
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f+0, 'f', 2, 64)); err != nil {
		return pgtype.Numeric{}, false
	}
	return n, true
}

// validateGrade checks status, score and notes; status falls back to def when
// absent.
func validateGrade(fields map[string]string, status pgtype.Text, score json.RawMessage, def string) (string, pgtype.Numeric) {
	st := def
	if status.Valid {
		st = status.String
	}
	if !trainingStatusValid(st) {
		fields["status"] = "must be one of programado, en_progreso, completado"
	}
	n, ok := optionalScore(score)
	if !ok {
		fields["score"] = "must be a number between 0 and 100"
	}
	return st, n
}

// writeEnrollmentErr is writeDBErr plus the one-enrollment-per-pair rule
// (migration 0016), answered in Spanish like the other panel conflicts.
func (a *API) writeEnrollmentErr(w http.ResponseWriter, err error, op string) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "uq_enrollments_training_employee" {
		writeErrCode(w, http.StatusConflict, codeConflict, "El empleado ya está inscrito en esta capacitación")
		return
	}
	a.writeDBErr(w, err, op)
}

// GET /api/enrollments — filters id, training_id, employee_id.
func (a *API) ListEnrollments(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	p, ok := parseList(w, r, "id", "training_id", "employee_id")
	if !ok {
		return
	}
	var ids [3]pgtype.UUID
	for i, k := range []string{"id", "training_id", "employee_id"} {
		v, err := optionalUUIDFilter(p.Filters[k])
		if err != nil {
			writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid "+k)
			return
		}
		ids[i] = v
	}
	rows, err := a.Queries.ListEnrollments(r.Context(), db.ListEnrollmentsParams{
		CompanyID:  companyID, // $1 — from ctx, never from a query param
		ID:         ids[0],
		TrainingID: ids[1],
		EmployeeID: ids[2],
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list enrollments")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// GET /api/enrollments/{id} — another company's id is 404.
func (a *API) GetEnrollment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	row, err := a.Queries.GetEnrollment(r.Context(), db.GetEnrollmentParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get enrollment")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// POST /api/enrollments — 201 with a one-element array; another company's (or
// a missing) course or employee is 404, a repeated pair is 409.
func (a *API) CreateEnrollment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	var req enrollmentCreate
	if !decodeStrictBody(w, r, &req) {
		return
	}
	fields := map[string]string{}
	trainingID, err := stringToUUID(req.TrainingID)
	if err != nil {
		fields["training_id"] = "required uuid"
	}
	employeeID, err := stringToUUID(req.EmployeeID)
	if err != nil {
		fields["employee_id"] = "required uuid"
	}
	status, score := validateGrade(fields, req.Status, req.Score, trainingStatusScheduled)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	row, err := a.Queries.CreateEnrollment(r.Context(), db.CreateEnrollmentParams{
		CompanyID:  companyID, // from ctx, never from req
		TrainingID: trainingID,
		EmployeeID: employeeID,
		Status:     status,
		Score:      score,
		Notes:      req.Notes,
	})
	if err != nil {
		a.writeEnrollmentErr(w, err, "create enrollment")
		return
	}
	writeRows(w, http.StatusCreated, []db.Enrollment{row})
}

// PATCH /api/enrollments/{id} — grading (the panel's "Calificar"): status,
// score and notes; absent keeps, null clears score or notes (status cannot be
// null). Returns a one-element array.
func (a *API) UpdateEnrollment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	var req enrollmentGrade
	sent, ok := decodeStrictPatch(w, r, &req)
	if !ok {
		return
	}

	tx, q, ok := a.beginWriteTx(w, r, "update enrollment")
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())

	cur, err := q.GetEnrollmentForMerge(r.Context(), db.GetEnrollmentForMergeParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update enrollment")
		return
	}

	has := func(k string) bool { _, ok := sent[k]; return ok }
	status := cur.Status
	if has("status") {
		status = req.Status
		if !status.Valid {
			status = pgtype.Text{Valid: true} // explicit null: rejected below
		}
	}
	scoreRaw, _ := json.Marshal(cur.Score)
	if has("score") {
		scoreRaw = req.Score
	}
	notes := cur.Notes
	if has("notes") {
		notes = req.Notes
	}
	fields := map[string]string{}
	st, score := validateGrade(fields, status, scoreRaw, trainingStatusScheduled)
	onlySentFieldErrors(fields, sent, nil)
	if !has("score") {
		score = cur.Score // an imported score outside 0-100 is kept, not re-parsed
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	storedStatus := pgtype.Text{String: st, Valid: true}
	if !has("status") {
		storedStatus = cur.Status // an imported NULL stays NULL, not the create default
	}

	row, err := q.UpdateEnrollment(r.Context(), db.UpdateEnrollmentParams{
		CompanyID: companyID,
		ID:        id,
		Status:    storedStatus,
		Score:     score,
		Notes:     notes,
	})
	if err != nil {
		a.writeDBErr(w, err, "update enrollment")
		return
	}
	if !a.commitWriteTx(w, r, tx, "update enrollment") {
		return
	}
	writeRows(w, http.StatusOK, []db.Enrollment{row})
}

// DELETE /api/enrollments/{id} — 204.
func (a *API) DeleteEnrollment(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	n, err := a.Queries.DeleteEnrollment(r.Context(), db.DeleteEnrollmentParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete enrollment")
		return
	}
	if n == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
