package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// generatedDocumentRequest declares exactly the keys generateAndDownload
// sends. employee_name/template_name are declared-and-ignored when the
// matching id resolves in the caller's tenant (the server derives them) and
// stored as sent only when that id is absent. company_id is never declared
// (A1 rule 2). content is an opaque, already-substituted string.
type generatedDocumentRequest struct {
	DocumentName *string `json:"document_name"`
	EmployeeID   *string `json:"employee_id"`
	EmployeeName *string `json:"employee_name"`
	TemplateID   *string `json:"template_id"`
	TemplateName *string `json:"template_name"`
	Content      string  `json:"content"`
}

func optText(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// optUUIDField parses an optional UUID body field; empty/absent is NULL.
func optUUIDField(p *string) (pgtype.UUID, bool) {
	if p == nil || strings.TrimSpace(*p) == "" {
		return pgtype.UUID{}, true
	}
	id, err := stringToUUID(strings.TrimSpace(*p))
	return id, err == nil
}

// GET /api/generated_documents
func (a *API) ListGeneratedDocuments(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "template_id")
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
	templateID, err := optionalUUIDFilter(p.Filters["template_id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid template_id")
		return
	}

	rows, err := a.Queries.ListGeneratedDocuments(r.Context(), db.ListGeneratedDocumentsParams{
		CompanyID:  companyID,
		Column2:    id,
		EmployeeID: employeeID,
		TemplateID: templateID,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list generated_documents")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/generated_documents/{id}
func (a *API) GetGeneratedDocument(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetGeneratedDocument(r.Context(), db.GetGeneratedDocumentParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get generated_document")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/generated_documents — issued record: there is no PATCH/DELETE.
// A supplied employee_id/template_id that is not in the caller's tenant makes
// the INSERT ... SELECT yield zero rows -> pgx.ErrNoRows -> 404 (A1 rule 6).
func (a *API) CreateGeneratedDocument(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req generatedDocumentRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	fields := map[string]string{}
	if strings.TrimSpace(req.Content) == "" {
		fields["content"] = "required"
	}
	employeeID, okEmp := optUUIDField(req.EmployeeID)
	if !okEmp {
		fields["employee_id"] = "invalid uuid"
	}
	templateID, okTpl := optUUIDField(req.TemplateID)
	if !okTpl {
		fields["template_id"] = "invalid uuid"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := a.Queries.CreateGeneratedDocument(r.Context(), db.CreateGeneratedDocumentParams{
		CompanyID:    companyID,
		EmployeeID:   employeeID,
		EmployeeName: optText(req.EmployeeName),
		TemplateID:   templateID,
		TemplateName: optText(req.TemplateName),
		DocumentName: optText(req.DocumentName),
		Content:      pgtype.Text{String: req.Content, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "create generated_document")
		return
	}

	writeRows(w, http.StatusCreated, []db.GeneratedDocument{row})
}
