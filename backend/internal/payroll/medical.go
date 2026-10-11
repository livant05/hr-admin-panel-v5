package payroll

import (
	"errors"
	"time"
)

// medical.go is the port of calcMedical/saveMedical
// (hr_admin_panel.html:4616-4636): the employer/CSS split of a medical
// incapacity and its cost. Pure: stdlib and Num only. Rounding to 2 decimals
// happens at persistence, not here.

// IncapacityType is the allowlisted kind of medical incapacity.
type IncapacityType string

const (
	IncEnfermedad IncapacityType = "enfermedad"
	IncAccidente  IncapacityType = "accidente"
	IncMaternidad IncapacityType = "maternidad"
	IncPaternidad IncapacityType = "paternidad"
	IncFamiliar   IncapacityType = "familiar"
)

// ParseIncapacityType accepts only the exact lowercase allowlisted values.
func ParseIncapacityType(s string) (IncapacityType, bool) {
	switch t := IncapacityType(s); t {
	case IncEnfermedad, IncAccidente, IncMaternidad, IncPaternidad, IncFamiliar:
		return t, true
	}
	return "", false
}

// ErrEndBeforeStart is returned when the end date precedes the start date.
// The JS silently produced days=0 here; the Go port rejects it.
var ErrEndBeforeStart = errors.New("payroll: end_date before start_date")

// IncapacityInput holds the inputs of the incapacity cost. Dates are civil
// dates: time-of-day and location are ignored.
type IncapacityInput struct {
	Type      IncapacityType
	StartDate time.Time
	EndDate   time.Time
	Salary    Num // employees.salary snapshot (salary_basis)
}

// IncapacityResult is the exact (unrounded) outcome.
type IncapacityResult struct {
	Days, EmployerDays, CSSDays int
	DailyRate                   Num // salary / 30
	Cost                        Num // employerDays*daily + cssDays*daily*0.70
}

// civilDay normalizes t's calendar date (as written in its own location) to
// UTC midnight.
func civilDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// CalculateIncapacity computes the day split and exact cost.
func CalculateIncapacity(in IncapacityInput) (IncapacityResult, error) {
	start, end := civilDay(in.StartDate), civilDay(in.EndDate)
	if end.Before(start) {
		return IncapacityResult{}, ErrEndBeforeStart
	}
	days := int(end.Sub(start)/(24*time.Hour)) + 1

	emp := days
	if emp > incapacityEmployerDaysMax {
		emp = incapacityEmployerDaysMax
	}
	if in.Type == IncPaternidad {
		emp = paternidadEmployerDays
	}
	css := days - emp
	if css < 0 {
		css = 0
	}

	daily, err := in.Salary.Div(daysPerMonth)
	if err != nil { // unreachable: daysPerMonth is a non-zero constant
		return IncapacityResult{}, err
	}
	cost := FromInt(int64(emp)).Mul(daily).
		Add(FromInt(int64(css)).Mul(daily).Mul(cssIncapacitySubsidy))

	return IncapacityResult{
		Days: days, EmployerDays: emp, CSSDays: css,
		DailyRate: daily, Cost: cost,
	}, nil
}
