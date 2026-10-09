package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/livant05/rrhh-go/internal/db/generated"
	"github.com/livant05/rrhh-go/internal/payroll"
)

// liquidationCalcRequest is the shared calculation-input contract both
// CalculateLiquidation (preview, decoded from query params, task 7.3) and
// CreateLiquidation (the committing create, decoded from the JSON body)
// resolve through resolveLiquidation. employee_name and total_amount are
// declared only so a saveLiqHistory-shaped body
// (hr_admin_panel.html:3756 -- {employee_id, employee_name, reason,
// exit_date, total_amount}) is accepted by DisallowUnknownFields without
// being trusted: both values are always computed server-side
// (employee_name is derived from the employees row via the rule-6 rider,
// total_amount is always CalculateLiquidation's own NetTotal) -- the Q6
// precedent, extended here to a body saveLiqHistory cannot yet fully supply
// (it structurally cannot send the ~35-field breakdown this slice computes).
type liquidationCalcRequest struct {
	EmployeeID   string         `json:"employee_id"`
	EmployeeName pgtype.Text    `json:"employee_name"` // accepted, never trusted -- always derived
	Reason       string         `json:"reason"`
	ExitDate     string         `json:"exit_date"`    // "YYYY-MM-DD"
	TotalAmount  pgtype.Numeric `json:"total_amount"` // accepted, never trusted -- always recomputed (NetTotal)
	Notes        pgtype.Text    `json:"notes"`

	SalPend   pgtype.Numeric `json:"sal_pend"`   // #liq-sal-pend, item 1
	Otros     pgtype.Numeric `json:"otros"`      // #liq-otros, item 6
	VacDays   pgtype.Numeric `json:"vac_days"`   // #liq-vac, fallback input for item 3
	DecMonths pgtype.Numeric `json:"dec_months"` // #liq-dec, fallback input for item 4

	AcumVac   pgtype.Numeric `json:"acum_vac"`   // #liq-acum-vac (11-month accumulation)
	AcumDec   pgtype.Numeric `json:"acum_dec"`   // #liq-acum-dec (12-month accumulation)
	AcumPrima pgtype.Numeric `json:"acum_prima"` // #liq-acum-prima (60-month accumulation)
	Acum6m    pgtype.Numeric `json:"acum_6m"`    // #liq-acum-6m (6-month accumulation)
	Sal30     pgtype.Numeric `json:"sal30"`      // #liq-sal30

	// DeductionIDs is persisted into inputs.deductionIds for audit only
	// (design R6) -- the server never re-looks-up each deduction's quota
	// from it; DeductionQuotaTotal is the trusted sum, the same class of
	// manual override as SalPend/Otros/the acumulados fields above (all are
	// calcLiq form inputs the operator can see and edit, not a
	// server-aggregated value like the payroll run's deduction_quota).
	DeductionIDs        []string       `json:"deduction_ids"`
	DeductionQuotaTotal pgtype.Numeric `json:"deduction_quota_total"`
}

// liquidationBranchesPayload mirrors payroll.Branches for JSON encoding
// (design R6's inputs.branches, task 7.3/7.4).
type liquidationBranchesPayload struct {
	VacProp bool `json:"vacProp"`
	DecProp bool `json:"decProp"`
	Prima   bool `json:"prima"`
	Indem6m bool `json:"indem6m"`
	Sal30   bool `json:"sal30"`
}

// liquidationInputsPayload is the design R6 / spec risk #3 answer: the five
// raw acumulados inputs (so a future audit can recompute from scratch) PLUS
// which of the five independent branches fired (so the audit does not have
// to infer the branch from a value both branches could have produced).
// Marshaled verbatim into liquidation_history.inputs (JSONB).
type liquidationInputsPayload struct {
	Salary    json.Number `json:"salary"`
	SalPend   json.Number `json:"salPend"`
	Otros     json.Number `json:"otros"`
	VacDays   json.Number `json:"vacDays"`
	DecMonths json.Number `json:"decMonths"`

	AcumVac   json.Number `json:"acumVac"`
	AcumDec   json.Number `json:"acumDec"`
	AcumPrima json.Number `json:"acumPrima"`
	Acum6m    json.Number `json:"acum6m"`
	Sal30Raw  json.Number `json:"sal30Raw"`

	DeductionIDs        []string    `json:"deductionIds"`
	DeductionQuotaTotal json.Number `json:"deductionQuotaTotal"`

	Branches liquidationBranchesPayload `json:"branches"`
}

// buildLiquidationInputsPayload converts the exact LiquidationInput the
// calculation ran against (so inputs always matches what breakdown was
// computed from, never a separately-reparsed copy of the request) plus the
// branch flags CalculateLiquidation returned.
func buildLiquidationInputsPayload(in payroll.LiquidationInput, branches payroll.Branches, deductionIDs []string) liquidationInputsPayload {
	ids := deductionIDs
	if ids == nil {
		ids = []string{}
	}
	return liquidationInputsPayload{
		Salary:    numToJSONNumber(in.Salary, 2),
		SalPend:   numToJSONNumber(in.SalPend, 2),
		Otros:     numToJSONNumber(in.Otros, 2),
		VacDays:   numToJSONNumber(in.VacDays, 2),
		DecMonths: numToJSONNumber(in.DecMonths, 2),

		AcumVac:   numToJSONNumber(in.AcumVac, 2),
		AcumDec:   numToJSONNumber(in.AcumDec, 2),
		AcumPrima: numToJSONNumber(in.AcumPrima, 2),
		Acum6m:    numToJSONNumber(in.Acum6m, 2),
		Sal30Raw:  numToJSONNumber(in.Sal30Raw, 2),

		DeductionIDs:        ids,
		DeductionQuotaTotal: numToJSONNumber(in.DeductionQuotaTotal, 2),

		Branches: liquidationBranchesPayload{
			VacProp: branches.VacProp,
			DecProp: branches.DecProp,
			Prima:   branches.Prima,
			Indem6m: branches.Indem6m,
			Sal30:   branches.Sal30,
		},
	}
}

// liquidationBreakdown is the spec's authoritative full-breakdown list
// (design R6), marshaled verbatim into liquidation_history.breakdown
// (JSONB). Keys use the JS names verbatim so a stored record drops straight
// into the existing printLiq template. RoundedYears is the Go field name
// (hazard 2, see payroll/liquidation.go) while "floorYears" is its JSON key
// -- the rename is where the hazard is documented, not where it is hidden.
// Precision matches golden_test.go's own comparison rule exactly (design
// R2): every field is money/count at FloatString(2) except RecargoPct and
// ISRRate, which are ratios at FloatString(6).
type liquidationBreakdown struct {
	Years       json.Number `json:"years"`
	TotalMonths int         `json:"totalMonths"`
	FloorYears  json.Number `json:"floorYears"`

	Salario  json.Number `json:"salario"`
	Preaviso json.Number `json:"preaviso"`
	VacProp  json.Number `json:"vacProp"`
	DecProp  json.Number `json:"decProp"`
	Parcial  json.Number `json:"parcial"`

	PrimaMonths    json.Number `json:"primaMonths"`
	PrimaTotal     json.Number `json:"primaTotal"`
	PrimaMensual   json.Number `json:"primaMensual"`
	PrimaSemanal   json.Number `json:"primaSemanal"`
	AntigSem       json.Number `json:"antigSem"`
	PrimaDeduccion json.Number `json:"primaDeduccion"`
	AntigSemNeta   json.Number `json:"antigSemNeta"`

	Indem6mMensual  json.Number `json:"indem6mMensual"`
	Sal30           json.Number `json:"sal30"`
	IndemSemanalFav json.Number `json:"indemSemanalFav"`
	IndemWeeks      json.Number `json:"indemWeeks"`
	IndemBase       json.Number `json:"indemBase"`
	RecargoPct      json.Number `json:"recargoPct"`
	Recargo         json.Number `json:"recargo"`
	Indemnizacion   json.Number `json:"indemnizacion"`

	Total json.Number `json:"total"`

	CSSBase      json.Number `json:"cssBase"`
	CSS91        json.Number `json:"css91"`
	SE92         json.Number `json:"se92"`
	ISRAnual     json.Number `json:"isrAnual"`
	ISRRate      json.Number `json:"isrRate"`
	ISR93        json.Number `json:"isr93"`
	Art701Base   json.Number `json:"art701Base"`
	Art701Sujeta json.Number `json:"art701Sujeta"`
	ISR94        json.Number `json:"isr94"`

	TotalLegal json.Number `json:"totalLegal"`
	TotalDed   json.Number `json:"totalDed"`
	NetTotal   json.Number `json:"netTotal"`
}

func buildLiquidationBreakdown(r payroll.LiquidationResult) liquidationBreakdown {
	return liquidationBreakdown{
		Years:       numToJSONNumber(r.Years, 2),
		TotalMonths: r.TotalMonths,
		FloorYears:  numToJSONNumber(r.RoundedYears, 2),

		Salario:  numToJSONNumber(r.Salario, 2),
		Preaviso: numToJSONNumber(r.Preaviso, 2),
		VacProp:  numToJSONNumber(r.VacProp, 2),
		DecProp:  numToJSONNumber(r.DecProp, 2),
		Parcial:  numToJSONNumber(r.Parcial, 2),

		PrimaMonths:    numToJSONNumber(r.PrimaMonths, 2),
		PrimaTotal:     numToJSONNumber(r.PrimaTotal, 2),
		PrimaMensual:   numToJSONNumber(r.PrimaMensual, 2),
		PrimaSemanal:   numToJSONNumber(r.PrimaSemanal, 2),
		AntigSem:       numToJSONNumber(r.AntigSem, 2),
		PrimaDeduccion: numToJSONNumber(r.PrimaDeduccion, 2),
		AntigSemNeta:   numToJSONNumber(r.AntigSemNeta, 2),

		Indem6mMensual:  numToJSONNumber(r.Indem6mMensual, 2),
		Sal30:           numToJSONNumber(r.Sal30, 2),
		IndemSemanalFav: numToJSONNumber(r.IndemSemanalFav, 2),
		IndemWeeks:      numToJSONNumber(r.IndemWeeks, 2),
		IndemBase:       numToJSONNumber(r.IndemBase, 2),
		RecargoPct:      numToJSONNumber(r.RecargoPct, 6), // ratio (design R2)
		Recargo:         numToJSONNumber(r.Recargo, 2),
		Indemnizacion:   numToJSONNumber(r.Indemnizacion, 2),

		Total: numToJSONNumber(r.Total, 2),

		CSSBase:      numToJSONNumber(r.CSSBase, 2),
		CSS91:        numToJSONNumber(r.CSS91, 2),
		SE92:         numToJSONNumber(r.SE92, 2),
		ISRAnual:     numToJSONNumber(r.ISRAnual, 2),
		ISRRate:      numToJSONNumber(r.ISRRate, 6), // ratio (design R2)
		ISR93:        numToJSONNumber(r.ISR93, 2),
		Art701Base:   numToJSONNumber(r.Art701Base, 2),
		Art701Sujeta: numToJSONNumber(r.Art701Sujeta, 2),
		ISR94:        numToJSONNumber(r.ISR94, 2),

		TotalLegal: numToJSONNumber(r.TotalLegal, 2),
		TotalDed:   numToJSONNumber(r.TotalDed, 2),
		NetTotal:   numToJSONNumber(r.NetTotal, 2),
	}
}

// liquidationResolveResult bundles everything CalculateLiquidation (preview)
// and CreateLiquidation (the committing create) both need after resolving a
// calculation request: the tenant-validated employee row, the parsed
// reason/exit_date, the computed LiquidationResult, and the two JSONB-ready
// payloads -- so the calculation pipeline (validate, read the employee,
// build LiquidationInput, call CalculateLiquidation, build the two
// payloads) is authored exactly once, matching calculatePayrollRun's own
// shared-pipeline precedent (slice 3e/3f, task 6.4's reuse note).
type liquidationResolveResult struct {
	EmployeeID pgtype.UUID
	Employee   db.GetEmployeeForLiquidationRow
	Reason     payroll.Reason
	ExitDate   time.Time
	Result     payroll.LiquidationResult
	Breakdown  liquidationBreakdown
	Inputs     liquidationInputsPayload
}

// numericFromQuery builds a pgtype.Numeric from a GET query parameter
// (task 7.3's preview endpoint has no JSON body to decode pgtype.Numeric
// from directly). An empty string means "not supplied" -- an invalid
// pgtype.Numeric, which numericToNum converts to an exact zero, matching
// every `||0` the JS does at its own read sites (same contract numeric.go
// already documents for numericToNum).
func numericFromQuery(raw string) (pgtype.Numeric, error) {
	if raw == "" {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(raw); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// resolveLiquidation validates employee_id/reason/exit_date, tenant-validates
// the employee via GetEmployeeForLiquidation (A1 rule 6, slice 3e), builds
// the LiquidationInput, and runs payroll.CalculateLiquidation. It writes the
// appropriate error response itself and returns ok=false on any failure, so
// both call sites (preview and create) share one validation/calculation
// path and cannot diverge on it.
func (a *API) resolveLiquidation(w http.ResponseWriter, r *http.Request, companyID pgtype.UUID, req liquidationCalcRequest) (liquidationResolveResult, bool) {
	fields := map[string]string{}

	employeeID, idErr := stringToUUID(req.EmployeeID)
	if idErr != nil {
		fields["employee_id"] = "required"
	}
	reason, okReason := payroll.ParseReason(req.Reason)
	if !okReason {
		fields["reason"] = "must be one of the 10 recognized termination reasons"
	}
	exitDate, dateErr := time.Parse("2006-01-02", req.ExitDate)
	if dateErr != nil {
		fields["exit_date"] = "must be a YYYY-MM-DD date"
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return liquidationResolveResult{}, false
	}

	employee, err := a.Queries.GetEmployeeForLiquidation(r.Context(), db.GetEmployeeForLiquidationParams{
		CompanyID: companyID,
		ID:        employeeID,
	})
	if err != nil {
		a.writeDBErr(w, err, "resolve liquidation: get employee")
		return liquidationResolveResult{}, false
	}
	if !employee.StartDate.Valid {
		writeFieldErr(w, map[string]string{"employee_id": "employee has no start_date set"})
		return liquidationResolveResult{}, false
	}

	nums := make(map[string]payroll.Num, 11)
	for _, f := range []struct {
		key string
		val pgtype.Numeric
	}{
		{"salary", employee.Salary},
		{"sal_pend", req.SalPend}, {"otros", req.Otros},
		{"vac_days", req.VacDays}, {"dec_months", req.DecMonths},
		{"acum_vac", req.AcumVac}, {"acum_dec", req.AcumDec},
		{"acum_prima", req.AcumPrima}, {"acum_6m", req.Acum6m},
		{"sal30", req.Sal30}, {"deduction_quota_total", req.DeductionQuotaTotal},
	} {
		n, err := numericToNum(f.val)
		if err != nil {
			a.Log.Error("resolve liquidation: parse numeric", "field", f.key, "err", err)
			writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
			return liquidationResolveResult{}, false
		}
		nums[f.key] = n
	}

	in := payroll.LiquidationInput{
		Salary:    nums["salary"],
		StartDate: employee.StartDate.Time,
		ExitDate:  exitDate,
		Reason:    reason,

		SalPend:   nums["sal_pend"],
		Otros:     nums["otros"],
		VacDays:   nums["vac_days"],
		DecMonths: nums["dec_months"],

		AcumVac:   nums["acum_vac"],
		AcumDec:   nums["acum_dec"],
		AcumPrima: nums["acum_prima"],
		Acum6m:    nums["acum_6m"],
		Sal30Raw:  nums["sal30"],

		DeductionQuotaTotal: nums["deduction_quota_total"],
	}

	result, err := payroll.CalculateLiquidation(in)
	if err != nil {
		if err == payroll.ErrExitBeforeStart {
			writeFieldErr(w, map[string]string{"exit_date": "must not precede the employee's start_date"})
			return liquidationResolveResult{}, false
		}
		a.Log.Error("resolve liquidation: calculate", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return liquidationResolveResult{}, false
	}

	return liquidationResolveResult{
		EmployeeID: employeeID,
		Employee:   employee,
		Reason:     reason,
		ExitDate:   exitDate,
		Result:     result,
		Breakdown:  buildLiquidationBreakdown(result),
		Inputs:     buildLiquidationInputsPayload(in, result.Branches, req.DeductionIDs),
	}, true
}

// GET /api/liquidation_history/calculate?employee_id=&reason=&exit_date=&...
// -- pure preview (task 7.3), mirroring CalculatePayroll's shape (design
// R4e). Writes nothing; returns the full breakdown object directly (a bare
// object, like GetEmployeePayBases -- there is exactly one calculation
// result per request, not a list). The literal "/calculate" segment ranks
// above liquidation_history's "/{id}" under Go 1.22 ServeMux (Q5c).
func (a *API) CalculateLiquidation(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	req := liquidationCalcRequest{
		EmployeeID: q.Get("employee_id"),
		Reason:     q.Get("reason"),
		ExitDate:   q.Get("exit_date"),
	}

	fields := map[string]string{}
	for _, f := range []struct {
		key string
		dst *pgtype.Numeric
	}{
		{"sal_pend", &req.SalPend}, {"otros", &req.Otros},
		{"vac_days", &req.VacDays}, {"dec_months", &req.DecMonths},
		{"acum_vac", &req.AcumVac}, {"acum_dec", &req.AcumDec},
		{"acum_prima", &req.AcumPrima}, {"acum_6m", &req.Acum6m},
		{"sal30", &req.Sal30}, {"deduction_quota_total", &req.DeductionQuotaTotal},
	} {
		v, err := numericFromQuery(q.Get(f.key))
		if err != nil {
			fields[f.key] = "must be a decimal number"
			continue
		}
		*f.dst = v
	}
	if len(fields) > 0 {
		writeFieldErr(w, fields)
		return
	}

	resolved, ok := a.resolveLiquidation(w, r, companyID, req)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, resolved.Breakdown)
}

// POST /api/liquidation_history -- the committing create (task 7.3, design
// R6/settled decision #2+#3). Recomputes server-side via
// payroll.CalculateLiquidation (saveLiqHistory's own call sends only 5
// fields and structurally cannot supply the breakdown); employee_name is
// derived from the employees row via the A1 rule 6 rider on the insert
// itself; client-sent total_amount is declared-and-ignored (Q6 precedent) --
// the persisted total_amount is always CalculateLiquidation's NetTotal.
func (a *API) CreateLiquidation(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	var req liquidationCalcRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, codeBadRequest, "invalid body")
		return
	}

	resolved, ok := a.resolveLiquidation(w, r, companyID, req)
	if !ok {
		return
	}

	breakdownJSON, err := json.Marshal(resolved.Breakdown)
	if err != nil {
		a.Log.Error("create liquidation: marshal breakdown", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}
	inputsJSON, err := json.Marshal(resolved.Inputs)
	if err != nil {
		a.Log.Error("create liquidation: marshal inputs", "err", err)
		writeErrCode(w, http.StatusInternalServerError, codeInternal, "internal error")
		return
	}

	row, err := a.Queries.CreateLiquidationHistoryWithEmployee(r.Context(), db.CreateLiquidationHistoryWithEmployeeParams{
		CompanyID:   companyID, // $1 -- from ctx, never from req
		ID:          resolved.EmployeeID,
		Reason:      pgtype.Text{String: string(resolved.Reason), Valid: true},
		ExitDate:    pgtype.Date{Time: resolved.ExitDate, Valid: true},
		TotalAmount: numToNumeric(resolved.Result.NetTotal),
		Notes:       req.Notes,
		StartDate:   resolved.Employee.StartDate,
		Breakdown:   breakdownJSON,
		Inputs:      inputsJSON,
		CalcVersion: pgtype.Text{String: payroll.CalcVersion, Valid: true},
	})
	if err != nil {
		a.writeDBErr(w, err, "create liquidation")
		return
	}

	writeRows(w, http.StatusCreated, []db.LiquidationHistory{row})
}

// GET /api/liquidation_history?id=&employee_id=&_limit=&_offset=
// Filterable by employee_id (spec "List filtered by employee returns only
// that employee's liquidaciones").
func (a *API) ListLiquidationHistory(w http.ResponseWriter, r *http.Request) {
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

	rows, err := a.Queries.ListLiquidationHistory(r.Context(), db.ListLiquidationHistoryParams{
		CompanyID:  companyID,
		Column2:    id,
		EmployeeID: employeeID,
		Limit:      p.Limit,
		Offset:     p.Offset,
	})
	if err != nil {
		a.writeDBErr(w, err, "list liquidation history")
		return
	}

	writeRows(w, http.StatusOK, rows)
}

// GET /api/liquidation_history/{id}
func (a *API) GetLiquidationHistory(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	row, err := a.Queries.GetLiquidationHistory(r.Context(), db.GetLiquidationHistoryParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "get liquidation history")
		return
	}

	writeJSON(w, http.StatusOK, row)
}

// DELETE /api/liquidation_history/{id} -- hard delete (spec "List, get,
// delete" -- matches delLiq's existing behavior). No PATCH exists for this
// resource -- immutable historical record (design R7). Nothing to cascade:
// liquidation_history carries no FK relationship to employee_pay_records at
// all, so unlike payroll_history's no-cascade decision (design R5), this is
// not even a design choice -- it is structurally the only possible outcome.
func (a *API) DeleteLiquidationHistory(w http.ResponseWriter, r *http.Request) {
	companyID, ok := a.tenant(w, r)
	if !ok {
		return
	}

	id, err := stringToUUID(r.PathValue("id"))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	affected, err := a.Queries.DeleteLiquidationHistory(r.Context(), db.DeleteLiquidationHistoryParams{
		CompanyID: companyID,
		ID:        id,
	})
	if err != nil {
		a.writeDBErr(w, err, "delete liquidation history")
		return
	}
	if affected == 0 {
		writeErrCode(w, http.StatusNotFound, codeNotFound, "not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
