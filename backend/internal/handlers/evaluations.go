package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/evaluation"
)

// evaluationRequest declares exactly the keys saveEval sends. employee_name,
// avg and category are declared-and-ignored: the server derives the name from
// the employees row and recomputes avg/category from scores (design D1).
// company_id is never declared (A1 rule 2).
type evaluationRequest struct {
	EmployeeID   string          `json:"employee_id"`
	EmployeeName *string         `json:"employee_name"`
	Period       *string         `json:"period"`
	Evaluator    *string         `json:"evaluator"`
	Scores       json.RawMessage `json:"scores"`
	Avg          json.RawMessage `json:"avg"`
	Category     *string         `json:"category"`
	Comments     *string         `json:"comments"`
	Status       *string         `json:"status"`
}

const evaluationStatusCompleted = "completada"

// GET /api/evaluations
func (a *API) ListEvaluations(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}
	employeeID, err := optionalUUIDFilter(p.Filters["employee_id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid employee_id")
		return
	}

	rows, err := a.Queries.ListEvaluations(r.Context(), db.ListEvaluationsParams{
		CompanyID:  companyID,
		Column2:    id,
		EmployeeID: employeeID,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list evaluations")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/evaluations/{id}
func (a *API) GetEvaluation(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetEvaluation(r.Context(), db.GetEvaluationParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get evaluation")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/evaluations — avg and category are recomputed from scores; the
// stored scores are the canonical 8-key integer object. There is no PATCH.
func (a *API) CreateEvaluation(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req evaluationRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	fields := map[string]string{}

	employeeID, err := stringToUUID(strings.TrimSpace(req.EmployeeID))
	if strings.TrimSpace(req.EmployeeID) == "" {
		fields["employee_id"] = "required"
	} else if err != nil {
		fields["employee_id"] = "invalid uuid"
	}

	status := evaluationStatusCompleted
	if req.Status != nil && *req.Status != "" {
		status = *req.Status
	}
	if status != evaluationStatusCompleted {
		fields["status"] = "must be completada"
	}

	scores, err := evaluation.ParseScores(req.Scores)
	if err != nil {
		var ve *evaluation.ValidationError
		if errors.As(err, &ve) {
			fields[ve.Field] = ve.Msg
		} else {
			fields["scores"] = "invalid"
		}
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	res := evaluation.Evaluate(scores)
	var avg pgtype.Numeric
	if err := avg.Scan(res.Avg()); err != nil {
		// Unreachable: Avg() always formats a plain decimal literal.
		a.writeDBErr(w, err, "create evaluation avg")
		return
	}

	row, err := a.Queries.CreateEvaluation(r.Context(), db.CreateEvaluationParams{
		CompanyID: companyID,
		ID:        employeeID,
		Period:    optText(req.Period),
		Evaluator: optText(req.Evaluator),
		Scores:    res.CanonicalScores(),
		Avg:       avg,
		Category:  pgtype.Text{String: string(res.Category), Valid: true},
		Comments:  optText(req.Comments),
		Status:    pgtype.Text{String: status, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "create evaluation")
		return
	}

	writeRows(w, http.StatusCreated, []db.Evaluation{row})
}

// DELETE /api/evaluations/{id} — hard delete.
func (a *API) DeleteEvaluation(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteEvaluation(r.Context(), db.DeleteEvaluationParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete evaluation")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
