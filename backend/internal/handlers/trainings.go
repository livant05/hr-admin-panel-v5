package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// Values of trainings.status and enrollments.status; the column default is
// programado. The panel moves a course (and an enrollment) through them.
const (
	trainingStatusScheduled  = "programado"
	trainingStatusInProgress = "en_progreso"
	trainingStatusDone       = "completado"

	maxTrainingName  = 200
	maxTrainingHours = 10000
)

func trainingStatusValid(s string) bool {
	return s == trainingStatusScheduled || s == trainingStatusInProgress || s == trainingStatusDone
}

// trainingFields is the writable surface of a course. No id, company_id or
// created_at: DisallowUnknownFields makes a body carrying them a 400. The
// dates arrive as text and are parsed by optionalDateField (YYYY-MM-DD).
type trainingFields struct {
	Name        string      `json:"name"`
	Category    pgtype.Text `json:"category"`
	Hours       pgtype.Int4 `json:"hours"`
	StartDate   pgtype.Text `json:"start_date"`
	EndDate     pgtype.Text `json:"end_date"`
	Mode        pgtype.Text `json:"mode"`
	Instructor  pgtype.Text `json:"instructor"`
	Description pgtype.Text `json:"description"`
	Status      pgtype.Text `json:"status"`
}

// optionalBoundedText trims a nullable text field and checks its length.
func optionalBoundedText(fields map[string]string, name string, v pgtype.Text, max int) pgtype.Text {
	if !v.Valid {
		return v
	}
	s := strings.TrimSpace(v.String)
	if utf8.RuneCountInString(s) > max {
		fields[name] = "too long"
	}
	return pgtype.Text{String: s, Valid: true}
}

// validateTraining checks a full course and returns the sqlc params (minus
// company_id). Failures are reported per field.
func validateTraining(f trainingFields) (db.CreateTrainingParams, map[string]string) {
	fields := map[string]string{}

	name := strings.TrimSpace(f.Name)
	if name == "" || utf8.RuneCountInString(name) > maxTrainingName {
		fields["name"] = "required, max 200 characters"
	}
	if f.Hours.Valid && (f.Hours.Int32 < 0 || f.Hours.Int32 > maxTrainingHours) {
		fields["hours"] = "must be a whole number between 0 and 10000"
	}
	status := trainingStatusScheduled
	if f.Status.Valid {
		status = f.Status.String
	}
	if !trainingStatusValid(status) {
		fields["status"] = "must be one of programado, en_progreso, completado"
	}
	start := optionalDateField(fields, "start_date", f.StartDate)
	end := optionalDateField(fields, "end_date", f.EndDate)
	if start.Valid && end.Valid && end.Time.Before(start.Time) {
		fields["end_date"] = "must not be before start_date"
	}

	return db.CreateTrainingParams{
		Name:        name,
		Category:    optionalBoundedText(fields, "category", f.Category, 100),
		Hours:       f.Hours,
		StartDate:   start,
		EndDate:     end,
		Mode:        optionalBoundedText(fields, "mode", f.Mode, 50),
		Instructor:  optionalBoundedText(fields, "instructor", f.Instructor, 200),
		Description: f.Description,
		Status:      pgtype.Text{String: status, Valid: true},
	}, fields
}

// dateText renders a stored date back as the YYYY-MM-DD text a body carries,
// so a PATCH re-validates the merged course as a whole.
func dateText(d pgtype.Date) pgtype.Text {
	if !d.Valid {
		return pgtype.Text{}
	}
	return pgtype.Text{String: d.Time.Format(dateLayout), Valid: true}
}

// mergeTraining overlays the sent fields on the stored course: absent keeps,
// null clears an optional field (name and status cannot be null).
func mergeTraining(cur db.Training, req trainingFields, sent map[string]json.RawMessage) trainingFields {
	m := trainingFields{
		Name:        cur.Name,
		Category:    cur.Category,
		Hours:       cur.Hours,
		StartDate:   dateText(cur.StartDate),
		EndDate:     dateText(cur.EndDate),
		Mode:        cur.Mode,
		Instructor:  cur.Instructor,
		Description: cur.Description,
		Status:      cur.Status,
	}
	has := func(k string) bool { _, ok := sent[k]; return ok }
	if has("name") {
		m.Name = req.Name
	}
	if has("category") {
		m.Category = req.Category
	}
	if has("hours") {
		m.Hours = req.Hours
	}
	if has("start_date") {
		m.StartDate = req.StartDate
	}
	if has("end_date") {
		m.EndDate = req.EndDate
	}
	if has("mode") {
		m.Mode = req.Mode
	}
	if has("instructor") {
		m.Instructor = req.Instructor
	}
	if has("description") {
		m.Description = req.Description
	}
	if has("status") {
		m.Status = req.Status
		if !req.Status.Valid {
			m.Status = pgtype.Text{Valid: true} // explicit null: rejected, never the default
		}
	}
	return m
}

// GET /api/trainings — filter id.
func (a *API) ListTrainings(w http.ResponseWriter, r *http.Request) {
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
	rows, err := a.Queries.ListTrainings(r.Context(), db.ListTrainingsParams{
		CompanyID: companyID, // $1 — from ctx, never from a query param
		ID:        id,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list trainings")
		return
	}
	writeRows(w, http.StatusOK, rows)
}

// GET /api/trainings/{id} — another company's id is 404.
func (a *API) GetTraining(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	row, err := a.Queries.GetTraining(r.Context(), db.GetTrainingParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get training")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// POST /api/trainings — 201 with a one-element array.
func (a *API) CreateTraining(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	var req trainingFields
	if !decodeRecruitmentBody(w, r, &req) {
		return
	}
	params, fields := validateTraining(req)
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	params.CompanyID = companyID // from ctx, never from req
	row, err := a.Queries.CreateTraining(r.Context(), params)
	if err != nil {
		a.writeDBErr(w, err, "create training")
		return
	}
	writeRows(w, http.StatusCreated, []db.Training{row})
}

// PATCH /api/trainings/{id} — partial update (the panel changes the status
// through it); returns a one-element array.
func (a *API) UpdateTraining(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	var req trainingFields
	sent, ok := decodeRecruitmentPatch(w, r, &req)
	if !ok {
		return
	}

	tx, q, ok := a.beginRecruitmentTx(w, r, "update training")
	if !ok {
		return
	}
	defer tx.Rollback(r.Context())

	cur, err := q.GetTrainingForMerge(r.Context(), db.GetTrainingForMergeParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update training")
		return
	}
	v, fields := validateTraining(mergeTraining(cur, req, sent))
	onlySentFieldErrors(fields, sent, map[string][]string{"end_date": {"start_date", "end_date"}})
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}
	row, err := q.UpdateTraining(r.Context(), db.UpdateTrainingParams{
		CompanyID:   companyID,
		ID:          id,
		Name:        v.Name,
		Category:    v.Category,
		Hours:       v.Hours,
		StartDate:   v.StartDate,
		EndDate:     v.EndDate,
		Mode:        v.Mode,
		Instructor:  v.Instructor,
		Description: v.Description,
		Status:      v.Status,
	})
	if err != nil {
		a.writeDBErr(w, err, "update training")
		return
	}
	if !a.commitRecruitmentTx(w, r, tx, "update training") {
		return
	}
	writeRows(w, http.StatusOK, []db.Training{row})
}

// DELETE /api/trainings/{id} — 204; the course's enrollments go with it.
func (a *API) DeleteTraining(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}
	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	n, err := a.Queries.DeleteTraining(r.Context(), db.DeleteTrainingParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete training")
		return
	}
	if n == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// onlySentFieldErrors drops the errors of fields the client did not send: a
// PATCH re-validates the merged record, and an imported row may already break
// today's rules in a field nobody is editing. Those stored values are kept as
// they are. linked names the sent keys that make a cross-field error count
// (an end_date before start_date matters when either date is sent).
func onlySentFieldErrors(fields map[string]string, sent map[string]json.RawMessage, linked map[string][]string) {
	for name := range fields {
		keys := linked[name]
		if keys == nil {
			keys = []string{name}
		}
		keep := false
		for _, k := range keys {
			if _, ok := sent[k]; ok {
				keep = true
			}
		}
		if !keep {
			delete(fields, name)
		}
	}
}
