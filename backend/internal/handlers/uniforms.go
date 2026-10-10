package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

const (
	uniformStatusActive   = "activo"
	uniformStatusReturned = "devuelto"

	// maxUniformQuantity keeps quantity well inside INT4.
	maxUniformQuantity = 100000
	// maxUniformValue is the largest NUMERIC(10,2) magnitude; anything above
	// would overflow the column and surface as a 500 instead of a 400.
	maxUniformValue = 99999999.99
	// maxUniformNumberLiteral bounds a client numeric literal BEFORE any
	// parsing, so an oversized literal can never drive allocation.
	maxUniformNumberLiteral = 32
)

// uniformFields are the keys saveUniform / returnUniform send. employee_name
// is declared-and-ignored (derived from the employees row) and kept as
// json.RawMessage so no client value is ever parsed. quantity and value are
// json.RawMessage too: they are bounded and parsed by hand, never handed to a
// big-number parser. company_id is NOT declared, so sending it is a 400.
type uniformFields struct {
	EmployeeName json.RawMessage `json:"employee_name"`
	Item         pgtype.Text     `json:"item"`
	Category     pgtype.Text     `json:"category"`
	Size         pgtype.Text     `json:"size"`
	Quantity     json.RawMessage `json:"quantity"`
	Date         pgtype.Date     `json:"date"`
	Value        json.RawMessage `json:"value"`
	Notes        pgtype.Text     `json:"notes"`
	Status       pgtype.Text     `json:"status"`
}

type createUniformRequest struct {
	EmployeeID string `json:"employee_id"`
	uniformFields
}

// updateUniformRequest has no employee_id: it is immutable, so sending it is
// a 400.
type updateUniformRequest struct {
	uniformFields
}

func decodeUniformBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	return true
}

// rawPresent reports whether a RawMessage field carried a non-null value.
func rawPresent(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// boundedNumber validates a client JSON number literal without any big-number
// parsing: it must be a bare number token, at most maxUniformNumberLiteral
// bytes, and strconv.ParseFloat (cheap for any exponent: it saturates) must
// land inside [lo, hi].
func boundedNumber(raw json.RawMessage, lo, hi float64) (float64, bool) {
	lit := string(bytes.TrimSpace(raw))
	if lit == "" || len(lit) > maxUniformNumberLiteral {
		return 0, false
	}
	if c := lit[0]; c != '-' && (c < '0' || c > '9') {
		return 0, false
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil || f < lo || f > hi {
		return 0, false
	}
	return f, true
}

// parseUniformQuantity returns a whole number in [1, maxUniformQuantity].
func parseUniformQuantity(raw json.RawMessage) (int32, bool) {
	if _, ok := boundedNumber(raw, 1, maxUniformQuantity); !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 32)
	if err != nil {
		return 0, false // fractional or exponent form
	}
	return int32(n), true
}

// parseUniformValue returns a non-negative amount that fits NUMERIC(10,2),
// rounded to two decimals.
func parseUniformValue(raw json.RawMessage) (pgtype.Numeric, bool) {
	f, ok := boundedNumber(raw, 0, maxUniformValue)
	if !ok {
		return pgtype.Numeric{}, false
	}
	if f == 0 {
		f = 0 // drop a negative-zero sign
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', 2, 64)); err != nil {
		return pgtype.Numeric{}, false
	}
	return n, true
}

func uniformStatusValid(s string) bool {
	return s == uniformStatusActive || s == uniformStatusReturned
}

// trimmedText returns t with its string trimmed, plus whether it is present
// (Valid) and non-blank.
func trimmedText(t pgtype.Text) (pgtype.Text, bool) {
	if !t.Valid {
		return t, false
	}
	s := strings.TrimSpace(t.String)
	return pgtype.Text{String: s, Valid: s != ""}, s != ""
}

// GET /api/uniforms
func (a *API) ListUniforms(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "employee_id", "status")
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
	var status pgtype.Text
	if s := p.Filters["status"]; s != "" {
		status = pgtype.Text{String: s, Valid: true}
	}

	rows, err := a.Queries.ListUniforms(r.Context(), db.ListUniformsParams{
		CompanyID:  companyID,
		Column2:    id,
		EmployeeID: employeeID,
		Status:     status,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list uniforms")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/uniforms/{id}
func (a *API) GetUniform(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetUniform(r.Context(), db.GetUniformParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get uniform")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/uniforms — employee_name is derived and the employee tenant check
// is the INSERT itself (A1 rule 6). date defaults to the current date.
func (a *API) CreateUniform(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createUniformRequest
	if !decodeUniformBody(w, r, &req) {
		return
	}

	fields := map[string]string{}
	rawEmployee := strings.TrimSpace(req.EmployeeID)
	employeeID, err := stringToUUID(rawEmployee)
	if rawEmployee == "" {
		fields["employee_id"] = "required"
	} else if err != nil {
		fields["employee_id"] = "invalid uuid"
	}
	item, okItem := trimmedText(req.Item)
	if !okItem {
		fields["item"] = "required"
	}

	quantity := int32(1)
	if rawPresent(req.Quantity) {
		q, okQ := parseUniformQuantity(req.Quantity)
		if !okQ {
			fields["quantity"] = fmt.Sprintf("must be a whole number between 1 and %d", maxUniformQuantity)
		}
		quantity = q
	}

	var value pgtype.Numeric
	if rawPresent(req.Value) {
		v, okV := parseUniformValue(req.Value)
		if !okV {
			fields["value"] = "must be a number between 0 and 99999999.99"
		}
		value = v
	} else {
		_ = value.Scan("0.00")
	}

	status := uniformStatusActive
	if req.Status.Valid && req.Status.String != "" {
		if !uniformStatusValid(req.Status.String) {
			fields["status"] = "must be activo or devuelto"
		} else {
			status = req.Status.String
		}
	}
	if req.Date.Valid && req.Date.InfinityModifier != pgtype.Finite {
		fields["date"] = "invalid date"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := a.Queries.CreateUniform(r.Context(), db.CreateUniformParams{
		CompanyID: companyID,
		ID:        employeeID,
		Item:      item.String,
		Category:  req.Category,
		Size:      req.Size,
		Quantity:  quantity,
		Date:      req.Date,
		Value:     value,
		Notes:     req.Notes,
		Status:    status,
	})
	if err != nil {
		a.writeDBErr(w, err, "create uniform")
		return
	}

	writeRows(w, http.StatusCreated, []db.Uniform{row})
}

// PATCH /api/uniforms/{id} — partial: absent or null keeps the stored value
// (returnUniform sends only {status:'devuelto'}). Validators run on the fields
// that were actually sent.
func (a *API) UpdateUniform(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updateUniformRequest
	if !decodeUniformBody(w, r, &req) {
		return
	}

	params := db.UpdateUniformParams{
		CompanyID: companyID,
		ID:        id,
		Category:  req.Category,
		Size:      req.Size,
		Date:      req.Date,
		Notes:     req.Notes,
	}

	fields := map[string]string{}
	if req.Item.Valid {
		item, okItem := trimmedText(req.Item)
		if !okItem {
			fields["item"] = "must not be empty"
		}
		params.Item = item
	}
	if rawPresent(req.Quantity) {
		q, okQ := parseUniformQuantity(req.Quantity)
		if !okQ {
			fields["quantity"] = fmt.Sprintf("must be a whole number between 1 and %d", maxUniformQuantity)
		}
		params.Quantity = pgtype.Int4{Int32: q, Valid: okQ}
	}
	if rawPresent(req.Value) {
		v, okV := parseUniformValue(req.Value)
		if !okV {
			fields["value"] = "must be a number between 0 and 99999999.99"
		}
		params.Value = v
	}
	if req.Status.Valid {
		if !uniformStatusValid(req.Status.String) {
			fields["status"] = "must be activo or devuelto"
		}
		params.Status = req.Status
	}
	if req.Date.Valid && req.Date.InfinityModifier != pgtype.Finite {
		fields["date"] = "invalid date"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	row, err := a.Queries.UpdateUniform(r.Context(), params)
	if err != nil {
		a.writeDBErr(w, err, "update uniform")
		return
	}

	writeRows(w, http.StatusOK, []db.Uniform{row})
}

// DELETE /api/uniforms/{id} — hard delete (Phase 2 Q3).
func (a *API) DeleteUniform(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteUniform(r.Context(), db.DeleteUniformParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete uniform")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
