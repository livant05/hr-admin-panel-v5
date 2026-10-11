package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// templateRequest is shared by POST and PATCH (PATCH is a full replace of
// name, type and content). No company_id/id/created_at/updated_at: the
// tenant is never client-bound (A1 rule 2) and updated_at belongs to
// trigger trg_tpl_upd.
type templateRequest struct {
	Name    string  `json:"name"`
	Type    *string `json:"type"`
	Content string  `json:"content"`
}

// decodeTemplate decodes and validates the body; ok=false means a response
// has already been written.
func decodeTemplate(w http.ResponseWriter, r *http.Request) (name string, typ pgtype.Text, content string, ok bool) {
	var req templateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return "", typ, "", false
	}

	fields := map[string]string{}
	name = strings.TrimSpace(req.Name)
	if name == "" {
		fields["name"] = "required"
	}
	if req.Content == "" {
		fields["content"] = "required"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return "", typ, "", false
	}

	// type is free text; an absent/empty value falls back to the DDL default.
	if req.Type != nil && strings.TrimSpace(*req.Type) != "" {
		typ = pgtype.Text{String: strings.TrimSpace(*req.Type), Valid: true}
	}
	return name, typ, req.Content, true
}

// GET /api/document_templates
func (a *API) ListDocumentTemplates(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "type")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}

	var typ pgtype.Text
	if v := p.Filters["type"]; v != "" {
		typ = pgtype.Text{String: v, Valid: true}
	}

	rows, err := a.Queries.ListDocumentTemplates(r.Context(), db.ListDocumentTemplatesParams{
		CompanyID: companyID,
		Column2:   id,
		Type:      typ,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list document_templates")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/document_templates/{id}
func (a *API) GetDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetDocumentTemplate(r.Context(), db.GetDocumentTemplateParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get document_template")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/document_templates
func (a *API) CreateDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	name, typ, content, ok := decodeTemplate(w, r)
	if !ok {
		return
	}

	row, err := a.Queries.CreateDocumentTemplate(r.Context(), db.CreateDocumentTemplateParams{
		CompanyID: companyID,
		Name:      name,
		Type:      typ,
		Content:   content,
	})
	if err != nil {
		a.writeDBErr(w, err, "create document_template")
		return
	}

	writeRows(w, http.StatusCreated, []db.DocumentTemplate{row})
}

// PATCH /api/document_templates/{id} — full replace of name, type, content.
func (a *API) UpdateDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	name, typ, content, ok := decodeTemplate(w, r)
	if !ok {
		return
	}
	if !typ.Valid {
		typ = pgtype.Text{String: "contract", Valid: true}
	}

	row, err := a.Queries.UpdateDocumentTemplate(r.Context(), db.UpdateDocumentTemplateParams{
		CompanyID: companyID,
		ID:        id,
		Name:      name,
		Type:      typ,
		Content:   content,
	})
	if err != nil {
		a.writeDBErr(w, err, "update document_template")
		return
	}

	writeRows(w, http.StatusOK, []db.DocumentTemplate{row})
}

// DELETE /api/document_templates/{id} — hard delete. A template still
// referenced by generated_documents (FK without ON DELETE) answers 409 with
// a handler-local message; the shared 23503 mapping is left unchanged.
func (a *API) DeleteDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteDocumentTemplate(r.Context(), db.DeleteDocumentTemplateParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		if pgErrCode(err) == "23503" {
			writeErrCode(w, http.StatusConflict, codeConflict,
				"La plantilla tiene documentos generados y no se puede eliminar")
			return
		}
		a.writeDBErr(w, err, "delete document_template")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
