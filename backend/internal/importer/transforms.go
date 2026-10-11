package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/livant05/rrhh-go/internal/evaluation"
	"github.com/livant05/rrhh-go/internal/overtime"
	"github.com/livant05/rrhh-go/internal/payroll"
	"github.com/livant05/rrhh-go/migrations"
)

// The transforms below reuse the pure domain packages the API handlers use
// (evaluation, payroll, overtime); they never re-implement a formula. Values
// derived by the server (avg, category, days, cost, amount...) always replace
// whatever the export carries.

const evaluationStatusCompleted = "completada"

// maxLegacyScoreWrap bounds how many times a JSON-string `scores` is unwrapped.
const maxLegacyScoreWrap = 3

// transformEvaluation unwraps the legacy double-encoded scores string,
// validates it with evaluation.ParseScores (no silent rounding), and stores
// the canonical object with the recomputed avg and category.
func transformEvaluation(_ context.Context, _ querier, row Row) error {
	if row["scores"] == nil {
		return invalidf("scores: required")
	}
	raw, err := json.Marshal(row["scores"])
	if err != nil {
		return invalidf("scores: %v", err)
	}
	for i := 0; i < maxLegacyScoreWrap && len(raw) > 0 && raw[0] == '"'; i++ {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return invalidf("scores: %v", err)
		}
		raw = json.RawMessage(inner)
	}
	scores, err := evaluation.ParseScores(raw)
	if err != nil {
		return invalidf("%v", err)
	}
	res := evaluation.Evaluate(scores)
	row["scores"] = res.CanonicalScores()
	row["avg"] = res.Avg()
	row["category"] = string(res.Category)
	if s, _ := row["status"].(string); s == "" {
		row["status"] = evaluationStatusCompleted
	}
	return nil
}

// transformMedicalRecord recomputes days, employer/CSS split, salary_basis and
// cost with payroll.CalculateIncapacity over the imported employee's salary.
func transformMedicalRecord(ctx context.Context, q querier, row Row) error {
	typ, ok := payroll.ParseIncapacityType(stringField(row, "type"))
	if !ok {
		return invalidf("type: must be one of enfermedad, accidente, maternidad, paternidad, familiar")
	}
	start, err := dateField(row, "start_date")
	if err != nil {
		return err
	}
	end, err := dateField(row, "end_date")
	if err != nil {
		return err
	}
	status := stringField(row, "status")
	if status == "" {
		status = payroll.MedicalStatusActive
	}
	if status != payroll.MedicalStatusActive && status != payroll.MedicalStatusClosed {
		return invalidf("status: must be activa or cerrada")
	}
	salary, err := employeeSalary(ctx, q, stringField(row, "employee_id"))
	if err != nil {
		return err
	}
	res, err := payroll.CalculateIncapacity(payroll.IncapacityInput{Type: typ, StartDate: start, EndDate: end, Salary: salary})
	if err != nil {
		if errors.Is(err, payroll.ErrEndBeforeStart) {
			return invalidf("end_date: must not be before start_date")
		}
		return err
	}
	if res.Days > payroll.MaxIncapacityDays {
		return invalidf("end_date: span must not exceed %d days", payroll.MaxIncapacityDays)
	}
	row["status"] = status
	row["days"] = res.Days
	row["employer_days"] = res.EmployerDays
	row["css_days"] = res.CSSDays
	row["salary_basis"] = salary.FloatString(2)
	row["cost"] = res.Cost.FloatString(2)
	return nil
}

// transformOvertimeLog recomputes amount = hourly_rate * rate(type) * hours and
// applies the API's type allowlist and 3-hour cap.
func transformOvertimeLog(_ context.Context, _ querier, row Row) error {
	typ := stringField(row, "type")
	if typ == "" {
		typ = overtime.DefaultType
	}
	if _, ok := overtime.Rate(typ); !ok {
		return invalidf("type: must be one of regular, mixed, night, sunday, holiday")
	}
	hours, err := numField(row, "hours")
	if err != nil {
		return err
	}
	if !hours.IsPositive() {
		return invalidf("hours: must be greater than 0")
	}
	if hours.Cmp(payroll.FromInt(overtime.MaxHoursPerRecord)) > 0 {
		return invalidf("hours: must not exceed %d hours per record (Art. 36 num. 4)", overtime.MaxHoursPerRecord)
	}
	hourly, err := numField(row, "hourly_rate")
	if err != nil {
		return err
	}
	amount, _ := overtime.Amount(hourly, hours, typ) // type validated above
	row["type"] = typ
	row["amount"] = amount.FloatString(2)
	return nil
}

// seedMedicalPermission re-runs migration 0009 after roles were loaded, so
// imported roles that lack the `medical` key get it. The migration is
// idempotent and only touches roles without the key.
func seedMedicalPermission(ctx context.Context, q querier, tr TableReport) error {
	if tr.Inserted == 0 {
		return nil
	}
	if _, err := q.Exec(ctx, migrations.MedicalPermission); err != nil {
		return fmt.Errorf("seed medical permission: %w", err)
	}
	return nil
}

// employeeSalary reads employees.salary (NULL counts as 0) inside the current
// transaction, so employees imported earlier in the same run are visible.
func employeeSalary(ctx context.Context, q querier, employeeID string) (payroll.Num, error) {
	var s string
	err := q.QueryRow(ctx, `SELECT COALESCE(salary, 0)::text FROM employees WHERE id::text = lower($1)`, employeeID).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return payroll.Num{}, invalidf("employee_id: employee not found")
	}
	if err != nil {
		return payroll.Num{}, err
	}
	return payroll.ParseDecimal(s)
}

func stringField(row Row, key string) string {
	s, _ := row[key].(string)
	return s
}

// dateField parses a PostgREST date ("2024-03-01").
func dateField(row Row, key string) (time.Time, error) {
	s := stringField(row, key)
	if s == "" {
		return time.Time{}, invalidf("%s: required", key)
	}
	if len(s) > 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, invalidf("%s: not a valid date", key)
	}
	return t, nil
}

// numField parses a JSON number (or numeric string) into an exact payroll.Num.
func numField(row Row, key string) (payroll.Num, error) {
	var lit string
	switch v := row[key].(type) {
	case json.Number:
		lit = v.String()
	case string:
		lit = strings.TrimSpace(v)
	case nil:
		return payroll.Num{}, invalidf("%s: required", key)
	default:
		return payroll.Num{}, invalidf("%s: must be a number", key)
	}
	n, err := payroll.ParseDecimal(lit)
	if err != nil {
		return payroll.Num{}, invalidf("%s: must be a number", key)
	}
	return n, nil
}
