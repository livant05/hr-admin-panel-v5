package handlers

import (
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

	rows, err := a.Queries.ListPayrollRunInputs(r.Context(), db.ListPayrollRunInputsParams{
		CompanyID: companyID,
		Year:      int32(year),
		Month:     int32(month),
	})
	if err != nil {
		a.writeDBErr(w, err, "list payroll run inputs")
		return
	}

	resp := make([]payrollPreviewRow, 0, len(rows))
	for _, row := range rows {
		salary, err := numericToNum(row.Salary)
		if err != nil {
			a.Log.Error("calculate payroll: parse salary", "err", err)
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
			return
		}
		otAmount, err := numericToNum(row.OvertimeAmount)
		if err != nil {
			a.Log.Error("calculate payroll: parse overtime amount", "err", err)
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
			return
		}
		dedQuota, err := numericToNum(row.DeductionQuota)
		if err != nil {
			a.Log.Error("calculate payroll: parse deduction quota", "err", err)
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
			return
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
			a.Log.Error("calculate payroll", "err", err)
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
			return
		}

		resp = append(resp, payrollPreviewRow{
			EmployeeID:   uuidToString(row.ID),
			EmployeeName: row.FirstName + " " + row.LastName,
			Cedula:       textOrEmpty(row.Cedula),
			SalBase:      numToNumeric(result.SalBase),
			AttDed:       numToNumeric(result.AttDed),
			OtAmt:        numToNumeric(result.OtAmt),
			B:            numToNumeric(result.B),
			Css:          numToNumeric(result.CSS),
			Se:           numToNumeric(result.SE),
			Isr:          numToNumeric(result.ISR),
			Ded:          numToNumeric(result.Ded),
			DedQuota:     numToNumeric(result.DedQuota),
			Net:          numToNumeric(result.Net),
			Pcss:         numToNumeric(result.PCSS),
			Pse:          numToNumeric(result.PSE),
			Dec:          numToNumeric(result.Dec),
			DecCssp:      numToNumeric(result.DecCSSP),
			Tot:          numToNumeric(result.Tot),
		})
	}

	writeRows(w, http.StatusOK, resp)
}
