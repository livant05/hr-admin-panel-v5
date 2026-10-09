package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/payroll"
)

// payrollPreviewRow mirrors payroll.Result field-for-field, plus the
// identifying employee columns prCalc's own JS signature doesn't carry
// (design R4e, task 5.9). This is the "Calcular" button's Go equivalent:
// read-only, writes nothing. The committing run (CreatePayrollRun) is slice
// 3f's job.
type payrollPreviewRow struct {
	EmployeeID   string         `json:"employee_id"`
	EmployeeName string         `json:"employee_name"`
	Cedula       string         `json:"cedula"`
	SalBase      pgtype.Numeric `json:"sal_base"`
	AttDed       pgtype.Numeric `json:"att_ded"`
	OtAmt        pgtype.Numeric `json:"ot_amt"`
	B            pgtype.Numeric `json:"b"`
	Css          pgtype.Numeric `json:"css"`
	Se           pgtype.Numeric `json:"se"`
	Isr          pgtype.Numeric `json:"isr"`
	Ded          pgtype.Numeric `json:"ded"`
	DedQuota     pgtype.Numeric `json:"ded_quota"`
	Net          pgtype.Numeric `json:"net"`
	Pcss         pgtype.Numeric `json:"pcss"`
	Pse          pgtype.Numeric `json:"pse"`
	Dec          pgtype.Numeric `json:"dec"`
	DecCssp      pgtype.Numeric `json:"dec_cssp"`
	Tot          pgtype.Numeric `json:"tot"`
}

// payrollFactor derives `factor` server-side (design R4e): 'mensual' -> 1,
// 'quincenal' -> 1/2. An unrecognized period is validation_failed (Q6's
// unknown-type precedent), never a silent factor=0.5 fallback --
// loadPayroll:2936's `period==='mensual'?1:0.5` would quietly halve every
// salary on a typo'd period.
func payrollFactor(period string) (payroll.Num, bool) {
	switch period {
	case "mensual":
		return payroll.FromInt(1), true
	case "quincenal":
		return payroll.FromFrac(1, 2), true
	default:
		return payroll.Num{}, false
	}
}

// employeeRunResult pairs one active employee's identity with their
// calculated payroll.Result. Shared by both CalculatePayroll (preview,
// read-only, slice 3e) and CreatePayrollRun (the committing run, slice 3f),
// so the calculation pipeline -- list inputs, convert through
// numericToNum, call payroll.Calculate -- is authored exactly once (task
// 6.4: reuse the preview's data-gathering, do not duplicate it).
type employeeRunResult struct {
	EmployeeID pgtype.UUID
	FirstName  string
	LastName   string
	Cedula     pgtype.Text
	Result     payroll.Result
}

// calculatePayrollRun runs ListPayrollRunInputs + payroll.Calculate for
// every active employee in companyID/year/month under factor. q may be the
// plain a.Queries (preview, no transaction) or a.Queries.WithTx(tx) (the
// committing run), so the committed write path calculates against the exact
// same transactional snapshot it then writes from.
func calculatePayrollRun(ctx context.Context, q *db.Queries, companyID pgtype.UUID, year, month int32, factor payroll.Num) ([]employeeRunResult, error) {
	rows, err := q.ListPayrollRunInputs(ctx, db.ListPayrollRunInputsParams{
		CompanyID: companyID,
		Year:      year,
		Month:     month,
	})
	if err != nil {
		return nil, fmt.Errorf("list payroll run inputs: %w", err)
	}

	out := make([]employeeRunResult, 0, len(rows))
	for _, row := range rows {
		salary, err := numericToNum(row.Salary)
		if err != nil {
			return nil, fmt.Errorf("parse salary: %w", err)
		}
		otAmount, err := numericToNum(row.OvertimeAmount)
		if err != nil {
			return nil, fmt.Errorf("parse overtime amount: %w", err)
		}
		dedQuota, err := numericToNum(row.DeductionQuota)
		if err != nil {
			return nil, fmt.Errorf("parse deduction quota: %w", err)
		}

		result, err := payroll.Calculate(payroll.Input{
			Salary: salary,
			Factor: factor,
			Attendance: &payroll.Attendance{
				HasData:        row.HasAttendance,
				AbsentDays:     int(row.AbsentDays),
				OvertimeAmount: otAmount,
			},
			Deduction: &payroll.Deduction{QuotaTotal: dedQuota},
		})
		if err != nil {
			return nil, fmt.Errorf("calculate: %w", err)
		}

		out = append(out, employeeRunResult{
			EmployeeID: row.ID,
			FirstName:  row.FirstName,
			LastName:   row.LastName,
			Cedula:     row.Cedula,
			Result:     result,
		})
	}
	return out, nil
}

// GET /api/payroll_history/calculate?month=&year=&period= -- pure preview
// (design R4e, task 5.9). Writes nothing; the committing run is slice 3f's
// CreatePayrollRun. The literal "/calculate" segment ranks above
// payroll_history's future "/{id}" under Go 1.22 ServeMux (Q5c).
func (a *API) CalculatePayroll(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	fields := map[string]string{}

	year, yearErr := strconv.Atoi(q.Get("year"))
	if yearErr != nil || year <= 0 {
		fields["year"] = "required"
	}
	month, monthErr := strconv.Atoi(q.Get("month"))
	if monthErr != nil || month < 1 || month > 12 {
		fields["month"] = "must be between 1 and 12"
	}
	factor, okPeriod := payrollFactor(q.Get("period"))
	if !okPeriod {
		fields["period"] = "must be 'mensual' or 'quincenal'"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	results, err := calculatePayrollRun(r.Context(), a.Queries, companyID, int32(year), int32(month), factor)
	if err != nil {
		a.Log.Error("calculate payroll", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	resp := make([]payrollPreviewRow, 0, len(results))
	for _, rr := range results {
		resp = append(resp, payrollPreviewRow{
			EmployeeID:   uuidToString(rr.EmployeeID),
			EmployeeName: rr.FirstName + " " + rr.LastName,
			Cedula:       textOrEmpty(rr.Cedula),
			SalBase:      numToNumeric(rr.Result.SalBase),
			AttDed:       numToNumeric(rr.Result.AttDed),
			OtAmt:        numToNumeric(rr.Result.OtAmt),
			B:            numToNumeric(rr.Result.B),
			Css:          numToNumeric(rr.Result.CSS),
			Se:           numToNumeric(rr.Result.SE),
			Isr:          numToNumeric(rr.Result.ISR),
			Ded:          numToNumeric(rr.Result.Ded),
			DedQuota:     numToNumeric(rr.Result.DedQuota),
			Net:          numToNumeric(rr.Result.Net),
			Pcss:         numToNumeric(rr.Result.PCSS),
			Pse:          numToNumeric(rr.Result.PSE),
			Dec:          numToNumeric(rr.Result.Dec),
			DecCssp:      numToNumeric(rr.Result.DecCSSP),
			Tot:          numToNumeric(rr.Result.Tot),
		})
	}

	writeRows(w, http.StatusOK, resp)
}

// createPayrollRunRequest is the exact contract savePayrollRun sends
// (hr_admin_panel.html:3121-3127, design R4e). total_bruto/total_isr/
// total_neto/total_empresa/month_name/employee_count are declared ONLY so
// DisallowUnknownFields accepts that existing payload shape -- every one of
// those values is computed server-side below and NEVER read (Q6's
// precedent, extended here to an aggregate write). The effective body is
// {period, month, year}.
type createPayrollRunRequest struct {
	Period string `json:"period"`
	Month  int32  `json:"month"`
	Year   int32  `json:"year"`

	TotalBruto    pgtype.Numeric `json:"total_bruto"`
	TotalIsr      pgtype.Numeric `json:"total_isr"`
	TotalNeto     pgtype.Numeric `json:"total_neto"`
	TotalEmpresa  pgtype.Numeric `json:"total_empresa"`
	MonthName     pgtype.Text    `json:"month_name"`
	EmployeeCount pgtype.Int4    `json:"employee_count"`
}

// POST /api/payroll_history -- the committing payroll run (design R4,
// settled decision #1; task 6.4). Recomputes the SAME payroll.Calculate the
// preview endpoint runs (via the shared calculatePayrollRun), then in ONE
// transaction: inserts the aggregate payroll_history row, then upserts N
// employee_pay_records rows (origin='run'). A failure on any employee rolls
// back the ENTIRE run, including the aggregate (TestPayrollRun_IsAtomic) --
// a1.Pool.Begin -> WithTx(tx) -> ... -> Commit, the same shape
// UpdateDepartment's rename-propagation tx uses (P6.1).
func (a *API) CreatePayrollRun(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req createPayrollRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	fields := map[string]string{}
	if req.Year <= 0 {
		fields["year"] = "required"
	}
	if req.Month < 1 || req.Month > 12 {
		fields["month"] = "must be between 1 and 12"
	}
	factor, okPeriod := payrollFactor(req.Period)
	if !okPeriod {
		fields["period"] = "must be 'mensual' or 'quincenal'"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		a.Log.Error("create payroll run begin tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	defer tx.Rollback(r.Context())

	q := a.Queries.WithTx(tx)

	results, err := calculatePayrollRun(r.Context(), q, companyID, req.Year, req.Month, factor)
	if err != nil {
		a.Log.Error("create payroll run: calculate", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	sumB, sumISR, sumNet, sumTot := payroll.Zero(), payroll.Zero(), payroll.Zero(), payroll.Zero()
	for _, rr := range results {
		sumB = sumB.Add(rr.Result.B)
		sumISR = sumISR.Add(rr.Result.ISR)
		sumNet = sumNet.Add(rr.Result.Net)
		sumTot = sumTot.Add(rr.Result.Tot)
	}

	monthName := payroll.MonthNameES(int(req.Month))
	periodo := fmt.Sprintf("%s %d", monthName, req.Year)

	historyRow, err := q.CreatePayrollHistory(r.Context(), db.CreatePayrollHistoryParams{
		CompanyID:     companyID,
		Period:        req.Period,
		Month:         req.Month,
		Year:          req.Year,
		MonthName:     pgtype.Text{String: monthName, Valid: true},
		EmployeeCount: pgtype.Int4{Int32: int32(len(results)), Valid: true},
		TotalBruto:    numToNumeric(sumB),
		TotalIsr:      numToNumeric(sumISR),
		TotalNeto:     numToNumeric(sumNet),
		TotalEmpresa:  numToNumeric(sumTot),
	})
	if err != nil {
		a.writeDBErr(w, err, "create payroll run: insert aggregate")
		return
	}

	for _, rr := range results {
		if _, err := q.UpsertPayrollRunPayRecord(r.Context(), db.UpsertPayrollRunPayRecordParams{
			CompanyID:       companyID,
			ID:              rr.EmployeeID,
			Periodo:         pgtype.Text{String: periodo, Valid: true},
			PeriodYear:      req.Year,
			PeriodMonth:     req.Month,
			GrossSalary:     numToNumeric(rr.Result.SalBase),
			OvertimeAmount:  numToNumeric(rr.Result.OtAmt),
			TotalEarned:     numToNumeric(rr.Result.B),
			CssEmployee:     numToNumeric(rr.Result.CSS),
			SeEmployee:      numToNumeric(rr.Result.SE),
			Isr:             numToNumeric(rr.Result.ISR),
			OtherDeductions: numToNumeric(rr.Result.DedQuota),
			NetSalary:       numToNumeric(rr.Result.Net),
		}); err != nil {
			a.writeDBErr(w, err, "create payroll run: upsert pay record")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		a.Log.Error("create payroll run commit tx", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	writeRows(w, http.StatusCreated, []db.PayrollHistory{historyRow})
}

// GET /api/payroll_history?id=&year=&month=&_limit=&_offset=
func (a *API) ListPayrollHistory(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	p, ok := parseList(w, r, "id", "year", "month")
	if !ok {
		return
	}

	id, err := optionalUUIDFilter(p.Filters["id"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid id")
		return
	}
	year, err := optionalIntFilter(p.Filters["year"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid year")
		return
	}
	month, err := optionalIntFilter(p.Filters["month"])
	if err != nil {
		writeErrCode(w, http.StatusBadRequest, codeValidation, "invalid month")
		return
	}

	rows, err := a.Queries.ListPayrollHistory(r.Context(), db.ListPayrollHistoryParams{
		CompanyID: companyID,
		Column2:   id,
		Year:      year,
		Month:     month,
		Limit:     p.Limit,
		Offset:    p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list payroll history")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/payroll_history/{id}
func (a *API) GetPayrollHistory(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetPayrollHistory(r.Context(), db.GetPayrollHistoryParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get payroll history")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// DELETE /api/payroll_history/{id} -- hard delete, NO cascade to
// employee_pay_records (design R5, settled decision #2): no FK exists
// between the tables, so the N ledger rows a run wrote survive by default.
func (a *API) DeletePayrollHistory(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeletePayrollHistory(r.Context(), db.DeletePayrollHistoryParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete payroll history")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
