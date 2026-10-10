package payroll

import (
	"errors"
	"testing"
	"time"
)

func TestParseIncapacityType(t *testing.T) {
	tests := []struct {
		in   string
		want IncapacityType
		ok   bool
	}{
		{"enfermedad", IncEnfermedad, true},
		{"accidente", IncAccidente, true},
		{"maternidad", IncMaternidad, true},
		{"paternidad", IncPaternidad, true},
		{"familiar", IncFamiliar, true},
		{"", "", false},
		{"Paternidad", "", false},
		{"paternity", "", false},
		{"paternida", "", false},
	}
	for _, tt := range tests {
		t.Run("in="+tt.in, func(t *testing.T) {
			got, ok := ParseIncapacityType(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseIncapacityType(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

func TestCalculateIncapacity(t *testing.T) {
	tests := []struct {
		name       string
		typ        IncapacityType
		start, end time.Time
		salary     string
		days, emp  int
		css        int
		cost2dp    string
		wantErr    error
	}{
		// 3*(1000/30) + 2*(1000/30)*0.7 = 100.00 + 46.666.. = 146.67
		{"enfermedad-5d-1000", IncEnfermedad, d(2025, 3, 10), d(2025, 3, 14), "1000", 5, 3, 2, "146.67", nil},
		// 2 days, all employer: 2*(1000/30) = 66.67
		{"enfermedad-2d", IncEnfermedad, d(2025, 3, 10), d(2025, 3, 11), "1000", 2, 2, 0, "66.67", nil},
		// 3*30 + 7*30*0.7 = 90 + 147 = 237.00
		{"enfermedad-10d-900", IncEnfermedad, d(2025, 3, 1), d(2025, 3, 10), "900", 10, 3, 7, "237.00", nil},
		// paternidad always 3 employer days: 3*30 = 90.00
		{"paternidad-1d-900", IncPaternidad, d(2025, 3, 1), d(2025, 3, 1), "900", 1, 3, 0, "90.00", nil},
		// exactly 3 days: css 0; 3*30 = 90.00
		{"exactly-3d", IncEnfermedad, d(2025, 3, 1), d(2025, 3, 3), "900", 3, 3, 0, "90.00", nil},
		{"same-day", IncAccidente, d(2025, 3, 5), d(2025, 3, 5), "900", 1, 1, 0, "30.00", nil},
		{"zero-salary", IncEnfermedad, d(2025, 3, 1), d(2025, 3, 10), "0", 10, 3, 7, "0.00", nil},
		// 3 * 1000.05/30 = 100.005 -> half away from zero -> 100.01
		{"cent-tie-half-away-from-zero", IncEnfermedad, d(2025, 3, 1), d(2025, 3, 3), "1000.05", 3, 3, 0, "100.01", nil},
		// 2024-02-28, 29 (leap), 03-01 = 3 days; 3*30 = 90.00
		{"cross-month-and-leap-day", IncEnfermedad, d(2024, 2, 28), d(2024, 3, 1), "900", 3, 3, 0, "90.00", nil},
		{"end-before-start", IncEnfermedad, d(2025, 3, 5), d(2025, 3, 4), "900", 0, 0, 0, "", ErrEndBeforeStart},
		{"end-before-start-paternidad", IncPaternidad, d(2025, 3, 5), d(2025, 3, 4), "900", 0, 0, 0, "", ErrEndBeforeStart},
		// maternidad uses the illness split (faithful port): 3*30 + 7*30*0.7 = 237.00
		{"maternidad-10d-900", IncMaternidad, d(2025, 3, 1), d(2025, 3, 10), "900", 10, 3, 7, "237.00", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateIncapacity(IncapacityInput{
				Type: tt.typ, StartDate: tt.start, EndDate: tt.end,
				Salary: MustParseDecimal(tt.salary),
			})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Days != tt.days || got.EmployerDays != tt.emp || got.CSSDays != tt.css {
				t.Fatalf("days/emp/css = %d/%d/%d, want %d/%d/%d",
					got.Days, got.EmployerDays, got.CSSDays, tt.days, tt.emp, tt.css)
			}
			if c := got.Cost.FloatString(2); c != tt.cost2dp {
				t.Fatalf("cost = %s, want %s", c, tt.cost2dp)
			}
		})
	}
}

func TestCalculateIncapacity_IgnoresTimeOfDayAndLocation(t *testing.T) {
	loc := time.FixedZone("UTC-5", -5*3600)
	// 23:30 local on the 10th == 04:30 UTC on the 11th; the civil date as
	// written (10th) must be used, not the UTC conversion.
	start := time.Date(2025, 3, 10, 23, 30, 0, 0, loc)
	end := time.Date(2025, 3, 14, 1, 0, 0, 0, loc)
	got, err := CalculateIncapacity(IncapacityInput{
		Type: IncEnfermedad, StartDate: start, EndDate: end, Salary: MustParseDecimal("1000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Days != 5 {
		t.Fatalf("days = %d, want 5", got.Days)
	}
}
