package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/payroll"
)

const (
	medicalStatusActive = "activa"
	medicalStatusClosed = "cerrada"
	// maxMedicalDays caps the incapacity span (about 10 years). With it the
	// computed cost fits medical_records.cost NUMERIC(12,2) for any monthly
	// salary up to ~82M; an unbounded span overflowed it into a 500.
	maxMedicalDays = 3650
)

// medicalRecordFields are the keys saveMedical / closeMedical send.
// employee_name, days, employer_days and css_days are declared-and-ignored:
// the server derives the name from the employees row and recomputes the day
// split and cost (design D2). They are json.RawMessage so no client-supplied
// value is ever parsed. cost, salary_basis and company_id are NOT declared, so
// DisallowUnknownFields turns an attempt to send them into a 400.
type medicalRecordFields struct {
	EmployeeName json.RawMessage `json:"employee_name"`
	Type         pgtype.Text     `json:"type"`
	Diagnosis    pgtype.Text     `json:"diagnosis"`
	StartDate    pgtype.Date     `json:"start_date"`
	EndDate      pgtype.Date     `json:"end_date"`
	Days         json.RawMessage `json:"days"`
	EmployerDays json.RawMessage `json:"employer_days"`
	CSSDays      json.RawMessage `json:"css_days"`
	CertNumber   pgtype.Text     `json:"cert_number"`
	Notes        pgtype.Text     `json:"notes"`
	Status       pgtype.Text     `json:"status"`
}

type createMedicalRecordRequest struct {
	EmployeeID string `json:"employee_id"`
	medicalRecordFields
}

// updateMedicalRecordRequest has no employee_id: it is immutable, so sending
// it is a 400.
type updateMedicalRecordRequest struct {
	medicalRecordFields
}

func decodeMedicalBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return false
	}
	return true
}

// validMedicalDate reports whether d is a finite, present date.
func validMedicalDate(d pgtype.Date) bool {
	return d.Valid && d.InfinityModifier == pgtype.Finite
}

// medicalStatus applies the default and allowlist; ok=false means invalid.
func medicalStatus(raw pgtype.Text, def string) (string, bool) {
	if !raw.Valid || raw.String == "" {
		return def, true
	}
	if raw.String != medicalStatusActive && raw.String != medicalStatusClosed {
		return "", false
	}
	return raw.String, true
}

// computeMedical validates the merged type/dates and runs the incapacity
// formula. On failure it writes the response and returns ok=false.
func computeMedical(w http.ResponseWriter, typ string, start, end pgtype.Date, salary payroll.Num) (payroll.IncapacityResult, bool) {
	fields := map[string]string{}
	it, okType := payroll.ParseIncapacityType(typ)
	if !okType {
		fields["type"] = "must be one of enfermedad, accidente, maternidad, paternidad, familiar"
	}
	if !validMedicalDate(start) {
		fields["start_date"] = "required"
	}
	if !validMedicalDate(end) {
		fields["end_date"] = "required"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return payroll.IncapacityResult{}, false
	}

	res, err := payroll.CalculateIncapacity(payroll.IncapacityInput{
		Type: it, StartDate: start.Time, EndDate: end.Time, Salary: salary,
	})
	if err != nil {
		if errors.Is(err, payroll.ErrEndBeforeStart) {
			writeFieldErr(w, map[string]string{"end_date": "must not be before start_date"})
		} else {
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		}
		return payroll.IncapacityResult{}, false
	}
	if res.Days > maxMedicalDays {
		writeFieldErr(w, map[string]string{"end_date": fmt.Sprintf("span must not exceed %d days", maxMedicalDays)})
		return payroll.IncapacityResult{}, false
	}
	return res, true
}

func int4(n int) pgtype.Int4 { return pgtype.Int4{Int32: int32(n), Valid: true} }

// GET /api/medical_records
func (a *API) ListMedicalRecords(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListMedicalRecords(r.Context(), db.ListMedicalRecordsParams{
		CompanyID:  companyID,
		Column2:    id,
		EmployeeID: employeeID,
		Status:     status,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list medical records")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/medical_records/{id}
func (a *API) GetMedicalRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetMedicalRecord(r.Context(), db.GetMedicalRecordParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "get medical record")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// POST /api/medical_records — days, employer/CSS split, salary_basis and cost
// are computed server-side from the employee's salary read tenant-scoped
// inside the write (design D2, A1 rule 6).
func (a *API) CreateMedicalRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createMedicalRecordRequest
	if !decodeMedicalBody(w, r, &req) {
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
	status, okStatus := medicalStatus(req.Status, medicalStatusActive)
	if !okStatus {
		fields["status"] = "must be activa or cerrada"
	}
	if _, okType := payroll.ParseIncapacityType(req.Type.String); !okType || !req.Type.Valid {
		fields["type"] = "must be one of enfermedad, accidente, maternidad, paternidad, familiar"
	}
	if !validMedicalDate(req.StartDate) {
		fields["start_date"] = "required"
	}
	if !validMedicalDate(req.EndDate) {
		fields["end_date"] = "required"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	emp, err := a.Queries.GetEmployeeSalary(r.Context(), db.GetEmployeeSalaryParams{CompanyID: companyID, ID: employeeID})
	if err != nil {
		a.writeDBErr(w, err, "create medical record salary")
		return
	}
	salary, err := numericToNum(emp.Salary)
	if err != nil {
		a.writeDBErr(w, err, "create medical record salary")
		return
	}

	res, ok := computeMedical(w, req.Type.String, req.StartDate, req.EndDate, salary)
	if !ok {
		return
	}

	row, err := a.Queries.CreateMedicalRecord(r.Context(), db.CreateMedicalRecordParams{
		CompanyID:    companyID,
		ID:           employeeID,
		Type:         req.Type,
		Diagnosis:    req.Diagnosis,
		StartDate:    req.StartDate,
		EndDate:      req.EndDate,
		Days:         int4(res.Days),
		EmployerDays: int4(res.EmployerDays),
		CssDays:      int4(res.CSSDays),
		CertNumber:   req.CertNumber,
		Notes:        req.Notes,
		Status:       pgtype.Text{String: status, Valid: true},
		SalaryBasis:  numToNumeric(salary),
		Cost:         numToNumeric(res.Cost),
	})
	if err != nil {
		a.writeDBErr(w, err, "create medical record")
		return
	}

	writeRows(w, http.StatusCreated, []db.MedicalRecord{row})
}

// PATCH /api/medical_records/{id} — partial (absent or null keeps the stored
// value) and ALWAYS recomputes the day split and cost inside a row-locked
// transaction, from the stored salary_basis snapshot. A legacy row with no
// snapshot falls back to the employee's current salary, which is then
// persisted as salary_basis.
func (a *API) UpdateMedicalRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	var req updateMedicalRecordRequest
	if !decodeMedicalBody(w, r, &req) {
		return
	}

	// Validate the fields that were actually sent before taking the lock.
	fields := map[string]string{}
	if req.Status.Valid {
		if _, okStatus := medicalStatus(req.Status, ""); !okStatus {
			fields["status"] = "must be activa or cerrada"
		}
	}
	if req.Type.Valid {
		if _, okType := payroll.ParseIncapacityType(req.Type.String); !okType {
			fields["type"] = "must be one of enfermedad, accidente, maternidad, paternidad, familiar"
		}
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error("update medical record begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())

	q := a.Queries.WithTx(tx)

	cur, err := q.GetMedicalRecordForUpdate(r.Context(), db.GetMedicalRecordForUpdateParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "update medical record")
		return
	}

	typ := cur.Type
	if req.Type.Valid {
		typ = req.Type
	}
	diagnosis := cur.Diagnosis
	if req.Diagnosis.Valid {
		diagnosis = req.Diagnosis
	}
	start := cur.StartDate
	if req.StartDate.Valid {
		start = req.StartDate
	}
	end := cur.EndDate
	if req.EndDate.Valid {
		end = req.EndDate
	}
	certNumber := cur.CertNumber
	if req.CertNumber.Valid {
		certNumber = req.CertNumber
	}
	notes := cur.Notes
	if req.Notes.Valid {
		notes = req.Notes
	}
	status := cur.Status
	if s, _ := medicalStatus(req.Status, ""); s != "" {
		status = pgtype.Text{String: s, Valid: true}
	}

	salary, err := numericToNum(cur.EffectiveSalary)
	if err != nil {
		a.writeDBErr(w, err, "update medical record salary")
		return
	}

	res, ok := computeMedical(w, typ.String, start, end, salary)
	if !ok {
		return
	}

	row, err := q.UpdateMedicalRecord(r.Context(), db.UpdateMedicalRecordParams{
		CompanyID:    companyID,
		ID:           id,
		Type:         typ,
		Diagnosis:    diagnosis,
		StartDate:    start,
		EndDate:      end,
		Days:         int4(res.Days),
		EmployerDays: int4(res.EmployerDays),
		CssDays:      int4(res.CSSDays),
		CertNumber:   certNumber,
		Notes:        notes,
		Status:       status,
		SalaryBasis:  numToNumeric(salary),
		Cost:         numToNumeric(res.Cost),
	})
	if err != nil {
		a.writeDBErr(w, err, "update medical record")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("update medical record commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	writeRows(w, http.StatusOK, []db.MedicalRecord{row})
}

// DELETE /api/medical_records/{id} — hard delete (Phase 2 Q3).
func (a *API) DeleteMedicalRecord(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteMedicalRecord(r.Context(), db.DeleteMedicalRecordParams{CompanyID: companyID, ID: id})
	if err != nil {
		a.writeDBErr(w, err, "delete medical record")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
